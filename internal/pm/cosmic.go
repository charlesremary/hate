// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"sort"
	"strconv"
	"strings"

	"hate/internal/ticket"
)

// COSMIC functional-size calibration (experimental).
//
// Convention (all via tags — no schema change):
//   - A "feature" is a ticket tagged `cfp:<N>`. The COSMIC size lives ONLY on
//     the parent — never copy it to children, or size double-counts.
//   - Its children are tickets tagged `parent:<feature-id>`.
//   - Each child is classed `functional`, `config`, or `nonfunc`. Unclassed
//     children are surfaced as a data-quality warning.
//   - Hours come from each child's time entries. Hours logged on the parent
//     itself violate "parent carries size, children carry hours" and are
//     reported separately.
//
// Observed functional pace = Σ(functional child hours) ÷ feature CFP.
// Empirical wrap = (config + nonfunc hours) ÷ functional hours.

const (
	cfpTagPrefix    = "cfp:"
	parentTagPrefix = "parent:"
	classFunctional = "functional"
	classConfig     = "config"
	classNonfunc    = "nonfunc"
)

// CosmicAssumed holds the borrowed-industry knobs we're trying to replace with
// our own measured numbers.
type CosmicAssumed struct {
	BandLow  float64 `json:"band_low"`
	BandMid  float64 `json:"band_mid"`
	BandHigh float64 `json:"band_high"`
	WrapPct  float64 `json:"wrap_pct"`
}

// CosmicFeature is the per-parent rollup.
type CosmicFeature struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	CFP             int      `json:"cfp"`
	ChildCount      int      `json:"child_count"`
	FunctionalHours float64  `json:"functional_hours"`
	ConfigHours     float64  `json:"config_hours"`
	NonfuncHours    float64  `json:"nonfunc_hours"`
	UnclassedHours  float64  `json:"unclassed_hours"`
	ParentHours     float64  `json:"parent_hours"` // hours mistakenly on the parent
	TotalHours      float64  `json:"total_hours"`
	HPerCFP         *float64 `json:"h_per_cfp"` // nil if no functional hours
	WrapPct         *float64 `json:"wrap_pct"`  // nil if no functional hours
}

// CosmicAggregate is the project-level number you actually recalibrate from.
type CosmicAggregate struct {
	FeatureCount    int      `json:"feature_count"`
	TotalCFP        int      `json:"total_cfp"`
	FunctionalHours float64  `json:"functional_hours"`
	ConfigHours     float64  `json:"config_hours"`
	NonfuncHours    float64  `json:"nonfunc_hours"`
	UnclassedHours  float64  `json:"unclassed_hours"`
	ParentHours     float64  `json:"parent_hours"`
	HPerCFP         *float64 `json:"h_per_cfp"`        // Σ functional ÷ Σ cfp
	HPerCFPMedian   *float64 `json:"h_per_cfp_median"` // median of per-feature rates
	HPerCFPMin      *float64 `json:"h_per_cfp_min"`
	HPerCFPMax      *float64 `json:"h_per_cfp_max"`
	N               int      `json:"n"` // features contributing a rate
	WrapPct         *float64 `json:"wrap_pct"`
}

// CosmicEstimate is the manual initial-estimate projection: from the total CFP and
// a borrowed code rate (h/CFP) + wrap %, the projected project hours. The inputs
// are persisted per-project; the hours are computed. Inputs are nil until set.
type CosmicEstimate struct {
	HPerCFP    *float64 `json:"h_per_cfp"`  // input: borrowed code rate
	WrapPct    *float64 `json:"wrap_pct"`   // input: borrowed wrap %
	TotalCFP   int      `json:"total_cfp"`  // from the project's cfp: tags
	CodeHours  float64  `json:"code_hours"` // totalCFP × h/CFP
	WrapHours  float64  `json:"wrap_hours"` // codeHours × wrap%
	TotalHours float64  `json:"total_hours"`
}

// BuildCosmicEstimate projects hours from total CFP and the borrowed rates.
// With no h/CFP set, the hours stay zero (nothing to project from).
func BuildCosmicEstimate(totalCFP int, hPerCFP, wrapPct *float64) CosmicEstimate {
	e := CosmicEstimate{HPerCFP: hPerCFP, WrapPct: wrapPct, TotalCFP: totalCFP}
	if hPerCFP != nil && *hPerCFP > 0 {
		e.CodeHours = float64(totalCFP) * *hPerCFP
		w := 0.0
		if wrapPct != nil && *wrapPct > 0 {
			w = *wrapPct
		}
		e.WrapHours = e.CodeHours * w / 100
		e.TotalHours = e.CodeHours + e.WrapHours
	}
	return e
}

