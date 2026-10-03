// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"testing"
	"time"

	"hate/internal/ticket"
)

func strp(s string) *string { return &s }

// Wed 2026-07-22 is a weekday.
var schedStart = time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)

// capT is an open config (wrap) ticket with an hours estimate.
func capT(id string, hours float64, assignee string, preds ...string) *ticket.Ticket {
	t := &ticket.Ticket{ID: id, Title: id, Status: "not_started", Priority: "medium",
		Tags: []string{ticket.ClassConfig}, Predecessors: preds}
	if hours > 0 {
		h := hours
		t.EstimateHours = &h
	}
	if assignee != "" {
		t.Assignee = strp(assignee)
	}
	return t
}

func dh(v float64) *float64 { return &v }

func schedule(t *testing.T, tickets []*ticket.Ticket, res []ticket.Resource) (*CapacityPlan, map[string]*CapacityItem) {
	t.Helper()
	plan := ScheduleCapacity(tickets, res, NewEstimateContext(tickets, 0, nil), schedStart)
	byID := map[string]*CapacityItem{}
	for _, it := range plan.Items {
		byID[it.Ticket.ID] = it
	}
	return plan, byID
}

func capNear(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// HATE-todx tc1: one person, three independent 4h tickets at 8h/day run one
// after another over 1.5 days, not in parallel.
func TestCapacitySequentialOnePerson(t *testing.T) {
	res := []ticket.Resource{{Email: "a@x", DailyHoursAvailable: dh(8)}}
	tickets := []*ticket.Ticket{capT("T1", 4, "a@x"), capT("T2", 4, "a@x"), capT("T3", 4, "a@x")}
	plan, by := schedule(t, tickets, res)
	want := map[string][2]float64{"T1": {0, 0.5}, "T2": {0.5, 1}, "T3": {1, 1.5}}
	for id, w := range want {
		if !capNear(by[id].Start, w[0]) || !capNear(by[id].End, w[1]) {
			t.Errorf("%s = %.2f..%.2f, want %.2f..%.2f", id, by[id].Start, by[id].End, w[0], w[1])
		}
	}
	if l := plan.Lane("a@x"); !capNear(l.FreeAt, 1.5) {
		t.Errorf("lane free at %.2f, want 1.5", l.FreeAt)
	}
	// T1, T2 share Wed; T3 is Thu.
	if fmtDate(by["T2"].StartD) != "2026-07-22" || fmtDate(by["T2"].EndD) != "2026-07-22" || fmtDate(by["T3"].EndD) != "2026-07-23" {
		t.Errorf("dates T2 %s..%s T3 ..%s", fmtDate(by["T2"].StartD), fmtDate(by["T2"].EndD), fmtDate(by["T3"].EndD))
	}
}

// HATE-todx tc2: a chain of ten 0.5h tickets for one person at 8h/day
// finishes in 5h (0.625 days), not ten days.
func TestCapacityShortChain(t *testing.T) {
	res := []ticket.Resource{{Email: "a@x", DailyHoursAvailable: dh(8)}}
	var tickets []*ticket.Ticket
	prev := ""
	for i := 0; i < 10; i++ {
		id := string(rune('A' + i))
		if prev == "" {
			tickets = append(tickets, capT(id, 0.5, "a@x"))
		} else {
			tickets = append(tickets, capT(id, 0.5, "a@x", prev))
		}
		prev = id
	}
	plan, by := schedule(t, tickets, res)
	if l := plan.Lane("a@x"); !capNear(l.FreeAt, 0.625) {
		t.Errorf("chain ends at %.3f days, want 0.625", l.FreeAt)
	}
	if fmtDate(by["J"].EndD) != "2026-07-22" {
		t.Errorf("last link ends %s, want the start day", fmtDate(by["J"].EndD))
	}
	snap := plan.Snapshot("P", "Proj")
	if len(snap.Tasks) != 10 || len(snap.CriticalPathIDs) == 0 {
		t.Errorf("snapshot tasks=%d critical=%v", len(snap.Tasks), snap.CriticalPathIDs)
	}
}

// HATE-todx tc3: two people's independent tickets run in parallel.
func TestCapacityTwoPeopleParallel(t *testing.T) {
	res := []ticket.Resource{{Email: "a@x"}, {Email: "b@x"}}
	tickets := []*ticket.Ticket{capT("A1", 8, "a@x"), capT("A2", 8, "a@x"), capT("B1", 8, "b@x"), capT("B2", 8, "b@x")}
	plan, by := schedule(t, tickets, res)
	if !capNear(by["A1"].Start, 0) || !capNear(by["B1"].Start, 0) || !capNear(by["A2"].Start, 1) || !capNear(by["B2"].Start, 1) {
		t.Errorf("starts A1=%.1f B1=%.1f A2=%.1f B2=%.1f, want 0 0 1 1", by["A1"].Start, by["B1"].Start, by["A2"].Start, by["B2"].Start)
	}
	if !capNear(plan.Lane("a@x").FreeAt, 2) || !capNear(plan.Lane("b@x").FreeAt, 2) {
		t.Error("each person should be free after 2 days")
	}
}

// HATE-todx tc4: with exactly one resource, unassigned tickets go on that
// resource (and burn their daily hours). An assignee matching the resource's
// git user also lands there.
func TestCapacityUnassignedSingleResource(t *testing.T) {
	res := []ticket.Resource{{Email: "a@x", GitUser: "ninja", DailyHoursAvailable: dh(2)}}
	tickets := []*ticket.Ticket{capT("U1", 2, ""), capT("U2", 2, ""), capT("G1", 2, "ninja")}
	plan, by := schedule(t, tickets, res)
	if len(plan.Lanes) != 1 {
		t.Fatalf("lanes = %d, want 1 (no unassigned lane)", len(plan.Lanes))
	}
	for _, id := range []string{"U1", "U2", "G1"} {
		if by[id].Lane != "a@x" {
			t.Errorf("%s lane = %q, want a@x", id, by[id].Lane)
		}
	}
	if l := plan.Lane("a@x"); !capNear(l.FreeAt, 3) || l.Hours != 6 {
		t.Errorf("lane free at %.2f hours %.1f, want 3 days / 6h at 2h/day", l.FreeAt, l.Hours)
	}
}

// HATE-todx tc5: with two resources, unassigned tickets get their own
// "unassigned" lane at the default daily hours.
func TestCapacityUnassignedLane(t *testing.T) {
	res := []ticket.Resource{{Email: "a@x"}, {Email: "b@x"}}
	tickets := []*ticket.Ticket{capT("A1", 8, "a@x"), capT("U1", 4, ""), capT("U2", 4, "")}
	plan, by := schedule(t, tickets, res)
	if by["U1"].Lane != UnassignedLane || by["U2"].Lane != UnassignedLane {
		t.Errorf("unassigned lanes = %q %q", by["U1"].Lane, by["U2"].Lane)
	}
	l := plan.Lane(UnassignedLane)
	if l == nil || l.DailyHours != ticket.DefaultDailyHours || !capNear(l.FreeAt, 1) {
		t.Fatalf("unassigned lane = %+v", l)
	}
	if plan.Lanes[len(plan.Lanes)-1].Key != UnassignedLane {
		t.Error("unassigned lane should sort last")
	}
}

// No resources: each assignee string (and "unassigned") is its own lane.
func TestCapacityNoResources(t *testing.T) {
	tickets := []*ticket.Ticket{capT("A", 8, "ann"), capT("B", 8, "bob"), capT("U", 8, "")}
	plan, by := schedule(t, tickets, nil)
	if len(plan.Lanes) != 3 || by["A"].Lane != "ann" || by["B"].Lane != "bob" || by["U"].Lane != UnassignedLane {
		t.Errorf("lanes = %d, A=%s B=%s U=%s", len(plan.Lanes), by["A"].Lane, by["B"].Lane, by["U"].Lane)
	}
	for _, id := range []string{"A", "B", "U"} {
		if !capNear(by[id].Start, 0) {
			t.Errorf("%s should start at 0 (own lane), got %.2f", id, by[id].Start)
		}
	}
}

// HATE-todx tc6: a ticket whose predecessor belongs to someone else starts no
// earlier than the predecessor finishes, even though its own lane is free.
func TestCapacityCrossLanePredecessor(t *testing.T) {
	res := []ticket.Resource{{Email: "a@x"}, {Email: "b@x", DailyHoursAvailable: dh(4)}}
	tickets := []*ticket.Ticket{capT("P", 6, "b@x"), capT("S", 2, "a@x", "P"), capT("F", 2, "a@x")}
	_, by := schedule(t, tickets, res)
	if !capNear(by["P"].End, 1.5) {
		t.Fatalf("P ends %.2f, want 1.5 (6h at 4h/day)", by["P"].End)
	}
	if by["S"].Start < by["P"].End-1e-9 {
		t.Errorf("S starts %.2f before P ends %.2f", by["S"].Start, by["P"].End)
	}
	// The lane fills the wait with ready work rather than idling.
	if !capNear(by["F"].Start, 0) {
		t.Errorf("F starts %.2f, want 0", by["F"].Start)
	}
}

// Order within a lane: priority, then dependency work order, then ID; an
// explicit planned_start_date is a floor.
func TestCapacityOrderAndFloor(t *testing.T) {
	res := []ticket.Resource{{Email: "a@x"}}
	low := capT("A", 8, "a@x")
	low.Priority = "low"
	crit := capT("Z", 8, "a@x")
	crit.Priority = "critical"
	later := capT("M", 8, "a@x")
	later.PlannedStartDate = strp("2026-07-27") // Mon = business day index 3
	_, by := schedule(t, []*ticket.Ticket{low, crit, later}, res)
	if !(by["Z"].Start < by["A"].Start) {
		t.Errorf("critical Z (%.1f) should run before low A (%.1f)", by["Z"].Start, by["A"].Start)
	}
	if by["M"].Start < 3-1e-9 || fmtDate(by["M"].StartD) != "2026-07-27" {
		t.Errorf("M starts %.2f (%s), want floor at 2026-07-27", by["M"].Start, fmtDate(by["M"].StartD))
	}
}

// Scope: backlog and done tickets aren't scheduled; unsized open tickets get the
// placeholder and are counted; parents consume no capacity and finish with
// their children; weekends are skipped; a cycle doesn't hang.
func TestCapacityScope(t *testing.T) {
	m := "m"
	done := capT("D", 8, "")
	done.Status = "complete"
	backlog := capT("BL", 8, "")
	backlog.Tags = append(backlog.Tags, ticket.BacklogTag)
	unsized := capT("U", 0, "")
	legacy := &ticket.Ticket{ID: "L", Status: "not_started", Effort: &m} // legacy effort: not a usable estimate
	parent := &ticket.Ticket{ID: "F", Status: "not_started", Tags: []string{"cfp:8"}}
	child := &ticket.Ticket{ID: "F-1", Status: "not_started", Tags: []string{"parent:F", ticket.ClassFunctional}}
	cyc1 := capT("C1", 1, "", "C2")
	cyc2 := capT("C2", 1, "", "C1")
	tickets := []*ticket.Ticket{done, backlog, unsized, legacy, parent, child, cyc1, cyc2}
	plan, by := schedule(t, tickets, nil)
	if plan.Done != 1 || plan.Unsized != 2 {
		t.Errorf("done=%d unsized=%d, want 1/2", plan.Done, plan.Unsized)
	}
	if by["D"] != nil || by["BL"] != nil {
		t.Error("done/backlog tickets should not be scheduled")
	}
	if !by["U"].Unsized || by["U"].Hours != UnsizedPlaceholderHours {
		t.Errorf("unsized U = %+v", by["U"])
	}
	if by["F"].Lane != "" || !by["F"].Parent || !capNear(by["F"].End, by["F-1"].End) {
		t.Errorf("parent F lane=%q end=%.2f, child ends %.2f", by["F"].Lane, by["F"].End, by["F-1"].End)
	}
	if by["C1"] == nil || by["C2"] == nil || by["C1"].End == 0 && by["C2"].End == 0 {
		t.Error("cycle members should still be scheduled")
	}
	l := plan.Lane(UnassignedLane)
	// 2 (F-1 = 8 CFP x 0.25) + 1 + 1 estimated; U and L placeholders excluded.
	if l == nil || !capNear(l.Hours, 4) {
		t.Fatalf("unassigned hours = %+v, want 4", l)
	}
	// 4.5h at 8h/day = 0.56 days: done the same Wednesday.
	if got := fmtDate(plan.EndDate(l.FreeAt)); got != "2026-07-22" {
		t.Errorf("lane ends %s, want 2026-07-22", got)
	}
	snap := plan.Snapshot("P", "Proj")
	if len(snap.Tasks) != 6 {
		t.Errorf("snapshot tasks = %d, want 6 open tickets", len(snap.Tasks))
	}
}

// ProjectSchedule (the Gantt / draw.io entry point) still produces a renderable
// snapshot from the capacity plan.
func TestProjectScheduleRenders(t *testing.T) {
	tickets := []*ticket.Ticket{capT("A", 8, ""), capT("B", 4, "", "A")}
	snap, unsized := ProjectSchedule("P", "Proj", tickets, nil, NewEstimateContext(tickets, 0, nil), schedStart)
	if unsized != 0 || len(snap.Tasks) != 2 {
		t.Fatalf("unsized=%d tasks=%d", unsized, len(snap.Tasks))
	}
	if snap.Tasks[1].Baseline.PlannedStart != "2026-07-23" || snap.Tasks[1].Dependencies[0] != "A" {
		t.Errorf("B = %+v", snap.Tasks[1])
	}
	if html := RenderProjectedGanttHTML("P", "Proj", tickets, nil, NewEstimateContext(tickets, 0, nil), schedStart, "/x", nil); !contains(html, "gantt-svg") || !contains(html, "Capacity-aware projection") {
		t.Error("projected Gantt did not render")
	}
	if x := RenderGanttDrawio(snap, nil); !contains(x, "<mxfile") {
		t.Error("draw.io export did not render")
	}
}

// Remaining capacity: dev_complete and later count as done; an in-progress
// ticket needs only its estimate minus the hours already logged.
func TestCapacityRemainingHours(t *testing.T) {
	built := capT("B", 4, "")
	built.Status = "dev_complete"
	part := capT("P", 4, "")
	part.Status = "in_progress"
	part.TimeEntries = []ticket.TimeEntry{{Hours: 3}}
	plan, by := schedule(t, []*ticket.Ticket{built, part}, nil)
	if by["B"] != nil || plan.Done != 1 {
		t.Errorf("dev_complete ticket should count as done (done=%d)", plan.Done)
	}
	if l := plan.Lane(UnassignedLane); l == nil || !capNear(l.Hours, 1) {
		t.Errorf("remaining = %+v, want 1h (4h estimate - 3h logged)", l)
	}
}
