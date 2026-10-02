// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"html"
	"strings"
	"time"

	"hate/internal/ticket"
)

// The Load table answers "is anyone overcommitted?" directly: per person (and
// "unassigned" when applicable), the remaining estimated hours of open work,
// their daily hours, how many working days that is, and when they're free
// according to the capacity-aware schedule from today. With a requested end
// date (project settings) it also shows the working days available and how far
// past it each person runs. No dates on tickets are needed.

// LoadRow is one lane of the Load table.
type LoadRow struct {
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	IsResource bool    `json:"is_resource"`
	Hours      float64 `json:"hours"`        // remaining estimated hours (unsized excluded)
	DailyHours float64 `json:"daily_hours"`  // h/day
	DaysOfWork float64 `json:"days_of_work"` // Hours ÷ DailyHours, one decimal
	Tickets    int     `json:"tickets"`      // open tickets in the lane
	Unsized    int     `json:"unsized"`      // of which unsized
	FreeFrom   string  `json:"free_from"`    // last scheduled end (YYYY-MM-DD); "" when no work
	OverBy     int     `json:"over_by"`      // working days past the target (0 when on time or no target)
}

// LoadReport is the Load table for a project.
type LoadReport struct {
	Start               string    `json:"start"`  // schedule start (today, aligned to a weekday)
	Target              string    `json:"target"` // requested end, "" when unset
	WorkingDaysToTarget int       `json:"working_days_to_target"`
	Rows                []LoadRow `json:"rows"`
	TotalHours          float64   `json:"total_hours"`
	Unsized             int       `json:"unsized"`
	FreeFrom            string    `json:"free_from"` // latest lane free-from
	OverBy              int       `json:"over_by"`   // project-level working days past the target
	SpareDays           int       `json:"spare_days"`
}

// overByDays counts the working days after target up to and including end
// (0 when end is on or before target).
func overByDays(target, end time.Time) int {
	if target.IsZero() || end.IsZero() || !end.After(target) {
		return 0
	}
	return businessDaysBetween(target.AddDate(0, 0, 1), end)
}

// ComputeLoad builds the Load table from the capacity-aware schedule starting
// `today`. target is the project's requested end (YYYY-MM-DD) or "".
func ComputeLoad(tickets []*ticket.Ticket, resources []ticket.Resource, ctx EstimateContext, today time.Time, target string) LoadReport {
	plan := ScheduleCapacity(tickets, resources, ctx, today)
	rep := LoadReport{Start: fmtDate(plan.Start), Unsized: plan.Unsized, Rows: []LoadRow{}}
	tgt := parseDate(target)
	if !tgt.IsZero() {
		rep.Target = fmtDate(tgt)
		rep.WorkingDaysToTarget = businessDaysBetween(plan.Start, tgt)
	}
	var latest time.Time
	for _, l := range plan.Lanes {
		row := LoadRow{
			Key: l.Key, Label: l.Label, IsResource: l.IsResource,
			Hours: round1(l.Hours), DailyHours: l.DailyHours,
			Tickets: l.Tickets, Unsized: l.Unsized,
		}
		if l.DailyHours > 0 {
			row.DaysOfWork = round1(l.Hours / l.DailyHours)
		}
		if l.Tickets > 0 {
			end := plan.EndDate(l.FreeAt)
			row.FreeFrom = fmtDate(end)
			row.OverBy = overByDays(tgt, end)
			if end.After(latest) {
				latest = end
			}
		}
		rep.TotalHours += l.Hours
		rep.Rows = append(rep.Rows, row)
	}
	rep.TotalHours = round1(rep.TotalHours)
	if !latest.IsZero() {
		rep.FreeFrom = fmtDate(latest)
		rep.OverBy = overByDays(tgt, latest)
		if !tgt.IsZero() && !latest.After(tgt) {
			rep.SpareDays = businessDaysBetween(latest.AddDate(0, 0, 1), tgt)
		}
	}
	return rep
}

// fmtNiceDate renders YYYY-MM-DD as "Mon Jan 2, 2006" (or the input when unparsable).
func fmtNiceDate(s string) string {
	if d := parseDate(s); !d.IsZero() {
		return d.Format("Mon Jan 2, 2006")
	}
	return s
}

