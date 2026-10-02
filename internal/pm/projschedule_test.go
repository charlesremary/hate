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
// tickets get 1 day and are counted, and backlog is excluded. Durations are
// estimated hours at the assignee's daily hours (8 when unknown).
func TestProjectSchedule(t *testing.T) {
	m := "m" // legacy 3 days
	h24 := 24.0
	deps := func(ids ...string) []string { return ids }
	tickets := []*ticket.Ticket{
		{ID: "A", Title: "Root", Status: "in_progress", Tags: []string{ticket.ClassConfig}, EstimateHours: &h24}, // 24h → 3 business days
		{ID: "B", Title: "After A", Status: "not_started", Effort: &m, Predecessors: deps("A")},                  // unclassed legacy m → 24h, starts after A
		{ID: "U", Title: "Unsized", Status: "not_started"},                                                       // 1 day, counted
		{ID: "BL", Title: "Backlog", Status: "not_started", Effort: &m, Tags: []string{ticket.BacklogTag}},       // excluded
	}
	// Wed 2026-07-22 is a weekday.
	start := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)

	snap, unsized := ProjectSchedule("P", "Proj", tickets, nil, NewEstimateContext(tickets, 0, nil), start)

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

// TestProjectScheduleAssigneeHours converts hours to days at the assignee's
// daily capacity: 8h at 4h/day is 2 days; 1h rounds up to the 1-day minimum.
func TestProjectScheduleAssigneeHours(t *testing.T) {
	h8, h1 := 8.0, 1.0
	four := 4.0
	res := []ticket.Resource{{Email: "half@x", DailyHoursAvailable: &four}}
	tickets := []*ticket.Ticket{
		{ID: "H", Status: "not_started", Assignee: strp("half@x"), Tags: []string{ticket.ClassNonfunc}, EstimateHours: &h8},
		{ID: "S", Status: "not_started", Tags: []string{ticket.ClassConfig}, EstimateHours: &h1},
	}
	start := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	snap, unsized := ProjectSchedule("P", "Proj", tickets, res, NewEstimateContext(tickets, 0, nil), start)
	if unsized != 0 {
		t.Errorf("unsized = %d, want 0", unsized)
	}
	days := map[string]int{}
	for _, tk := range snap.Tasks {
		days[tk.TaskID] = tk.Baseline.PlannedDays
	}
	if days["H"] != 2 || days["S"] != 1 {
		t.Errorf("planned days H=%d S=%d, want 2 / 1", days["H"], days["S"])
	}
}
