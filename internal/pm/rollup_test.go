// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

// TestPhaseRollup exercises the effort-weighted percentage, cancelled-ticket
// exclusion, the no-effort count fallback, date rollup, and the "(no phase)"
// bucket — the decisions a stakeholder-facing number depends on.
func TestPhaseRollup(t *testing.T) {
	xl, m, s := "xl", "m", "s"
	cancel := "duplicate of AMPL-0001"

	tickets := []*ticket.Ticket{
		// Phase "01 - Build": effort-weighted. 8 of 13 in-scope effort-days done.
		{ID: "B1", Phase: strp("01 - Build"), Effort: &xl, Status: "complete",
			PlannedStartDate: strp("2026-01-05"), DueDate: strp("2026-01-20")},
		{ID: "B2", Phase: strp("01 - Build"), Effort: &m, Status: "in_progress",
			PlannedStartDate: strp("2026-01-10"), DueDate: strp("2026-02-01")},
		{ID: "B3", Phase: strp("01 - Build"), Effort: &s, Status: "not_started"},
		// Force-closed → descoped: excluded from scope and percentage.
		{ID: "B4", Phase: strp("01 - Build"), Effort: strp("l"), Status: "closed",
			CancellationReason: &cancel},

		// Phase "02 - QA": no effort sizes anywhere → count-based fallback.
		{ID: "Q1", Phase: strp("02 - QA"), Status: "complete"},
		{ID: "Q2", Phase: strp("02 - QA"), Status: "not_started"},

		// No phase: a blocked, sized ticket → "(no phase)" bucket, sorts last.
		{ID: "X1", Effort: &s, Status: "blocked"},
	}

	rep := PhaseRollup(tickets, ticket.DefaultEffortToDays)

	if rep.Basis != "effort-weighted" {
		t.Errorf("basis = %q, want effort-weighted", rep.Basis)
	}
	if len(rep.Phases) != 3 {
		t.Fatalf("got %d phases, want 3: %+v", len(rep.Phases), rep.Phases)
	}

	// Phase 1 — effort-weighted, cancelled excluded.
	p := rep.Phases[0]
	if p.Phase != "01 - Build" {
		t.Fatalf("phases[0] = %q, want '01 - Build' (sort order wrong)", p.Phase)
	}
	if !p.EffortBased || p.PercentComplete != 61.5 {
		t.Errorf("phase 1: effortBased=%v pct=%v, want true / 61.5 (8 of 13 effort-days)",
			p.EffortBased, p.PercentComplete)
	}
	if p.TicketCount != 3 || p.CompleteCount != 1 || p.InProgressCount != 1 ||
		p.NotStartedCount != 1 || p.CancelledCount != 1 {
		t.Errorf("phase 1 counts: total=%d done=%d wip=%d todo=%d cancelled=%d, want 3/1/1/1/1",
			p.TicketCount, p.CompleteCount, p.InProgressCount, p.NotStartedCount, p.CancelledCount)
	}
	if p.TotalEffortDays != 13 || p.DoneEffortDays != 8 {
		t.Errorf("phase 1 effort: done=%g total=%g, want 8/13", p.DoneEffortDays, p.TotalEffortDays)
	}
	if p.PlannedStart != "2026-01-05" || p.DueDate != "2026-02-01" {
		t.Errorf("phase 1 dates: start=%q due=%q, want 2026-01-05 → 2026-02-01", p.PlannedStart, p.DueDate)
	}

	// Phase 2 — no effort sizes → count-based fallback.
	q := rep.Phases[1]
	if q.Phase != "02 - QA" {
		t.Fatalf("phases[1] = %q, want '02 - QA'", q.Phase)
	}
	if q.EffortBased {
		t.Errorf("phase 2 should be count-based (no effort sizes)")
	}
	if q.PercentComplete != 50 || q.NoEffortCount != 2 || q.TicketCount != 2 {
		t.Errorf("phase 2: pct=%v noEffort=%d total=%d, want 50 / 2 / 2",
			q.PercentComplete, q.NoEffortCount, q.TicketCount)
	}

	// No-phase bucket — labeled and last.
	x := rep.Phases[2]
	if x.Phase != "" || x.Label != "(no phase)" {
		t.Errorf("phases[2]: phase=%q label=%q, want '' / '(no phase)'", x.Phase, x.Label)
	}
	if x.BlockedCount != 1 || x.PercentComplete != 0 {
		t.Errorf("no-phase: blocked=%d pct=%v, want 1 / 0", x.BlockedCount, x.PercentComplete)
	}

	// Project totals — effort-weighted across phases that have effort (8 of 15).
	if rep.TotalTickets != 6 {
		t.Errorf("total tickets = %d, want 6 (cancelled excluded)", rep.TotalTickets)
	}
	if rep.PercentComplete != 53.3 {
		t.Errorf("project pct = %v, want 53.3 (8 of 15 effort-days)", rep.PercentComplete)
	}
}
