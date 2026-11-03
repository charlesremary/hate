// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"hate/internal/ticket"
)

// Rollup groupings.
const (
	RollupByLifecycle = "lifecycle"
	RollupByBlock     = "block"
)

// GroupProgress is the hours-weighted rollup for one group: a lifecycle
// value or a planning block.
type GroupProgress struct {
	Key   string `json:"key"`   // the lifecycle text, or the block number; "" = none
	Label string `json:"label"` // human-readable; "(no lifecycle)" / "(no block)" for ""
	// Block is the block number when grouping by block (0 = no block);
	// BlockStart / BlockEnd are its dates (see ApplyBlockDates).
	Block      int    `json:"block,omitempty"`
	BlockStart string `json:"block_start,omitempty"`
	BlockEnd   string `json:"block_end,omitempty"`
	BlockDates string `json:"block_dates,omitempty"` // "Nov 30-Dec 11"
	BlockState string `json:"block_state,omitempty"` // past / current / upcoming

	TicketCount     int `json:"ticket_count"`      // tickets in scope (excludes cancelled)
	CompleteCount   int `json:"complete_count"`    // reached a non-cancelled terminal status
	InProgressCount int `json:"in_progress_count"` // started but not done
	NotStartedCount int `json:"not_started_count"`
	BlockedCount    int `json:"blocked_count"`
	CancelledCount  int `json:"cancelled_count"` // force-closed / descoped; excluded from %

	EstHoursTotal float64 `json:"est_hours_total"` // sum of estimated hours in scope
	EstHoursDone  float64 `json:"est_hours_done"`  // estimated hours of complete tickets
	UnsizedCount  int     `json:"unsized_count"`   // in-scope work tickets with no estimate

	PercentComplete float64 `json:"percent_complete"` // 0..100
	HoursBased      bool    `json:"hours_based"`      // false -> % fell back to ticket count

	PlannedStart string `json:"planned_start"` // earliest planned_start_date in the group
	DueDate      string `json:"due_date"`      // latest due_date in the group
}

// RollupReport is the progress rollup for a project, by lifecycle or block.
type RollupReport struct {
	GeneratedAt     string          `json:"generated_at"`
	By              string          `json:"by"`               // RollupByLifecycle or RollupByBlock
	Basis           string          `json:"basis"`            // "hours-weighted"
	TotalTickets    int             `json:"total_tickets"`    // in-scope (cancelled excluded)
	PercentComplete float64         `json:"percent_complete"` // project-level, hours-weighted
	Groups          []GroupProgress `json:"groups"`
}

// isCancelled reports whether a ticket was force-closed / descoped rather than
// genuinely completed. Such tickets are dropped scope, not finished work, so
// they're excluded from the percentage and counted on their own line.
func isCancelled(t *ticket.Ticket) bool {
	return ticket.IsDescoped(t) // force-closed as done counts as finished
}

// isComplete reports whether a ticket has reached a non-cancelled terminal
// status (complete or a normal close).
func isComplete(t *ticket.Ticket) bool {
	return ticket.Contains(ticket.ClosedStatuses, t.Status) && !isCancelled(t)
}

