// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"path/filepath"
	"sort"
	"strconv"

	"hate/internal/config"
	"hate/internal/ticket"
)

// One estimate function. Every place that needs "how many hours is this ticket
// expected to take" (schedule, load, rollup, baseline, variance, strict time)
// goes through EstimatedHours, so there is a single source:
//
//	wrap ticket (config/nonfunc) -> estimate_hours                ("estimate")
//	                                legacy effort x 8 (read-time) ("converted")
//	functional child             -> parent CFP x ref rate / n     ("feature")
//	self-contained functional    -> own CFP x ref rate            ("feature")
//	unclassed with legacy effort -> effort days x 8               ("legacy")
//	parent, meeting, admin       -> 0                             ("")
//
// The functional numbers are for scheduling only; the feature-level estimate
// (Monte Carlo on the COSMIC tab) is the real code estimate.

// Estimate sources returned by EstimatedHours.
const (
	SourceEstimate  = "estimate"
	SourceConverted = "converted"
	SourceFeature   = "feature"
	SourceLegacy    = "legacy"
)

// DefaultHPerCFP is the fallback code rate (hours per CFP) when a project has no
// reference set, or fewer than 3 reference features: the pooled NEI + Tactic
// median as of 2026-10-02 (32 features, 3+ CFP).
const DefaultHPerCFP = 0.25

// DefaultEstimateMinCFP is the smallest feature (in CFP) used as a reference
// sample when the project hasn't set estimate_min_cfp.
const DefaultEstimateMinCFP = 3

// minReferenceFeatures is the fewest reference features needed before the
// reference median replaces DefaultHPerCFP.
const minReferenceFeatures = 3

// EstimateContext is the project-wide input EstimatedHours needs.
type EstimateContext struct {
	Tickets       []*ticket.Ticket   // whole project
	RefMedianRate float64            // h/CFP used for functional scheduling estimates
	EffortToDays  map[string]float64 // legacy fallback only

	idx *estimateIndex // lazily built lookup over Tickets (see prepared)
}

// estimateIndex caches the per-project lookups EstimatedHours needs, so loops
// over every ticket stay linear.
type estimateIndex struct {
	byID          map[string]*ticket.Ticket
	hasChildren   map[string]bool
	functionalCnt map[string]int // parent id -> in-scope functional children
}

// NewEstimateContext builds a context for a project's tickets.
func NewEstimateContext(tickets []*ticket.Ticket, refMedianRate float64, effortToDays map[string]float64) EstimateContext {
	return EstimateContext{Tickets: tickets, RefMedianRate: refMedianRate, EffortToDays: effortToDays}.prepared()
}

// ProjectEstimateContext builds the context for a project on disk: its
// reference median rate and legacy effort map come from its config.
func ProjectEstimateContext(projectRoot string, tickets []*ticket.Ticket, cfg *ticket.ProjectConfig) EstimateContext {
	var etd map[string]float64
	if cfg != nil {
		etd = cfg.EffortToDays
	}
	return NewEstimateContext(tickets, ProjectRefMedianRate(projectRoot), etd)
}

// prepared returns ctx with its lookup index built (no-op when already built).
func (ctx EstimateContext) prepared() EstimateContext {
	if ctx.idx != nil {
		return ctx
	}
	ix := &estimateIndex{
		byID:          map[string]*ticket.Ticket{},
		hasChildren:   map[string]bool{},
		functionalCnt: map[string]int{},
	}
	for _, t := range ctx.Tickets {
		ix.byID[t.ID] = t
	}
	for _, t := range ctx.Tickets {
		pid := ticket.ParentID(t)
		if pid == "" {
			continue
		}
		ix.hasChildren[pid] = true
		if ticket.ClassOf(t) == ticket.ClassFunctional && inHoursScope(t) {
			ix.functionalCnt[pid]++
		}
	}
	ctx.idx = ix
	return ctx
}

// refRate is the h/CFP used for functional scheduling estimates.
func (ctx EstimateContext) refRate() float64 {
	if ctx.RefMedianRate > 0 {
		return ctx.RefMedianRate
	}
	return DefaultHPerCFP
}

