// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"
	"time"

	"hate/internal/ticket"
)

// changeByID finds a ticket's change entry in a balance report.
func changeByID(report BalanceReport, id string) (BalanceChange, bool) {
	for _, c := range report.Changes {
		if c.TicketID == id {
			return c, true
		}
	}
	return BalanceChange{}, false
}

// TestBalanceTerminalPredecessorOffset pins the fix for the predsReady terminal
// branch: a successor of an already-terminal (complete/closed) predecessor must
// become ready the weekday *after* the predecessor's due date — the same offset
// the scheduled-predecessor path gets for free from the start-of-day readiness
// snapshot. Before the fix the terminal branch released the successor on the due
// date itself (a day early), making the two predecessor kinds inconsistent.
func TestBalanceTerminalPredecessorOffset(t *testing.T) {
	xs := "xs"
	// 2026-06-10 is a Wednesday; 2026-06-11 a Thursday. Both weekdays, so no
	// weekend skipping masks the off-by-one.
	projectStart := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	due := "2026-06-10"

	tickets := []*ticket.Ticket{
		// Terminal predecessor: already complete, due on the project start day.
		{ID: "T-pterm", Type: "task", Status: "complete", Title: "done dep",
			Priority: "medium", DueDate: &due},
		// Successor of the terminal predecessor.
		{ID: "T-sterm", Type: "task", Status: "not_started", Title: "after terminal",
			Priority: "medium", Effort: &xs, Assignee: strp("a@x"),
			Predecessors: []string{"T-pterm"}},
		// Scheduled predecessor: finishes on the project start day (xs = 1 day).
		{ID: "T-psched", Type: "task", Status: "not_started", Title: "live dep",
			Priority: "medium", Effort: &xs, Assignee: strp("b@x")},
		// Successor of the scheduled predecessor.
		{ID: "T-ssched", Type: "task", Status: "not_started", Title: "after scheduled",
			Priority: "medium", Effort: &xs, Assignee: strp("c@x"),
			Predecessors: []string{"T-psched"}},
	}

	report := BalanceProject(tickets, nil, ticket.DefaultEffortToDays, projectStart)

	if report.CycleDetected {
		t.Fatalf("unexpected cycle detected: %v", report.CycleTicketIDs)
	}

	psched, ok := changeByID(report, "T-psched")
	if !ok {
		t.Fatalf("scheduled predecessor T-psched was not scheduled; skipped=%v", report.Skipped)
	}
	if psched.NewStart != "2026-06-10" || psched.NewDue != "2026-06-10" {
		t.Fatalf("T-psched: got start=%s due=%s, want 2026-06-10/2026-06-10",
			psched.NewStart, psched.NewDue)
	}

	ssched, ok := changeByID(report, "T-ssched")
	if !ok {
		t.Fatalf("scheduled successor T-ssched was not scheduled; skipped=%v", report.Skipped)
	}
	sterm, ok := changeByID(report, "T-sterm")
	if !ok {
		t.Fatalf("terminal successor T-sterm was not scheduled; skipped=%v", report.Skipped)
	}

	// The day after the dependency's 2026-06-10 finish, not the same day.
	const wantStart = "2026-06-11"
	if sterm.NewStart != wantStart {
		t.Errorf("terminal successor started %s, want %s (must not start on the dependency's due date)",
			sterm.NewStart, wantStart)
	}
	if ssched.NewStart != wantStart {
		t.Errorf("scheduled successor started %s, want %s", ssched.NewStart, wantStart)
	}
	// The core consistency guarantee: both predecessor kinds gate identically.
	if sterm.NewStart != ssched.NewStart {
		t.Errorf("inconsistent gating: terminal successor starts %s but scheduled successor starts %s",
			sterm.NewStart, ssched.NewStart)
	}
}

func strp(s string) *string { return &s }
