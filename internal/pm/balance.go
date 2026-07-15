// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"sort"
	"time"

	"hate/internal/ticket"
)

// BalanceChange is one ticket's proposed (or applied) date change.
type BalanceChange struct {
	TicketID    string  `json:"ticket_id"`
	Title       string  `json:"title"`
	Assignee    string  `json:"assignee"`
	OldStart    string  `json:"old_start"`
	OldDue      string  `json:"old_due"`
	NewStart    string  `json:"new_start"`
	NewDue      string  `json:"new_due"`
	HoursNeeded float64 `json:"hours_needed"`
}

// BalanceSkip is a ticket the algorithm couldn't schedule and why.
type BalanceSkip struct {
	TicketID string `json:"ticket_id"`
	Title    string `json:"title"`
	Reason   string `json:"reason"`
}

// BalanceReport is the full output of BalanceProject.
type BalanceReport struct {
	Algorithm       string          `json:"algorithm"`
	OriginalEndDate string          `json:"original_end_date"` // latest old due across affected tickets
	ProposedEndDate string          `json:"proposed_end_date"` // latest new due across affected tickets
	TicketsAffected int             `json:"tickets_affected"`
	Changes         []BalanceChange `json:"changes"`
	Skipped         []BalanceSkip   `json:"skipped"`
	CycleDetected   bool            `json:"cycle_detected"`
	CycleTicketIDs  []string        `json:"cycle_ticket_ids,omitempty"`
}

// balanceTicket is the algorithm's internal state per ticket.
type balanceTicket struct {
	t              *ticket.Ticket
	hoursNeeded    float64
	hoursRemaining float64
	assignee       string
	predecessors   []string
	newStart       time.Time
	newDue         time.Time
	priorityRank   int // lower = higher priority
	effortDays     float64
}

