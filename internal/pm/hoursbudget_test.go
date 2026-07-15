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

// TestComputeHoursBudget exercises the projected-vs-spent roll-up: effort-based
// projection, logged-hours spend, the unsized bucket, and cancelled/backlog
// exclusion. Defaults: xs=1d, s=2d, m=3d → 8h/16h/24h at HoursPerDay=8.
func TestComputeHoursBudget(t *testing.T) {
	xs, s, m := "xs", "s", "m"
	cancel := "descoped"

	tickets := []*ticket.Ticket{
		{ID: "A", Effort: &m, Status: "complete", TimeEntries: te(30)}, // proj 24, spent 30
		{ID: "B", Effort: &s, Status: "in_progress", TimeEntries: te(5)}, // proj 16, spent 5
		{ID: "C", Effort: &xs, Status: "not_started"},                    // proj 8, spent 0
		{ID: "D", Status: "in_progress", TimeEntries: te(7)},             // unsized: spent 7, no proj
		// Cancelled (descoped) — excluded entirely.
		{ID: "E", Effort: &m, Status: "closed", CancellationReason: &cancel, TimeEntries: te(100)},
		// Backlog — uncommitted, excluded entirely.
		{ID: "F", Effort: &m, Status: "not_started", Tags: []string{ticket.BacklogTag}, TimeEntries: te(100)},
	}

	b := ComputeHoursBudget(tickets, ticket.DefaultEffortToDays)

	if b.ProjectedHours != 48 { // 24 + 16 + 8
		t.Errorf("projected = %.1f, want 48", b.ProjectedHours)
	}
	if b.SpentHours != 42 { // 30 + 5 + 0 + 7
		t.Errorf("spent = %.1f, want 42", b.SpentHours)
	}
	if b.RemainingHours != 6 { // 48 - 42
		t.Errorf("remaining = %.1f, want 6", b.RemainingHours)
	}
	if b.SizedTickets != 3 {
		t.Errorf("sized = %d, want 3", b.SizedTickets)
	}
	if b.UnsizedTickets != 1 || b.UnsizedHours != 7 {
		t.Errorf("unsized = %d/%.1fh, want 1/7.0h", b.UnsizedTickets, b.UnsizedHours)
	}
	if b.ExcludedCount != 2 || b.ExcludedHours != 200 { // E + F, 100h each
		t.Errorf("excluded = %d/%.1fh, want 2/200.0h", b.ExcludedCount, b.ExcludedHours)
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

// TestEstimateVarianceSortOrder confirms the biggest miss surfaces first in each list.
func TestEstimateVarianceSortOrder(t *testing.T) {
	m, l := "m", "l" // 24h, 40h

	tickets := []*ticket.Ticket{
		{ID: "SMALL_OVER", Effort: &m, Status: "complete", TimeEntries: te(26)}, // +2
		{ID: "BIG_OVER", Effort: &m, Status: "complete", TimeEntries: te(40)},   // +16
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
