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

// TestComputeHoursBudget checks that logged hours bucket into the work vs
// admin/meeting pools by ticket type and burn down each pool's budget.
func TestComputeHoursBudget(t *testing.T) {
	tickets := []*ticket.Ticket{
		{ID: "A", Type: "dev_task", TimeEntries: te(30)},      // work
		{ID: "B", Type: "task", TimeEntries: te(10)},          // work
		{ID: "C", Type: "meeting", TimeEntries: te(4)},        // admin/meeting
		{ID: "D", Type: "administration", TimeEntries: te(2)}, // admin/meeting
	}

	work, admin := 50.0, 8.0
	b := ComputeHoursBudget(tickets, &work, &admin)
	if b.Work.Spent != 40 || b.Work.Remaining != 10 || b.Work.PercentUsed != 80 { // 40/50
		t.Errorf("work = spent %.1f/rem %.1f/pct %.1f, want 40/10/80", b.Work.Spent, b.Work.Remaining, b.Work.PercentUsed)
	}
	if b.Admin.Spent != 6 || b.Admin.Remaining != 2 || b.Admin.PercentUsed != 75 { // 6/8
		t.Errorf("admin = spent %.1f/rem %.1f/pct %.1f, want 6/2/75", b.Admin.Spent, b.Admin.Remaining, b.Admin.PercentUsed)
	}

	// No budgets set: pools still total their spend, nothing to burn against.
	nb := ComputeHoursBudget(tickets, nil, nil)
	if nb.Work.Budget != nil || nb.Work.Spent != 40 || nb.Work.PercentUsed != 0 {
		t.Errorf("no-budget work = %+v, want spent 40, pct 0, nil budget", nb.Work)
	}
	if nb.Admin.Spent != 6 {
		t.Errorf("no-budget admin spent = %.1f, want 6", nb.Admin.Spent)
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
