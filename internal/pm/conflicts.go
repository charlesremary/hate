// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"sort"
	"time"

	"hate/internal/ticket"
)

// HoursPerDay converts a person-day of effort into hours. Mirrors the help
// docs: "an L = 5 means roughly five working days of focused work."
const HoursPerDay = 8.0

// ConflictTicket points at one ticket contributing to a day's overload.
type ConflictTicket struct {
	TicketID  string  `json:"ticket_id"`
	Title     string  `json:"title"`
	Hours     float64 `json:"hours"`        // hours this ticket contributes on the conflict day
	StartDate string  `json:"start_date"`   // planned_start_date
	DueDate   string  `json:"due_date"`     // due_date
}

// ConflictDay is a single (resource, date) where assigned hours exceed capacity.
type ConflictDay struct {
	Date          string           `json:"date"`            // YYYY-MM-DD
	AssignedHours float64          `json:"assigned_hours"`  // sum of all contributing tickets
	CapacityHours float64          `json:"capacity_hours"`  // the resource's daily availability
	OverByHours   float64          `json:"over_by_hours"`
	Tickets       []ConflictTicket `json:"tickets"`
}

// ResourceConflicts collects every over-allocated day for one resource.
type ResourceConflicts struct {
	Email         string        `json:"email"`
	Name          string        `json:"name"`
	CapacityHours float64       `json:"capacity_hours"`
	Days          []ConflictDay `json:"days"`
}

// ScheduleWarning flags tickets that are skipped by the conflict check (no
// dates, no assignee) so the PM knows what's invisible to the analysis.
type ScheduleWarning struct {
	TicketID string `json:"ticket_id"`
	Title    string `json:"title"`
	Reason   string `json:"reason"`
}

// PhaseConflictSummary is the conflict picture restricted to one phase. The
// per-day totals reflect only the tickets in that phase — useful when the PM
// wants to fix one phase at a time.
type PhaseConflictSummary struct {
	Phase           string              `json:"phase"`            // "" for tickets with no phase
	Label           string              `json:"label"`            // human-readable, e.g. "Phase 1" or "(no phase)"
	TicketsAnalyzed int                 `json:"tickets_analyzed"`
	DaysOver        int                 `json:"days_over"`        // total over-allocated day-cells across resources
	Conflicts       []ResourceConflicts `json:"conflicts"`
	Warnings        []ScheduleWarning   `json:"warnings"`
}

// ConflictReport is the full output of CheckScheduleConflicts. Top-level
// Conflicts/Warnings hold the "All phases" view; PhaseSummaries hold the
// per-phase breakdown so the frontend can pivot without re-fetching.
type ConflictReport struct {
	CheckedAt        string                 `json:"checked_at"`
	TicketsAnalyzed  int                    `json:"tickets_analyzed"`
	ResourcesChecked int                    `json:"resources_checked"`
	Conflicts        []ResourceConflicts    `json:"conflicts"`
	Warnings         []ScheduleWarning      `json:"warnings"`
	PhaseSummaries   []PhaseConflictSummary `json:"phase_summaries"`
}

// effortDaysFor returns the configured planned-days for a t-shirt size, falling
// back to the project's effort_to_days map and then to defaults.
func effortDaysFor(effort string, effortToDays map[string]int) int {
	if effort == "" {
		return 0
	}
	if d, ok := effortToDays[effort]; ok {
		return d
	}
	if d, ok := ticket.DefaultEffortToDays[effort]; ok {
		return d
	}
	return 0
}

// businessDaysBetween counts inclusive business days from start to end (skips Sat/Sun).
func businessDaysBetween(start, end time.Time) int {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	n := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		wd := d.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			continue
		}
		n++
	}
	return n
}

// CheckScheduleConflicts walks every non-terminal ticket with a planned start,
// due date, and assignee, computes per-day load on that assignee, and returns
// every day where the load exceeds the resource's daily capacity.
//
// Tickets skipped (no dates, no assignee, no effort, unknown assignee) are
// reported as warnings so the PM knows the analysis isn't pretending to be
// complete.
//
// PhaseSummaries break the same picture down per phase so the PM can fix one
// phase at a time without seeing tickets from later phases.
func CheckScheduleConflicts(tickets []*ticket.Ticket, resources []ticket.Resource, effortToDays map[string]int, now time.Time) ConflictReport {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	conflicts, warnings, analyzed := analyzeTickets(tickets, resources, effortToDays)

	// Collect distinct phases in order of first appearance, including ""
	// for "no phase".
	phaseOrder := []string{}
	seen := map[string]bool{}
	for _, t := range tickets {
		phase := ""
		if t.Phase != nil {
			phase = *t.Phase
		}
		if !seen[phase] {
			seen[phase] = true
			phaseOrder = append(phaseOrder, phase)
		}
	}
	sort.SliceStable(phaseOrder, func(i, j int) bool {
		// Empty ("(no phase)") last; otherwise alphabetical.
		if phaseOrder[i] == "" {
			return false
		}
		if phaseOrder[j] == "" {
			return true
		}
		return phaseOrder[i] < phaseOrder[j]
	})

	var phaseSummaries []PhaseConflictSummary
	for _, phase := range phaseOrder {
		filtered := []*ticket.Ticket{}
		for _, t := range tickets {
			p := ""
			if t.Phase != nil {
				p = *t.Phase
			}
			if p == phase {
				filtered = append(filtered, t)
			}
		}
		pc, pw, pa := analyzeTickets(filtered, resources, effortToDays)
		daysOver := 0
		for _, rc := range pc {
			daysOver += len(rc.Days)
		}
		label := phase
		if label == "" {
			label = "(no phase)"
		}
		phaseSummaries = append(phaseSummaries, PhaseConflictSummary{
			Phase:           phase,
			Label:           label,
			TicketsAnalyzed: pa,
			DaysOver:        daysOver,
			Conflicts:       pc,
			Warnings:        pw,
		})
	}

	return ConflictReport{
		CheckedAt:        now.Format("2006-01-02T15:04:05Z"),
		TicketsAnalyzed:  analyzed,
		ResourcesChecked: len(resources),
		Conflicts:        conflicts,
		Warnings:         warnings,
		PhaseSummaries:   phaseSummaries,
	}
}

