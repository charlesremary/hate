// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"testing"

	"hate/internal/config"
	"hate/internal/ticket"
)

// TestEstimatedHoursSources covers every source EstimatedHours can return.
func TestEstimatedHoursSources(t *testing.T) {
	s, m := "s", "m"
	cancel := "dropped"
	tickets := []*ticket.Ticket{
		// Feature parent with three functional children (one cancelled, one
		// backlog: neither counts toward the split) and a wrap child.
		{ID: "F", Type: "task", Tags: []string{"cfp:20"}, Effort: &m},
		{ID: "c1", Type: "dev_task", Tags: []string{"parent:F", ticket.ClassFunctional}},
		{ID: "c2", Type: "dev_task", Tags: []string{"parent:F", ticket.ClassFunctional}},
		{ID: "cx", Type: "dev_task", Status: "closed", CancellationReason: &cancel, Tags: []string{"parent:F", ticket.ClassFunctional}},
		{ID: "cb", Type: "dev_task", Tags: []string{"parent:F", ticket.ClassFunctional, ticket.BacklogTag}},
		{ID: "w1", Type: "task", Tags: []string{"parent:F", ticket.ClassConfig}, EstimateHours: f64(2.5)},
		// Wrap with only a legacy effort → converted (s = 2 days in this map).
		{ID: "w2", Type: "task", Tags: []string{"parent:F", ticket.ClassNonfunc}, Effort: &s},
		// Wrap with nothing → unsized.
		{ID: "w3", Type: "task", Tags: []string{"parent:F", ticket.ClassConfig}},
		// Self-contained functional feature.
		{ID: "S", Type: "dev_task", Tags: []string{"cfp:6", ticket.ClassFunctional}},
		// Functional child whose parent has no cfp → unsized.
		{ID: "P2", Type: "task"},
		{ID: "c3", Type: "dev_task", Tags: []string{"parent:P2", ticket.ClassFunctional}},
		// Unclassed with legacy effort → legacy; unclassed with nothing → unsized.
		{ID: "L", Type: "task", Effort: &m},
		{ID: "N", Type: "task"},
		// Meeting / admin never carry an estimate, even with a legacy effort.
		{ID: "M", Type: "meeting", Effort: &m},
		{ID: "A", Type: "administration", Tags: []string{ticket.ClassConfig}, EstimateHours: f64(1)},
	}
	ctx := NewEstimateContext(tickets, 0.5, map[string]float64{"s": 2, "m": 3})
	byID := map[string]*ticket.Ticket{}
	for _, tk := range tickets {
		byID[tk.ID] = tk
	}

	cases := []struct {
		id     string
		hours  float64
		source string
	}{
		{"F", 0, ""},                        // parent (has children), even with a legacy effort
		{"c1", 20 * 0.5 / 2, SourceFeature}, // 20 CFP x 0.5 / 2 in-scope functional children
		{"c2", 5, SourceFeature},            //
		{"w1", 2.5, SourceEstimate},         //
		{"w2", 16, SourceConverted},         // 2 days x 8
		{"w3", 0, ""},                       //
		{"S", 3, SourceFeature},             // 6 CFP x 0.5
		{"c3", 0, ""},                       // parent has no cfp
		{"L", 24, SourceLegacy},             // 3 days x 8
		{"N", 0, ""},                        //
		{"M", 0, ""},                        //
		{"A", 0, ""},                        //
	}
	for _, c := range cases {
		h, src := EstimatedHours(byID[c.id], ctx)
		if !approx(h, c.hours) || src != c.source {
			t.Errorf("%s: got %.3fh/%q, want %.3fh/%q", c.id, h, src, c.hours, c.source)
		}
	}

	// No reference rate in the context → DefaultHPerCFP.
	h, _ := EstimatedHours(byID["S"], NewEstimateContext(tickets, 0, nil))
	if !approx(h, 6*DefaultHPerCFP) {
		t.Errorf("S with no ref rate = %v, want %v", h, 6*DefaultHPerCFP)
	}
	// An unprepared context (built as a literal) gives the same answers.
	h, src := EstimatedHours(byID["c1"], EstimateContext{Tickets: tickets, RefMedianRate: 0.5})
	if !approx(h, 5) || src != SourceFeature {
		t.Errorf("unprepared ctx c1 = %v/%q, want 5/feature", h, src)
	}
}