// ticketCFP parses a ticket's cfp:N tag (0 when absent or invalid).
func ticketCFP(t *ticket.Ticket) int {
	s, ok := cosmicTagValue(t.Tags, cfpTagPrefix)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// HoursPerDay is the default daily capacity and the legacy effort conversion
// constant (retired t-shirt effort days x 8 = hours). Scheduling otherwise
// converts estimated hours to days at the assignee's own daily hours.
const HoursPerDay = 8.0

// effortDaysFor returns the configured planned-days for a t-shirt size, falling
// back to the project's effort_to_days map and then to defaults.
func effortDaysFor(effort string, effortToDays map[string]float64) float64 {
	if effort == "" {
		return 0
	}
	if d, ok := effortToDays[effort]; ok {
		return d
	}
	if d, ok := ticket.DefaultEffortToDays[effort]; ok {
		return d
	}
	return 0
}

// legacyEffortHours converts a retired t-shirt effort to hours (days x 8).
func legacyEffortHours(t *ticket.Ticket, effortToDays map[string]float64) float64 {
	if t.Effort == nil {
		return 0
	}
	return effortDaysFor(*t.Effort, effortToDays) * HoursPerDay
}

// EstimatedHours returns a ticket's expected hours and where the number came
// from (SourceEstimate, SourceConverted, SourceFeature, SourceLegacy, or "" for
// an unsized ticket, which always has 0 hours).
func EstimatedHours(t *ticket.Ticket, ctx EstimateContext) (float64, string) {
	if t.Type == "meeting" || t.Type == "administration" {
		return 0, ""
	}
	ctx = ctx.prepared()
	if ctx.idx.hasChildren[t.ID] {
		return 0, "" // a parent is a container; its children carry the hours
	}
	switch class := ticket.ClassOf(t); {
	case ticket.IsWrapClass(class):
		if t.EstimateHours != nil {
			return *t.EstimateHours, SourceEstimate
		}
		if h := legacyEffortHours(t, ctx.EffortToDays); h > 0 {
			return h, SourceConverted
		}
		return 0, ""
	case class == ticket.ClassFunctional:
		if cfp := ticketCFP(t); cfp > 0 { // self-contained feature
			return float64(cfp) * ctx.refRate(), SourceFeature
		}
		parent := ctx.idx.byID[ticket.ParentID(t)]
		if parent == nil {
			return 0, ""
		}
		cfp := ticketCFP(parent)
		n := ctx.idx.functionalCnt[parent.ID]
		if cfp <= 0 || n <= 0 {
			return 0, ""
		}
		return float64(cfp) * ctx.refRate() / float64(n), SourceFeature
	default:
		if h := legacyEffortHours(t, ctx.EffortToDays); h > 0 {
			return h, SourceLegacy
		}
		return 0, ""
	}
}

// WrapAllotment returns a wrap ticket's hours allotment (its estimate, or the
// converted legacy effort). ok is false for non-wrap tickets (functional,
// unclassed, meeting, admin), which have no per-ticket allotment.
func WrapAllotment(t *ticket.Ticket, ctx EstimateContext) (float64, bool) {
	if !ticket.IsWrapClass(ticket.ClassOf(t)) || t.Type == "meeting" || t.Type == "administration" {
		return 0, false
	}
	h, _ := EstimatedHours(t, ctx)
	return h, true
}

// DailyHoursFor returns the assignee's daily capacity (Resource.EffectiveDailyHours),
// or the default 8 when the assignee is unset or not a known resource. The
// assignee matches a resource by email, git user, or name.
func DailyHoursFor(assignee *string, resources []ticket.Resource) float64 {
	if assignee != nil {
		if r, ok := findResource(*assignee, resources); ok {
			return r.EffectiveDailyHours()
		}
	}
	return ticket.DefaultDailyHours
}

// HoursToDays converts estimated hours to working days at the assignee's daily
// capacity.
func HoursToDays(hours float64, assignee *string, resources []ticket.Resource) float64 {
	if hours <= 0 {
		return 0
	}
	return hours / DailyHoursFor(assignee, resources)
}

// ---------------------------------------------------------------------------
// Reference features (the h/CFP samples estimates are drawn from)
// ---------------------------------------------------------------------------

// IsFunctionalDoneStatus reports whether a functional ticket's code work is done:
// dev_complete or later (QA, review, approval, complete, closed). not_started,
// in_progress, rework, and blocked are not done.
func IsFunctionalDoneStatus(status string) bool {
	switch status {
	case "dev_complete", "qa_testing", "submitted_for_review", "approved", "complete", "closed":
		return true
	}
	return false
}

// FeatureFunctionalDone reports whether every functional ticket of a feature is
// done (its functional children, plus the feature itself when it is a
// self-contained functional ticket). A feature with no functional tickets is
// not done.
func FeatureFunctionalDone(feature *ticket.Ticket, tickets []*ticket.Ticket) bool {
	n := 0
	if ticket.ClassOf(feature) == ticket.ClassFunctional {
		if !IsFunctionalDoneStatus(feature.Status) {
			return false
		}
		n++
	}
	for _, c := range tickets {
		if ticket.ParentID(c) != feature.ID || ticket.ClassOf(c) != ticket.ClassFunctional {
			continue
		}
		if !IsFunctionalDoneStatus(c.Status) {
			return false
		}
		n++
	}
	return n > 0
}

// EffectiveEstimateMinCFP is the project's minimum reference feature size,
// defaulting to DefaultEstimateMinCFP.
func EffectiveEstimateMinCFP(cfg *ticket.ProjectConfig) int {
	if cfg != nil && cfg.EstimateMinCFP != nil && *cfg.EstimateMinCFP >= 1 {
		return *cfg.EstimateMinCFP
	}
	return DefaultEstimateMinCFP
}

// ReferenceRates returns the h/CFP of a project's finished reference features:
// features (as computed by ComputeCosmic) with functional hours, CFP >= minCFP,
// and all functional tickets done.
func ReferenceRates(tickets []*ticket.Ticket, minCFP int) []float64 {
	byID := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		byID[t.ID] = t
	}
	var rates []float64
	for _, f := range ComputeCosmic(tickets).Features {
		if f.FunctionalHours <= 0 || f.CFP < minCFP {
			continue
		}
		if ft := byID[f.ID]; ft == nil || !FeatureFunctionalDone(ft, tickets) {
			continue
		}
		rates = append(rates, f.FunctionalHours/float64(f.CFP))
	}
	return rates
}

