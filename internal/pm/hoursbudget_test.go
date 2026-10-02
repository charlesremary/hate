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
	b := ComputeHoursBudget(tickets, &work, &admin, nil)
	if b.Work.Spent != 40 || b.Work.Remaining != 10 || b.Work.PercentUsed != 80 { // 40/50
		t.Errorf("work = spent %.1f/rem %.1f/pct %.1f, want 40/10/80", b.Work.Spent, b.Work.Remaining, b.Work.PercentUsed)
	}
	if b.Admin.Spent != 6 || b.Admin.Remaining != 2 || b.Admin.PercentUsed != 75 { // 6/8
		t.Errorf("admin = spent %.1f/rem %.1f/pct %.1f, want 6/2/75", b.Admin.Spent, b.Admin.Remaining, b.Admin.PercentUsed)
	}

	// No budgets set: pools still total their spend, nothing to burn against.
	nb := ComputeHoursBudget(tickets, nil, nil, nil)
	if nb.Work.Budget != nil || nb.Work.Spent != 40 || nb.Work.PercentUsed != 0 {
		t.Errorf("no-budget work = %+v, want spent 40, pct 0, nil budget", nb.Work)
	}
	if nb.Admin.Spent != 6 {
		t.Errorf("no-budget admin spent = %.1f, want 6", nb.Admin.Spent)
	}
}

// wrapT builds a wrap (config) ticket with an hours estimate (nil = none).
func wrapT(id, status string, est *float64, logged ...float64) *ticket.Ticket {
	tk := &ticket.Ticket{ID: id, Status: status, Tags: []string{ticket.ClassConfig}, EstimateHours: est}
	for _, h := range logged {
		tk.TimeEntries = append(tk.TimeEntries, ticket.TimeEntry{Hours: h})
	}
	return tk
}

