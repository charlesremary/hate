// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

func ids(ws []WorkItem) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.ID)
	}
	return out
}

func eqIDs(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// HATE-43sr tc5: chain A -> B -> C with A open: ready=[A], next=[B]; once A is
// done, ready=[B], next=[C].
func TestComputeReadyChain(t *testing.T) {
	a, b, c := capT("A", 2, ""), capT("B", 2, "", "A"), capT("C", 2, "", "B")
	tickets := []*ticket.Ticket{c, b, a}
	rep := ComputeReady(tickets, NewEstimateContext(tickets, 0, nil))
	if !eqIDs(ids(rep.Ready), "A") || !eqIDs(ids(rep.Next), "B") {
		t.Fatalf("ready=%v next=%v, want [A] [B]", ids(rep.Ready), ids(rep.Next))
	}
	if len(rep.Stages) != 3 || rep.Stages[0].Stage != 1 || !eqIDs(ids(rep.Stages[2].Tickets), "C") || rep.Stages[1].Hours != 2 {
		t.Errorf("stages = %+v", rep.Stages)
	}
	if w := rep.Stages[2].Tickets[0]; !eqIDs(w.WaitsOn, "B") || w.Stage != 3 {
		t.Errorf("C = %+v", w)
	}

	a.Status = "complete"
	rep = ComputeReady(tickets, NewEstimateContext(tickets, 0, nil))
	if !eqIDs(ids(rep.Ready), "B") || !eqIDs(ids(rep.Next), "C") {
		t.Errorf("after A done: ready=%v next=%v, want [B] [C]", ids(rep.Ready), ids(rep.Next))
	}
}

// Blocked tickets are never ready (and unlock nothing); parents, backlog and
// cancelled tickets are left out; ready is in priority order; next needs every
// open predecessor in the ready set.
func TestComputeReadyRules(t *testing.T) {
	blocked := capT("BL", 1, "")
	blocked.Status = "blocked"
	afterBlocked := capT("AB", 1, "", "BL")
	hi := capT("HI", 1, "")
	hi.Priority = "high"
	lo := capT("LO", 1, "")
	both := capT("BOTH", 1, "", "HI", "AB") // AB isn't ready → not next
	parent := &ticket.Ticket{ID: "F", Title: "Feature", Status: "not_started", Tags: []string{"cfp:5"}}
	child := capT("CH", 1, "")
	child.Tags = append(child.Tags, "parent:F")
	bl := capT("BK", 1, "")
	bl.Tags = append(bl.Tags, ticket.BacklogTag)
	dependsOnBacklog := capT("DB", 1, "", "BK")
	tickets := []*ticket.Ticket{lo, blocked, afterBlocked, hi, both, parent, child, bl, dependsOnBacklog}
	rep := ComputeReady(tickets, NewEstimateContext(tickets, 0, nil))
	if !eqIDs(ids(rep.Ready), "HI", "CH", "DB", "LO") {
		t.Errorf("ready = %v, want [HI CH DB LO]", ids(rep.Ready))
	}
	if len(rep.Next) != 0 {
		t.Errorf("next = %v, want none", ids(rep.Next))
	}
	for _, g := range rep.Stages {
		for _, w := range g.Tickets {
			if w.ID == "F" || w.ID == "BK" {
				t.Errorf("%s should not be in the work order", w.ID)
			}
		}
	}
	if st := DependencyStages(tickets); st["BOTH"] != 2 || st["AB"] != 1 || st["F"] != 0 {
		t.Errorf("stages = %v", st)
	}
}

// A cycle doesn't hang and every ticket still gets a stage.
func TestDependencyStagesCycle(t *testing.T) {
	tickets := []*ticket.Ticket{capT("X", 1, "", "Y"), capT("Y", 1, "", "X")}
	st := DependencyStages(tickets)
	if len(st) != 2 {
		t.Errorf("stages = %v", st)
	}
}
