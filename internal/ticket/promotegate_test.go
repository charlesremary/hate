// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import "testing"

func TestStatusEnteredAt(t *testing.T) {
	tk := &Ticket{
		Status:    "qa_testing",
		CreatedAt: "2026-01-01T00:00:00Z",
		Activity: []Activity{
			{Timestamp: "2026-01-02T00:00:00Z", Action: "status_changed", Detail: "not_started -> in_progress"},
			{Timestamp: "2026-01-03T00:00:00Z", Action: "status_changed", Detail: "in_progress -> dev_complete"},
			{Timestamp: "2026-01-04T00:00:00Z", Action: "status_changed", Detail: "dev_complete -> qa_testing"},
			{Timestamp: "2026-01-05T00:00:00Z", Action: "comment", Detail: "note"},
		},
	}
	if got := statusEnteredAt(tk); got != "2026-01-04T00:00:00Z" {
		t.Errorf("statusEnteredAt = %q, want the qa_testing entry time", got)
	}
	// No matching activity → falls back to created_at.
	tk2 := &Ticket{Status: "in_progress", CreatedAt: "2026-01-01T00:00:00Z"}
	if got := statusEnteredAt(tk2); got != "2026-01-01T00:00:00Z" {
		t.Errorf("fallback = %q, want created_at", got)
	}
}

func TestHasTimeLoggedSince(t *testing.T) {
	since := "2026-01-04T00:00:00Z"
	tk := &Ticket{TimeEntries: []TimeEntry{
		{Hours: 2, Description: "old dev work", LoggedAt: "2026-01-02T09:00:00Z"}, // before → doesn't count
	}}
	if hasTimeLoggedSince(tk, since) {
		t.Error("time logged before entering status should not qualify")
	}
	// After, but no description → doesn't count.
	tk.TimeEntries = append(tk.TimeEntries, TimeEntry{Hours: 1, Description: "  ", LoggedAt: "2026-01-04T10:00:00Z"})
	if hasTimeLoggedSince(tk, since) {
		t.Error("time without a description should not qualify")
	}
	// After, with description → counts.
	tk.TimeEntries = append(tk.TimeEntries, TimeEntry{Hours: 0.5, Description: "ran QA", LoggedAt: "2026-01-04T11:00:00Z"})
	if !hasTimeLoggedSince(tk, since) {
		t.Error("described time after entering status should qualify")
	}
}
