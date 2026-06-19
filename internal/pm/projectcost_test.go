// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

func TestComputeProjectCost(t *testing.T) {
	tickets := []*ticket.Ticket{
		tkt("P", []string{"type:kb-article"}, 1),       // deliverable parent, 1h on itself
		tkt("p1", []string{"parent:P", "functional"}, 5),
		tkt("p2", []string{"parent:P", "config"}, 3),    // P group = 1+5+3 = 9h
		tkt("Q", []string{"type:kb-article"}),           // second kb-article, 0h on parent
		tkt("q1", []string{"parent:Q"}, 6),              // Q group = 6h
		tkt("S", nil, 4),                                // standalone, uncategorized
	}

	pc := ComputeProjectCost(tickets)

	if len(pc.Types) != 1 {
		t.Fatalf("expected 1 deliverable type, got %d", len(pc.Types))
	}
	d := pc.Types[0]
	if d.Type != "kb-article" || d.Count != 2 {
		t.Errorf("got type=%s count=%d, want kb-article/2", d.Type, d.Count)
	}
	if !approx(d.Hours, 15) {
		t.Errorf("kb-article hours = %v, want 15", d.Hours)
	}
	if !approx(d.AvgPerUnit, 7.5) {
		t.Errorf("avg/unit = %v, want 7.5", d.AvgPerUnit)
	}
	if !approx(pc.UncategorizedHours, 4) {
		t.Errorf("uncategorized = %v, want 4", pc.UncategorizedHours)
	}
	if !approx(pc.TotalHours, 19) {
		t.Errorf("total = %v, want 19", pc.TotalHours)
	}
}
