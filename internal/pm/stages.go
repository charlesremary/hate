// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"sort"

	"hate/internal/ticket"
)

// Dependency stages: the one place stages are computed. A ticket's stage is the
// length of its longest chain of open predecessors, over the open, in-scope
// tickets (not backlog, not cancelled, not yet past the build stage, as in the
// capacity schedule): stage 0 needs nothing still open, stage 1 waits on stage
// 0 work, and so on. Stages ignore people and time, so they are an ordering,
// not a schedule. They order the capacity schedule's work, the Tickets tab's
// Work order view, GET /ready for agents, and block planning.

// DependencyStages returns the 0-based stage of every open, in-scope ticket
// (feature parents included). Predecessors that are done, cancelled, backlog or
// unknown don't count. A dependency cycle is cut where it closes (the edge
// back into the chain counts as 0).
func DependencyStages(tickets []*ticket.Ticket) map[string]int {
	open := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		if isOpenWork(t) {
			open[t.ID] = t
		}
	}
	stage := make(map[string]int, len(open))
	var depthOf func(id string, stk map[string]bool) int
	depthOf = func(id string, stk map[string]bool) int {
		if d, ok := stage[id]; ok {
			return d
		}
		if stk[id] {
			return 0
		}
		stk[id] = true
		best := 0
		for _, p := range open[id].Predecessors {
			if _, ok := open[p]; ok && p != id {
				if d := depthOf(p, stk) + 1; d > best {
					best = d
				}
			}
		}
		delete(stk, id)
		stage[id] = best
		return best
	}
	for id := range open {
		depthOf(id, map[string]bool{})
	}
	return stage
}

// WorkItem is one open ticket in the work order (GET /ready).
type WorkItem struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	Priority       string   `json:"priority"`
	Assignee       string   `json:"assignee"`
	Phase          string   `json:"phase"`
	Stage          int      `json:"stage"`           // 1-based
	EstimatedHours float64  `json:"estimated_hours"` // 0 when unsized
	WaitsOn        []string `json:"waits_on"`        // open predecessors
}

// StageGroup is one dependency stage of the work order.
type StageGroup struct {
	Stage   int        `json:"stage"` // 1-based
	Hours   float64    `json:"hours"` // Σ estimated hours
	Tickets []WorkItem `json:"tickets"`
}

// ReadyReport is GET /ready: what can be worked now and what it unlocks.
type ReadyReport struct {
	// Ready: open work tickets whose predecessors are all done (stage 1), not
	// blocked. Work these in parallel.
	Ready []WorkItem `json:"ready"`
	// Next: tickets unlocked once the ready set finishes (every open
	// predecessor is in Ready).
	Next []WorkItem `json:"next"`
	// Stages: every open work ticket in dependency order (stage, then
	// priority, then id).
	Stages []StageGroup `json:"stages"`
}

// ComputeReady builds the work order from DependencyStages. Feature parents
// (containers) and backlog are left out; blocked tickets are listed in their
// stage but never ready. Read-only.
func ComputeReady(tickets []*ticket.Ticket, ctx EstimateContext) ReadyReport {
	ctx = ctx.prepared()
	stages := DependencyStages(tickets)
	rep := ReadyReport{Ready: []WorkItem{}, Next: []WorkItem{}, Stages: []StageGroup{}}

	var items []WorkItem
	byID := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		byID[t.ID] = t
	}
	for _, t := range tickets {
		st, ok := stages[t.ID]
		if !ok || ctx.idx.hasChildren[t.ID] {
			continue
		}
		h, src := EstimatedHours(t, ctx)
		if src == SourceConverted || src == SourceLegacy || h < 0 {
			h = 0
		}
		w := WorkItem{ID: t.ID, Title: t.Title, Status: t.Status, Priority: t.Priority, Stage: st + 1, EstimatedHours: h, WaitsOn: []string{}}
		if t.Assignee != nil {
			w.Assignee = *t.Assignee
		}
		if t.Phase != nil {
			w.Phase = *t.Phase
		}
		for _, p := range t.Predecessors {
			if _, open := stages[p]; open && p != t.ID {
				w.WaitsOn = append(w.WaitsOn, p)
			}
		}
		items = append(items, w)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Stage != b.Stage {
			return a.Stage < b.Stage
		}
		if ra, rb := priorityRank(a.Priority), priorityRank(b.Priority); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})

	ready := map[string]bool{}
	for _, w := range items {
		if w.Stage == 1 && w.Status != "blocked" {
			ready[w.ID] = true
			rep.Ready = append(rep.Ready, w)
		}
	}
	for _, w := range items {
		if ready[w.ID] || w.Status == "blocked" || len(w.WaitsOn) == 0 {
			continue
		}
		all := true
		for _, p := range w.WaitsOn {
			if !ready[p] {
				all = false
				break
			}
		}
		if all {
			rep.Next = append(rep.Next, w)
		}
	}
	for _, w := range items {
		n := len(rep.Stages)
		if n == 0 || rep.Stages[n-1].Stage != w.Stage {
			rep.Stages = append(rep.Stages, StageGroup{Stage: w.Stage, Tickets: []WorkItem{}})
			n++
		}
		rep.Stages[n-1].Tickets = append(rep.Stages[n-1].Tickets, w)
		rep.Stages[n-1].Hours += w.EstimatedHours
	}
	return rep
}
