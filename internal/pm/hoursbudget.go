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
func estimateHours(t *ticket.Ticket, effortToDays map[string]int) float64 {
	effort := ""
	if t.Effort != nil {
		effort = *t.Effort
	}
	return float64(effortDaysFor(effort, effortToDays)) * HoursPerDay
}

// inHoursScope reports whether a ticket counts toward the committed hours
// budget. Cancelled (descoped) and backlog (uncommitted) tickets are excluded,
// matching the phase-rollup / baseline conventions.
func inHoursScope(t *ticket.Ticket) bool {
	return !isCancelled(t) && !ticket.IsBacklog(t)
}

// ---------------------------------------------------------------------------
// Feature 1: hours budget (projected vs spent vs remaining)
// ---------------------------------------------------------------------------

// HoursBudget is the project-wide roll-up of sizing hours vs logged hours.
type HoursBudget struct {
	ProjectedHours float64 `json:"projected_hours"` // Σ effort-hours over in-scope tickets
	SpentHours     float64 `json:"spent_hours"`     // Σ logged hours over in-scope tickets
	RemainingHours float64 `json:"remaining_hours"` // Projected − Spent (negative = over budget)

	SizedTickets   int     `json:"sized_tickets"`   // in-scope tickets carrying an effort size
	UnsizedTickets int     `json:"unsized_tickets"` // in-scope tickets with no effort size
	UnsizedHours   float64 `json:"unsized_hours"`   // hours logged on those unsized tickets
	ExcludedCount  int     `json:"excluded_count"`  // cancelled/backlog tickets left out of scope
	ExcludedHours  float64 `json:"excluded_hours"`  // hours logged on those excluded tickets
}

// ComputeHoursBudget sums projected (effort-based) and spent (logged) hours
// across a project's committed tickets.
func ComputeHoursBudget(tickets []*ticket.Ticket, effortToDays map[string]int) HoursBudget {
	var b HoursBudget
	for _, t := range tickets {
		if !inHoursScope(t) {
			b.ExcludedCount++
			b.ExcludedHours += cosmicLoggedHours(t)
			continue
		}
		spent := cosmicLoggedHours(t)
		b.SpentHours += spent
		est := estimateHours(t, effortToDays)
		if est > 0 {
			b.ProjectedHours += est
			b.SizedTickets++
		} else {
			b.UnsizedTickets++
			b.UnsizedHours += spent
		}
	}
	b.RemainingHours = b.ProjectedHours - b.SpentHours
	return b
}

// RenderHoursBudgetHTML renders the "Hours Budget" summary. Self-styled to drop
// into either dashboard flavor (same idiom as RenderProjectCostHTML).
func RenderHoursBudgetHTML(b HoursBudget) string {
	remColor := "#22c55e" // under budget
	if b.RemainingHours < 0 {
		remColor = "#ef4444" // over budget
	}
	remLabel := "Remaining"
	if b.RemainingHours < 0 {
		remLabel = "Over budget"
	}

	var notes []string
	if b.UnsizedTickets > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d unsized ticket%s (%.1fh logged) — no effort size, so counted in spent but not projected.",
			b.UnsizedTickets, plural(b.UnsizedTickets), b.UnsizedHours))
	}
	if b.ExcludedCount > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d cancelled/backlog ticket%s (%.1fh logged on descoped work) excluded from the committed budget.",
			b.ExcludedCount, plural(b.ExcludedCount), b.ExcludedHours))
	}
	notes = append(notes, "Projected hours are derived from t-shirt effort sizing, not a precise estimate.")
	noteHTML := `<p style="font-size:12px;color:#999;margin-top:10px">` +
		html.EscapeString(strings.Join(notes, " ")) + `</p>`

	stat := func(value, label, color string) string {
		style := "font-size:24px;font-weight:700;line-height:1.1"
		if color != "" {
			style += ";color:" + color
		}
		return fmt.Sprintf(
			`<div style="flex:1;min-width:120px"><div style="%s">%s</div><div style="font-size:12px;color:#666;margin-top:4px">%s</div></div>`,
			style, value, label)
	}

	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:14px">Hours Budget &mdash; projected vs spent</h3>
  <div style="display:flex;gap:24px;flex-wrap:wrap">
    %s
    %s
    %s
  </div>%s
</div>`,
		stat(fmt.Sprintf("%.1fh", b.ProjectedHours), fmt.Sprintf("Projected (%d sized ticket%s)", b.SizedTickets, plural(b.SizedTickets)), ""),
		stat(fmt.Sprintf("%.1fh", b.SpentHours), "Spent", "#1976d2"),
		stat(fmt.Sprintf("%+.1fh", b.RemainingHours), remLabel, remColor),
		noteHTML,
	)
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
func ComputeEstimateVariance(tickets []*ticket.Ticket, effortToDays map[string]int) EstimateVariance {
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

// plural returns "s" unless n is exactly 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
