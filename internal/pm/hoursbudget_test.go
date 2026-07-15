// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

// te builds a ticket carrying a single logged time entry of h hours.
func te(h float64) []ticket.TimeEntry {
	return []ticket.TimeEntry{{Hours: h}}
}

// TestComputeHoursBudget checks logged hours vs the max-hours cap. Every logged
// hour counts — including descoped work — since the cap is a hard ceiling on
// total project effort.
func TestComputeHoursBudget(t *testing.T) {
	m, s := "m", "s"
	cancel := "descoped"

	tickets := []*ticket.Ticket{
		{ID: "A", Effort: &m, Status: "complete", TimeEntries: te(30)},
		{ID: "B", Effort: &s, Status: "in_progress", TimeEntries: te(5)},
		// Descoped work still burned hours against the cap.
		{ID: "C", Status: "closed", CancellationReason: &cancel, TimeEntries: te(10)},
	}

	max := 50.0
	b := ComputeHoursBudget(tickets, &max)
	if b.SpentHours != 45 { // 30 + 5 + 10
		t.Errorf("spent = %.1f, want 45", b.SpentHours)
	}
	if b.RemainingHours != 5 { // 50 - 45
		t.Errorf("remaining = %.1f, want 5", b.RemainingHours)
	}
	if b.PercentUsed != 90 { // 45/50
		t.Errorf("percentUsed = %.1f, want 90", b.PercentUsed)
	}

	// No cap set: spend still totals, but there's nothing to track against.
	nb := ComputeHoursBudget(tickets, nil)
	if nb.MaxHours != nil {
		t.Errorf("maxHours = %v, want nil", nb.MaxHours)
	}
	if nb.SpentHours != 45 || nb.RemainingHours != 0 || nb.PercentUsed != 0 {
		t.Errorf("no-cap = spent %.1f/rem %.1f/pct %.1f, want 45/0/0", nb.SpentHours, nb.RemainingHours, nb.PercentUsed)
	}
}

// TestComputeEstimateVariance checks the over/under split: only completed sized
// tickets land in the tables, in-flight overruns become warnings, and the
// data-gap buckets (no time, unsized, on-target) are counted, not miscounted.
func TestComputeEstimateVariance(t *testing.T) {
	xs, s, m := "xs", "s", "m"
	cancel := "descoped"

	tickets := []*ticket.Ticket{
		{ID: "OVER", Effort: &m, Status: "complete", TimeEntries: te(30)},    // proj 24, +6 overran
		{ID: "UNDER", Effort: &m, Status: "complete", TimeEntries: te(20)},   // proj 24, -4 underran
		{ID: "EXACT", Effort: &s, Status: "complete", TimeEntries: te(16)},   // proj 16, on target
		{ID: "WIP", Effort: &xs, Status: "in_progress", TimeEntries: te(12)}, // proj 8, +4 in-progress-over
		{ID: "WIPOK", Effort: &m, Status: "in_progress", TimeEntries: te(5)}, // under, not done → ignored
		{ID: "NOTIME", Effort: &s, Status: "complete"},                       // completed, 0 hours → data gap
		{ID: "UNSIZED", Status: "complete", TimeEntries: te(9)},              // completed, no size
		// Excluded from scope entirely.
		{ID: "CANC", Effort: &m, Status: "closed", CancellationReason: &cancel, TimeEntries: te(99)},
	}

	v := ComputeEstimateVariance(tickets, ticket.DefaultEffortToDays)

	if len(v.Overran) != 1 || v.Overran[0].ID != "OVER" || v.Overran[0].VarianceHours != 6 {
		t.Errorf("overran = %+v, want [OVER +6]", v.Overran)
	}
	if len(v.Underran) != 1 || v.Underran[0].ID != "UNDER" || v.Underran[0].VarianceHours != -4 {
		t.Errorf("underran = %+v, want [UNDER -4]", v.Underran)
	}
	if len(v.InProgressOver) != 1 || v.InProgressOver[0].ID != "WIP" {
		t.Errorf("inProgressOver = %+v, want [WIP]", v.InProgressOver)
	}
	if v.TotalOverrunHours != 6 || v.TotalUnderrunHours != 4 {
		t.Errorf("totals = +%.1f/-%.1f, want +6/-4", v.TotalOverrunHours, v.TotalUnderrunHours)
	}
	if v.OnTargetCount != 1 {
		t.Errorf("onTarget = %d, want 1", v.OnTargetCount)
	}
	if v.CompletedNoTime != 1 {
		t.Errorf("completedNoTime = %d, want 1", v.CompletedNoTime)
	}
	if v.UnsizedCompleted != 1 {
		t.Errorf("unsizedCompleted = %d, want 1", v.UnsizedCompleted)
	}
}

// TestComputeOverrides collects only entries carrying an authorization reason,
// most recent first.
func TestComputeOverrides(t *testing.T) {
	tickets := []*ticket.Ticket{
		{ID: "A", Title: "Task A", TimeEntries: []ticket.TimeEntry{
			{Date: "2026-07-01", Hours: 2, Author: "sam@x.com", LoggedAt: "2026-07-01T00:00:00Z"},                          // no reason → skip
			{Date: "2026-07-03", Hours: 1, Author: "sam@x.com", ExtendReason: "boss ok", LoggedAt: "2026-07-03T00:00:00Z"}, // include
		}},
		{ID: "B", Title: "Task B", TimeEntries: []ticket.TimeEntry{
			{Date: "2026-07-05", Hours: 3, Author: "kai@x.com", ExtendReason: "scope grew", LoggedAt: "2026-07-05T00:00:00Z"}, // include, newest
		}},
	}

	rows := ComputeOverrides(tickets)
	if len(rows) != 2 {
		t.Fatalf("got %d override rows, want 2", len(rows))
	}
	if rows[0].TicketID != "B" || rows[0].Date != "2026-07-05" { // most recent first
		t.Errorf("row[0] = %s/%s, want B/2026-07-05", rows[0].TicketID, rows[0].Date)
	}
	if rows[1].TicketID != "A" || rows[1].Reason != "boss ok" || rows[1].Author != "sam@x.com" {
		t.Errorf("row[1] = %+v, want A / boss ok / sam@x.com", rows[1])
	}
}

// TestEstimateVarianceSortOrder confirms the biggest miss surfaces first in each list.
func TestEstimateVarianceSortOrder(t *testing.T) {
	m, l := "m", "l" // 24h, 40h

	tickets := []*ticket.Ticket{
		{ID: "SMALL_OVER", Effort: &m, Status: "complete", TimeEntries: te(26)},  // +2
		{ID: "BIG_OVER", Effort: &m, Status: "complete", TimeEntries: te(40)},    // +16
		{ID: "SMALL_UNDER", Effort: &m, Status: "complete", TimeEntries: te(22)}, // -2
		{ID: "BIG_UNDER", Effort: &l, Status: "complete", TimeEntries: te(10)},   // -30
	}

	v := ComputeEstimateVariance(tickets, ticket.DefaultEffortToDays)

	if v.Overran[0].ID != "BIG_OVER" {
		t.Errorf("overran[0] = %s, want BIG_OVER", v.Overran[0].ID)
	}
	if v.Underran[0].ID != "BIG_UNDER" {
		t.Errorf("underran[0] = %s, want BIG_UNDER", v.Underran[0].ID)
	}
}
