// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// xlsxGanttFixture: from Mon 2026-11-02, ann's A (complete, critical, Nov 2-6)
// then B (in progress, critical, Nov 9-13, projected to Tue Nov 17), bob's C
// (Nov 4-5) and an unowned milestone M on Wed Nov 18.
func xlsxGanttFixture() *Snapshot {
	return &Snapshot{
		ProjectID: "TEST", ProjectName: "Test", SnapshotDate: "2026-11-02",
		CriticalPathIDs: []string{"A", "B"},
		Tasks: []SnapshotTask{
			{TaskID: "A", Title: "Foundation", Owner: "ann@x.com", Status: "complete",
				Baseline: BaselineInfo{PlannedStart: "2026-11-02", PlannedEnd: "2026-11-06", PlannedDays: 5}},
			{TaskID: "B", Title: "Build", Owner: "ann@x.com", Status: "in_progress", Dependencies: []string{"A"},
				Baseline: BaselineInfo{PlannedStart: "2026-11-09", PlannedEnd: "2026-11-13", PlannedDays: 5},
				Current:  CurrentInfo{ProjectedEnd: strptr("2026-11-17")}},
			{TaskID: "C", Title: "Side work", Owner: "bob@x.com", Status: "not_started",
				Baseline: BaselineInfo{PlannedStart: "2026-11-04", PlannedEnd: "2026-11-05", PlannedDays: 2}},
			{TaskID: "M", Title: "Ship", IsMilestone: true, Status: "not_started",
				Baseline: BaselineInfo{PlannedStart: "2026-11-18", PlannedEnd: "2026-11-18"}},
		},
	}
}

// buildGantt renders only a Gantt sheet and parses it back.
func buildGantt(t *testing.T, s *Snapshot, blocks []Block, note string) (*xlsxWorkbook, map[string]xlsxReadCell, string) {
	t.Helper()
	wb := newXLSXWorkbook()
	addGanttSheet(wb, s, blocks, note)
	b, err := wb.bytes()
	if err != nil {
		t.Fatal(err)
	}
	x := readXLSX(t, b)
	if x.sheets[0] != "Gantt" {
		t.Fatalf("sheet = %q", x.sheets[0])
	}
	return wb, x.cells[0], x.parts["xl/worksheets/sheet1.xml"]
}

// filledCols lists the timeline columns of a row whose style has fill.
func filledCols(wb *xlsxWorkbook, c map[string]xlsxReadCell, row, ncols int, fill string) []string {
	var out []string
	for i := 0; i < ncols; i++ {
		ref := xlsxRef(ganttLeftCols+i, row)
		if cell, ok := c[ref]; ok && strings.EqualFold(wb.styles[cell.style].Fill, fill) {
			out = append(out, xlsxCol(ganttLeftCols+i))
		}
	}
	return out
}

func serial(d string) string {
	return strconv.Itoa(int(excelSerial(parseDate(d))))
}

