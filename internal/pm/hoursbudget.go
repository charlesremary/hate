// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"hate/internal/ticket"
)

// Estimated hours for a ticket derive from its t-shirt Effort size: the
// project's effort_to_days mapping (falling back to defaults) converted to hours
// at HoursPerDay. This is coarse sizing, not a precise estimate — the reports
// below label their numbers as a "sizing budget," never as a hard estimate.

// estimateHours returns a ticket's projected hours from its effort size, or 0
// when the ticket carries no effort size.
func estimateHours(t *ticket.Ticket, effortToDays map[string]float64) float64 {
	effort := ""
	if t.Effort != nil {
		effort = *t.Effort
	}
	return effortDaysFor(effort, effortToDays) * HoursPerDay
}

// EffortHours returns the allotted hours for an effort size (days × HoursPerDay),
// or 0 when the size is empty/unknown. Same basis as the estimate-variance math,
// exported so the time-log gate enforces against the same number.
func EffortHours(effort string, effortToDays map[string]float64) float64 {
	return effortDaysFor(effort, effortToDays) * HoursPerDay
}

// inHoursScope reports whether a ticket counts toward the committed hours
// budget. Cancelled (descoped) and backlog (uncommitted) tickets are excluded,
// matching the phase-rollup / baseline conventions.
func inHoursScope(t *ticket.Ticket) bool {
	return !isCancelled(t) && !ticket.IsBacklog(t)
}

// ---------------------------------------------------------------------------
// Feature 1: hours budget — two pools (work vs admin/meeting) burned down
// ---------------------------------------------------------------------------

// isAdminMeeting reports whether a ticket draws from the admin/meeting pool
// (administration + meeting) rather than the work pool.
func isAdminMeeting(t *ticket.Ticket) bool {
	return t.Type == "administration" || t.Type == "meeting"
}

// PoolBudget is one hour pool's burn-down: logged hours vs its budget.
type PoolBudget struct {
	Budget      *float64 `json:"budget"`       // nil when no budget set
	Spent       float64  `json:"spent"`        // Σ logged hours in this pool
	Remaining   float64  `json:"remaining"`    // Budget − Spent (0 when no budget)
	PercentUsed float64  `json:"percent_used"` // Spent ÷ Budget × 100 (0 when no budget)
}

func newPoolBudget(budget *float64, spent float64) PoolBudget {
	p := PoolBudget{Budget: budget, Spent: spent}
	if budget != nil && *budget > 0 {
		p.Remaining = *budget - spent
		p.PercentUsed = round1(spent / *budget * 100)
	}
	return p
}

// HoursBudget tracks logged hours against the project's two hour pools: work
// (task/dev_task/design_task) and admin/meeting (administration/meeting).
type HoursBudget struct {
	Work  PoolBudget `json:"work"`
	Admin PoolBudget `json:"admin"`
}

// ComputeHoursBudget buckets every logged hour into the work or admin/meeting
// pool by ticket type and compares each to its budget.
func ComputeHoursBudget(tickets []*ticket.Ticket, workHours, adminHours *float64) HoursBudget {
	var workSpent, adminSpent float64
	for _, t := range tickets {
		if isAdminMeeting(t) {
			adminSpent += cosmicLoggedHours(t)
		} else {
			workSpent += cosmicLoggedHours(t)
		}
	}
	return HoursBudget{
		Work:  newPoolBudget(workHours, workSpent),
		Admin: newPoolBudget(adminHours, adminSpent),
	}
}

