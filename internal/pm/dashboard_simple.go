// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"sort"
	"strings"

	"hate/internal/ticket"
)

// GenerateSimpleDashboard returns a self-contained HTML dashboard for pre-baseline view.
func GenerateSimpleDashboard(tickets []*ticket.Ticket, projectID, projectName, costHTML string) string {
	// Backlog-tagged tickets are out of committed scope — exclude them from every
	// rollup (count, completion, status mix, table). Surface how many were hidden.
	backlogCount := 0
	active := make([]*ticket.Ticket, 0, len(tickets))
	for _, t := range tickets {
		if ticket.IsBacklog(t) {
			backlogCount++
			continue
		}
		active = append(active, t)
	}
	tickets = active

	backlogNote := ""
	if backlogCount > 0 {
		backlogNote = fmt.Sprintf(`<div style="font-size:11px;color:#999;margin-top:4px">+%d backlog (excluded)</div>`, backlogCount)
	}

	total := len(tickets)
	completionPct := 0
	if total > 0 {
		done := 0
		for _, t := range tickets {
			if t.Status == "complete" || t.Status == "closed" {
				done++
			}
		}
		completionPct = done * 100 / total
	}

	// Status counts
	statusCounts := map[string]int{}
	for _, t := range tickets {
		s := t.Status
		if s == "" {
			s = "unknown"
		}
		statusCounts[s]++
	}

	// Total hours logged
	totalHours := 0.0
	for _, t := range tickets {
		for _, te := range t.TimeEntries {
			totalHours += te.Hours
		}
	}

	// Type counts
	typeCounts := map[string]int{}
	for _, t := range tickets {
		tp := t.Type
		if tp == "" {
			tp = "unknown"
		}
		typeCounts[tp]++
	}

	// Status bar segments
	simpleStatusColors := map[string]string{
		"not_started":  "#bdbdbd",
		"in_progress":  "#ff9800",
		"dev_complete": "#42a5f5",
		"qa_testing":   "#ab47bc",
		"complete":     "#66bb6a",
		"closed":       "#999",
		"rework":       "#e65100",
		"blocked":      "#ef5350",
	}

	var barSegments strings.Builder
	for status, count := range statusCounts {
		pct := 0.0
		if total > 0 {
			pct = float64(count) / float64(total) * 100
		}
		color := simpleStatusColors[status]
		if color == "" {
			color = "#bbb"
		}
		label := strings.Title(strings.ReplaceAll(status, "_", " "))
		barSegments.WriteString(fmt.Sprintf(
			`<div style="width:%.1f%%;background:%s" title="%s: %d"></div>`,
			pct, color, label, count,
		))
	}

	// Status legend (sorted)
	var sortedStatuses []string
	for s := range statusCounts {
		sortedStatuses = append(sortedStatuses, s)
	}
	sort.Strings(sortedStatuses)

	var legendItems strings.Builder
	for _, status := range sortedStatuses {
		count := statusCounts[status]
		color := simpleStatusColors[status]
		if color == "" {
			color = "#bbb"
		}
		label := strings.Title(strings.ReplaceAll(status, "_", " "))
		legendItems.WriteString(fmt.Sprintf(
			`<span style="display:inline-flex;align-items:center;gap:4px;margin-right:14px"><span style="width:10px;height:10px;border-radius:2px;background:%s;display:inline-block"></span>%s: %d</span>`,
			color, label, count,
		))
	}

	// Ticket table rows
	var tableRows strings.Builder
	for _, t := range tickets {
		hours := 0.0
		for _, te := range t.TimeEntries {
			hours += te.Hours
		}
		hoursStr := "\u2014"
		if hours > 0 {
			hoursStr = fmt.Sprintf("%.2f", hours)
		}
		assignee := "\u2014"
		if t.Assignee != nil && *t.Assignee != "" {
			assignee = *t.Assignee
			if idx := strings.Index(assignee, "@"); idx >= 0 {
				assignee = assignee[:idx]
			}
		}
		due := "\u2014"
		if t.DueDate != nil && *t.DueDate != "" {
			due = *t.DueDate
		}
		statusLabel := strings.Title(strings.ReplaceAll(t.Status, "_", " "))
		tableRows.WriteString(fmt.Sprintf(`<tr>
            <td><strong>%s</strong></td>
            <td>%s</td>
            <td>%s</td>
            <td>%s</td>
            <td>%s</td>
            <td>%s</td>
            <td>%s</td>
        </tr>`,
			t.ID, t.Type, t.Title, statusLabel, assignee, hoursStr, due,
		))
	}

	tableBody := tableRows.String()
	if tableBody == "" {
		tableBody = `<tr><td colspan="7" style="color:#999;padding:16px">No tickets yet.</td></tr>`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s &mdash; Dashboard</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; font-size: 14px; color: #1a1a1a; background: #f4f5f7; }
.header { background: #263238; color: #fff; padding: 16px 24px; display: flex; justify-content: space-between; align-items: center; }
.header-left { display: flex; align-items: center; gap: 12px; }
.project-id { font-weight: 700; font-size: 16px; }
.project-name { color: #b0bec5; font-size: 14px; }
.cards { display: flex; gap: 16px; padding: 20px 24px; flex-wrap: wrap; }
.card { background: #fff; border-radius: 8px; padding: 18px 22px; box-shadow: 0 1px 4px rgba(0,0,0,.08); min-width: 150px; flex: 1; }
.card-value { font-size: 28px; font-weight: 700; margin-bottom: 4px; }
.card-label { font-size: 12px; color: #666; text-transform: uppercase; letter-spacing: .5px; }
.status-bar { display: flex; height: 24px; border-radius: 6px; overflow: hidden; margin: 0 24px; }
.status-bar > div { transition: width .3s; }
.legend { padding: 8px 24px 16px; font-size: 12px; color: #666; }
.section { padding: 0 24px 20px; }
.section h3 { font-size: 13px; text-transform: uppercase; color: #666; margin-bottom: 10px; letter-spacing: .5px; }
table { width: 100%%; border-collapse: collapse; background: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 1px 4px rgba(0,0,0,.08); }
th { background: #263238; color: #fff; padding: 8px 12px; text-align: left; font-size: 12px; font-weight: 500; }
td { padding: 8px 12px; border-bottom: 1px solid #eee; font-size: 13px; }
tr:hover td { background: #f5f5f5; }
.baseline-section { background: #fff3e0; border: 1px solid #ffe0b2; border-radius: 8px; padding: 20px 24px; margin: 0 24px 20px; }
.baseline-section h3 { color: #e65100; margin-bottom: 8px; font-size: 15px; text-transform: none; letter-spacing: 0; }
.baseline-section p { color: #555; font-size: 13px; margin-bottom: 14px; }
.btn { display: inline-block; padding: 8px 18px; border-radius: 6px; border: none; cursor: pointer; font-size: 13px; font-weight: 500; text-decoration: none; }
.btn-primary { background: #1976d2; color: #fff; }
.btn-primary:hover { background: #1565c0; }
.btn-secondary { background: #fff; color: #333; border: 1px solid #ccc; margin-left: 8px; }
.btn-secondary:hover { background: #f5f5f5; }
.toast { position: fixed; bottom: 24px; right: 24px; background: #333; color: #fff; padding: 10px 18px; border-radius: 6px; font-size: 13px; display: none; z-index: 999; }
.toast.error { background: #c62828; }
.toast.show { display: block; }
</style>
</head>
<body>

<div class="header">
    <div class="header-left">
        <span class="project-id">%s</span>
        <span class="project-name">%s</span>
    </div>
    <div style="color:#b0bec5;font-size:13px">Pre-baseline view</div>
</div>

<div class="cards">
    <div class="card">
        <div class="card-value">%d</div>
        <div class="card-label">Tickets</div>
        %s
    </div>
    <div class="card">
        <div class="card-value" style="color:#66bb6a">%d%%</div>
        <div class="card-label">Complete</div>
    </div>
    <div class="card">
        <div class="card-value" style="color:#1976d2">%.1f</div>
        <div class="card-label">Hours Logged</div>
    </div>
    <div class="card">
        <div class="card-value">%d</div>
        <div class="card-label">Ticket Types</div>
    </div>
</div>

<div class="status-bar">%s</div>
<div class="legend">%s</div>

%s

<div class="baseline-section">
    <h3>Ready to Baseline?</h3>
    <p>Baselining locks in the current plan as the schedule to track against. Once set, it cannot be changed.
       Slip tracking, critical path analysis, and health metrics will activate after baselining.</p>
    <button class="btn btn-primary" onclick="baselineNow()">Baseline Now</button>
</div>

<div class="section">
    <h3>All Tickets</h3>
    <table>
        <thead><tr><th>ID</th><th>Type</th><th>Title</th><th>Status</th><th>Assignee</th><th>Hours</th><th>Due</th></tr></thead>
        <tbody>%s</tbody>
    </table>
</div>

<div id="toast" class="toast"></div>

<script>
function showToast(msg, isError) {
    var t = document.getElementById('toast');
    t.textContent = msg;
    t.className = 'toast show' + (isError ? ' error' : '');
    setTimeout(function() { t.className = 'toast'; }, 3000);
}

async function baselineNow() {
    try {
        var r = await fetch('/api/projects/%s/baseline-now', { method: 'POST' });
        var data = await r.json();
        if (!r.ok) { showToast(data.detail || 'Error', true); return; }
        showToast('Baseline created! Reloading...');
        setTimeout(function() { location.reload(); }, 1000);
    } catch (e) { showToast(e.message, true); }
}
</script>
</body>
</html>`,
		projectID,
		projectID, projectName,
		total,
		backlogNote,
		completionPct,
		totalHours,
		len(typeCounts),
		barSegments.String(),
		legendItems.String(),
		costHTML,
		tableBody,
		projectID,
	)
}
