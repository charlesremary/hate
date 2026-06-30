// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/catalog"
)

func TestComputeEstimate(t *testing.T) {
	cat := &catalog.Catalog{Entries: []catalog.CatalogEntry{
		{Type: "flow", Activity: "configure", Unit: "flow", SeedHours: 0.75},
		{Type: "bot", Activity: "configure", Unit: "bot", SeedHours: 1.5},
	}}
	// Live aggregate: flow has a measured rate (N=3), bot has none.
	avg := 1.0
	live := &WrapAggregate{Rates: []WrapUnitRate{
		{Platform: "connect", Type: "flow", AvgHoursPerUnit: &avg, Tickets: 3},
	}}

	req := EstimateRequest{
		Platform: "connect",
		CFP:      100,
		Items: []EstimateItem{
			{Type: "flow", Count: 2}, // measured 1.0 → 2h
			{Type: "bot", Count: 1},  // seed 1.5 → 1.5h
			{Type: "ghost", Count: 5},// unknown → 0h + warning
		},
	}

	res := ComputeEstimate(req, nil, live, cat, 0.015)

	// code: 100 * 0.015 = 1.5h likely
	if res.CodeHours.Likely != 1.5 {
		t.Errorf("code likely = %v, want 1.5", res.CodeHours.Likely)
	}
	// wrap likely: 2 (flow) + 1.5 (bot) + 0 (ghost) = 3.5
	if res.WrapHours.Likely != 3.5 {
		t.Errorf("wrap likely = %v, want 3.5", res.WrapHours.Likely)
	}
	// total likely = 1.5 + 3.5 = 5.0
	if res.Total.Likely != 5.0 {
		t.Errorf("total likely = %v, want 5.0", res.Total.Likely)
	}
	// range must straddle the likely
	if !(res.Total.Low < res.Total.Likely && res.Total.High > res.Total.Likely) {
		t.Errorf("range doesn't straddle likely: %+v", res.Total)
	}
	// sources: flow measured (N=3), bot seed, ghost unknown
	src := map[string]string{}
	for _, l := range res.WrapLines {
		src[l.Type] = l.Source
	}
	if src["flow"] != "measured" || src["bot"] != "seed" || src["ghost"] != "unknown" {
		t.Errorf("line sources wrong: %v", src)
	}
	// a warning for the unknown type
	foundGhost := false
	for _, wn := range res.Warnings {
		if contains(wn, "ghost") {
			foundGhost = true
		}
	}
	if !foundGhost {
		t.Errorf("expected a warning for the unknown type, got %v", res.Warnings)
	}
}

func TestBandWidensWithLowN(t *testing.T) {
	lo5, hi5 := bandFor(5)
	lo1, hi1 := bandFor(1)
	lo0, hi0 := bandFor(0)
	if !(hi5-lo5 < hi1-lo1 && hi1-lo1 < hi0-lo0) {
		t.Errorf("band should widen as N shrinks: n5=%v..%v n1=%v..%v n0=%v..%v", lo5, hi5, lo1, hi1, lo0, hi0)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
