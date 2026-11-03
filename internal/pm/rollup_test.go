// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

// TestRollupLifecycle exercises the hours-weighted percentage, cancelled-ticket
// exclusion, the unsized count fallback, date rollup, and the "(no lifecycle)"
// bucket — the decisions a stakeholder-facing number depends on.
func TestRollupLifecycle(t *testing.T) {
	m := "m" // legacy: 3 days = 24h
	cancel := "duplicate of AMPL-0001"
	wrap := []string{ticket.ClassConfig}

	tickets := []*ticket.Ticket{
		// Phase "01 - Build": hours-weighted. 64 of 104 in-scope hours done.
		{ID: "B1", Lifecycle: strp("01 - Build"), Tags: wrap, EstimateHours: f64(64), Status: "complete",
			PlannedStartDate: strp("2026-01-05"), DueDate: strp("2026-01-20")},
		{ID: "B2", Lifecycle: strp("01 - Build"), Effort: &m, Status: "in_progress", // legacy unclassed → 24h
			PlannedStartDate: strp("2026-01-10"), DueDate: strp("2026-02-01")},
		{ID: "B3", Lifecycle: strp("01 - Build"), Tags: wrap, EstimateHours: f64(16), Status: "not_started"},
		// Force-closed → descoped: excluded from scope and percentage.
		{ID: "B4", Lifecycle: strp("01 - Build"), Tags: wrap, EstimateHours: f64(40), Status: "closed",
			CancellationReason: &cancel},

		// Phase "02 - QA": no estimates anywhere → count-based fallback.
		{ID: "Q1", Lifecycle: strp("02 - QA"), Status: "complete"},
		{ID: "Q2", Lifecycle: strp("02 - QA"), Status: "not_started"},
		// A meeting is never sized, so it doesn't count as unsized.
		{ID: "Q3", Lifecycle: strp("02 - QA"), Type: "meeting", Status: "complete"},

		// No phase: a blocked, sized ticket → "(no lifecycle)" bucket, sorts last.
		{ID: "X1", Tags: wrap, EstimateHours: f64(16), Status: "blocked"},
	}

	rep := Rollup(tickets, NewEstimateContext(tickets, 0, nil), RollupByLifecycle)

	if rep.Basis != "hours-weighted" {
		t.Errorf("basis = %q, want hours-weighted", rep.Basis)
	}
	if len(rep.Groups) != 3 {
		t.Fatalf("got %d phases, want 3: %+v", len(rep.Groups), rep.Groups)
	}

	// Phase 1 — hours-weighted, cancelled excluded.
	p := rep.Groups[0]
	if p.Key != "01 - Build" {
		t.Fatalf("phases[0] = %q, want '01 - Build' (sort order wrong)", p.Key)
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
	q := rep.Groups[1]
	if q.Key != "02 - QA" {
		t.Fatalf("phases[1] = %q, want '02 - QA'", q.Key)
	}
	if q.HoursBased {
		t.Errorf("phase 2 should be count-based (no estimates)")
	}
	if q.PercentComplete != 66.7 || q.UnsizedCount != 2 || q.TicketCount != 3 {
		t.Errorf("phase 2: pct=%v unsized=%d total=%d, want 66.7 / 2 / 3",
			q.PercentComplete, q.UnsizedCount, q.TicketCount)
	}

	// No-phase bucket — labeled and last.
	x := rep.Groups[2]
	if x.Key != "" || x.Label != "(no lifecycle)" {
		t.Errorf("phases[2]: phase=%q label=%q, want '' / '(no lifecycle)'", x.Key, x.Label)
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

// TestRollupLifecycleFunctionalWeighting: functional children are weighted by their
// share of the parent's CFP x rate; the parent itself carries no hours and isn't
// counted as unsized.
func TestRollupLifecycleFunctionalWeighting(t *testing.T) {
	ph := strp("01")
	tickets := []*ticket.Ticket{
		{ID: "F", Lifecycle: ph, Status: "in_progress", Tags: []string{"cfp:40"}},
		{ID: "a", Lifecycle: ph, Status: "complete", Tags: []string{"parent:F", ticket.ClassFunctional}},
		{ID: "b", Lifecycle: ph, Status: "not_started", Tags: []string{"parent:F", ticket.ClassFunctional}},
		{ID: "w", Lifecycle: ph, Status: "not_started", Tags: []string{"parent:F", ticket.ClassConfig}, EstimateHours: f64(10)},
	}
	rep := Rollup(tickets, NewEstimateContext(tickets, 0.25, nil), RollupByLifecycle)
	p := rep.Groups[0]
	// 40 CFP x 0.25 = 10h over 2 functional children → 5h each; wrap 10h.
	if p.EstHoursTotal != 20 || p.EstHoursDone != 5 || p.PercentComplete != 25 || p.UnsizedCount != 0 {
		t.Errorf("got total=%g done=%g pct=%v unsized=%d, want 20/5/25/0",
			p.EstHoursTotal, p.EstHoursDone, p.PercentComplete, p.UnsizedCount)
	}
}

// HATE-wgxl: by block, groups sort by block number (Block 10 after Block 9),
// "no block" last, and carry their block's dates and dated label.
func TestRollupByBlock(t *testing.T) {
	wrap := []string{ticket.ClassConfig}
	tickets := []*ticket.Ticket{
		{ID: "a", Block: 10, Tags: wrap, EstimateHours: f64(1), Status: "not_started"},
		{ID: "b", Block: 9, Lifecycle: strp("Build"), Tags: wrap, EstimateHours: f64(1), Status: "not_started"},
		{ID: "c", Block: 2, Tags: wrap, EstimateHours: f64(1), Status: "complete"},
		{ID: "d", Lifecycle: strp("Admin"), Tags: wrap, EstimateHours: f64(1), Status: "not_started"},
	}
	rep := Rollup(tickets, NewEstimateContext(tickets, 0, nil), RollupByBlock)
	if rep.By != RollupByBlock {
		t.Errorf("by = %q", rep.By)
	}
	var got []int
	for _, g := range rep.Groups {
		got = append(got, g.Block)
	}
	if len(got) != 4 || got[0] != 2 || got[1] != 9 || got[2] != 10 || got[3] != 0 {
		t.Fatalf("blocks = %v, want [2 9 10 0]", got)
	}
	if rep.Groups[3].Label != "(no block)" || rep.Groups[0].Label != "Block 02" {
		t.Errorf("labels %q %q", rep.Groups[0].Label, rep.Groups[3].Label)
	}
	ApplyBlockDates(&rep, BuildBlocks(2, day("2026-11-02"), day("2026-11-20")))
	if g := rep.Groups[0]; g.BlockStart != "2026-11-16" || g.BlockEnd != "2026-11-27" || g.Label != "Block 02 (Nov 16-27)" {
		t.Errorf("Block 2 = %s..%s %q", g.BlockStart, g.BlockEnd, g.Label)
	}
	if g := rep.Groups[2]; g.BlockStart != "" {
		t.Errorf("Block 10 is past the block list, got dates %s", g.BlockStart)
	}
	// By lifecycle, the same tickets group by kind of work.
	lc := Rollup(tickets, NewEstimateContext(tickets, 0, nil), RollupByLifecycle)
	if len(lc.Groups) != 3 || lc.Groups[0].Key != "Admin" || lc.Groups[1].Key != "Build" || lc.Groups[2].Key != "" {
		t.Errorf("lifecycle groups = %+v", lc.Groups)
	}
}