// renderPool renders one pool's row: a label, the stat blocks, and a bar.
func renderPool(label string, p PoolBudget) string {
	stat := func(value, sub, color string) string {
		style := "font-size:22px;font-weight:700;line-height:1.1"
		if color != "" {
			style += ";color:" + color
		}
		return fmt.Sprintf(
			`<div style="flex:1;min-width:110px"><div style="%s">%s</div><div style="font-size:12px;color:#666;margin-top:3px">%s</div></div>`,
			style, value, sub)
	}
	head := fmt.Sprintf(`<div style="font-size:12px;text-transform:uppercase;letter-spacing:.5px;color:#888;margin:0 0 8px">%s</div>`, label)

	// No budget for this pool — just report the spend.
	if p.Budget == nil || *p.Budget <= 0 {
		return fmt.Sprintf(`<div style="margin-bottom:18px">%s<div style="display:flex;gap:20px;flex-wrap:wrap">%s</div><p style="font-size:12px;color:#999;margin-top:6px">No budget set for this pool.</p></div>`,
			head, stat(fmt.Sprintf("%.1fh", p.Spent), "Spent", "#1976d2"))
	}

	remColor, remLabel := "#22c55e", "Remaining"
	if p.Remaining < 0 {
		remColor, remLabel = "#ef4444", "Over budget"
	}
	spentColor := "#1976d2"
	if p.PercentUsed >= 100 {
		spentColor = "#ef4444"
	} else if p.PercentUsed >= 85 {
		spentColor = "#eab308"
	}
	barPct := p.PercentUsed
	if barPct > 100 {
		barPct = 100
	}
	bar := fmt.Sprintf(`<div style="margin-top:12px"><div style="height:8px;background:#eee;border-radius:4px;overflow:hidden"><div style="height:100%%;width:%.1f%%;background:%s"></div></div></div>`, barPct, spentColor)

	return fmt.Sprintf(`<div style="margin-bottom:18px">%s<div style="display:flex;gap:20px;flex-wrap:wrap">%s%s%s</div>%s</div>`,
		head,
		stat(fmt.Sprintf("%.1fh", *p.Budget), "Budget", ""),
		stat(fmt.Sprintf("%.1fh", p.Spent), fmt.Sprintf("Spent (%.1f%%)", p.PercentUsed), spentColor),
		stat(fmt.Sprintf("%+.1fh", p.Remaining), remLabel, remColor),
		bar,
	)
}