// RenderLoadHTML renders the Load card for the PM dashboard.
func RenderLoadHTML(rep LoadReport) string {
	esc := html.EscapeString
	header := `<h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">Load &mdash; remaining work by person</h3>`
	card := func(inner string) string {
		return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  %s
  %s
</div>`, header, inner)
	}

	if rep.TotalHours <= 0 {
		msg := "No remaining estimated work."
		if rep.Unsized > 0 {
			msg = fmt.Sprintf("No remaining estimated work &mdash; %d open ticket%s %s no estimate.", rep.Unsized, pluralS(rep.Unsized), boolStr(rep.Unsized == 1, "has", "have"))
		}
		return card(fmt.Sprintf(`<p style="font-size:13px;color:#999">%s</p>`, msg))
	}

	hasTarget := rep.Target != ""
	th := func(s string) string {
		return `<th style="text-align:left;padding:6px 10px;font-size:12px;color:#555;border-bottom:2px solid #e5e7eb;white-space:nowrap">` + s + `</th>`
	}
	td := `<td style="padding:6px 10px;border-bottom:1px solid #f1f5f9;font-size:13px;white-space:nowrap">`
	var sb strings.Builder
	sb.WriteString(`<table style="width:100%;border-collapse:collapse;box-shadow:none"><thead><tr>`)
	sb.WriteString(th("Person") + th("Remaining est. hours") + th("h/day") + th("Days of work") + th("Free from"))
	if hasTarget {
		sb.WriteString(th("Working days to requested end") + th("Over"))
	}
	sb.WriteString(`</tr></thead><tbody>`)
	for _, r := range rep.Rows {
		label := esc(r.Label)
		if !r.IsResource {
			label = `<span style="color:#6b7280">` + label + `</span>`
		}
		unsized := ""
		if r.Unsized > 0 {
			unsized = fmt.Sprintf(` <span style="color:#999;font-size:11px" title="Open tickets with no hour estimate, only an old effort size, or already at their estimate (scheduled at a %gh placeholder, not counted in hours)">+%d unsized</span>`, UnsizedPlaceholderHours, r.Unsized)
		}
		free := "free now"
		if r.FreeFrom != "" {
			free = esc(fmtNiceDate(r.FreeFrom))
		}
		sb.WriteString(`<tr>`)
		sb.WriteString(td + label + `</td>`)
		sb.WriteString(fmt.Sprintf(`%s%.1fh%s</td>`, td, r.Hours, unsized))
		sb.WriteString(fmt.Sprintf(`%s%g</td>`, td, r.DailyHours))
		sb.WriteString(fmt.Sprintf(`%s%.1f</td>`, td, r.DaysOfWork))
		sb.WriteString(td + free + `</td>`)
		if hasTarget {
			sb.WriteString(fmt.Sprintf(`%s%d</td>`, td, rep.WorkingDaysToTarget))
			if r.OverBy > 0 {
				sb.WriteString(fmt.Sprintf(`<td class="load-over" style="padding:6px 10px;border-bottom:1px solid #f1f5f9;font-size:13px;white-space:nowrap;background:#fee2e2;color:#b91c1c;font-weight:600">over by %d day%s</td>`, r.OverBy, pluralS(r.OverBy)))
			} else {
				sb.WriteString(td + `<span style="color:#16a34a">on time</span></td>`)
			}
		}
		sb.WriteString(`</tr>`)
	}
	sb.WriteString(`</tbody></table>`)

	// Project-level line.
	var proj string
	if hasTarget {
		status := `<span style="color:#16a34a;font-weight:600">on time</span>`
		if rep.SpareDays > 0 {
			status += fmt.Sprintf(` (%d working day%s spare)`, rep.SpareDays, pluralS(rep.SpareDays))
		}
		if rep.OverBy > 0 {
			status = fmt.Sprintf(`<span style="background:#fee2e2;color:#b91c1c;font-weight:600;padding:1px 6px;border-radius:4px">over by %d day%s</span>`, rep.OverBy, pluralS(rep.OverBy))
		}
		proj = fmt.Sprintf(`<p style="font-size:13px;color:#333;margin:0 0 10px"><strong>Requested end:</strong> %s &mdash; %d working day%s from today. All remaining work done %s: %s.</p>`,
			esc(fmtNiceDate(rep.Target)), rep.WorkingDaysToTarget, pluralS(rep.WorkingDaysToTarget), esc(fmtNiceDate(rep.FreeFrom)), status)
	} else {
		proj = fmt.Sprintf(`<p style="font-size:13px;color:#333;margin:0 0 10px">All remaining work done <strong>%s</strong> (%.1fh estimated). <span style="color:#999">Set a requested end in Settings to see who is over.</span></p>`,
			esc(fmtNiceDate(rep.FreeFrom)), rep.TotalHours)
	}
	unsizedNote := ""
	if rep.Unsized > 0 {
		unsizedNote = fmt.Sprintf(` %d open ticket%s %s no usable estimate (none, only an old effort size, or already used up) and %s not counted in the hours (each is scheduled at a %gh placeholder).`,
			rep.Unsized, pluralS(rep.Unsized), boolStr(rep.Unsized == 1, "has", "have"), boolStr(rep.Unsized == 1, "is", "are"), UnsizedPlaceholderHours)
	}
	foot := `<p style="font-size:12px;color:#999;margin:10px 0 0">Remaining = estimated hours of open tickets (not complete, closed, cancelled or backlog). ` +
		`Days of work = hours &divide; h/day. Free from = the day their last ticket finishes in the capacity-aware schedule from today, which also waits on predecessors, so it can be later than days of work alone. ` +
		fmt.Sprintf(`Only people consume capacity; unassigned work goes to the only resource when there is one, otherwise to &ldquo;unassigned&rdquo; at %g h/day.`, ticket.DefaultDailyHours) + unsizedNote + `</p>`
	return card(proj + sb.String() + foot)
}