// analyzeTickets is the conflict-detection core. Pure: given a (possibly
// pre-filtered) ticket slice, returns the conflicts, the skip-warnings, and
// the count of tickets actually analyzed.
func analyzeTickets(tickets []*ticket.Ticket, resources []ticket.Resource, effortToDays map[string]int) ([]ResourceConflicts, []ScheduleWarning, int) {
	// Index resources by email for quick lookup.
	resByEmail := map[string]ticket.Resource{}
	for _, r := range resources {
		resByEmail[r.Email] = r
	}

	// Accumulator: email -> date(YYYY-MM-DD) -> [{ticket, hours}]
	type loadEntry struct {
		ticket *ticket.Ticket
		hours  float64
	}
	load := map[string]map[string][]loadEntry{}

	var warnings []ScheduleWarning
	analyzed := 0

	for _, t := range tickets {
		// Skip terminal tickets — closed work isn't competing for capacity.
		if t.Status == "closed" || t.Status == "complete" {
			continue
		}
		// Must have both dates.
		if t.PlannedStartDate == nil || t.DueDate == nil || *t.PlannedStartDate == "" || *t.DueDate == "" {
			warnings = append(warnings, ScheduleWarning{
				TicketID: t.ID, Title: t.Title,
				Reason: "missing planned start or due date",
			})
			continue
		}
		// Must have an assignee.
		if t.Assignee == nil || *t.Assignee == "" {
			warnings = append(warnings, ScheduleWarning{
				TicketID: t.ID, Title: t.Title,
				Reason: "no assignee",
			})
			continue
		}
		// Must have an effort.
		effort := ""
		if t.Effort != nil {
			effort = *t.Effort
		}
		days := effortDaysFor(effort, effortToDays)
		if days <= 0 {
			warnings = append(warnings, ScheduleWarning{
				TicketID: t.ID, Title: t.Title,
				Reason: "no effort size set",
			})
			continue
		}

		start := parseDate(*t.PlannedStartDate)
		end := parseDate(*t.DueDate)
		span := businessDaysBetween(start, end)
		if span <= 0 {
			warnings = append(warnings, ScheduleWarning{
				TicketID: t.ID, Title: t.Title,
				Reason: "due date is before planned start",
			})
			continue
		}
		totalHours := float64(days) * HoursPerDay
		dailyHours := totalHours / float64(span)

		// Distribute the daily-hours load over every business day in the span.
		assignee := *t.Assignee
		if _, ok := load[assignee]; !ok {
			load[assignee] = map[string][]loadEntry{}
		}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			wd := d.Weekday()
			if wd == time.Saturday || wd == time.Sunday {
				continue
			}
			key := d.Format("2006-01-02")
			load[assignee][key] = append(load[assignee][key], loadEntry{ticket: t, hours: dailyHours})
		}
		analyzed++
	}

	var conflicts []ResourceConflicts
	for email, days := range load {
		res, known := resByEmail[email]
		capacity := ticket.DefaultDailyHours
		name := email
		if known {
			capacity = res.EffectiveDailyHours()
			if res.Name != "" {
				name = res.Name
			}
		}
		// Walk days in chronological order.
		dayKeys := make([]string, 0, len(days))
		for k := range days {
			dayKeys = append(dayKeys, k)
		}
		sort.Strings(dayKeys)

		var conflictDays []ConflictDay
		for _, dk := range dayKeys {
			entries := days[dk]
			total := 0.0
			for _, e := range entries {
				total += e.hours
			}
			if total <= capacity+0.001 {
				continue
			}
			ct := make([]ConflictTicket, 0, len(entries))
			for _, e := range entries {
				start := ""
				if e.ticket.PlannedStartDate != nil {
					start = *e.ticket.PlannedStartDate
				}
				due := ""
				if e.ticket.DueDate != nil {
					due = *e.ticket.DueDate
				}
				ct = append(ct, ConflictTicket{
					TicketID:  e.ticket.ID,
					Title:     e.ticket.Title,
					Hours:     e.hours,
					StartDate: start,
					DueDate:   due,
				})
			}
			// Sort tickets within a day by hours desc so the worst offender is first.
			sort.SliceStable(ct, func(i, j int) bool { return ct[i].Hours > ct[j].Hours })
			conflictDays = append(conflictDays, ConflictDay{
				Date:          dk,
				AssignedHours: total,
				CapacityHours: capacity,
				OverByHours:   total - capacity,
				Tickets:       ct,
			})
		}
		if len(conflictDays) == 0 {
			continue
		}
		conflicts = append(conflicts, ResourceConflicts{
			Email:         email,
			Name:          name,
			CapacityHours: capacity,
			Days:          conflictDays,
		})
	}
	// Sort resources by worst overage first.
	sort.SliceStable(conflicts, func(i, j int) bool {
		var mi, mj float64
		for _, d := range conflicts[i].Days {
			if d.OverByHours > mi {
				mi = d.OverByHours
			}
		}
		for _, d := range conflicts[j].Days {
			if d.OverByHours > mj {
				mj = d.OverByHours
			}
		}
		return mi > mj
	})

	return conflicts, warnings, analyzed
}