// RenderHoursBudgetHTML renders the two-pool "Hours Budget" summary. Self-styled
// to drop into either dashboard flavor (same idiom as RenderProjectCostHTML).
func RenderHoursBudgetHTML(b HoursBudget) string {
	header := `<h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:14px">Hours Budget &mdash; logged vs available</h3>`
	hint := ""
	if (b.Work.Budget == nil || *b.Work.Budget <= 0) && (b.Admin.Budget == nil || *b.Admin.Budget <= 0) {
		hint = `<p style="font-size:12px;color:#999;margin-top:2px">Set <strong>Work hours</strong> and <strong>Admin / meeting hours</strong> in Settings to track burn-down against each pool.</p>`
	}
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  %s
  %s
  %s
  %s
</div>`, header, renderPool("Work", b.Work), renderPool("Admin / meeting", b.Admin), hint)
}

// ---------------------------------------------------------------------------
// Feature 2: estimate variance (over/under the sizing budget)
// ---------------------------------------------------------------------------

// VarianceRow is one ticket's projected-vs-spent comparison.
type VarianceRow struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Status        string  `json:"status"`
	Effort        string  `json:"effort"`
	EstimateHours float64 `json:"estimate_hours"`
	SpentHours    float64 `json:"spent_hours"`
	VarianceHours float64 `json:"variance_hours"` // Spent − Estimate (positive = overran)
}

// EstimateVariance splits sized tickets into those that overran vs underran
// their sizing budget. Over/under is only meaningful once a ticket is finished,
// so the two tables cover completed tickets; in-flight tickets already past
// their budget are surfaced as an early-warning list instead.
type EstimateVariance struct {
	Overran        []VarianceRow `json:"overran"`          // completed, spent > estimate
	Underran       []VarianceRow `json:"underran"`         // completed, spent < estimate
	InProgressOver []VarianceRow `json:"in_progress_over"` // not done, already spent > estimate

	OnTargetCount    int `json:"on_target_count"`   // completed, spent == estimate
	CompletedNoTime  int `json:"completed_no_time"` // completed but no hours logged (data gap)
	UnsizedCompleted int `json:"unsized_completed"` // completed but no effort size

	TotalOverrunHours  float64 `json:"total_overrun_hours"`  // Σ positive variance (completed)
	TotalUnderrunHours float64 `json:"total_underrun_hours"` // Σ |negative variance| (completed)
}

// ComputeEstimateVariance builds the over/under-the-budget report.
func ComputeEstimateVariance(tickets []*ticket.Ticket, effortToDays map[string]float64) EstimateVariance {
	var v EstimateVariance
	for _, t := range tickets {
		if !inHoursScope(t) {
			continue
		}
		est := estimateHours(t, effortToDays)
		done := isComplete(t)
		if est <= 0 {
			if done {
				v.UnsizedCompleted++
			}
			continue // no budget to compare against
		}
		spent := cosmicLoggedHours(t)
		row := VarianceRow{
			ID: t.ID, Title: t.Title, Status: t.Status,
			EstimateHours: est, SpentHours: spent, VarianceHours: spent - est,
		}
		if t.Effort != nil {
			row.Effort = *t.Effort
		}

		if !done {
			// In-flight work can only meaningfully be flagged for already
			// exceeding its budget; it hasn't "underrun" until it's finished.
			if spent > est {
				v.InProgressOver = append(v.InProgressOver, row)
			}
			continue
		}

		switch {
		case spent == 0:
			v.CompletedNoTime++ // completed with no logged time — a data gap, not an underrun
		case spent > est:
			v.Overran = append(v.Overran, row)
			v.TotalOverrunHours += row.VarianceHours
		case spent < est:
			v.Underran = append(v.Underran, row)
			v.TotalUnderrunHours += -row.VarianceHours
		default:
			v.OnTargetCount++
		}
	}

	// Biggest miss first in each list.
	sort.SliceStable(v.Overran, func(i, j int) bool { return v.Overran[i].VarianceHours > v.Overran[j].VarianceHours })
	sort.SliceStable(v.Underran, func(i, j int) bool { return v.Underran[i].VarianceHours < v.Underran[j].VarianceHours })
	sort.SliceStable(v.InProgressOver, func(i, j int) bool { return v.InProgressOver[i].VarianceHours > v.InProgressOver[j].VarianceHours })
	return v
}

// RenderEstimateVarianceHTML renders the over/under report. Self-styled to drop
// into either dashboard flavor.
func RenderEstimateVarianceHTML(v EstimateVariance) string {
	esc := html.EscapeString

	varianceTable := func(title, color string, rows []VarianceRow, totalLabel string, total float64) string {
		var body strings.Builder
		for _, r := range rows {
			body.WriteString(fmt.Sprintf(
				`<tr><td style="padding:6px 8px">%s</td><td style="padding:6px 8px">%s</td>`+
					`<td style="padding:6px 8px;text-align:center;text-transform:uppercase;color:#999">%s</td>`+
					`<td style="padding:6px 8px;text-align:right">%.1f</td>`+
					`<td style="padding:6px 8px;text-align:right">%.1f</td>`+
					`<td style="padding:6px 8px;text-align:right;color:%s;font-weight:600">%+.1f</td></tr>`,
				esc(r.ID), esc(r.Title), esc(r.Effort), r.EstimateHours, r.SpentHours, color, r.VarianceHours))
		}
		if body.Len() == 0 {
			return ""
		}
		return fmt.Sprintf(`
  <h4 style="font-size:12px;text-transform:uppercase;color:%s;letter-spacing:.5px;margin:16px 0 8px">%s</h4>
  <table style="width:100%%;border-collapse:collapse;font-size:13px">
    <thead><tr style="text-align:left;color:#666;border-bottom:1px solid #eee">
      <th style="padding:6px 8px">ID</th>
      <th style="padding:6px 8px">Title</th>
      <th style="padding:6px 8px;text-align:center">Size</th>
      <th style="padding:6px 8px;text-align:right">Projected</th>
      <th style="padding:6px 8px;text-align:right">Spent</th>
      <th style="padding:6px 8px;text-align:right">Variance</th>
    </tr></thead>
    <tbody>%s</tbody>
    <tfoot><tr style="font-weight:700;border-top:2px solid #eee">
      <td style="padding:6px 8px" colspan="5">%s</td>
      <td style="padding:6px 8px;text-align:right;color:%s">%+.1fh</td>
    </tr></tfoot>
  </table>`, color, title, body.String(), totalLabel, color, total)
	}

	overHTML := varianceTable("Overran the budget (completed)", "#ef4444", v.Overran, "Total overrun", v.TotalOverrunHours)
	underHTML := varianceTable("Underran the budget (completed)", "#22c55e", v.Underran, "Total underrun", -v.TotalUnderrunHours)

	// Early-warning list: in-flight tickets already past their budget.
	warnHTML := ""
	if len(v.InProgressOver) > 0 {
		var rows strings.Builder
		for _, r := range v.InProgressOver {
			rows.WriteString(fmt.Sprintf(
				`<li style="margin:3px 0">%s &mdash; %s: projected %.1fh, spent %.1fh (<strong style="color:#eab308">%+.1fh</strong>)</li>`,
				esc(r.ID), esc(r.Title), r.EstimateHours, r.SpentHours, r.VarianceHours))
		}
		warnHTML = fmt.Sprintf(`
  <h4 style="font-size:12px;text-transform:uppercase;color:#eab308;letter-spacing:.5px;margin:16px 0 8px">&#9888; In progress, already over budget</h4>
  <ul style="font-size:13px;color:#444;margin:0 0 4px 18px;padding:0">%s</ul>`, rows.String())
	}

	// Footnotes for the tickets that couldn't be compared.
	var notes []string
	if v.OnTargetCount > 0 {
		notes = append(notes, fmt.Sprintf("%d completed on budget.", v.OnTargetCount))
	}
	if v.CompletedNoTime > 0 {
		notes = append(notes, fmt.Sprintf("%d completed with no logged time (excluded).", v.CompletedNoTime))
	}
	if v.UnsizedCompleted > 0 {
		notes = append(notes, fmt.Sprintf("%d completed without an effort size (no budget to compare).", v.UnsizedCompleted))
	}
	noteHTML := ""
	if len(notes) > 0 {
		noteHTML = `<p style="font-size:12px;color:#999;margin-top:12px">` + esc(strings.Join(notes, " ")) + `</p>`
	}

	inner := overHTML + underHTML + warnHTML
	if inner == "" {
		inner = `<p style="padding:4px 0;color:#999;font-size:13px">No completed sized tickets to compare yet.</p>`
	}

	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:4px">Estimate Variance &mdash; over/under the sizing budget</h3>%s%s
</div>`, inner, noteHTML)
}