// TestWrapAllotment: only wrap tickets have an allotment (ok=true), which is
// their estimate or converted legacy effort; everything else is ok=false.
func TestWrapAllotment(t *testing.T) {
	xs := "xs"
	tickets := []*ticket.Ticket{
		{ID: "E", Tags: []string{ticket.ClassConfig}, EstimateHours: f64(4)},
		{ID: "C", Tags: []string{ticket.ClassNonfunc}, Effort: &xs},
		{ID: "Z", Tags: []string{ticket.ClassConfig}},
		{ID: "F", Tags: []string{ticket.ClassFunctional, "cfp:8"}},
		{ID: "U", Effort: &xs},
	}
	ctx := NewEstimateContext(tickets, 0, nil)
	want := map[string]struct {
		h  float64
		ok bool
	}{"E": {4, true}, "C": {8, true}, "Z": {0, true}, "F": {0, false}, "U": {0, false}}
	for _, tk := range tickets {
		h, ok := WrapAllotment(tk, ctx)
		if h != want[tk.ID].h || ok != want[tk.ID].ok {
			t.Errorf("%s: got %v/%v, want %v/%v", tk.ID, h, ok, want[tk.ID].h, want[tk.ID].ok)
		}
	}
}

func TestHoursToDays(t *testing.T) {
	four := 4.0
	res := []ticket.Resource{{Email: "a@x", DailyHoursAvailable: &four}}
	if d := HoursToDays(8, strp("a@x"), res); d != 2 {
		t.Errorf("8h at 4h/day = %v, want 2", d)
	}
	if d := HoursToDays(8, strp("unknown@x"), res); d != 1 {
		t.Errorf("unknown assignee = %v, want 1 (8h/day default)", d)
	}
	if d := HoursToDays(8, nil, res); d != 1 {
		t.Errorf("no assignee = %v, want 1", d)
	}
}

// refFeature builds a feature with n functional children, each logging h hours
// at the given status.
func f64(v float64) *float64 { return &v }

func refFeature(id string, cfp, n int, h float64, status string) []*ticket.Ticket {
	out := []*ticket.Ticket{{ID: id, Tags: []string{fmt.Sprintf("cfp:%d", cfp)}}}
	for i := 0; i < n; i++ {
		out = append(out, &ticket.Ticket{
			ID: fmt.Sprintf("%s-%d", id, i), Status: status,
			Tags:        []string{"parent:" + id, ticket.ClassFunctional},
			TimeEntries: te(h),
		})
	}
	return out
}

// TestReferenceRates applies the done rule and the minimum-CFP filter.
func TestReferenceRates(t *testing.T) {
	var tickets []*ticket.Ticket
	tickets = append(tickets, refFeature("A", 10, 2, 1, "dev_complete")...) // 2h/10 = 0.2 ✓
	tickets = append(tickets, refFeature("B", 4, 1, 2, "closed")...)        // 0.5 ✓
	tickets = append(tickets, refFeature("C", 2, 1, 2, "complete")...)      // under min CFP ✗
	tickets = append(tickets, refFeature("D", 10, 1, 3, "in_progress")...)  // not done ✗
	tickets = append(tickets, refFeature("E", 10, 0, 0, "complete")...)     // no functional hours ✗
	// Mixed: one done, one in rework → not done.
	tickets = append(tickets, refFeature("G", 10, 1, 1, "approved")...)
	tickets = append(tickets, &ticket.Ticket{ID: "G-x", Status: "rework", Tags: []string{"parent:G", ticket.ClassFunctional}, TimeEntries: te(1)})

	rates := ReferenceRates(tickets, 3)
	if len(rates) != 2 {
		t.Fatalf("rates = %v, want [0.2 0.5]", rates)
	}
	sum := rates[0] + rates[1]
	if !approx(sum, 0.7) {
		t.Errorf("rates = %v, want 0.2 and 0.5", rates)
	}
}

