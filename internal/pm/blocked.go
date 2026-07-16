// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"html"
	"strings"

	"hate/internal/ticket"
)

// BlockedRow is one currently-blocked ticket for the PM dashboard.
type BlockedRow struct {
	TicketID string `json:"ticket_id"`
	Title    string `json:"title"`
	Assignee string `json:"assignee"`
	Reason   string `json:"reason"`
	Since    string `json:"since"`
}

// ComputeBlocked lists tickets currently in the "blocked" state, with the reason
// captured when they were blocked and the date they were last blocked.
func ComputeBlocked(tickets []*ticket.Ticket) []BlockedRow {
	var rows []BlockedRow
	for _, t := range tickets {
		if t.Status != "blocked" {
			continue
		}
		reason := ""
		if t.BlockReason != nil {
			reason = strings.TrimSpace(*t.BlockReason)
		}
		assignee := ""
		if t.Assignee != nil {
			assignee = *t.Assignee
		}
		rows = append(rows, BlockedRow{
			TicketID: t.ID,
			Title:    t.Title,
			Assignee: assignee,
			Reason:   reason,
			Since:    lastBlockedDate(t),
		})
	}
	return rows
}

// lastBlockedDate returns the date (YYYY-MM-DD) of the most recent transition
// into the blocked state, from the activity log; "" if none is recorded.
func lastBlockedDate(t *ticket.Ticket) string {
	for i := len(t.Activity) - 1; i >= 0; i-- {
		a := t.Activity[i]
		if a.Action == "status_changed" && strings.Contains(a.Detail, "-> blocked") {
			if len(a.Timestamp) >= 10 {
				return a.Timestamp[:10]
			}
			return a.Timestamp
		}
	}
	return ""
}

// RenderBlockedHTML renders the blocked-tickets section for the dashboard. Always
// shown, with an all-clear empty state.
func RenderBlockedHTML(rows []BlockedRow) string {
	esc := html.EscapeString
	body := ""
	for _, r := range rows {
		assignee := "—"
		if r.Assignee != "" {
			assignee = r.Assignee
			if at := strings.IndexByte(assignee, '@'); at > 0 {
				assignee = assignee[:at]
			}
		}
		reason := esc(r.Reason)
		if r.Reason == "" {
			reason = `<span style="color:#999">no reason given</span>`
		}
		body += fmt.Sprintf(
			`<tr>`+
				`<td style="padding:6px 8px;white-space:nowrap">%s</td>`+
				`<td style="padding:6px 8px">%s</td>`+
				`<td style="padding:6px 8px">%s</td>`+
				`<td style="padding:6px 8px;white-space:nowrap;color:#999">%s</td>`+
				`<td style="padding:6px 8px">%s</td></tr>`,
			esc(r.TicketID), esc(r.Title), esc(assignee), esc(r.Since), reason)
	}
	inner := ""
	if body == "" {
		inner = `<p style="padding:4px 0;color:#999;font-size:13px">No blocked tickets.</p>`
	} else {
		inner = fmt.Sprintf(`<table style="width:100%%;border-collapse:collapse;font-size:13px">
    <thead><tr style="text-align:left;color:#666;border-bottom:1px solid #eee">
      <th style="padding:6px 8px">Ticket</th>
      <th style="padding:6px 8px">Title</th>
      <th style="padding:6px 8px">Assignee</th>
      <th style="padding:6px 8px">Blocked since</th>
      <th style="padding:6px 8px">Reason</th>
    </tr></thead><tbody>%s</tbody></table>`, body)
	}
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">&#9940; Blocked tickets</h3>
  %s
</div>`, inner)
}