func TestGanttSheetDaily(t *testing.T) {
	wb, c, ws := buildGantt(t, xlsxGanttFixture(), nil, "Baseline of 2026-10-30")
	if c["A1"].value != "Baseline of 2026-10-30" {
		t.Errorf("A1 = %q", c["A1"].value)
	}
	for i, h := range ganttLeftHeaders {
		if got := c[xlsxRef(i, 3)].value; got != h {
			t.Errorf("header %d = %q, want %q", i, got, h)
		}
	}
	// Nov 2-18 working days: 2-6, 9-13, 16-18 = 13 columns H..T; no weekends.
	wantDays := []int{2, 3, 4, 5, 6, 9, 10, 11, 12, 13, 16, 17, 18}
	for i, d := range wantDays {
		if got := c[xlsxRef(ganttLeftCols+i, 3)].value; got != strconv.Itoa(d) {
			t.Errorf("day col %d = %q, want %d", i, got, d)
		}
	}
	if _, ok := c["U3"]; ok {
		t.Error("timeline runs past the last bar")
	}
	if c["H2"].value != "Nov 2026" || !strings.Contains(ws, `<mergeCell ref="H2:T2"/>`) {
		t.Errorf("month header: %q", c["H2"].value)
	}
	if !strings.Contains(ws, `xSplit="7" ySplit="3" topLeftCell="H4"`) {
		t.Error("left columns and header rows not frozen")
	}

	// Group order: ann, bob, unassigned; rows within ann by start.
	want := map[string]string{"A4": "ann", "A5": "A", "A6": "B", "A7": "bob", "A8": "C", "A9": UnassignedLane, "A10": "M"}
	for ref, w := range want {
		if !strings.HasPrefix(c[ref].value, w) {
			t.Errorf("%s = %q, want %s…", ref, c[ref].value, w)
		}
	}
	if !strings.Contains(ws, `<mergeCell ref="A4:G4"/>`) || !wb.styles[c["A4"].style].Bold {
		t.Error("group header not bold / merged over the left columns")
	}

	green, _ := ganttBarColor("complete", true)
	blue, _ := ganttBarColor("in_progress", true)
	gray, _ := ganttBarColor("not_started", false)
	// A: Nov 2-6 = H..L, nothing else filled; critical marker.
	if got := filledCols(wb, c, 5, 13, green); strings.Join(got, ",") != "H,I,J,K,L" {
		t.Errorf("A filled %v", got)
	}
	if c["E5"].value != serial("2026-11-02") || c["F5"].value != serial("2026-11-06") || c["G5"].value != "!" {
		t.Errorf("A start/end/crit = %v %v %v", c["E5"], c["F5"], c["G5"])
	}
	// B: Nov 9-13 blue (M..Q), slip Nov 16-17 (R, S); End is the projected end.
	if got := filledCols(wb, c, 6, 13, blue); strings.Join(got, ",") != "M,N,O,P,Q" {
		t.Errorf("B filled %v", got)
	}
	if got := filledCols(wb, c, 6, 13, ganttSlipFill); strings.Join(got, ",") != "R,S" {
		t.Errorf("B slip %v", got)
	}
	if c["F6"].value != serial("2026-11-17") || c["D6"].value != "In Progress" || c["C6"].value != "ann" {
		t.Errorf("B end/status/owner = %v %v %v", c["F6"], c["D6"], c["C6"])
	}
	// C: not critical, no marker, gray Nov 4-5.
	if got := filledCols(wb, c, 8, 13, gray); strings.Join(got, ",") != "J,K" || c["G8"].value != "" {
		t.Errorf("C filled %v crit %q", got, c["G8"].value)
	}
	// M: one diamond on Nov 18 (T).
	if got := filledCols(wb, c, 10, 13, ganttMilestone); strings.Join(got, ",") != "T" || c["T10"].value != ganttDiamond {
		t.Errorf("milestone %v %q", got, c["T10"].value)
	}
	// Legend below the grid.
	var legend []string
	for r := 11; r < 30; r++ {
		legend = append(legend, c["B"+strconv.Itoa(r)].value)
	}
	all := strings.Join(legend, "|")
	for _, w := range []string{"Not Started", "In Progress", "Complete", "Blocked", "Milestone", "Critical path", "Slip"} {
		if !strings.Contains(all, w) {
			t.Errorf("legend missing %q (%s)", w, all)
		}
	}
}

// HATE-98ta tc7: past 130 working days, one column per week headed by its
// Monday; a Wed-to-next-Tue bar fills two weeks.
func TestGanttSheetWeekly(t *testing.T) {
	s := &Snapshot{Tasks: []SnapshotTask{
		{TaskID: "L", Title: "Long", Status: "in_progress",
			Baseline: BaselineInfo{PlannedStart: "2026-01-05", PlannedEnd: "2026-09-30"}},
		{TaskID: "W", Title: "Wed to Tue", Status: "not_started",
			Baseline: BaselineInfo{PlannedStart: "2026-03-04", PlannedEnd: "2026-03-10"}},
	}}
	wb, c, _ := buildGantt(t, s, nil, "Projected from 2026-01-05, not baselined")
	n := 0
	for i := 0; ; i++ {
		cell, ok := c[xlsxRef(ganttLeftCols+i, 3)]
		if !ok {
			break
		}
		n++
		f, _ := strconv.Atoi(cell.value)
		d := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, f)
		if d.Weekday() != time.Monday || wb.styles[cell.style].NumFmt == "" {
			t.Fatalf("week header %d = %s (%s), want a Monday date", i, d.Format("2006-01-02 Mon"), cell.value)
		}
	}
	if n < 38 || n > 40 {
		t.Errorf("weeks = %d, want ~39", n)
	}
	// Row 4 = group header, 5 = L, 6 = W (by start).
	gray, _ := ganttBarColor("not_started", false)
	got := filledCols(wb, c, 6, n, gray)
	if len(got) != 2 {
		t.Fatalf("W fills %v, want two week cells", got)
	}
	for i, col := range got {
		ref := col + "3"
		want := []string{"2026-03-02", "2026-03-09"}[i]
		if c[ref].value != serial(want) {
			t.Errorf("W week %d header = %s, want %s", i, c[ref].value, want)
		}
	}
}

