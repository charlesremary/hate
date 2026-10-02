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
// topHTML (the Plan strip, then the Schedule vs request card) sits right under
// the header; costHTML holds the report sections.
func GenerateSimpleDashboard(tickets []*ticket.Ticket, projectID, projectName, topHTML, costHTML string) string {
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
%s

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

</body>
</html>`,
		projectID,
		projectID, projectName,
		topHTML,
		total,
		backlogNote,
		completionPct,
		totalHours,
		len(typeCounts),
		barSegments.String(),
		legendItems.String(),
		costHTML,
	)
}
