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

const typeTagPrefix = "type:"

// DeliverableCost is the per-deliverable-type hours rollup.
type DeliverableCost struct {
	Type       string  `json:"type"`
	Count      int     `json:"count"`
	Hours      float64 `json:"hours"`
	AvgPerUnit float64 `json:"avg_per_unit"`
}

// ProjectCost breaks a project's logged hours down by deliverable type so the
// numbers carry forward to the next similar project ("a kb-article costs ~Xh").
//
// A deliverable is a parent ticket tagged `type:<name>`; its cost is the hours
// logged on it plus the hours on its `parent:`-linked children. Hours not under
// any typed deliverable are uncategorized, so the breakdown reconciles to total.
type ProjectCost struct {
	Types              []DeliverableCost `json:"types"`
	UncategorizedHours float64           `json:"uncategorized_hours"`
	TotalHours         float64           `json:"total_hours"`
}

// ComputeProjectCost rolls hours up by deliverable type across all tickets.
func ComputeProjectCost(tickets []*ticket.Ticket) ProjectCost {
	// Parents that declare a deliverable type.
	parentType := map[string]string{}
	for _, t := range tickets {
		if tv, ok := cosmicTagValue(t.Tags, typeTagPrefix); ok && tv != "" {
			parentType[t.ID] = tv
		}
	}
	// One deliverable per typed parent.
	countByType := map[string]int{}
	for _, typ := range parentType {
		countByType[typ]++
	}

	hoursByType := map[string]float64{}
	var total, uncategorized float64
	for _, t := range tickets {
		h := cosmicLoggedHours(t)
		total += h
		// Each ticket's hours land in exactly one bucket — its own type, its
		// typed parent's type, or uncategorized.
		typ := ""
		if tv, ok := cosmicTagValue(t.Tags, typeTagPrefix); ok && tv != "" {
			typ = tv
		} else if pid, ok := cosmicTagValue(t.Tags, parentTagPrefix); ok {
			typ = parentType[pid] // "" if the parent isn't typed
		}
		if typ == "" {
			uncategorized += h
			continue
		}
		hoursByType[typ] += h
	}

	types := make([]DeliverableCost, 0, len(countByType))
	for typ, c := range countByType {
		hrs := hoursByType[typ]
		avg := 0.0
		if c > 0 {
			avg = hrs / float64(c)
		}
		types = append(types, DeliverableCost{Type: typ, Count: c, Hours: hrs, AvgPerUnit: avg})
	}
	sort.Slice(types, func(i, j int) bool {
		if types[i].Hours != types[j].Hours {
			return types[i].Hours > types[j].Hours
		}
		return types[i].Type < types[j].Type
	})

	return ProjectCost{Types: types, UncategorizedHours: uncategorized, TotalHours: total}
}

// RenderProjectCostHTML renders the "Project Cost" dashboard section. Self-styled
// so it drops cleanly into either dashboard flavor.
func RenderProjectCostHTML(pc ProjectCost) string {
	esc := html.EscapeString
	var rows strings.Builder
	for _, d := range pc.Types {
		rows.WriteString(fmt.Sprintf(
			`<tr><td style="padding:6px 8px">%s</td><td style="padding:6px 8px;text-align:right">%d</td><td style="padding:6px 8px;text-align:right">%.1f</td><td style="padding:6px 8px;text-align:right">%.1f</td></tr>`,
			esc(d.Type), d.Count, d.Hours, d.AvgPerUnit))
	}
	if pc.UncategorizedHours > 0 {
		rows.WriteString(fmt.Sprintf(
			`<tr style="color:#999"><td style="padding:6px 8px">Uncategorized</td><td style="padding:6px 8px;text-align:right">&mdash;</td><td style="padding:6px 8px;text-align:right">%.1f</td><td style="padding:6px 8px;text-align:right">&mdash;</td></tr>`,
			pc.UncategorizedHours))
	}
	body := rows.String()
	if body == "" {
		body = `<tr><td colspan="4" style="padding:10px 8px;color:#999">No hours logged yet.</td></tr>`
	}
	hint := ""
	if len(pc.Types) == 0 {
		hint = `<p style="font-size:12px;color:#999;margin-top:10px">Tag a deliverable parent <code>type:&lt;name&gt;</code> (e.g. <code>type:kb-article</code>) to see cost per deliverable here — its child tickets roll up automatically.</p>`
	}
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">Project Cost &mdash; hours by deliverable</h3>
  <table style="width:100%%;border-collapse:collapse;font-size:13px">
    <thead><tr style="text-align:left;color:#666;border-bottom:1px solid #eee">
      <th style="padding:6px 8px">Deliverable type</th>
      <th style="padding:6px 8px;text-align:right">Count</th>
      <th style="padding:6px 8px;text-align:right">Hours</th>
      <th style="padding:6px 8px;text-align:right">Avg / unit</th>
    </tr></thead>
    <tbody>%s</tbody>
    <tfoot><tr style="font-weight:700;border-top:2px solid #eee">
      <td style="padding:6px 8px">Total</td><td></td>
      <td style="padding:6px 8px;text-align:right">%.1f</td><td></td>
    </tr></tfoot>
  </table>%s
</div>`, body, pc.TotalHours, hint)
}