// HATE-98ta tc11: with blocks, one group header per block labeled like
// "Block 01 (Nov 2-13)", and the second block's columns shaded.
func TestGanttSheetBlocks(t *testing.T) {
	blocks := BuildBlocks(2, parseDate("2026-11-02"), parseDate("2026-11-30"))
	wb, c, _ := buildGantt(t, xlsxGanttFixture(), blocks, "x")
	if !strings.HasPrefix(c["A4"].value, "Block 01 (Nov 2-13)") {
		t.Errorf("first group = %q", c["A4"].value)
	}
	found := false
	for r := 5; r < 12; r++ {
		if strings.HasPrefix(c["A"+strconv.Itoa(r)].value, "Block 02") {
			found = true
		}
	}
	if !found {
		t.Error("no Block 02 group header")
	}
	// Nov 16 (col R) is in Block 02: its day header is banded; Nov 2 is not.
	if wb.styles[c["R3"].style].Fill != strings.ToUpper(ganttBandFill) || wb.styles[c["H3"].style].Fill != "" {
		t.Errorf("block band: R3 %+v H3 %+v", wb.styles[c["R3"].style], wb.styles[c["H3"].style])
	}
}

// HATE-98ta tc5: a task finished early fills from its Start to its actual End
// and nothing after.
func TestGanttSheetEarlyFinish(t *testing.T) {
	s := &Snapshot{Tasks: []SnapshotTask{
		{TaskID: "E", Title: "Early", Status: "complete",
			Baseline: BaselineInfo{PlannedStart: "2026-11-02", PlannedEnd: "2026-11-06"},
			Current:  CurrentInfo{ActualEnd: strptr("2026-11-03")}},
	}}
	wb, c, _ := buildGantt(t, s, nil, "x")
	green, _ := ganttBarColor("complete", false)
	if got := filledCols(wb, c, 5, 5, green); strings.Join(got, ",") != "H,I" {
		t.Errorf("early finish fills %v, want H,I", got)
	}
	if c["F5"].value != serial("2026-11-03") {
		t.Errorf("End = %v, want the actual end", c["F5"])
	}
}

func TestGanttSheetEmpty(t *testing.T) {
	_, c, _ := buildGantt(t, &Snapshot{}, nil, "Projected from 2026-11-02, not baselined")
	if c["A1"].value != "Projected from 2026-11-02, not baselined" || !strings.HasPrefix(c["A2"].value, "No scheduled tasks") {
		t.Errorf("empty gantt: %v %v", c["A1"], c["A2"])
	}
}

// HATE-98ta tc1: the workbook has Tickets then Gantt.
func TestProjectWorkbookSheets(t *testing.T) {
	tickets := exportFixture()
	b, err := ProjectWorkbook(tickets, NewEstimateContext(tickets, 1, nil), xlsxGanttFixture(), nil, "Baseline of 2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	x := readXLSX(t, b)
	if strings.Join(x.sheets, ",") != "Tickets,Gantt" {
		t.Errorf("sheets = %v", x.sheets)
	}
	if !strings.Contains(x.parts["xl/worksheets/sheet1.xml"], `tabSelected="1"`) || strings.Contains(x.parts["xl/worksheets/sheet2.xml"], `tabSelected`) {
		t.Error("only the first sheet should be selected")
	}
}
