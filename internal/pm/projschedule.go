// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"math"
	"sort"
	"time"

	"hate/internal/ticket"
)

// A projected schedule is a "floating" plan: rather than a committed baseline,
// it forward-schedules the current tickets from a start date (default today)
// using effort durations and predecessor chaining, entirely in memory. Nothing
// is written and no dates are stored — open it tomorrow and it slides a day.
// This lets a project with no real dates still produce a Gantt / draw.io
// artifact, clearly labelled as a projection rather than a commitment.

// ganttStatus maps a ticket status to the coarse status the Gantt colours by.
func ganttStatus(s string) string {
	switch s {
	case "complete", "closed":
		return "complete"
	case "blocked":
		return "blocked"
	case "not_started":
		return "not_started"
	default:
		return "in_progress"
	}
}

// ownerOf returns a task owner from assignee, falling back to creator.
func ownerOf(t *ticket.Ticket) string {
	if t.Assignee != nil && *t.Assignee != "" {
		return *t.Assignee
	}
	return t.Creator
}

// ProjectSchedule forward-schedules committed tickets from `start` using effort
// durations (business days, min 1) and finish-to-start predecessor chaining. It
// returns an in-memory projected Snapshot (with critical path computed) and the
// number of unsized tickets that were assumed to be 1 day. Nothing is persisted.
func ProjectSchedule(projectID, projectName string, tickets []*ticket.Ticket, effortToDays map[string]float64, start time.Time) (*Snapshot, int) {
	byID := map[string]*ticket.Ticket{}
	var scope []*ticket.Ticket
	for _, t := range tickets {
		if ticket.IsBacklog(t) {
			continue // out of committed scope
		}
		scope = append(scope, t)
		byID[t.ID] = t
	}
	start = alignToWeekday(start)

	// Durations (whole business days, min 1); count unsized (assumed 1 day).
	unsized := 0
	dur := map[string]int{}
	for _, t := range scope {
		if t.Effort == nil || *t.Effort == "" {
			unsized++
			dur[t.ID] = 1
			continue
		}
		d := int(math.Round(effortDaysFor(*t.Effort, effortToDays)))
		if d < 1 {
			d = 1
		}
		dur[t.ID] = d
	}

	// In-scope predecessor graph + Kahn topo forward pass.
	preds := map[string][]string{}
	succ := map[string][]string{}
	indeg := map[string]int{}
	for _, t := range scope {
		for _, p := range t.Predecessors {
			if _, ok := byID[p]; ok {
				preds[t.ID] = append(preds[t.ID], p)
				succ[p] = append(succ[p], t.ID)
				indeg[t.ID]++
			}
		}
	}

	startT := map[string]time.Time{}
	endT := map[string]time.Time{}
	scheduled := map[string]bool{}

	var queue []string
	for _, t := range scope {
		if indeg[t.ID] == 0 {
			queue = append(queue, t.ID)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if scheduled[id] {
			continue
		}
		es := start
		if t := byID[id]; t.PlannedStartDate != nil && *t.PlannedStartDate != "" {
			if d := parseDate(*t.PlannedStartDate); !d.IsZero() && d.After(es) {
				es = d // honor an explicit planned start as a floor
			}
		}
		for _, p := range preds[id] {
			if pe, ok := endT[p]; ok {
				if cand := nextWeekday(pe); cand.After(es) {
					es = cand
				}
			}
		}
		es = alignToWeekday(es)
		startT[id] = es
		endT[id] = addBusinessDays(es, dur[id]-1)
		scheduled[id] = true
		for _, s := range succ[id] {
			indeg[s]--
			if indeg[s] == 0 {
				queue = append(queue, s)
			}
		}
		sort.Strings(queue)
	}
	// Any tickets left unscheduled sit in a dependency cycle — place them at the
	// start so they still appear rather than vanish.
	for _, t := range scope {
		if !scheduled[t.ID] {
			startT[t.ID] = start
			endT[t.ID] = addBusinessDays(start, dur[t.ID]-1)
		}
	}

	snap := &Snapshot{
		ProjectID:    projectID,
		ProjectName:  projectName,
		SnapshotDate: fmtDate(start),
	}
	for _, t := range scope {
		snap.Tasks = append(snap.Tasks, SnapshotTask{
			TaskID:       t.ID,
			Title:        t.Title,
			Owner:        ownerOf(t),
			Status:       ganttStatus(t.Status),
			Dependencies: preds[t.ID],
			Baseline: BaselineInfo{
				PlannedStart: fmtDate(startT[t.ID]),
				PlannedEnd:   fmtDate(endT[t.ID]),
				PlannedDays:  dur[t.ID],
			},
		})
	}
	ComputeCriticalPath(snap)
	return snap, unsized
}

// projectedNote builds the descriptor shown above a projected Gantt.
func projectedNote(start time.Time, unsized int) string {
	note := fmt.Sprintf("Projected from %s — not baselined; recomputes each view.", start.Format("Jan 2, 2006"))
	if unsized > 0 {
		note += fmt.Sprintf(" %d unsized task%s assumed 1 day.", unsized, pluralS(unsized))
	}
	return note
}

// RenderProjectedGanttHTML builds the floating schedule and renders the Gantt
// panel for the pre-baseline dashboard.
func RenderProjectedGanttHTML(projectID, projectName string, tickets []*ticket.Ticket, effortToDays map[string]float64, start time.Time, exportURL string) string {
	snap, unsized := ProjectSchedule(projectID, projectName, tickets, effortToDays, start)
	return renderGanttPanel(snap, projectedNote(start, unsized), exportURL)
}
