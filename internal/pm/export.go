// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"sort"
	"strings"

	"hate/internal/ticket"
)

// The Excel export (GET /api/projects/{id}/export.xlsx): one workbook with a
// Tickets sheet (every ticket and its current state) and a Gantt sheet (the
// schedule as a colored cell grid, export_gantt.go), for PMs who report to
// clients in Excel. Written by the package's own xlsx writer (xlsx.go).

// statusLabels and typeLabels mirror STATUS_LABELS / TYPE_LABELS in
// static/app.js, so the sheet reads like the UI.
var statusLabels = map[string]string{
	"not_started": "Not Started", "in_progress": "In Progress",
	"dev_complete": "Dev Complete", "qa_testing": "QA Testing",
	"submitted_for_review": "Submitted for Review", "approved": "Approved",
	"complete": "Complete", "closed": "Closed", "rework": "Rework", "blocked": "Blocked",
}

var typeLabels = map[string]string{
	"task": "Task", "dev_task": "Dev Task", "design_task": "Design Task",
	"meeting": "Meeting", "administration": "Administration",
}

// classLabels are the UI labels for the class tags (CLASS_LABELS in app.js).
var classLabels = map[string]string{
	ticket.ClassFunctional: "Code", ticket.ClassConfig: "Config", ticket.ClassNonfunc: "Non-functional",
}

func labelOr(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

// ticketsSheetHeaders are the Tickets sheet columns, in order.
var ticketsSheetHeaders = []string{
	"ID", "Title", "Type", "Status", "Priority", "Assignee", "Lifecycle", "Block", "Parent",
	"Class", "CFP", "Estimate h", "Logged h", "Planned start", "Due",
	"Predecessors", "Backlog", "Tags",
}

// ticketsSheetWidths are the column widths in characters (Title ~60).
var ticketsSheetWidths = []float64{12, 60, 14, 20, 10, 24, 18, 7, 12, 14, 6, 11, 10, 13, 12, 22, 9, 40}

// ProjectWorkbook builds the project's Excel export as .xlsx bytes. snapshot
// is the schedule the Gantt sheet charts (the one the draw.io export uses),
// blocks the project's planning blocks (may be empty), and scheduleNote the
// line above the grid saying which schedule it is.
func ProjectWorkbook(tickets []*ticket.Ticket, ctx EstimateContext, snapshot *Snapshot, blocks []Block, scheduleNote string) ([]byte, error) {
	wb := newXLSXWorkbook()
	addTicketsSheet(wb, tickets, ctx)
	addGanttSheet(wb, snapshot, blocks, scheduleNote)
	return wb.bytes()
}

// addTicketsSheet adds the Tickets sheet: one row per ticket (closed,
// force-closed and backlog included), sorted by phase (unphased last), then id.
func addTicketsSheet(wb *xlsxWorkbook, tickets []*ticket.Ticket, ctx EstimateContext) {
	sh := wb.addSheet("Tickets")
	header := wb.style(xlsxStyle{Bold: true, Fill: "#eef2f7"})
	date := wb.style(xlsxStyle{NumFmt: "yyyy-mm-dd"})

	hdr := make([]xlsxCell, len(ticketsSheetHeaders))
	for i, h := range ticketsSheetHeaders {
		hdr[i] = xStr(h, header)
	}
	sh.addRow(hdr...)

	sorted := append([]*ticket.Ticket(nil), tickets...)
	blockKey := func(t *ticket.Ticket) int {
		if t.Block <= 0 {
			return 1 << 30 // no block last
		}
		return t.Block
	}
	lifecycleKey := func(t *ticket.Ticket) string {
		if t.Lifecycle == nil || *t.Lifecycle == "" {
			return "\uffff" // no lifecycle last
		}
		return *t.Lifecycle
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if bi, bj := blockKey(sorted[i]), blockKey(sorted[j]); bi != bj {
			return bi < bj
		}
		if li, lj := lifecycleKey(sorted[i]), lifecycleKey(sorted[j]); li != lj {
			return li < lj
		}
		return sorted[i].ID < sorted[j].ID
	})

	str := func(s string) xlsxCell {
		if s == "" {
			return xlsxCell{}
		}
		return xStr(s, 0)
	}
	strp := func(p *string) xlsxCell {
		if p == nil {
			return xlsxCell{}
		}
		return str(*p)
	}
	datep := func(p *string) xlsxCell {
		if p == nil {
			return xlsxCell{}
		}
		d := parseDate(*p)
		if d.IsZero() {
			return str(*p) // not a YYYY-MM-DD date: keep the text
		}
		return xDate(d, date)
	}
	// Hours are General numbers (12, 1.5, 0.25), rounded to 2 decimals.
	round2 := func(v float64) float64 { return math.Round(v*100) / 100 }

	for _, t := range sorted {
		est := xlsxCell{}
		if h, src := EstimatedHours(t, ctx); src != "" {
			est = xNum(round2(h), 0)
		}
		logged := 0.0
		for _, e := range t.TimeEntries {
			logged += e.Hours
		}
		cfp := xlsxCell{}
		if n := ticketCFP(t); n > 0 {
			cfp = xNum(float64(n), 0)
		}
		backlog := "No"
		if ticket.IsBacklog(t) {
			backlog = "Yes"
		}
		sh.addRow(
			xStr(t.ID, 0),
			str(t.Title),
			str(labelOr(typeLabels, t.Type)),
			str(labelOr(statusLabels, t.Status)),
			str(t.Priority),
			strp(t.Assignee),
			strp(t.Lifecycle),
			blockCell(t.Block),
			str(ticket.ParentID(t)),
			str(classLabels[ticket.ClassOf(t)]),
			cfp,
			est,
			xNum(round2(logged), 0),
			datep(t.PlannedStartDate),
			datep(t.DueDate),
			str(strings.Join(t.Predecessors, ", ")),
			xStr(backlog, 0),
			str(strings.Join(t.Tags, ", ")),
		)
	}

	sh.colWidths = ticketsSheetWidths
	sh.freezeRows = 1
	sh.autoFilter = "A1:" + xlsxRef(len(ticketsSheetHeaders)-1, len(sh.rows))
}

// blockCell is a ticket's block number, or an empty cell for none.
func blockCell(n int) xlsxCell {
	if n <= 0 {
		return xlsxCell{}
	}
	return xNum(float64(n), 0)
}
