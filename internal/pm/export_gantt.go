// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"time"
)

// The Excel export's Gantt sheet is a classic cell-grid Gantt: one row per
// scheduled task, one narrow column per working day (per week past
// ganttMaxDayCols), bar cells filled in the dashboard's status colors. Rows
// and groups come from ganttData, so they match the dashboard Gantt. A grid,
// not a chart object, so it opens the same in Excel, Google Sheets and Numbers
// and the PM can edit it. Dependency arrows are not drawn (cells can't).

const (
	ganttMaxDayCols = 130 // more working days than this: one column per week
	ganttLeftCols   = 7   // ID, Title, Owner, Status, Start, End, Crit
	ganttHeaderRows = 3   // note, months, column headers / days
	ganttGroupFill  = "#eef2f7"
	ganttBandFill   = "#f3effd" // alternate planning blocks (the block band)
	ganttSlipFill   = "#f8b4b4" // past the planned end (the dashboard's slip red)
	ganttMilestone  = "#7c3aed"
	ganttCritColor  = "#dc2626"
	ganttCritMark   = "!"
	ganttDiamond    = "◆"
)

var (
	ganttLeftHeaders = []string{"ID", "Title", "Owner", "Status", "Start", "End", "Crit"}
	ganttLeftWidths  = []float64{12, 44, 14, 12, 11, 11, 5}
)

