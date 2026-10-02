// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

// Baseline planned days: a legacy effort keeps its configured days regardless
// of the assignee's daily hours; a real hour estimate goes through them.
func TestCreateBaselinePlannedDays(t *testing.T) {
	root := t.TempDir()
	cfg := ticket.DefaultConfig("Client", "Proj", "P", "P")
	two := 2.0
	cfg.Resources = []ticket.Resource{{Name: "Pat", Email: "pat@x", DailyHoursAvailable: &two}}
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	m := "m" // 3 days in the default map
	pat := "pat@x"
	four := 4.0
	tickets := []*ticket.Ticket{
		{ID: "P-lgcy", Type: "task", Status: "not_started", Title: "legacy", Effort: &m, Assignee: &pat},
		{ID: "P-conv", Type: "task", Status: "not_started", Title: "converted", Effort: &m, Assignee: &pat, Tags: []string{ticket.ClassConfig}},
		{ID: "P-est", Type: "task", Status: "not_started", Title: "estimated", EstimateHours: &four, Assignee: &pat, Tags: []string{ticket.ClassConfig}},
	}
	for _, tk := range tickets {
		tk.SchemaVersion = ticket.SchemaVersion
		tk.CreatedAt, tk.UpdatedAt = "2026-10-01T00:00:00Z", "2026-10-01T00:00:00Z"
		if err := ticket.WriteTicket(root, tk); err != nil {
			t.Fatal(err)
		}
	}
	bl, err := CreateBaselineFromTickets(root, "P", "Proj", "tester")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"P-lgcy": 3, "P-conv": 3, "P-est": 2}
	for _, bt := range bl.Tasks {
		if bt.PlannedDays != want[bt.TaskID] {
			t.Errorf("%s planned days = %d, want %d", bt.TaskID, bt.PlannedDays, want[bt.TaskID])
		}
	}
}