// TestComputeEstimateVariance checks the over/under split: only completed
// estimated WRAP tickets land in the tables, in-flight overruns become
// warnings, the data-gap buckets (no time, unsized, on-target) are counted, and
// functional / unclassed tickets never get per-ticket rows.
func TestComputeEstimateVariance(t *testing.T) {
	xs, m := "xs", "m"
	cancel := "descoped"

	tickets := []*ticket.Ticket{
		wrapT("OVER", "complete", f64(24), 30),    // +6 overran
		wrapT("UNDER", "complete", f64(24), 20),   // -4 underran
		wrapT("EXACT", "complete", f64(16), 16),   // on target
		wrapT("WIP", "in_progress", f64(8), 12),   // +4 in-progress-over
		wrapT("WIPOK", "in_progress", f64(24), 5), // under, not done → ignored
		wrapT("NOTIME", "complete", f64(16)),      // completed, 0 hours → data gap
		wrapT("UNSIZED", "complete", nil, 9),      // completed wrap, no estimate
		// Converted: wrap with only a legacy effort (xs = 8h) → source "converted".
		{ID: "CONV", Status: "complete", Tags: []string{ticket.ClassNonfunc}, Effort: &xs, TimeEntries: te(10)},
		// Not wrap → no per-ticket row even with a legacy effort.
		{ID: "LEGACY", Status: "complete", Effort: &m, TimeEntries: te(99)},
		{ID: "FUNC", Status: "complete", Tags: []string{ticket.ClassFunctional}, TimeEntries: te(99)},
		// Excluded from scope entirely.
		{ID: "CANC", Status: "closed", Tags: []string{ticket.ClassConfig}, EstimateHours: f64(24), CancellationReason: &cancel, TimeEntries: te(99)},
	}

	v := ComputeEstimateVariance(tickets, NewEstimateContext(tickets, 0, nil))

	if len(v.Overran) != 2 || v.Overran[0].ID != "OVER" || v.Overran[0].VarianceHours != 6 {
		t.Errorf("overran = %+v, want [OVER +6, CONV +2]", v.Overran)
	}
	if len(v.Overran) == 2 && (v.Overran[1].ID != "CONV" || v.Overran[1].Source != SourceConverted || v.Overran[1].EstimateHours != 8) {
		t.Errorf("overran[1] = %+v, want CONV converted 8h", v.Overran[1])
	}
	if v.Overran[0].Source != SourceEstimate {
		t.Errorf("OVER source = %q, want estimate", v.Overran[0].Source)
	}
	if len(v.Underran) != 1 || v.Underran[0].ID != "UNDER" || v.Underran[0].VarianceHours != -4 {
		t.Errorf("underran = %+v, want [UNDER -4]", v.Underran)
	}
	if len(v.InProgressOver) != 1 || v.InProgressOver[0].ID != "WIP" {
		t.Errorf("inProgressOver = %+v, want [WIP]", v.InProgressOver)
	}
	if v.TotalOverrunHours != 8 || v.TotalUnderrunHours != 4 {
		t.Errorf("totals = +%.1f/-%.1f, want +8/-4", v.TotalOverrunHours, v.TotalUnderrunHours)
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

// TestEstimateVarianceFeatures: the per-feature code section compares CFP x
// rate with functional hours, only for features whose functional children are
// all done (dev_complete or later).
func TestEstimateVarianceFeatures(t *testing.T) {
	tickets := []*ticket.Ticket{
		{ID: "F1", Title: "Done feature", Tags: []string{"cfp:10"}},
		{ID: "a", Status: "dev_complete", Tags: []string{"parent:F1", ticket.ClassFunctional}, TimeEntries: te(2)},
		{ID: "b", Status: "complete", Tags: []string{"parent:F1", ticket.ClassFunctional}, TimeEntries: te(2)},
		{ID: "w", Status: "in_progress", Tags: []string{"parent:F1", ticket.ClassConfig}, EstimateHours: f64(1), TimeEntries: te(1)}, // wrap: not part of the done rule

		{ID: "F2", Title: "Open feature", Tags: []string{"cfp:8"}},
		{ID: "c", Status: "complete", Tags: []string{"parent:F2", ticket.ClassFunctional}, TimeEntries: te(1)},
		{ID: "d", Status: "rework", Tags: []string{"parent:F2", ticket.ClassFunctional}, TimeEntries: te(1)},
	}
	v := ComputeEstimateVariance(tickets, NewEstimateContext(tickets, 0.3, nil))
	if len(v.Features) != 1 {
		t.Fatalf("features = %+v, want only F1", v.Features)
	}
	f := v.Features[0]
	if f.ID != "F1" || f.CFP != 10 || !approx(f.EstimateHours, 3) || f.SpentHours != 4 || !approx(f.VarianceHours, 1) || f.Rate != 0.3 {
		t.Errorf("F1 row = %+v, want cfp 10, est 3, spent 4, var +1, rate 0.3", f)
	}
	// Functional children never get per-ticket variance rows.
	for _, r := range append(append(v.Overran, v.Underran...), v.InProgressOver...) {
		if r.ID == "a" || r.ID == "b" || r.ID == "c" || r.ID == "d" {
			t.Errorf("functional ticket %s got a per-ticket row", r.ID)
		}
	}
}

// TestComputeHoursAtRisk lists active wrap tickets at ≥90% of allotment,
// including over 100%, most-consumed first; completed, unestimated, functional
// and unclassed tickets are excluded.
func TestComputeHoursAtRisk(t *testing.T) {
	xs := "xs"
	tickets := []*ticket.Ticket{
		wrapT("NEAR", "in_progress", f64(8), 7.5),                                                                 // 93.8% → in
		wrapT("OVER", "in_progress", f64(8), 10),                                                                  // 125% → in
		wrapT("LOW", "in_progress", f64(8), 4),                                                                    // 50% → out
		wrapT("DONE", "complete", f64(8), 8),                                                                      // completed → out
		wrapT("UNSIZED", "in_progress", nil, 99),                                                                  // no allotment → out
		{ID: "LEGACY", Status: "in_progress", Effort: &xs, TimeEntries: te(99)},                                   // unclassed → no allotment
		{ID: "FUNC", Status: "in_progress", Tags: []string{ticket.ClassFunctional, "cfp:1"}, TimeEntries: te(99)}, // functional → never gated
	}

	rows := ComputeHoursAtRisk(tickets, NewEstimateContext(tickets, 0, nil))
	if len(rows) != 2 {
		t.Fatalf("got %d at-risk rows, want 2 (%+v)", len(rows), rows)
	}
	if rows[0].TicketID != "OVER" || rows[1].TicketID != "NEAR" { // most-consumed first
		t.Errorf("order = %s,%s, want OVER,NEAR", rows[0].TicketID, rows[1].TicketID)
	}
	if rows[1].Allocated != 8 || rows[1].PercentUsed != 93.8 {
		t.Errorf("NEAR = %.1fh alloc / %.1f%%, want 8 / 93.8", rows[1].Allocated, rows[1].PercentUsed)
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
	tickets := []*ticket.Ticket{
		wrapT("SMALL_OVER", "complete", f64(24), 26),  // +2
		wrapT("BIG_OVER", "complete", f64(24), 40),    // +16
		wrapT("SMALL_UNDER", "complete", f64(24), 22), // -2
		wrapT("BIG_UNDER", "complete", f64(40), 10),   // -30
	}

	v := ComputeEstimateVariance(tickets, NewEstimateContext(tickets, 0, nil))

	if v.Overran[0].ID != "BIG_OVER" {
		t.Errorf("overran[0] = %s, want BIG_OVER", v.Overran[0].ID)
	}
	if v.Underran[0].ID != "BIG_UNDER" {
		t.Errorf("underran[0] = %s, want BIG_UNDER", v.Underran[0].ID)
	}
}
