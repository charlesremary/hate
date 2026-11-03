// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"strings"
	"testing"

	"hate/internal/ticket"
)

// exportFixture: a feature with two code children (one in QA), a closed
// config ticket, a backlog ticket with an awkward title, and one with no lifecycle.
func exportFixture() []*ticket.Ticket {
	f := func(v float64) *float64 { return &v }
	return []*ticket.Ticket{
		{ID: "T-0001", Type: "task", Status: "not_started", Title: "Feature", Priority: "high",
			Lifecycle: strptr("01 - Build"), Tags: []string{"cfp:6"}},
		{ID: "T-0003", Type: "dev_task", Status: "qa_testing", Title: "Second child", Priority: "medium",
			Lifecycle: strptr("01 - Build"), Assignee: strptr("ann@x.com"), Tags: []string{"parent:T-0001", "functional"},
			Predecessors: []string{"T-0002"}, PlannedStartDate: strptr("2026-11-02"), DueDate: strptr("2026-11-06"),
			TimeEntries: []ticket.TimeEntry{{Hours: 1.5}, {Hours: 0.25}}},
		{ID: "T-0002", Type: "dev_task", Status: "in_progress", Title: "First child", Priority: "medium",
			Lifecycle: strptr("01 - Build"), Tags: []string{"parent:T-0001", "functional"}},
		{ID: "T-0004", Type: "task", Status: "closed", Title: "Configure", Priority: "low",
			Lifecycle: strptr("00 - Setup"), Tags: []string{"config"}, EstimateHours: f(2)},
		{ID: "T-0005", Type: "task", Status: "not_started", Title: `Fix <tag> & "quotes" 🚀`, Priority: "low",
			Tags: []string{"backlog", "nonfunc"}, EstimateHours: f(0.5)},
	}
}

func TestTicketsSheet(t *testing.T) {
	tickets := exportFixture()
	ctx := NewEstimateContext(tickets, 1, nil)
	b, err := ProjectWorkbook(tickets, ctx, nil, nil, "Projected from 2026-11-02, not baselined")
	if err != nil {
		t.Fatal(err)
	}
	x := readXLSX(t, b)
	if x.sheets[0] != "Tickets" {
		t.Fatalf("first sheet = %q, want Tickets", x.sheets[0])
	}
	c := x.cells[0]
	if x.rows[0] != 1+len(tickets) {
		t.Errorf("rows = %d, want header + %d", x.rows[0], len(tickets))
	}
	for i, h := range ticketsSheetHeaders {
		ref := xlsxRef(i, 1)
		if c[ref].value != h {
			t.Errorf("%s = %q, want %q", ref, c[ref].value, h)
		}
	}
	if c["A1"].style == 0 {
		t.Error("header row not styled (bold)")
	}
	ws := x.parts["xl/worksheets/sheet1.xml"]
	if !strings.Contains(ws, `ySplit="1"`) || !strings.Contains(ws, `<autoFilter ref="A1:R6"/>`) {
		t.Error("header row not frozen / autofilter missing")
	}

	// Sorted by block, then lifecycle (none last), then id.
	wantOrder := []string{"T-0004", "T-0001", "T-0002", "T-0003", "T-0005"}
	for i, id := range wantOrder {
		if got := c[xlsxRef(0, i+2)].value; got != id {
			t.Errorf("row %d = %s, want %s", i+2, got, id)
		}
	}
	// T-0003 is row 5.
	row := func(col string) xlsxReadCell { return c[col+"5"] }
	checks := map[string]string{"C": "Dev Task", "D": "QA Testing", "F": "ann@x.com", "I": "T-0001", "J": "Code", "P": "T-0002", "Q": "No"}
	for col, want := range checks {
		if got := row(col).value; got != want {
			t.Errorf("T-0003 %s = %q, want %q", col, got, want)
		}
	}
	// Estimate: cfp 6 x rate 1 / 2 code children = 3h; logged 1.75h.
	if row("L").typ != "" || row("L").value != "3" {
		t.Errorf("Estimate h = %+v, want number 3", row("L"))
	}
	if row("M").typ != "" || row("M").value != "1.75" {
		t.Errorf("Logged h = %+v, want number 1.75", row("M"))
	}
	if row("N").value != "46328" || row("N").style == 0 || row("O").value != "46332" {
		t.Errorf("dates = %+v / %+v, want serials with a date style", row("N"), row("O"))
	}
	// Parent row: CFP as a number, no class, no estimate.
	if c["K3"].value != "6" || c["K3"].typ != "" || c["J3"].value != "" || c["L3"].value != "" {
		t.Errorf("feature row: CFP %+v class %+v est %+v", c["K3"], c["J3"], c["L3"])
	}
	// Closed config ticket and backlog ticket.
	if c["D2"].value != "Closed" || c["J2"].value != "Config" || c["L2"].value != "2" {
		t.Errorf("config row: %+v %+v %+v", c["D2"], c["J2"], c["L2"])
	}
	if c["B6"].value != `Fix <tag> & "quotes" 🚀` || c["Q6"].value != "Yes" || c["J6"].value != "Non-functional" {
		t.Errorf("backlog row: %+v %+v %+v", c["B6"], c["Q6"], c["J6"])
	}
	if c["R6"].value != "backlog, nonfunc" {
		t.Errorf("tags = %q", c["R6"].value)
	}
}
