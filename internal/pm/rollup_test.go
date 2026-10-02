// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

// TestPhaseRollup exercises the hours-weighted percentage, cancelled-ticket
// exclusion, the unsized count fallback, date rollup, and the "(no phase)"
// bucket — the decisions a stakeholder-facing number depends on.
func TestPhaseRollup(t *testing.T) {
	m := "m" // legacy: 3 days = 24h
	cancel := "duplicate of AMPL-0001"
	wrap := []string{ticket.ClassConfig}

	tickets := []*ticket.Ticket{
		// Phase "01 - Build": hours-weighted. 64 of 104 in-scope hours done.
		{ID: "B1", Phase: strp("01 - Build"), Tags: wrap, EstimateHours: f64(64), Status: "complete",
			PlannedStartDate: strp("2026-01-05"), DueDate: strp("2026-01-20")},
		{ID: "B2", Phase: strp("01 - Build"), Effort: &m, Status: "in_progress", // legacy unclassed → 24h
			PlannedStartDate: strp("2026-01-10"), DueDate: strp("2026-02-01")},
		{ID: "B3", Phase: strp("01 - Build"), Tags: wrap, EstimateHours: f64(16), Status: "not_started"},
		// Force-closed → descoped: excluded from scope and percentage.
		{ID: "B4", Phase: strp("01 - Build"), Tags: wrap, EstimateHours: f64(40), Status: "closed",
			CancellationReason: &cancel},

		// Phase "02 - QA": no estimates anywhere → count-based fallback.
		{ID: "Q1", Phase: strp("02 - QA"), Status: "complete"},
		{ID: "Q2", Phase: strp("02 - QA"), Status: "not_started"},
		// A meeting is never sized, so it doesn't count as unsized.
		{ID: "Q3", Phase: strp("02 - QA"), Type: "meeting", Status: "complete"},

		// No phase: a blocked, sized ticket → "(no phase)" bucket, sorts last.
		{ID: "X1", Tags: wrap, EstimateHours: f64(16), Status: "blocked"},
	}

	rep := PhaseRollup(tickets, NewEstimateContext(tickets, 0, nil))

	if rep.Basis != "hours-weighted" {
		t.Errorf("basis = %q, want hours-weighted", rep.Basis)
	}
	if len(rep.Phases) != 3 {
		t.Fatalf("got %d phases, want 3: %+v", len(rep.Phases), rep.Phases)
	}

	// Phase 1 — hours-weighted, cancelled excluded.
	p := rep.Phases[0]
	if p.Phase != "01 - Build" {
		t.Fatalf("phases[0] = %q, want '01 - Build' (sort order wrong)", p.Phase)
	}
	if !p.HoursBased || p.PercentComplete != 61.5 {
		t.Errorf("phase 1: hoursBased=%v pct=%v, want true / 61.5 (64 of 104 hours)",
			p.HoursBased, p.PercentComplete)
	}
	if p.TicketCount != 3 || p.CompleteCount != 1 || p.InProgressCount != 1 ||
		p.NotStartedCount != 1 || p.CancelledCount != 1 {
		t.Errorf("phase 1 counts: total=%d done=%d wip=%d todo=%d cancelled=%d, want 3/1/1/1/1",
			p.TicketCount, p.CompleteCount, p.InProgressCount, p.NotStartedCount, p.CancelledCount)
	}
	if p.EstHoursTotal != 104 || p.EstHoursDone != 64 || p.UnsizedCount != 0 {
		t.Errorf("phase 1 hours: done=%g total=%g unsized=%d, want 64/104/0", p.EstHoursDone, p.EstHoursTotal, p.UnsizedCount)
	}
	if p.PlannedStart != "2026-01-05" || p.DueDate != "2026-02-01" {
		t.Errorf("phase 1 dates: start=%q due=%q, want 2026-01-05 → 2026-02-01", p.PlannedStart, p.DueDate)
	}

	// Phase 2 — no estimates → count-based fallback.
	q := rep.Phases[1]
	if q.Phase != "02 - QA" {
		t.Fatalf("phases[1] = %q, want '02 - QA'", q.Phase)
	}
	if q.HoursBased {
		t.Errorf("phase 2 should be count-based (no estimates)")
	}
	if q.PercentComplete != 66.7 || q.UnsizedCount != 2 || q.TicketCount != 3 {
		t.Errorf("phase 2: pct=%v unsized=%d total=%d, want 66.7 / 2 / 3",
			q.PercentComplete, q.UnsizedCount, q.TicketCount)
	}

	// No-phase bucket — labeled and last.
	x := rep.Phases[2]
	if x.Phase != "" || x.Label != "(no phase)" {
		t.Errorf("phases[2]: phase=%q label=%q, want '' / '(no phase)'", x.Phase, x.Label)
	}
	if x.BlockedCount != 1 || x.PercentComplete != 0 {
		t.Errorf("no-phase: blocked=%d pct=%v, want 1 / 0", x.BlockedCount, x.PercentComplete)
	}

	// Project totals — hours-weighted across phases that have hours (64 of 120).
	if rep.TotalTickets != 7 {
		t.Errorf("total tickets = %d, want 7 (cancelled excluded)", rep.TotalTickets)
	}
	if rep.PercentComplete != 53.3 {
		t.Errorf("project pct = %v, want 53.3 (64 of 120 hours)", rep.PercentComplete)
	}
}

// TestPhaseRollupFunctionalWeighting: functional children are weighted by their
// share of the parent's CFP x rate; the parent itself carries no hours and isn't
// counted as unsized.
func TestPhaseRollupFunctionalWeighting(t *testing.T) {
	ph := strp("01")
	tickets := []*ticket.Ticket{
		{ID: "F", Phase: ph, Status: "in_progress", Tags: []string{"cfp:40"}},
		{ID: "a", Phase: ph, Status: "complete", Tags: []string{"parent:F", ticket.ClassFunctional}},
		{ID: "b", Phase: ph, Status: "not_started", Tags: []string{"parent:F", ticket.ClassFunctional}},
		{ID: "w", Phase: ph, Status: "not_started", Tags: []string{"parent:F", ticket.ClassConfig}, EstimateHours: f64(10)},
	}
	rep := PhaseRollup(tickets, NewEstimateContext(tickets, 0.25, nil))
	p := rep.Phases[0]
	// 40 CFP x 0.25 = 10h over 2 functional children → 5h each; wrap 10h.
	if p.EstHoursTotal != 20 || p.EstHoursDone != 5 || p.PercentComplete != 25 || p.UnsizedCount != 0 {
		t.Errorf("got total=%g done=%g pct=%v unsized=%d, want 20/5/25/0",
			p.EstHoursTotal, p.EstHoursDone, p.PercentComplete, p.UnsizedCount)
	}
}
