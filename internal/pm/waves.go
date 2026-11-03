// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"sort"

	"hate/internal/ticket"
)

// Dependency waves: the one place waves are computed. A ticket's wave is the
// length of its longest chain of open predecessors, over the open, in-scope
// tickets (not backlog, not cancelled, not yet past development (dev_complete or later), as in the
// capacity schedule): wave 0 needs nothing still open, wave 1 waits on wave
// 0 work, and so on. Waves ignore people and time, so they are an ordering,
// not a schedule. They order the capacity schedule's work, the Tickets tab's
// Work order view, GET /ready for agents, and block planning.

// DependencyWaves returns the 0-based wave of every open, in-scope ticket
// (feature parents included). Predecessors that are done, cancelled, backlog or
// unknown don't count. A dependency cycle is cut where it closes (the edge
// back into the chain counts as 0).
func DependencyWaves(tickets []*ticket.Ticket) map[string]int {
	open := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		if isOpenWork(t) {
			open[t.ID] = t
		}
	}
	wave := make(map[string]int, len(open))
	var depthOf func(id string, stk map[string]bool) int
	depthOf = func(id string, stk map[string]bool) int {
		if d, ok := wave[id]; ok {
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
		wave[id] = best
		return best
	}
	for id := range open {
		depthOf(id, map[string]bool{})
	}
	return wave
}

// WorkItem is one open ticket in the work order (GET /ready).
type WorkItem struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	Priority       string   `json:"priority"`
	Assignee       string   `json:"assignee"`
	Lifecycle      string   `json:"lifecycle"`
	Block          int      `json:"block,omitempty"`
	Wave           int      `json:"wave"`            // 1-based
	EstimatedHours float64  `json:"estimated_hours"` // 0 when unsized
	WaitsOn        []string `json:"waits_on"`        // open predecessors
	Blocked        bool     `json:"blocked,omitempty"` // flagged blocked: never ready
}

// WaveGroup is one dependency wave of the work order.
type WaveGroup struct {
	Wave    int        `json:"wave"`  // 1-based
	Hours   float64    `json:"hours"` // Σ estimated hours
	Tickets []WorkItem `json:"tickets"`
}

// ReadyReport is GET /ready: what can be worked now and what it unlocks.
type ReadyReport struct {
	// Ready: open work tickets whose predecessors are all done (wave 1), not
	// blocked. Work these in parallel.
	Ready []WorkItem `json:"ready"`
	// Next: tickets unlocked once the ready set finishes (every open
	// predecessor is in Ready).
	Next []WorkItem `json:"next"`
	// Waves: every open work ticket in dependency order (wave, then
	// priority, then id).
	Waves []WaveGroup `json:"waves"`
}

// ComputeReady builds the work order from DependencyWaves. Feature parents
// (containers) and backlog are left out; blocked tickets are listed in their
// wave but never ready. Read-only.
func ComputeReady(tickets []*ticket.Ticket, ctx EstimateContext) ReadyReport {
	ctx = ctx.prepared()
	waves := DependencyWaves(tickets)
	rep := ReadyReport{Ready: []WorkItem{}, Next: []WorkItem{}, Waves: []WaveGroup{}}

	var items []WorkItem
	byID := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		byID[t.ID] = t
	}
	for _, t := range tickets {
		st, ok := waves[t.ID]
		if !ok || ctx.idx.hasChildren[t.ID] {
			continue
		}
		h, src := EstimatedHours(t, ctx)
		if src == SourceConverted || src == SourceLegacy || h < 0 {
			h = 0
		}
		w := WorkItem{ID: t.ID, Title: t.Title, Status: t.Status, Priority: t.Priority, Wave: st + 1, EstimatedHours: h, WaitsOn: []string{}}
		if t.Assignee != nil {
			w.Assignee = *t.Assignee
		}
		if t.Lifecycle != nil {
			w.Lifecycle = *t.Lifecycle
		}
		w.Block = t.Block
		w.Blocked = t.Blocked != nil
		for _, p := range t.Predecessors {
			if _, open := waves[p]; open && p != t.ID {
				w.WaitsOn = append(w.WaitsOn, p)
			}
		}
		items = append(items, w)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Wave != b.Wave {
			return a.Wave < b.Wave
		}
		if ra, rb := priorityRank(a.Priority), priorityRank(b.Priority); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})

	ready := map[string]bool{}
	for _, w := range items {
		if w.Wave == 1 && !w.Blocked {
			ready[w.ID] = true
			rep.Ready = append(rep.Ready, w)
		}
	}
	for _, w := range items {
		if ready[w.ID] || w.Blocked || len(w.WaitsOn) == 0 {
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
		n := len(rep.Waves)
		if n == 0 || rep.Waves[n-1].Wave != w.Wave {
			rep.Waves = append(rep.Waves, WaveGroup{Wave: w.Wave, Tickets: []WorkItem{}})
			n++
		}
		rep.Waves[n-1].Tickets = append(rep.Waves[n-1].Tickets, w)
		rep.Waves[n-1].Hours += w.EstimatedHours
	}
	return rep
}
