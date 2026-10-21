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
// Tickets sheet (every ticket and its current state) for PMs who report to
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
	"ID", "Title", "Type", "Status", "Priority", "Assignee", "Phase", "Parent",
	"Class", "CFP", "Estimate h", "Logged h", "Planned start", "Due",
	"Predecessors", "Backlog", "Tags",
}

// ticketsSheetWidths are the column widths in characters (Title ~60).
var ticketsSheetWidths = []float64{12, 60, 14, 20, 10, 24, 22, 12, 14, 6, 11, 10, 13, 12, 22, 9, 40}

// ProjectWorkbook builds the project's Excel export as .xlsx bytes.
func ProjectWorkbook(tickets []*ticket.Ticket, ctx EstimateContext) ([]byte, error) {
	wb := newXLSXWorkbook()
	addTicketsSheet(wb, tickets, ctx)
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
	phaseKey := func(t *ticket.Ticket) string {
		if t.Phase == nil || *t.Phase == "" {
			return "￿" // sort unphased last
		}
		return *t.Phase
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if pi, pj := phaseKey(sorted[i]), phaseKey(sorted[j]); pi != pj {
			return pi < pj
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
			strp(t.Phase),
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