// ---------------------------------------------------------------------------
// Manual baseline (a typed h/CFP range used as a reference)
// ---------------------------------------------------------------------------

// ManualPreset is a named manual baseline (h/CFP; Low / Likely / High read as
// P10 / P50 / P90).
type ManualPreset struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Low    float64 `json:"low"`
	Likely float64 `json:"likely"`
	High   float64 `json:"high"`
}

// Manual baseline preset ids. ManualPresetCustom is any range that matches no
// preset.
const (
	ManualPresetAgentic     = "agentic"
	ManualPresetTraditional = "traditional"
	ManualPresetCustom      = "custom"
)

// ManualPresets are the offered baselines. Agentic (the default) is the NEI +
// Tactic pool (32 features), High set near the worst observed 0.97 because the
// backtest showed ranges run wide; Traditional is the industry hand-coded band.
var ManualPresets = []ManualPreset{
	{ID: ManualPresetAgentic, Label: "Agentic (Claude-assisted)", Low: 0.08, Likely: 0.25, High: 1.0},
	{ID: ManualPresetTraditional, Label: "Traditional (hand-coded)", Low: 8, Likely: 12, High: 18},
}

// manualBaselineWeight is how many features of evidence the manual baseline
// counts as when blended with borrowed features (and in the 3-feature minimum).
const manualBaselineWeight = 5

// DefaultManualBaseline is the Agentic preset's range.
func DefaultManualBaseline() ticket.ManualBaseline {
	p := ManualPresets[0]
	return ticket.ManualBaseline{Low: p.Low, Likely: p.Likely, High: p.High}
}