func TestFeatureFunctionalDone(t *testing.T) {
	self := &ticket.Ticket{ID: "S", Status: "qa_testing", Tags: []string{"cfp:5", ticket.ClassFunctional}}
	if !FeatureFunctionalDone(self, []*ticket.Ticket{self}) {
		t.Error("self-contained functional feature in qa_testing should be done")
	}
	self.Status = "blocked"
	if FeatureFunctionalDone(self, []*ticket.Ticket{self}) {
		t.Error("blocked self-contained feature should not be done")
	}
	empty := &ticket.Ticket{ID: "E", Tags: []string{"cfp:5"}}
	if FeatureFunctionalDone(empty, []*ticket.Ticket{empty}) {
		t.Error("a feature with no functional tickets is not done")
	}
}

// TestGatherReferenceFeatures covers own + borrowed pools, "all" excluding the
// current project, specific ids, dedupe, missing ids, and the median fallback.
func TestGatherReferenceFeatures(t *testing.T) {
	// Fake projects: each root maps to its tickets.
	mk := func(rates ...float64) []*ticket.Ticket {
		var out []*ticket.Ticket
		for i, r := range rates {
			out = append(out, refFeature(fmt.Sprintf("F%d", i), 10, 1, r*10, "complete")...)
		}
		return out
	}
	data := map[string][]*ticket.Ticket{
		"/p/self":  mk(0.1, 0.2),
		"/p/other": mk(0.3, 0.4, 0.5),
		"/p/third": mk(0.9),
	}
	read := func(root string) ([]*ticket.Ticket, error) {
		if tk, ok := data[root]; ok {
			return tk, nil
		}
		return nil, fmt.Errorf("no such project")
	}
	projects := []config.ProjectInfo{
		{ID: "self", Path: "/p/self"},
		{ID: "other", Path: "/p/other"},
		{ID: "third", Path: "/p/third"},
	}
	resolve := func(id string) (string, error) { return "", fmt.Errorf("Project not found: %s", id) }

	// No saved inputs → defaults: manual baseline (Agentic) + own features;
	// with 2 own features (< 3) the median is the manual Likely.
	rs := gatherReferenceFeatures("self", "/p/self", &ticket.ProjectConfig{}, projects, resolve, read)
	if !rs.Configured || rs.Manual == nil || *rs.Manual != DefaultManualBaseline() || rs.NOwn != 2 ||
		rs.NBorrowed != 0 || rs.MedianRate() != 0.25 || rs.MinCFP != DefaultEstimateMinCFP {
		t.Errorf("no saved inputs: %+v median %v, want manual agentic + own", rs, rs.MedianRate())
	}

	// Explicitly unticked everything → not configured, default rate.
	off := false
	rs = gatherReferenceFeatures("self", "/p/self", &ticket.ProjectConfig{EstimateRefManual: &off}, projects, resolve, read)
	if rs.Configured || rs.Manual != nil || rs.NOwn != 0 || rs.MedianRate() != DefaultHPerCFP {
		t.Errorf("unticked: %+v median %v, want unconfigured/default", rs, rs.MedianRate())
	}

	// Manual (Traditional) + own: < 3 real features → the manual Likely.
	on := true
	trad := ticket.ManualBaseline{Low: 8, Likely: 12, High: 18}
	rs = gatherReferenceFeatures("self", "/p/self", &ticket.ProjectConfig{EstimateRefManual: &on, EstimateManual: &trad, EstimateRefOwn: true}, projects, resolve, read)
	if rs.Manual == nil || *rs.Manual != trad || rs.MedianRate() != 12 {
		t.Errorf("manual traditional: %+v median %v, want 12", rs, rs.MedianRate())
	}
	// Manual + all: 4 real features → their median, not the manual Likely.
	rs = gatherReferenceFeatures("self", "/p/self", &ticket.ProjectConfig{EstimateRefManual: &on, EstimateManual: &trad, EstimateRefAll: true}, projects, resolve, read)
	if !approx(rs.MedianRate(), 0.45) {
		t.Errorf("manual + all median = %v, want 0.45", rs.MedianRate())
	}

	// Own only: 2 features (< 3) → still the default.
	rs = gatherReferenceFeatures("self", "/p/self", &ticket.ProjectConfig{EstimateRefOwn: true}, projects, resolve, read)
	if rs.NOwn != 2 || rs.NBorrowed != 0 || rs.MedianRate() != DefaultHPerCFP {
		t.Errorf("own only: own=%d borrowed=%d median=%v, want 2/0/default", rs.NOwn, rs.NBorrowed, rs.MedianRate())
	}

	// All: borrows other + third, never self.
	rs = gatherReferenceFeatures("self", "/p/self", &ticket.ProjectConfig{EstimateRefAll: true}, projects, resolve, read)
	if rs.NOwn != 0 || rs.NBorrowed != 4 || len(rs.BorrowedProjects) != 2 {
		t.Errorf("all: own=%d borrowed=%d projects=%v, want 0/4/[other third]", rs.NOwn, rs.NBorrowed, rs.BorrowedProjects)
	}
	if !approx(rs.MedianRate(), 0.45) { // median of .3 .4 .5 .9
		t.Errorf("all median = %v, want 0.45", rs.MedianRate())
	}

	// Specific + own, with a duplicate id, the current project, and a missing id.
	min := 3
	cfg := &ticket.ProjectConfig{EstimateRefOwn: true, EstimateRefProjects: []string{"other", "other", "self", "ghost"}, EstimateMinCFP: &min}
	rs = gatherReferenceFeatures("self", "/p/self", cfg, projects, resolve, read)
	if rs.NOwn != 2 || rs.NBorrowed != 3 {
		t.Errorf("specific: own=%d borrowed=%d, want 2/3", rs.NOwn, rs.NBorrowed)
	}
	if len(rs.MissingProjects) != 1 || rs.MissingProjects[0] != "ghost" {
		t.Errorf("missing = %v, want [ghost]", rs.MissingProjects)
	}
	if !approx(rs.MedianRate(), 0.3) { // median of .1 .2 .3 .4 .5
		t.Errorf("specific median = %v, want 0.3", rs.MedianRate())
	}

	// Min CFP above every feature → nothing qualifies → default.
	big := 50
	rs = gatherReferenceFeatures("self", "/p/self", &ticket.ProjectConfig{EstimateRefAll: true, EstimateMinCFP: &big}, projects, resolve, read)
	if rs.NBorrowed != 0 || rs.MedianRate() != DefaultHPerCFP {
		t.Errorf("min cfp 50: borrowed=%d median=%v, want 0/default", rs.NBorrowed, rs.MedianRate())
	}
}

// ProjectRefMedianRate uses the manual Likely when fewer than 3 real reference
// features exist and the baseline is in effect; without it, the default.
func TestProjectRefMedianRateManual(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_DIR", root+"/no-git")
	cfg := ticket.DefaultConfig("c", "P", "P", "P")
	write := func() {
		if err := ticket.WriteConfig(root, cfg); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if got := ProjectRefMedianRate(root); got != 0.25 { // default: manual agentic
		t.Errorf("no saved inputs: %v, want 0.25", got)
	}
	on, off := true, false
	cfg.EstimateRefManual = &on
	cfg.EstimateManual = &ticket.ManualBaseline{Low: 8, Likely: 12, High: 18}
	write()
	if got := ProjectRefMedianRate(root); got != 12 {
		t.Errorf("manual traditional: %v, want 12", got)
	}
	cfg.EstimateRefManual = &off
	cfg.EstimateRefOwn = true
	write()
	if got := ProjectRefMedianRate(root); got != DefaultHPerCFP {
		t.Errorf("manual off: %v, want default", got)
	}
}
