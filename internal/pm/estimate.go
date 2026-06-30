// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"sort"

	"hate/internal/catalog"
)

// Wrap-based estimator (HATE-y1wn).
//
//	code hours = CFP × code_constant                         (author — the constant slice)
//	wrap hours = Σ (count × hours-per-unit)                  (operate + configure, from catalog/profile)
//	total      = code + wrap, reported as a low/likely/high range
//
// Rates come from a chosen profile (or the live aggregate); when a type has no
// measured rate the catalog seed is used. The range widens as the sample (N)
// shrinks — a seed-only line is far less trustworthy than one with N≥5, and the
// estimate should say so rather than hide it behind a single number.

// EstimateItem is one line of the bill-of-materials.
type EstimateItem struct {
	Type  string  `json:"type"`
	Count float64 `json:"count"`
}

// EstimateRequest is the estimator input.
type EstimateRequest struct {
	Profile  string         `json:"profile"`  // optional saved-profile name
	Platform string         `json:"platform"` // platform for rate lookup
	CFP      int            `json:"cfp"`      // total author-code CFP
	Items    []EstimateItem `json:"items"`    // wrap bill-of-materials
}

// Range is a low/likely/high triple.
type Range struct {
	Low    float64 `json:"low"`
	Likely float64 `json:"likely"`
	High   float64 `json:"high"`
}

// EstimateLine is the costed result for one BoM item.
type EstimateLine struct {
	Type         string  `json:"type"`
	Activity     string  `json:"activity"`
	Count        float64 `json:"count"`
	HoursPerUnit float64 `json:"hours_per_unit"`
	Source       string  `json:"source"` // "measured" | "seed" | "unknown"
	N            int     `json:"n"`      // sample size behind a measured rate
	Low          float64 `json:"low"`
	Likely       float64 `json:"likely"`
	High         float64 `json:"high"`
}

// EstimateResult is the full estimate.
type EstimateResult struct {
	CFP          int            `json:"cfp"`
	CodeConstant float64        `json:"code_constant"`
	Platform     string         `json:"platform"`
	ProfileName  string         `json:"profile_name"` // "" when computed off the live aggregate
	CodeHours    Range          `json:"code_hours"`
	WrapHours    Range          `json:"wrap_hours"`
	WrapLines    []EstimateLine `json:"wrap_lines"`
	Total        Range          `json:"total"`
	Warnings     []string       `json:"warnings"`
}

// bandFor returns the (low, high) multipliers for a line given its sample size.
// Seed-only (n=0) is widest; the band tightens as N grows.
func bandFor(n int) (float64, float64) {
	switch {
	case n >= 5:
		return 0.85, 1.15
	case n >= 3:
		return 0.75, 1.25
	case n >= 1:
		return 0.6, 1.4
	default: // seed only — a guess
		return 0.4, 1.8
	}
}

// rateLookup finds the measured (avg, n) for a (platform, type) from a profile or
// the live aggregate; ok=false when there's no measured rate.
func rateLookup(prof *Profile, live *WrapAggregate, platform, typ string) (avg float64, n int, ok bool) {
	var r *WrapUnitRate
	if prof != nil {
		r = prof.RateFor(platform, typ)
	} else if live != nil {
		for i := range live.Rates {
			if live.Rates[i].Platform == platform && live.Rates[i].Type == typ {
				r = &live.Rates[i]
				break
			}
		}
	}
	if r == nil || r.AvgHoursPerUnit == nil {
		return 0, 0, false
	}
	return *r.AvgHoursPerUnit, r.Tickets, true
}

// ComputeEstimate produces a low/likely/high estimate from CFP + a wrap BoM.
func ComputeEstimate(req EstimateRequest, prof *Profile, live *WrapAggregate, cat *catalog.Catalog, constant float64) EstimateResult {
	res := EstimateResult{
		CFP:      req.CFP,
		Platform: req.Platform,
		Warnings: []string{},
	}

	codeConstant := constant
	if prof != nil {
		codeConstant = prof.CodeConstant
		res.ProfileName = prof.Name
	}
	res.CodeConstant = codeConstant

	// Code (author) slice. The constant is unvalidated (HATE-xd99), so band it.
	codeHours := float64(req.CFP) * codeConstant
	res.CodeHours = Range{Low: codeHours * 0.5, Likely: codeHours, High: codeHours * 1.5}
	if req.CFP > 0 {
		res.Warnings = append(res.Warnings, "code constant is an unvalidated seed (HATE-xd99) — verify against the measured COSMIC h/CFP spread")
	}

	catByType := map[string]catalog.CatalogEntry{}
	if cat != nil {
		for _, e := range cat.Entries {
			catByType[e.Type] = e
		}
	}

	var wrapLow, wrapLikely, wrapHigh float64
	thin := 0
	for _, item := range req.Items {
		if item.Count <= 0 {
			continue
		}
		line := EstimateLine{Type: item.Type, Count: item.Count}
		entry, inCat := catByType[item.Type]
		if inCat {
			line.Activity = entry.Activity
		}

		if avg, n, ok := rateLookup(prof, live, req.Platform, item.Type); ok {
			line.HoursPerUnit = avg
			line.Source = "measured"
			line.N = n
			if n < 3 {
				thin++
			}
		} else if inCat {
			seed := entry.SeedHours
			if ps, ok := entry.PlatformSeedHours[req.Platform]; ok {
				seed = ps
			}
			line.HoursPerUnit = seed
			line.Source = "seed"
		} else {
			line.Source = "unknown"
			res.Warnings = append(res.Warnings, fmt.Sprintf("no catalog entry or measured rate for %q — counted as 0h", item.Type))
		}

		base := item.Count * line.HoursPerUnit
		lo, hi := bandFor(line.N)
		if line.Source == "seed" {
			lo, hi = bandFor(0)
		}
		line.Likely = base
		line.Low = base * lo
		line.High = base * hi

		wrapLikely += line.Likely
		wrapLow += line.Low
		wrapHigh += line.High
		res.WrapLines = append(res.WrapLines, line)
	}

	sort.SliceStable(res.WrapLines, func(i, j int) bool { return res.WrapLines[i].Type < res.WrapLines[j].Type })

	res.WrapHours = Range{Low: wrapLow, Likely: wrapLikely, High: wrapHigh}
	res.Total = Range{
		Low:    res.CodeHours.Low + wrapLow,
		Likely: res.CodeHours.Likely + wrapLikely,
		High:   res.CodeHours.High + wrapHigh,
	}
	if thin > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d wrap line(s) rest on a thin sample (N<3) — the range is wide on purpose", thin))
	}
	if req.Platform == "" {
		res.Warnings = append(res.Warnings, "no platform given — measured rates are keyed by platform, so only seeds will match")
	}
	if res.WrapLines == nil {
		res.WrapLines = []EstimateLine{}
	}
	return res
}
