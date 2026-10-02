// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"strings"
	"testing"

	"hate/internal/ticket"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// loadFixture: a@x (4h/day) has 20h open; b@x (8h/day) has 8h open plus a done
// ticket; one unassigned ticket (two resources → "unassigned" lane).
func loadFixture() ([]*ticket.Ticket, []ticket.Resource) {
	res := []ticket.Resource{{Name: "Ann <A>", Email: "a@x", DailyHoursAvailable: dh(4)}, {Email: "b@x"}}
	done := capT("B0", 40, "b@x")
	done.Status = "closed"
	tickets := []*ticket.Ticket{
		capT("A1", 12, "a@x"), capT("A2", 8, "a@x"),
		capT("B1", 8, "b@x"), done,
		capT("U1", 2, ""),
	}
	return tickets, res
}

// HATE-1rne tc1: each person has remaining hours, h/day, days of work and a
// free-from date from the capacity schedule.
func TestComputeLoad(t *testing.T) {
	tickets, res := loadFixture()
	rep := ComputeLoad(tickets, res, NewEstimateContext(tickets, 0, nil), schedStart, "")
	if len(rep.Rows) != 3 {
		t.Fatalf("rows = %d, want 3 (a, b, unassigned)", len(rep.Rows))
	}
	a, b, u := rep.Rows[0], rep.Rows[1], rep.Rows[2]
	// a: 20h at 4h/day = 5 days → Wed..Tue.
	if a.Hours != 20 || a.DailyHours != 4 || a.DaysOfWork != 5 || a.FreeFrom != "2026-07-28" {
		t.Errorf("a = %+v", a)
	}
	// b: the closed ticket doesn't count; 8h = 1 day.
	if b.Hours != 8 || b.DaysOfWork != 1 || b.FreeFrom != "2026-07-22" {
		t.Errorf("b = %+v", b)
	}
	if u.Key != UnassignedLane || u.Hours != 2 || u.DaysOfWork != 0.3 {
		t.Errorf("unassigned = %+v", u)
	}
	if rep.TotalHours != 30 || rep.FreeFrom != "2026-07-28" || rep.Target != "" {
		t.Errorf("report = %+v", rep)
	}
	html := RenderLoadHTML(rep)
	for _, w := range []string{"Load &mdash;", "Remaining est. hours", "h/day", "Days of work", "Free from", "Tue Jul 28, 2026", "Ann &lt;A&gt;"} {
		if !contains(html, w) {
			t.Errorf("load HTML missing %q", w)
		}
	}
	// HATE-1rne tc4: with no target the target columns are absent.
	if contains(html, "Working days to requested end") || contains(html, "over by") {
		t.Error("target columns shown without a target date")
	}
}

// HATE-1rne tc3: a target before someone's free-from date shows "over by N
// days" (highlighted) on that row and at project level.
func TestComputeLoadTarget(t *testing.T) {
	tickets, res := loadFixture()
	// Target Fri 07-24: a is free Tue 07-28 → over by Mon + Tue = 2 days.
	rep := ComputeLoad(tickets, res, NewEstimateContext(tickets, 0, nil), schedStart, "2026-07-24")
	if rep.WorkingDaysToTarget != 3 {
		t.Errorf("working days to target = %d, want 3 (Wed-Fri)", rep.WorkingDaysToTarget)
	}
	if rep.Rows[0].OverBy != 2 || rep.Rows[1].OverBy != 0 || rep.OverBy != 2 {
		t.Errorf("over by a=%d b=%d project=%d, want 2/0/2", rep.Rows[0].OverBy, rep.Rows[1].OverBy, rep.OverBy)
	}
	html := RenderLoadHTML(rep)
	for _, w := range []string{"Working days to requested end", `class="load-over"`, "over by 2 days", "on time", "Requested end:</strong> Fri Jul 24, 2026"} {
		if !contains(html, w) {
			t.Errorf("load HTML missing %q", w)
		}
	}
	// A comfortable target: nobody over, spare days reported.
	rep = ComputeLoad(tickets, res, NewEstimateContext(tickets, 0, nil), schedStart, "2026-07-31")
	if rep.OverBy != 0 || rep.SpareDays != 3 {
		t.Errorf("over=%d spare=%d, want 0/3", rep.OverBy, rep.SpareDays)
	}
}

// HATE-1rne tc5: no estimated open work → the section says so.
func TestComputeLoadEmpty(t *testing.T) {
	done := capT("D", 8, "")
	done.Status = "complete"
	tickets := []*ticket.Ticket{done}
	rep := ComputeLoad(tickets, nil, NewEstimateContext(tickets, 0, nil), schedStart, "")
	if html := RenderLoadHTML(rep); !contains(html, "No remaining estimated work.") {
		t.Errorf("empty load HTML = %s", html)
	}
	unsized := []*ticket.Ticket{capT("U", 0, "")}
	rep = ComputeLoad(unsized, nil, NewEstimateContext(unsized, 0, nil), schedStart, "")
	if html := RenderLoadHTML(rep); !contains(html, "No remaining estimated work &mdash; 1 open ticket has no estimate.") {
		t.Errorf("unsized-only load HTML = %s", html)
	}
}
