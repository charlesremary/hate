// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"testing"

	"hate/internal/ticket"
)

func tkt(id string, tags []string, hours ...float64) *ticket.Ticket {
	var entries []ticket.TimeEntry
	for _, h := range hours {
		entries = append(entries, ticket.TimeEntry{Hours: h})
	}
	return &ticket.Ticket{ID: id, Title: id, Tags: tags, TimeEntries: entries}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestComputeCosmic(t *testing.T) {
	tickets := []*ticket.Ticket{
		tkt("P", []string{"cfp:10"}),                              // feature, CFP 10
		tkt("c1", []string{"parent:P", "functional"}, 20),         // functional 20h
		tkt("c2", []string{"parent:P", "functional"}, 6, 4),       // functional 10h
		tkt("c3", []string{"parent:P", "config"}, 6),              // config 6h
		tkt("c4", []string{"parent:P", "nonfunc"}, 4),             // nonfunc 4h
		tkt("c5", []string{"parent:P"}, 2),                        // unclassed 2h
		tkt("orphan", []string{"functional"}, 99),                 // no parent — ignored
	}

	rep := ComputeCosmic(tickets)

	if len(rep.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(rep.Features))
	}
	f := rep.Features[0]
	if f.CFP != 10 {
		t.Errorf("cfp = %d, want 10", f.CFP)
	}
	if !approx(f.FunctionalHours, 30) {
		t.Errorf("functional = %v, want 30", f.FunctionalHours)
	}
	if !approx(f.ConfigHours, 6) || !approx(f.NonfuncHours, 4) || !approx(f.UnclassedHours, 2) {
		t.Errorf("class hours = config %v nonfunc %v unclassed %v", f.ConfigHours, f.NonfuncHours, f.UnclassedHours)
	}
	if f.HPerCFP == nil || !approx(*f.HPerCFP, 3.0) {
		t.Errorf("h/CFP = %v, want 3.0", f.HPerCFP)
	}
	if f.WrapPct == nil || !approx(*f.WrapPct, 100.0*10/30) {
		t.Errorf("wrap = %v, want 33.33", f.WrapPct)
	}

	a := rep.Aggregate
	if a.TotalCFP != 10 || a.N != 1 {
		t.Errorf("aggregate totalCFP=%d n=%d", a.TotalCFP, a.N)
	}
	if a.HPerCFP == nil || !approx(*a.HPerCFP, 3.0) {
		t.Errorf("aggregate h/CFP = %v, want 3.0", a.HPerCFP)
	}
	if a.HPerCFPMedian == nil || !approx(*a.HPerCFPMedian, 3.0) {
		t.Errorf("median = %v, want 3.0", a.HPerCFPMedian)
	}
}

func TestComputeCosmicEmptyAndInvalid(t *testing.T) {
	tickets := []*ticket.Ticket{
		tkt("x", []string{"cfp:0"}),     // invalid CFP — skipped
		tkt("y", []string{"cfp:abc"}),   // unparseable — skipped
		tkt("z", []string{"functional"}), // no cfp — not a feature
	}
	rep := ComputeCosmic(tickets)
	if len(rep.Features) != 0 || rep.Aggregate.FeatureCount != 0 {
		t.Errorf("expected no features, got %d", len(rep.Features))
	}
	if rep.Aggregate.HPerCFP != nil {
		t.Errorf("expected nil aggregate rate, got %v", rep.Aggregate.HPerCFP)
	}
}