// priorityRank for sort: critical=0, high=1, medium=2, low=3, unknown=4.
func priorityRank(p string) int {
	switch p {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// nextWeekday advances d by one day, skipping Saturday and Sunday.
func nextWeekday(d time.Time) time.Time {
	d = d.AddDate(0, 0, 1)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// alignToWeekday returns d if it's a weekday, otherwise the next weekday.
func alignToWeekday(d time.Time) time.Time {
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// BalanceProject produces a feasible schedule by simulating workdays forward.
// On each weekday, ready tickets per assignee equally share that person's
// daily capacity. Tickets are considered ready when all their predecessors
// (open and closed) are scheduled (i.e. have a due date in our plan or were
// already terminal).
//
// Tickets that are already in a terminal state (closed, complete) are not
// rescheduled — their existing dates are left intact and they only act as
// satisfied predecessors for downstream work.
//
// Returns a report with proposed changes; the caller decides whether to apply
// them. The algorithm itself is read-only.
func BalanceProject(tickets []*ticket.Ticket, resources []ticket.Resource, effortToDays map[string]float64, projectStart time.Time) BalanceReport {
	if projectStart.IsZero() {
		projectStart = time.Now().UTC()
	}
	projectStart = alignToWeekday(time.Date(
		projectStart.Year(), projectStart.Month(), projectStart.Day(),
		0, 0, 0, 0, time.UTC,
	))

	// Index resources by email; default capacity for unknown assignees.
	resByEmail := map[string]ticket.Resource{}
	for _, r := range resources {
		resByEmail[r.Email] = r
	}

	report := BalanceReport{
		Algorithm: "priority-then-size; equal-split capacity; predecessor-respecting",
		Changes:   []BalanceChange{},
		Skipped:   []BalanceSkip{},
	}

	// Partition tickets: schedulable vs terminal vs skipped-with-reason.
	terminal := map[string]bool{}         // ticket IDs that are closed/complete — treat as already-done predecessors
	terminalDue := map[string]time.Time{} // their effective "due" date for predecessor satisfaction
	bts := []*balanceTicket{}
	btsByID := map[string]*balanceTicket{}

	for _, t := range tickets {
		if ticket.IsBacklog(t) {
			continue // backlog is out of committed scope — never scheduled
		}
		if t.Status == "closed" || t.Status == "complete" {
			terminal[t.ID] = true
			// Use the existing due_date or closed_at as their "done" date so
			// downstream tickets don't start before them.
			var d time.Time
			if t.DueDate != nil && *t.DueDate != "" {
				d = parseDate(*t.DueDate)
			}
			if d.IsZero() && t.ClosedAt != nil && *t.ClosedAt != "" {
				d = parseDate((*t.ClosedAt)[:10])
			}
			if d.IsZero() {
				d = projectStart
			}
			terminalDue[t.ID] = d
			continue
		}
		// Schedulable ticket — must have effort + assignee.
		if t.Assignee == nil || *t.Assignee == "" {
			report.Skipped = append(report.Skipped, BalanceSkip{
				TicketID: t.ID, Title: t.Title, Reason: "no assignee",
			})
			continue
		}
		effort := ""
		if t.Effort != nil {
			effort = *t.Effort
		}
		days := effortDaysFor(effort, effortToDays)
		if days <= 0 {
			report.Skipped = append(report.Skipped, BalanceSkip{
				TicketID: t.ID, Title: t.Title, Reason: "no effort size set",
			})
			continue
		}
		hours := days * HoursPerDay
		// Filter predecessors to those that actually exist in our set.
		preds := []string{}
		for _, pid := range t.Predecessors {
			preds = append(preds, pid)
		}
		bt := &balanceTicket{
			t:              t,
			hoursNeeded:    hours,
			hoursRemaining: hours,
			assignee:       *t.Assignee,
			predecessors:   preds,
			priorityRank:   priorityRank(t.Priority),
			effortDays:     days,
		}
		bts = append(bts, bt)
		btsByID[t.ID] = bt
	}

	if len(bts) == 0 {
		return report
	}

	// Cycle detection — Kahn's-style topo sort to confirm acyclic over our subset.
	inDeg := map[string]int{}
	for _, bt := range bts {
		for _, pid := range bt.predecessors {
			if _, ok := btsByID[pid]; ok {
				inDeg[bt.t.ID]++
			}
		}
	}
	queue := []string{}
	for _, bt := range bts {
		if inDeg[bt.t.ID] == 0 {
			queue = append(queue, bt.t.ID)
		}
	}
	processed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		processed++
		for _, bt := range bts {
			for _, pid := range bt.predecessors {
				if pid == id {
					inDeg[bt.t.ID]--
					if inDeg[bt.t.ID] == 0 {
						queue = append(queue, bt.t.ID)
					}
				}
			}
		}
	}
	if processed < len(bts) {
		report.CycleDetected = true
		for _, bt := range bts {
			if inDeg[bt.t.ID] > 0 {
				report.CycleTicketIDs = append(report.CycleTicketIDs, bt.t.ID)
			}
		}
		return report
	}

	// Helper: are all predecessors of bt scheduled (have a non-zero newDue) or terminal?
	predsReady := func(bt *balanceTicket, day time.Time) bool {
		for _, pid := range bt.predecessors {
			if terminal[pid] {
				// Terminal predecessor "finishes" on terminalDue[pid]; the ticket
				// is ready the weekday after. Block while day <= terminalDue[pid]
				// (i.e. terminalDue is not before day) so we only release strictly
				// after it. This mirrors the scheduled-predecessor path below,
				// which becomes ready a day late "for free" because readiness is
				// snapshotted at the start of each day, before any work is done.
				if !terminalDue[pid].Before(day) {
					return false
				}
				continue
			}
			pbt, ok := btsByID[pid]
			if !ok {
				// Predecessor isn't in our schedulable set and isn't terminal —
				// treat as satisfied (orphaned reference).
				continue
			}
			if pbt.hoursRemaining > 0 {
				return false
			}
			// Predecessor done; ensure we're at or past its due date.
			if !pbt.newDue.IsZero() && !pbt.newDue.Before(day) && !pbt.newDue.Equal(day) {
				return false
			}
		}
		return true
	}

	// Sort comparator: priority desc, then size desc, then id asc.
	cmp := func(a, b *balanceTicket) bool {
		if a.priorityRank != b.priorityRank {
			return a.priorityRank < b.priorityRank
		}
		if a.effortDays != b.effortDays {
			return a.effortDays > b.effortDays
		}
		return a.t.ID < b.t.ID
	}

	// Simulate workdays. Safety cap to avoid runaway loops.
	day := projectStart
	const maxDays = 365 * 5 // 5 years of weekdays
	dayCount := 0
	for {
		remaining := 0
		for _, bt := range bts {
			if bt.hoursRemaining > 0 {
				remaining++
			}
		}
		if remaining == 0 {
			break
		}
		if dayCount > maxDays {
			break
		}

		// Group ready tickets by assignee.
		readyBy := map[string][]*balanceTicket{}
		for _, bt := range bts {
			if bt.hoursRemaining <= 0 {
				continue
			}
			if !predsReady(bt, day) {
				continue
			}
			readyBy[bt.assignee] = append(readyBy[bt.assignee], bt)
		}

		// If nobody can work today, advance until someone can.
		if len(readyBy) == 0 {
			day = nextWeekday(day)
			dayCount++
			continue
		}

		for assignee, ready := range readyBy {
			res, known := resByEmail[assignee]
			capacity := ticket.DefaultDailyHours
			if known {
				capacity = res.EffectiveDailyHours()
			}
			sort.SliceStable(ready, func(i, j int) bool { return cmp(ready[i], ready[j]) })
			perTicket := capacity / float64(len(ready))
			for _, bt := range ready {
				if bt.newStart.IsZero() {
					bt.newStart = day
				}
				bt.hoursRemaining -= perTicket
				if bt.hoursRemaining <= 0.001 {
					bt.hoursRemaining = 0
					bt.newDue = day
				}
			}
		}

		day = nextWeekday(day)
		dayCount++
	}

	// Assemble changes.
	var latestOldDue, latestNewDue time.Time
	for _, bt := range bts {
		oldStart := ""
		if bt.t.PlannedStartDate != nil {
			oldStart = *bt.t.PlannedStartDate
		}
		oldDue := ""
		if bt.t.DueDate != nil {
			oldDue = *bt.t.DueDate
		}
		ns, nd := "", ""
		if !bt.newStart.IsZero() {
			ns = bt.newStart.Format("2006-01-02")
		}
		if !bt.newDue.IsZero() {
			nd = bt.newDue.Format("2006-01-02")
		}
		// Skip the entry if we never managed to schedule it (e.g., its predecessors
		// also never resolved). Surface as a warning instead.
		if ns == "" || nd == "" {
			report.Skipped = append(report.Skipped, BalanceSkip{
				TicketID: bt.t.ID, Title: bt.t.Title,
				Reason: "could not schedule (likely an unsatisfied predecessor or capacity ceiling)",
			})
			continue
		}
		report.Changes = append(report.Changes, BalanceChange{
			TicketID:    bt.t.ID,
			Title:       bt.t.Title,
			Assignee:    bt.assignee,
			OldStart:    oldStart,
			OldDue:      oldDue,
			NewStart:    ns,
			NewDue:      nd,
			HoursNeeded: bt.hoursNeeded,
		})
		if d := parseDate(oldDue); !d.IsZero() && d.After(latestOldDue) {
			latestOldDue = d
		}
		if bt.newDue.After(latestNewDue) {
			latestNewDue = bt.newDue
		}
	}

	// Sort changes: biggest forward shift first (so the most impactful surfaces).
	sort.SliceStable(report.Changes, func(i, j int) bool {
		shiftI := dateShiftDays(report.Changes[i].OldDue, report.Changes[i].NewDue)
		shiftJ := dateShiftDays(report.Changes[j].OldDue, report.Changes[j].NewDue)
		return shiftI > shiftJ
	})

	report.TicketsAffected = len(report.Changes)
	if !latestOldDue.IsZero() {
		report.OriginalEndDate = latestOldDue.Format("2006-01-02")
	}
	if !latestNewDue.IsZero() {
		report.ProposedEndDate = latestNewDue.Format("2006-01-02")
	}
	return report
}

// dateShiftDays returns (new - old) in days, treating missing dates as zero.
func dateShiftDays(oldS, newS string) int {
	od := parseDate(oldS)
	nd := parseDate(newS)
	if od.IsZero() || nd.IsZero() {
		return 0
	}
	return int(nd.Sub(od).Hours() / 24)
}

// ApplyBalance writes the new dates into each ticket file. Returns the list of
// ticket file paths it touched so the caller can stage them in a single git
// commit. Does not commit itself.
func ApplyBalance(repoRoot string, report BalanceReport, author string) ([]string, error) {
	if report.CycleDetected {
		return nil, fmt.Errorf("cannot apply: predecessor cycle detected")
	}
	var paths []string
	for _, c := range report.Changes {
		t, err := ticket.ReadTicket(repoRoot, c.TicketID)
		if err != nil {
			return paths, fmt.Errorf("read %s: %w", c.TicketID, err)
		}
		newStart := c.NewStart
		newDue := c.NewDue
		t.PlannedStartDate = &newStart
		t.DueDate = &newDue
		t.UpdatedAt = ticket.NowISO()
		// Record an activity entry so the change is searchable later.
		t.Activity = append(t.Activity, ticket.Activity{
			Timestamp: ticket.NowISO(),
			Author:    author,
			Action:    "balanced",
			Detail:    fmt.Sprintf("dates set by balance: %s → %s", newStart, newDue),
		})
		if err := ticket.WriteTicket(repoRoot, t); err != nil {
			return paths, fmt.Errorf("write %s: %w", c.TicketID, err)
		}
		paths = append(paths, ticket.TicketPath(repoRoot, c.TicketID))
	}
	return paths, nil
}
