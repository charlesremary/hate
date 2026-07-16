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

// TestSummaryRow is one active ticket's QA test-case tally for the dashboard.
type TestSummaryRow struct {
	TicketID string `json:"ticket_id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Total    int    `json:"total"`
	Pass     int    `json:"pass"`
	Fail     int    `json:"fail"`
	Untested int    `json:"untested"`
}

// ComputeTestSummary tallies test-case results for active (not closed/complete,
// not backlog) tickets that have test cases. Tickets with failures come first,
// then those with untested cases — the QA attention list.
func ComputeTestSummary(tickets []*ticket.Ticket) []TestSummaryRow {
	var rows []TestSummaryRow
	for _, t := range tickets {
		if len(t.TestCases) == 0 {
			continue
		}
		if ticket.Contains(ticket.ClosedStatuses, t.Status) || ticket.IsBacklog(t) {
			continue
		}
		r := TestSummaryRow{TicketID: t.ID, Title: t.Title, Status: t.Status, Total: len(t.TestCases)}
		for _, c := range t.TestCases {
			switch c.Status {
			case "pass":
				r.Pass++
			case "fail":
				r.Fail++
			default:
				r.Untested++
			}
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Fail != rows[j].Fail {
			return rows[i].Fail > rows[j].Fail
		}
		if rows[i].Untested != rows[j].Untested {
			return rows[i].Untested > rows[j].Untested
		}
		return rows[i].TicketID < rows[j].TicketID
	})
	return rows
}

// RenderTestSummaryHTML renders the QA test-case summary section. Always shown,
// with an all-clear empty state.
func RenderTestSummaryHTML(rows []TestSummaryRow) string {
	esc := html.EscapeString
	body := ""
	for _, r := range rows {
		failStyle := "text-align:right;color:#bbb"
		if r.Fail > 0 {
			failStyle = "text-align:right;color:#dc2626;font-weight:600"
		}
		untStyle := "text-align:right;color:#bbb"
		if r.Untested > 0 {
			untStyle = "text-align:right;color:#b45309;font-weight:600"
		}
		body += fmt.Sprintf(
			`<tr>`+
				`<td style="padding:6px 8px;white-space:nowrap">%s</td>`+
				`<td style="padding:6px 8px">%s</td>`+
				`<td style="padding:6px 8px;text-transform:uppercase;color:#999">%s</td>`+
				`<td style="padding:6px 8px;text-align:right">%d</td>`+
				`<td style="padding:6px 8px;text-align:right;color:#16a34a;font-weight:600">%d</td>`+
				`<td style="padding:6px 8px;%s">%d</td>`+
				`<td style="padding:6px 8px;%s">%d</td></tr>`,
			esc(r.TicketID), esc(r.Title), esc(strings.ReplaceAll(r.Status, "_", " ")),
			r.Total, r.Pass, failStyle, r.Fail, untStyle, r.Untested)
	}
	inner := ""
	if body == "" {
		inner = `<p style="padding:4px 0;color:#999;font-size:13px">No active tickets have test cases.</p>`
	} else {
		inner = fmt.Sprintf(`<table style="width:100%%;border-collapse:collapse;font-size:13px">
    <thead><tr style="text-align:left;color:#666;border-bottom:1px solid #eee">
      <th style="padding:6px 8px">Ticket</th>
      <th style="padding:6px 8px">Title</th>
      <th style="padding:6px 8px">Status</th>
      <th style="padding:6px 8px;text-align:right">Cases</th>
      <th style="padding:6px 8px;text-align:right">Pass</th>
      <th style="padding:6px 8px;text-align:right">Fail</th>
      <th style="padding:6px 8px;text-align:right">Untested</th>
    </tr></thead><tbody>%s</tbody></table>`, body)
	}
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">&#129514; Test cases</h3>
  %s
</div>`, inner)
}