// CosmicReport is the full calibration payload.
type CosmicReport struct {
	Features  []CosmicFeature `json:"features"`
	Aggregate CosmicAggregate `json:"aggregate"`
	Assumed   CosmicAssumed   `json:"assumed"`
	// Estimate is the manual initial-estimate projection (inputs persisted on the
	// project). Populated by the API handler, which has the config.
	Estimate CosmicEstimate `json:"estimate"`
}

func cosmicTagValue(tags []string, prefix string) (string, bool) {
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			return strings.TrimSpace(t[len(prefix):]), true
		}
	}
	return "", false
}

func cosmicClassOf(tags []string) string {
	for _, t := range tags {
		switch t {
		case classFunctional, classConfig, classNonfunc:
			return t
		}
	}
	return ""
}

func cosmicLoggedHours(t *ticket.Ticket) float64 {
	var h float64
	for _, te := range t.TimeEntries {
		h += te.Hours
	}
	return h
}

// ComputeCosmic builds the calibration report from all tickets in a project.
func ComputeCosmic(tickets []*ticket.Ticket) CosmicReport {
	childrenByParent := map[string][]*ticket.Ticket{}
	for _, t := range tickets {
		if pid, ok := cosmicTagValue(t.Tags, parentTagPrefix); ok && pid != "" {
			childrenByParent[pid] = append(childrenByParent[pid], t)
		}
	}

	report := CosmicReport{
		Features: []CosmicFeature{},
		Assumed:  CosmicAssumed{BandLow: 8, BandMid: 12, BandHigh: 18, WrapPct: 60},
	}

	var rates []float64
	var aggF, aggC, aggN, aggU, aggP float64
	var totalCFP int

	for _, t := range tickets {
		cfpStr, ok := cosmicTagValue(t.Tags, cfpTagPrefix)
		if !ok {
			continue
		}
		cfp, err := strconv.Atoi(cfpStr)
		if err != nil || cfp <= 0 {
			continue
		}
		f := CosmicFeature{ID: t.ID, Title: t.Title, CFP: cfp, ParentHours: cosmicLoggedHours(t)}
		for _, c := range childrenByParent[t.ID] {
			h := cosmicLoggedHours(c)
			switch cosmicClassOf(c.Tags) {
			case classFunctional:
				f.FunctionalHours += h
			case classConfig:
				f.ConfigHours += h
			case classNonfunc:
				f.NonfuncHours += h
			default:
				f.UnclassedHours += h
			}
			f.ChildCount++
		}
		f.TotalHours = f.FunctionalHours + f.ConfigHours + f.NonfuncHours + f.UnclassedHours
		if f.FunctionalHours > 0 {
			r := f.FunctionalHours / float64(cfp)
			f.HPerCFP = &r
			rates = append(rates, r)
			w := (f.ConfigHours + f.NonfuncHours) / f.FunctionalHours * 100
			f.WrapPct = &w
		}
		report.Features = append(report.Features, f)

		totalCFP += cfp
		aggF += f.FunctionalHours
		aggC += f.ConfigHours
		aggN += f.NonfuncHours
		aggU += f.UnclassedHours
		aggP += f.ParentHours
	}

	agg := CosmicAggregate{
		FeatureCount:    len(report.Features),
		TotalCFP:        totalCFP,
		FunctionalHours: aggF,
		ConfigHours:     aggC,
		NonfuncHours:    aggN,
		UnclassedHours:  aggU,
		ParentHours:     aggP,
		N:               len(rates),
	}
	if totalCFP > 0 && aggF > 0 {
		v := aggF / float64(totalCFP)
		agg.HPerCFP = &v
	}
	if aggF > 0 {
		w := (aggC + aggN) / aggF * 100
		agg.WrapPct = &w
	}
	if len(rates) > 0 {
		sort.Float64s(rates)
		med := cosmicMedian(rates)
		agg.HPerCFPMedian = &med
		mn, mx := rates[0], rates[len(rates)-1]
		agg.HPerCFPMin = &mn
		agg.HPerCFPMax = &mx
	}
	report.Aggregate = agg

	// Stable order: biggest features first, then id.
	sort.SliceStable(report.Features, func(i, j int) bool {
		if report.Features[i].CFP != report.Features[j].CFP {
			return report.Features[i].CFP > report.Features[j].CFP
		}
		return report.Features[i].ID < report.Features[j].ID
	})

	return report
}

func cosmicMedian(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