// Rollup groups tickets by lifecycle (by = RollupByLifecycle) or planning
// block (RollupByBlock) and computes an hours-weighted percent-complete per
// group, plus a status breakdown and rolled-up start/due dates.
//
// Decisions baked in (see the article series / README for the why):
//   - Percent is hours-weighted: done estimated hours ÷ total estimated hours,
//     using EstimatedHours. Completion is binary (a ticket is done only once it
//     reaches complete/closed) — no partial credit for in-flight work.
//   - Cancelled (force-closed) tickets are treated as descoped: counted on
//     CancelledCount and excluded from the scope and the percentage.
//   - Tickets with no estimate contribute nothing to the hours math. Work
//     tickets among them are surfaced via UnsizedCount (parents, meetings, and
//     admin are never sized, so they aren't counted). A group with no estimated
//     hours has no denominator, so its percentage falls back to ticket count
//     (HoursBased=false) rather than reporting a misleading zero.
//
// The algorithm is read-only.
func Rollup(tickets []*ticket.Ticket, ctx EstimateContext, by string) RollupReport {
	ctx = ctx.prepared()
	if by != RollupByBlock {
		by = RollupByLifecycle
	}
	byKey := map[string]*GroupProgress{}
	earliest := map[string]string{}
	latest := map[string]string{}

	get := func(t *ticket.Ticket) (string, *GroupProgress) {
		key, label, block := "", "", 0
		if by == RollupByBlock {
			if t.Block > 0 {
				block = t.Block
				key = strconv.Itoa(block)
				label = fmt.Sprintf("Block %02d", block)
			} else {
				label = "(no block)"
			}
		} else {
			if t.Lifecycle != nil {
				key = *t.Lifecycle
			}
			label = key
			if key == "" {
				label = "(no lifecycle)"
			}
		}
		p, ok := byKey[key]
		if !ok {
			p = &GroupProgress{Key: key, Label: label, Block: block}
			byKey[key] = p
		}
		return key, p
	}

	for _, t := range tickets {
		phase, p := get(t)

		if isCancelled(t) {
			p.CancelledCount++
			continue // descoped — excluded from scope and percentage
		}

		p.TicketCount++

		hours, _ := EstimatedHours(t, ctx)
		if hours <= 0 && !ctx.idx.hasChildren[t.ID] && t.Type != "meeting" && t.Type != "administration" {
			p.UnsizedCount++
		}
		p.EstHoursTotal += hours

		switch {
		case isComplete(t):
			p.CompleteCount++
			p.EstHoursDone += hours
		case t.Blocked != nil:
			p.BlockedCount++
		case t.Status == "not_started":
			p.NotStartedCount++
		default:
			p.InProgressCount++
		}

		if t.PlannedStartDate != nil && *t.PlannedStartDate != "" {
			if cur, ok := earliest[phase]; !ok || *t.PlannedStartDate < cur {
				earliest[phase] = *t.PlannedStartDate
			}
		}
		if t.DueDate != nil && *t.DueDate != "" {
			if cur, ok := latest[phase]; !ok || *t.DueDate > cur {
				latest[phase] = *t.DueDate
			}
		}
	}

	var phases []GroupProgress
	var projTotalHours, projDoneHours float64
	var projTotalTickets, projDoneTickets int
	for phase, p := range byKey {
		p.PlannedStart = earliest[phase]
		p.DueDate = latest[phase]
		switch {
		case p.EstHoursTotal > 0:
			p.PercentComplete = round1(p.EstHoursDone / p.EstHoursTotal * 100)
			p.HoursBased = true
		case p.TicketCount > 0:
			// No estimates anywhere in this group: fall back to ticket count.
			p.PercentComplete = round1(float64(p.CompleteCount) / float64(p.TicketCount) * 100)
			p.HoursBased = false
		}
		phases = append(phases, *p)

		projTotalHours += p.EstHoursTotal
		projDoneHours += p.EstHoursDone
		projTotalTickets += p.TicketCount
		projDoneTickets += p.CompleteCount
	}

	// Blocks by number (Block 10 after Block 9), lifecycles by text; the
	// "none" group last.
	sort.SliceStable(phases, func(i, j int) bool {
		a, b := phases[i], phases[j]
		if (a.Key == "") != (b.Key == "") {
			return b.Key == ""
		}
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		return a.Key < b.Key
	})

	report := RollupReport{
		GeneratedAt:  ticket.NowISO(),
		By:           by,
		Basis:        "hours-weighted",
		TotalTickets: projTotalTickets,
		Groups:       phases,
	}
	switch {
	case projTotalHours > 0:
		report.PercentComplete = round1(projDoneHours / projTotalHours * 100)
	case projTotalTickets > 0:
		report.PercentComplete = round1(float64(projDoneTickets) / float64(projTotalTickets) * 100)
	}
	if report.Groups == nil {
		report.Groups = []GroupProgress{}
	}
	return report
}

// ApplyBlockDates fills BlockStart / BlockEnd and the dated label on each
// block group from the project's blocks (by number). No-op when blocks is
// empty or the rollup is by lifecycle.
func ApplyBlockDates(rep *RollupReport, blocks []Block) {
	byN := map[int]Block{}
	for _, b := range blocks {
		byN[b.N] = b
	}
	for i := range rep.Groups {
		g := &rep.Groups[i]
		if b, ok := byN[g.Block]; ok && g.Block > 0 {
			g.BlockStart, g.BlockEnd, g.Label = b.Start, b.End, b.Label
			g.BlockDates, g.BlockState = b.Dates, b.State
		}
	}
}

// round1 rounds to one decimal place.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
