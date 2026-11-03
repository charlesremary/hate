// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"strings"
	"testing"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// closeIt walks a task to complete with time logged.
func closeIt(t *testing.T, root, id string) {
	t.Helper()
	if _, err := Promote(root, id, "a@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := LogTime(root, id, LogTimeParams{Hours: 1, Description: "work", Date: "2026-11-02", Author: "a@x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Promote(root, id, "a@x"); err != nil {
		t.Fatal(err)
	}
}

// HATE-b15m + HATE-kbq9: rework is a new linked ticket with a category and
// who introduced it; it never takes a share of the feature's CFP.
func TestRework(t *testing.T) {
	root := setupProject(t)
	feat := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "Feature", Tags: []string{"cfp:10"}})
	a := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "Build A", Tags: []string{"parent:" + feat.ID, ticket.ClassFunctional}, Lifecycle: "Build"})
	b := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "Build B", Tags: []string{"parent:" + feat.ID, ticket.ClassFunctional}})
	closeIt(t, root, a.ID)

	for _, tc := range []struct {
		in   ReworkInput
		want string
	}{
		{ReworkInput{Category: "changed request", IntroducedBy: "human", Reason: "client wants blue"}, "new scope"},
		{ReworkInput{Category: "oops", IntroducedBy: "human", Reason: "broken thing"}, "category"},
		{ReworkInput{Category: "defect", Reason: "broken thing"}, "introduced_by"},
		{ReworkInput{Category: "defect", IntroducedBy: "agent", Reason: "x"}, "reason"},
		{ReworkInput{Category: "defect", IntroducedBy: "agent", Reason: "broken thing", BrokenBy: b.ID}, "regression"},
	} {
		if _, err := Rework(root, a.ID, tc.in, "a@x"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.in, err, tc.want)
		}
	}
	if _, err := Rework(root, b.ID, ReworkInput{Category: "defect", IntroducedBy: "agent", Reason: "broken thing"}, "a@x"); err == nil {
		t.Error("rework of an open ticket should be refused")
	}

	est := 2.0
	res, err := Rework(root, a.ID, ReworkInput{Category: "regression", IntroducedBy: "agent", Reason: "transfer drops the caller", BrokenBy: b.ID, Estimate: &est}, "a@x")
	if err != nil {
		t.Fatal(err)
	}
	nt := res.Ticket
	if ticket.ReworkOf(nt) != a.ID || nt.ReworkCategory != "regression" || nt.IntroducedBy != "agent" || nt.BrokenBy != b.ID ||
		ticket.ParentID(nt) != feat.ID || nt.Lifecycle == nil || *nt.Lifecycle != "Build" || !strings.HasPrefix(nt.Title, "Rework: ") {
		t.Fatalf("rework ticket = %+v tags %v", nt, nt.Tags)
	}
	last := res.Original.Activity[len(res.Original.Activity)-1]
	if last.Action != "comment" || !strings.Contains(last.Detail, nt.ID) {
		t.Errorf("original's last activity = %+v", last)
	}
	// CFP split: 10 CFP x rate 1 over the two original code tickets, not three.
	all, _ := ticket.ReadAllTickets(root)
	ctx := pm.NewEstimateContext(all, 1, nil)
	for _, tk := range all {
		h, _ := pm.EstimatedHours(tk, ctx)
		switch tk.ID {
		case a.ID, b.ID:
			if h != 5 {
				t.Errorf("%s estimate %v, want 5 (rework doesn't dilute the split)", tk.ID, h)
			}
		case nt.ID:
			if h != 2 {
				t.Errorf("rework estimate %v, want its own 2h", h)
			}
		}
	}
}

// HATE-6wjb: a ticket waiting only on another is unblocked when that one
// finishes.
func TestWaitsOnClearsWhenFinished(t *testing.T) {
	root := setupProject(t)
	a := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "A"})
	b := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "B"})
	c := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "C"})
	if _, err := Block(root, b.ID, "needs A", []string{a.ID}, "a@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := Block(root, c.ID, "needs A and the client", []string{a.ID, "client IT"}, "a@x"); err != nil {
		t.Fatal(err)
	}
	closeIt(t, root, a.ID)
	if tk, _ := ticket.ReadTicket(root, b.ID); tk.Blocked != nil {
		t.Errorf("B still blocked: %+v", tk.Blocked)
	}
	if tk, _ := ticket.ReadTicket(root, c.ID); tk.Blocked == nil {
		t.Error("C waits on a person too; it should stay blocked")
	}
}

// HATE-1go0: force-closed as done counts as finished; descoped doesn't.
func TestForceCloseCategories(t *testing.T) {
	root := setupProject(t)
	a := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "A", Tags: []string{ticket.ClassConfig}, EstimateHours: f64(4)})
	b := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "B", Tags: []string{ticket.ClassConfig}, EstimateHours: f64(4)})
	create(t, root, ticket.CreateTicketParams{Type: "task", Title: "C", Tags: []string{ticket.ClassConfig}, EstimateHours: f64(4)})
	if _, err := ForceClose(root, a.ID, "", "finished elsewhere", "a@x"); err == nil {
		t.Error("force close without a category should fail")
	}
	if _, err := ForceClose(root, a.ID, "done", "Done: shipped outside hate", "a@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := ForceClose(root, b.ID, "descoped", "client dropped it", "a@x"); err != nil {
		t.Fatal(err)
	}
	all, _ := ticket.ReadAllTickets(root)
	rep := pm.Rollup(all, pm.NewEstimateContext(all, 0, nil), pm.RollupByLifecycle)
	// In scope: A (done) and C; B is out. 4 of 8 hours done.
	if rep.PercentComplete != 50 || rep.TotalTickets != 2 {
		t.Errorf("rollup = %v%% of %d", rep.PercentComplete, rep.TotalTickets)
	}
}

func f64(v float64) *float64 { return &v }