// ---------------------------------------------------------------------------
// Feature 3: authorized-override log (strict time enforcement)
// ---------------------------------------------------------------------------

// OverrideRow is one time entry that was authorized past a ticket's allotment.
type OverrideRow struct {
	TicketID string  `json:"ticket_id"`
	Title    string  `json:"title"`
	Date     string  `json:"date"`
	Hours    float64 `json:"hours"`
	Author   string  `json:"author"`
	Reason   string  `json:"reason"`
	loggedAt string  // for stable most-recent-first ordering
}

// ComputeOverrides collects every time entry logged past a ticket's allotment
// under strict enforcement (i.e. carrying a recorded authorization reason),
// most recent first.
func ComputeOverrides(tickets []*ticket.Ticket) []OverrideRow {
	var rows []OverrideRow
	for _, t := range tickets {
		for _, te := range t.TimeEntries {
			if strings.TrimSpace(te.ExtendReason) == "" {
				continue
			}
			rows = append(rows, OverrideRow{
				TicketID: t.ID, Title: t.Title, Date: te.Date,
				Hours: te.Hours, Author: te.Author, Reason: te.ExtendReason,
				loggedAt: te.LoggedAt,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date > rows[j].Date
		}
		return rows[i].loggedAt > rows[j].loggedAt
	})
	return rows
}

// RenderOverridesHTML renders the "Authorized overrides" section. Returns "" when
// there are none, so projects that never hit the gate get no empty table.
func RenderOverridesHTML(rows []OverrideRow) string {
	if len(rows) == 0 {
		return ""
	}
	esc := html.EscapeString
	var body strings.Builder
	var total float64
	for _, r := range rows {
		total += r.Hours
		author := r.Author
		if idx := strings.Index(author, "@"); idx >= 0 {
			author = author[:idx]
		}
		if author == "" {
			author = "—"
		}
		label := r.TicketID
		if r.Title != "" {
			label += " &mdash; " + esc(r.Title)
		}
		body.WriteString(fmt.Sprintf(
			`<tr>`+
				`<td style="padding:6px 8px;white-space:nowrap">%s</td>`+
				`<td style="padding:6px 8px;white-space:nowrap">%s</td>`+
				`<td style="padding:6px 8px;text-align:right">%.2f</td>`+
				`<td style="padding:6px 8px;white-space:nowrap">%s</td>`+
				`<td style="padding:6px 8px">%s</td></tr>`,
			label, esc(r.Date), r.Hours, esc(author), esc(r.Reason)))
	}
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">Authorized overrides &mdash; time logged past allotment</h3>
  <table style="width:100%%;border-collapse:collapse;font-size:13px">
    <thead><tr style="text-align:left;color:#666;border-bottom:1px solid #eee">
      <th style="padding:6px 8px">Ticket</th>
      <th style="padding:6px 8px">Date</th>
      <th style="padding:6px 8px;text-align:right">Hours</th>
      <th style="padding:6px 8px">By</th>
      <th style="padding:6px 8px">Reason</th>
    </tr></thead>
    <tbody>%s</tbody>
    <tfoot><tr style="font-weight:700;border-top:2px solid #eee">
      <td style="padding:6px 8px" colspan="2">%d override%s</td>
      <td style="padding:6px 8px;text-align:right">%.1f</td>
      <td style="padding:6px 8px" colspan="2">total extended</td>
    </tr></tfoot>
  </table>
</div>`, body.String(), len(rows), pluralS(len(rows)), total)
}

// pluralS returns "s" unless n == 1.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
