// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"
	"time"

	"hate/internal/ticket"
)

// TestProjectSchedule forward-schedules from a start date: roots start there,
// successors start the business day after their predecessor ends, unsized
// tickets get 1 day and are counted, and backlog is excluded.
func TestProjectSchedule(t *testing.T) {
	m := "m" // 3 days
	deps := func(ids ...string) []string { return ids }
	tickets := []*ticket.Ticket{
		{ID: "A", Title: "Root", Status: "in_progress", Effort: &m},                                        // 3 business days
		{ID: "B", Title: "After A", Status: "not_started", Effort: &m, Predecessors: deps("A")},            // starts after A
		{ID: "U", Title: "Unsized", Status: "not_started"},                                                 // 1 day, counted
		{ID: "BL", Title: "Backlog", Status: "not_started", Effort: &m, Tags: []string{ticket.BacklogTag}}, // excluded
	}
	// Wed 2026-07-22 is a weekday.
	start := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)

	snap, unsized := ProjectSchedule("P", "Proj", tickets, ticket.DefaultEffortToDays, start)

	if unsized != 1 {
		t.Errorf("unsized = %d, want 1", unsized)
	}
	if len(snap.Tasks) != 3 { // backlog excluded
		t.Fatalf("tasks = %d, want 3 (backlog excluded)", len(snap.Tasks))
	}
	byID := map[string]SnapshotTask{}
	for _, tk := range snap.Tasks {
		byID[tk.TaskID] = tk
	}
	// A starts at start (Wed 07-22), 3 business days → ends Fri 07-24.
	if byID["A"].Baseline.PlannedStart != "2026-07-22" || byID["A"].Baseline.PlannedEnd != "2026-07-24" {
		t.Errorf("A = %s..%s, want 2026-07-22..2026-07-24", byID["A"].Baseline.PlannedStart, byID["A"].Baseline.PlannedEnd)
	}
	// B starts the business day after A ends (Fri) → Mon 07-27.
	if byID["B"].Baseline.PlannedStart != "2026-07-27" {
		t.Errorf("B start = %s, want 2026-07-27 (business day after A)", byID["B"].Baseline.PlannedStart)
	}
	// B must never start before A ends — the core forward-scheduling invariant.
	if byID["B"].Baseline.PlannedStart <= byID["A"].Baseline.PlannedEnd {
		t.Errorf("B starts %s, not after A ends %s", byID["B"].Baseline.PlannedStart, byID["A"].Baseline.PlannedEnd)
	}
}