// addGanttSheet adds the Gantt sheet for snapshot (nil or empty: a note
// only). note says which schedule this is ("Baseline of …" / "Projected
// from …"); blocks may be empty.
func addGanttSheet(wb *xlsxWorkbook, snapshot *Snapshot, blocks []Block, note string) {
	sh := wb.addSheet("Gantt")
	bold := wb.style(xlsxStyle{Bold: true})
	header := wb.style(xlsxStyle{Bold: true, Fill: ganttGroupFill})
	date := wb.style(xlsxStyle{NumFmt: "yyyy-mm-dd"})
	crit := wb.style(xlsxStyle{Bold: true, Color: ganttCritColor, Center: true})
	groupFill := wb.style(xlsxStyle{Fill: ganttGroupFill})
	band := wb.style(xlsxStyle{Fill: ganttBandFill})
	slip := wb.style(xlsxStyle{Fill: ganttSlipFill})
	milestone := wb.style(xlsxStyle{Bold: true, Color: "#ffffff", Fill: ganttMilestone, Center: true})
	barStyle := func(status string, critical bool) int {
		fill, _ := ganttBarColor(status, critical)
		return wb.style(xlsxStyle{Fill: fill})
	}

	sh.addRow(xStr(note, bold))
	sh.colWidths = append([]float64(nil), ganttLeftWidths...)
	var rows []ganttRow
	var groups []ganttGroup
	if snapshot != nil {
		rows, groups, _, _ = ganttData(snapshot, blocks)
	}
	if len(rows) == 0 {
		sh.addRow(xStr("No scheduled tasks to chart yet — add estimates (and dependencies) to your tickets.", 0))
		return
	}

	// Bars run from the planned start to the actual/projected end, as in the
	// draw.io export; the timeline spans exactly the bars (no padding days).
	var winStart, winEnd time.Time
	for _, r := range rows {
		if winStart.IsZero() || r.plannedStart.Before(winStart) {
			winStart = r.plannedStart
		}
		end := r.plannedEnd
		if r.barEnd.After(end) {
			end = r.barEnd
		}
		if winEnd.IsZero() || end.After(winEnd) {
			winEnd = end
		}
	}

	// Timeline columns: the working days, or the Mondays when too many.
	var cols []time.Time
	for d := winStart; !d.After(winEnd); d = d.AddDate(0, 0, 1) {
		if !isWeekend(d) {
			cols = append(cols, d)
		}
	}
	weekly := len(cols) > ganttMaxDayCols
	if weekly {
		cols = nil
		for m := mondayOnOrBefore(winStart); !m.After(winEnd); m = m.AddDate(0, 0, 7) {
			cols = append(cols, m)
		}
	}
	if len(cols) == 0 { // the bars all fall on one weekend
		cols = []time.Time{winStart}
	}
	// covers reports whether timeline column i overlaps the days [s, e].
	covers := func(i int, s, e time.Time) bool {
		c := cols[i]
		if weekly {
			return !s.After(c.AddDate(0, 0, 6)) && !e.Before(c)
		}
		return !c.Before(s) && !c.After(e)
	}
	// colOf is the one column that shows day d (a milestone): its week, or
	// the first working day on or after it.
	colOf := func(d time.Time) int {
		for i := range cols {
			if covers(i, d, d) || cols[i].After(d) {
				return i
			}
		}
		return len(cols) - 1
	}
	// Alternate planning blocks are shaded, like the dashboard's block bands.
	banded := make([]bool, len(cols))
	for i, c := range cols {
		if bi := blockIndexAt(blocks, c); bi >= 0 && bi < len(blocks) && bi%2 == 1 {
			banded[i] = true
		}
	}
	empty := func(i int) xlsxCell {
		if banded[i] {
			return xFill(band)
		}
		return xlsxCell{}
	}

	// Header rows: the month (merged over its columns) above the column
	// headers and the day of the month (or the week's Monday).
	width := ganttLeftCols + len(cols)
	months := make([]xlsxCell, width)
	days := make([]xlsxCell, width)
	for i, h := range ganttLeftHeaders {
		days[i] = xStr(h, header)
	}
	dayStyle := func(banded bool) int {
		s := xlsxStyle{Bold: true, Center: true}
		if weekly {
			s.NumFmt = "d mmm"
		}
		if banded {
			s.Fill = ganttBandFill
		}
		return wb.style(s)
	}
	sameMonth := func(a, b time.Time) bool { return a.Year() == b.Year() && a.Month() == b.Month() }
	runStart := 0
	for i, c := range cols {
		col := ganttLeftCols + i
		if weekly {
			days[col] = xDate(c, dayStyle(banded[i]))
		} else {
			days[col] = xNum(float64(c.Day()), dayStyle(banded[i]))
		}
		if i == 0 || !sameMonth(c, cols[i-1]) {
			months[col] = xStr(c.Format("Jan 2006"), bold)
			runStart = i
		}
		if (i == len(cols)-1 || !sameMonth(c, cols[i+1])) && i > runStart {
			sh.merges = append(sh.merges, xlsxRef(ganttLeftCols+runStart, 2)+":"+xlsxRef(col, 2))
		}
	}
	sh.addRow(months...)
	sh.addRow(days...)

	// Task rows, with a group header row whenever the group changes.
	cur := -1
	for _, r := range rows {
		if r.group != cur {
			cur = r.group
			g := groups[cur]
			cells := make([]xlsxCell, width)
			cells[0] = xStr(fmt.Sprintf("%s — %d task%s, Σ %dd", g.label, g.count, plif(g.count), g.days), header)
			for i := 1; i < width; i++ {
				cells[i] = xFill(groupFill)
			}
			n := sh.addRow(cells...)
			sh.merges = append(sh.merges, xlsxRef(0, n)+":"+xlsxRef(ganttLeftCols-1, n))
		}
		t := r.task
		// The bar ends on the actual/projected end: past the planned end the
		// extra days are slip; a task finished early stops at its actual end.
		end := r.barEnd
		if r.milestone || end.Before(r.plannedStart) {
			end = r.plannedStart
		}
		statusEnd := r.plannedEnd
		if end.Before(statusEnd) {
			statusEnd = end
		}
		cells := make([]xlsxCell, ganttLeftCols, width)
		cells[0] = xStr(t.TaskID, 0)
		cells[1] = xStr(t.Title, 0)
		if o := ownerShort(t.Owner); o != "" {
			cells[2] = xStr(o, 0)
		}
		cells[3] = xStr(labelOr(statusLabels, t.Status), 0)
		cells[4] = xDate(r.plannedStart, date)
		cells[5] = xDate(end, date)
		if r.critical {
			cells[6] = xStr(ganttCritMark, crit)
		}
		fill := barStyle(t.Status, r.critical)
		mc := colOf(r.plannedStart)
		for i := range cols {
			switch {
			case r.milestone && i == mc:
				cells = append(cells, xStr(ganttDiamond, milestone))
			case r.milestone:
				cells = append(cells, empty(i))
			case covers(i, r.plannedStart, statusEnd):
				cells = append(cells, xFill(fill))
			case end.After(r.plannedEnd) && covers(i, r.plannedEnd.AddDate(0, 0, 1), end):
				cells = append(cells, xFill(slip))
			default:
				cells = append(cells, empty(i))
			}
		}
		sh.addRow(cells...)
	}

	// Legend: a swatch in column A, its meaning in column B.
	sh.addRow()
	sh.addRow(xStr("Legend", bold))
	legend := func(swatch xlsxCell, label string) { sh.addRow(swatch, xStr(label, 0)) }
	for _, st := range []string{"not_started", "in_progress", "complete", "blocked"} {
		legend(xFill(barStyle(st, false)), labelOr(statusLabels, st))
	}
	legend(xFill(slip), "Slip: past the planned end")
	legend(xStr(ganttDiamond, milestone), "Milestone")
	legend(xStr(ganttCritMark, crit), "Critical path (! in the Crit column): a slip here moves the end date")
	if len(blocks) > 0 {
		legend(xFill(band), "Planning block: alternate blocks shaded; rows grouped by the block they start in")
	} else {
		legend(xlsxCell{}, "Rows grouped by person: each works their tickets in order, top to bottom")
	}
	if weekly {
		legend(xlsxCell{}, "One column per week, headed by its Monday; a bar fills every week it touches")
	}

	w := 3.3
	if weekly {
		w = 7
	}
	for range cols {
		sh.colWidths = append(sh.colWidths, w)
	}
	sh.freezeRows, sh.freezeCols = ganttHeaderRows, ganttLeftCols
}