// ManualPresetID names the preset a range matches, or ManualPresetCustom.
func ManualPresetID(m ticket.ManualBaseline) string {
	eq := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	for _, p := range ManualPresets {
		if eq(m.Low, p.Low) && eq(m.Likely, p.Likely) && eq(m.High, p.High) {
			return p.ID
		}
	}
	return ManualPresetCustom
}

// ValidManualBaseline reports whether 0 < low <= likely <= high.
func ValidManualBaseline(m ticket.ManualBaseline) bool {
	return m.Low > 0 && m.Low <= m.Likely && m.Likely <= m.High
}

// EstimateRefs are a project's effective reference selections.
type EstimateRefs struct {
	Projects []string
	All      bool
	Own      bool
	Manual   bool
	// ManualBaseline is the saved range, or the default preset when none is
	// saved (or the saved one is invalid).
	ManualBaseline ticket.ManualBaseline
	// DefaultsApplied is true when the project has no saved estimate inputs, so
	// the defaults (manual baseline + own features) are in effect.
	DefaultsApplied bool
}

// HasSavedEstimateInputs reports whether any reference input has been saved:
// ref projects, ref_all, ref_own, ref_manual (even false), or a manual range.
func HasSavedEstimateInputs(cfg *ticket.ProjectConfig) bool {
	return cfg != nil && (len(cfg.EstimateRefProjects) > 0 || cfg.EstimateRefAll || cfg.EstimateRefOwn ||
		cfg.EstimateRefManual != nil || cfg.EstimateManual != nil)
}

// EffectiveEstimateRefs returns the reference selections in effect. With no
// saved inputs that is the manual baseline (Agentic) + own features; once
// saved, the saved values are used exactly.
func EffectiveEstimateRefs(cfg *ticket.ProjectConfig) EstimateRefs {
	r := EstimateRefs{ManualBaseline: DefaultManualBaseline()}
	if !HasSavedEstimateInputs(cfg) {
		r.Own, r.Manual, r.DefaultsApplied = true, true, true
		return r
	}
	r.Projects = cfg.EstimateRefProjects
	r.All = cfg.EstimateRefAll
	r.Own = cfg.EstimateRefOwn
	r.Manual = cfg.EstimateRefManual != nil && *cfg.EstimateRefManual
	if cfg.EstimateManual != nil && ValidManualBaseline(*cfg.EstimateManual) {
		r.ManualBaseline = *cfg.EstimateManual
	}
	return r
}

// ReferenceSet is the reference features gathered for a project's estimate,
// split into its own finished features and those borrowed from other projects,
// plus the manual baseline when selected.
type ReferenceSet struct {
	Configured bool      // any reference option is set (own, all, specific projects, or manual)
	MinCFP     int       // minimum feature size used
	Own        []float64 // h/CFP of this project's finished features (when ref_own)
	Borrowed   []float64 // h/CFP of borrowed projects' finished features
	NOwn       int
	NBorrowed  int
	// BorrowedProjects are the ids of the other projects that were read.
	BorrowedProjects []string
	// MissingProjects are configured ids that could not be resolved.
	MissingProjects []string
	// Manual is the manual baseline range when it is selected, else nil.
	Manual *ticket.ManualBaseline
}

// All returns own + borrowed rates (a new slice).
func (rs ReferenceSet) All() []float64 {
	out := make([]float64, 0, len(rs.Own)+len(rs.Borrowed))
	out = append(out, rs.Own...)
	return append(out, rs.Borrowed...)
}

// MedianRate is the median h/CFP of the whole set. With fewer than 3 real
// reference features it is the manual baseline's Likely when that is selected,
// else DefaultHPerCFP (also when no reference is configured).
func (rs ReferenceSet) MedianRate() float64 {
	all := rs.All()
	if rs.Configured && len(all) >= minReferenceFeatures {
		sort.Float64s(all)
		return cosmicMedian(all)
	}
	if rs.Manual != nil && rs.Manual.Likely > 0 {
		return rs.Manual.Likely
	}
	return DefaultHPerCFP
}

