// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"sort"

	"hate/internal/ticket"
)

// PhaseProgress is the hours-weighted rollup for a single phase.
type PhaseProgress struct {
	Phase string `json:"phase"` // raw phase value; "" for tickets with no phase
	Label string `json:"label"` // human-readable; "(no phase)" when Phase is ""

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
	HoursBased      bool    `json:"hours_based"`      // false → % fell back to ticket count

	PlannedStart string `json:"planned_start"` // earliest planned_start_date in phase
	DueDate      string `json:"due_date"`      // latest due_date in phase
}

// RollupReport is the per-phase progress rollup for a project.
type RollupReport struct {
	GeneratedAt     string          `json:"generated_at"`
	Basis           string          `json:"basis"`            // "hours-weighted"
	TotalTickets    int             `json:"total_tickets"`    // in-scope (cancelled excluded)
	PercentComplete float64         `json:"percent_complete"` // project-level, hours-weighted
	Phases          []PhaseProgress `json:"phases"`
}

// isCancelled reports whether a ticket was force-closed / descoped rather than
// genuinely completed. Such tickets are dropped scope, not finished work, so
// they're excluded from the percentage and counted on their own line.
func isCancelled(t *ticket.Ticket) bool {
	return t.Status == "closed" && t.CancellationReason != nil && *t.CancellationReason != ""
}

// isComplete reports whether a ticket has reached a non-cancelled terminal
// status (complete or a normal close).
func isComplete(t *ticket.Ticket) bool {
	return ticket.Contains(ticket.ClosedStatuses, t.Status) && !isCancelled(t)
}

// PhaseRollup groups tickets by their phase field and computes an
// hours-weighted percent-complete per phase, plus a status breakdown and
// rolled-up start/due dates.
//
// Decisions baked in (see the article series / README for the why):
//   - Percent is hours-weighted: done estimated hours ÷ total estimated hours,
//     using EstimatedHours. Completion is binary (a ticket is done only once it
//     reaches complete/closed) — no partial credit for in-flight work.
//   - Cancelled (force-closed) tickets are treated as descoped: counted on
//     CancelledCount and excluded from the scope and the percentage.
//   - Tickets with no estimate contribute nothing to the hours math. Work
//     tickets among them are surfaced via UnsizedCount (parents, meetings, and
//     admin are never sized, so they aren't counted). A phase with no estimated
//     hours has no denominator, so its percentage falls back to ticket count
//     (HoursBased=false) rather than reporting a misleading zero.
//
// The algorithm is read-only.
func PhaseRollup(tickets []*ticket.Ticket, ctx EstimateContext) RollupReport {
	ctx = ctx.prepared()
	byPhase := map[string]*PhaseProgress{}
	earliest := map[string]string{}
	latest := map[string]string{}

	get := func(phase string) *PhaseProgress {
		p, ok := byPhase[phase]
		if !ok {
			label := phase
			if phase == "" {
				label = "(no phase)"
			}
			p = &PhaseProgress{Phase: phase, Label: label}
			byPhase[phase] = p
		}
		return p
	}

	for _, t := range tickets {
		phase := ""
		if t.Phase != nil {
			phase = *t.Phase
		}
		p := get(phase)

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
		case t.Status == "blocked":
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

	var phases []PhaseProgress
	var projTotalHours, projDoneHours float64
	var projTotalTickets, projDoneTickets int
	for phase, p := range byPhase {
		p.PlannedStart = earliest[phase]
		p.DueDate = latest[phase]
		switch {
		case p.EstHoursTotal > 0:
			p.PercentComplete = round1(p.EstHoursDone / p.EstHoursTotal * 100)
			p.HoursBased = true
		case p.TicketCount > 0:
			// No estimates anywhere in this phase — fall back to ticket count.
			p.PercentComplete = round1(float64(p.CompleteCount) / float64(p.TicketCount) * 100)
			p.HoursBased = false
		}
		phases = append(phases, *p)

		projTotalHours += p.EstHoursTotal
		projDoneHours += p.EstHoursDone
		projTotalTickets += p.TicketCount
		projDoneTickets += p.CompleteCount
	}

	// Sort by phase string ascending; "(no phase)" sorts last.
	sort.SliceStable(phases, func(i, j int) bool {
		if (phases[i].Phase == "") != (phases[j].Phase == "") {
			return phases[j].Phase == ""
		}
		return phases[i].Phase < phases[j].Phase
	})

	report := RollupReport{
		GeneratedAt:  ticket.NowISO(),
		Basis:        "hours-weighted",
		TotalTickets: projTotalTickets,
		Phases:       phases,
	}
	switch {
	case projTotalHours > 0:
		report.PercentComplete = round1(projDoneHours / projTotalHours * 100)
	case projTotalTickets > 0:
		report.PercentComplete = round1(float64(projDoneTickets) / float64(projTotalTickets) * 100)
	}
	if report.Phases == nil {
		report.Phases = []PhaseProgress{}
	}
	return report
}

// round1 rounds to one decimal place.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
