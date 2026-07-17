// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"sort"

	"hate/internal/ticket"
)

// TestSummaryRow is one active ticket's QA test-case tally. The dashboard uses
// the counts; the Test cases tab also uses Cases for the detail view.
type TestSummaryRow struct {
	TicketID string            `json:"ticket_id"`
	Title    string            `json:"title"`
	Status   string            `json:"status"`
	Assignee string            `json:"assignee"`
	Total    int               `json:"total"`
	Pass     int               `json:"pass"`
	Fail     int               `json:"fail"`
	Untested int               `json:"untested"`
	Cases    []ticket.TestCase `json:"cases"`
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
		assignee := ""
		if t.Assignee != nil {
			assignee = *t.Assignee
		}
		r := TestSummaryRow{TicketID: t.ID, Title: t.Title, Status: t.Status, Assignee: assignee, Total: len(t.TestCases), Cases: t.TestCases}
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

// RenderTestSummaryLineHTML renders the compact test-case summary for the PM
// dashboard: project-wide tallies, with the per-ticket detail living on the
// Test cases tab.
func RenderTestSummaryLineHTML(rows []TestSummaryRow) string {
	tickets := len(rows)
	var total, pass, fail, untested int
	for _, r := range rows {
		total += r.Total
		pass += r.Pass
		fail += r.Fail
		untested += r.Untested
	}
	var inner string
	if tickets == 0 {
		inner = `<p style="padding:2px 0;color:#999;font-size:13px">No active tickets have test cases.</p>`
	} else {
		plural := ""
		if tickets != 1 {
			plural = "s"
		}
		inner = fmt.Sprintf(`<div style="font-size:14px;color:#333"><strong>%d</strong> cases across <strong>%d</strong> ticket%s &mdash; `+
			`<span style="color:#16a34a;font-weight:600">%d pass</span> &middot; `+
			`<span style="color:#dc2626;font-weight:600">%d fail</span> &middot; `+
			`<span style="color:#b45309;font-weight:600">%d untested</span></div>`+
			`<p style="font-size:12px;color:#999;margin-top:6px">Full breakdown on the <strong>Test cases</strong> tab.</p>`,
			total, tickets, plural, pass, fail, untested)
	}
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">&#129514; Test cases</h3>
  %s
</div>`, inner)
}