// GatherReferenceFeatures builds a project's reference set from its config's
// effective estimate inputs (EffectiveEstimateRefs: estimate_ref_own /
// estimate_ref_all / estimate_ref_projects / estimate_ref_manual, with the
// no-saved-inputs defaults; estimate_min_cfp). Borrowed projects come from config.ListProjects, ids
// resolved with config.GetProjectPath; the current project is never borrowed.
// cfg may be nil, in which case it's read from projectRoot.
func GatherReferenceFeatures(projectID, projectRoot string, cfg *ticket.ProjectConfig) ReferenceSet {
	if cfg == nil {
		c, err := ticket.ReadConfig(projectRoot)
		if err != nil {
			return ReferenceSet{MinCFP: DefaultEstimateMinCFP}
		}
		cfg = c
	}
	var projects []config.ProjectInfo
	if cfg.EstimateRefAll || len(cfg.EstimateRefProjects) > 0 {
		projects = config.ListProjects()
	}
	return gatherReferenceFeatures(projectID, projectRoot, cfg, projects, config.GetProjectPath, ticket.ReadAllTickets)
}

// gatherReferenceFeatures is GatherReferenceFeatures with its project listing,
// id resolution, and ticket reading injected (for tests).
func gatherReferenceFeatures(projectID, projectRoot string, cfg *ticket.ProjectConfig,
	projects []config.ProjectInfo, resolve func(id string) (string, error),
	readTickets func(root string) ([]*ticket.Ticket, error)) ReferenceSet {

	rs := ReferenceSet{MinCFP: EffectiveEstimateMinCFP(cfg)}
	if cfg == nil {
		return rs
	}
	refs := EffectiveEstimateRefs(cfg)
	rs.Configured = refs.Own || refs.All || len(refs.Projects) > 0 || refs.Manual
	if refs.Manual {
		m := refs.ManualBaseline
		rs.Manual = &m
	}
	self := filepath.Clean(projectRoot)
	isSelf := func(id, path string) bool {
		return (projectID != "" && id == projectID) || filepath.Clean(path) == self
	}

	if refs.Own {
		if tickets, err := readTickets(projectRoot); err == nil {
			rs.Own = ReferenceRates(tickets, rs.MinCFP)
		}
	}

	// Collect borrowed project roots (deduped by path, in a stable order).
	type src struct{ id, path string }
	var srcs []src
	seen := map[string]bool{}
	add := func(id, path string) {
		p := filepath.Clean(path)
		if seen[p] || isSelf(id, path) {
			return
		}
		seen[p] = true
		srcs = append(srcs, src{id, p})
	}
	if refs.All {
		for _, p := range projects {
			add(p.ID, p.Path)
		}
	}
	for _, id := range refs.Projects {
		found := false
		for _, p := range projects {
			if p.ID == id {
				add(p.ID, p.Path)
				found = true
				break
			}
		}
		if found {
			continue
		}
		if path, err := resolve(id); err == nil {
			add(id, path)
		} else {
			rs.MissingProjects = append(rs.MissingProjects, id)
		}
	}
	for _, s := range srcs {
		tickets, err := readTickets(s.path)
		if err != nil {
			continue
		}
		rs.BorrowedProjects = append(rs.BorrowedProjects, s.id)
		rs.Borrowed = append(rs.Borrowed, ReferenceRates(tickets, rs.MinCFP)...)
	}
	rs.NOwn, rs.NBorrowed = len(rs.Own), len(rs.Borrowed)
	return rs
}

// ProjectRefMedianRate returns the project's reference median h/CFP (the rate
// functional tickets are scheduled at). With fewer than 3 real reference
// features it is the manual baseline's Likely when that is in effect (also by
// default), else DefaultHPerCFP.
func ProjectRefMedianRate(projectRoot string) float64 {
	cfg, err := ticket.ReadConfig(projectRoot)
	if err != nil {
		return DefaultHPerCFP
	}
	id := cfg.ProjectID
	if id == "" {
		id = filepath.Base(filepath.Clean(projectRoot))
	}
	return GatherReferenceFeatures(id, projectRoot, cfg).MedianRate()
}
