// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"strings"
	"testing"
	"time"

	"hate/internal/ticket"
)

// wrapDone is a finished config ticket: created, started, completed on the
// given dates, with an estimate and logged hours.
func wrapDone(id string, est, logged float64, created, started, done string) *ticket.Ticket {
	tk := ticket.BlankTicket(id, "task", id, "a@x")
	tk.Tags = []string{ticket.ClassConfig}
	tk.EstimateHours = &est
	tk.CreatedAt = created + "T09:00:00Z"
	tk.Activity = []ticket.Activity{{Timestamp: tk.CreatedAt, Action: "created"}}
	if started != "" {
		tk.Status = "in_progress"
		tk.Activity = append(tk.Activity, ticket.Activity{Timestamp: started + "T09:00:00Z", Action: "status_changed", Detail: "not_started -> in_progress"})
	}
	if done != "" {
		tk.Status = "complete"
		tk.Activity = append(tk.Activity, ticket.Activity{Timestamp: done + "T17:00:00Z", Action: "status_changed", Detail: "in_progress -> complete"})
		tk.TimeEntries = []ticket.TimeEntry{{ID: "t1", Date: done, Hours: logged, Description: "work"}}
	}
	return tk
}

// HATE-q4h5 tc1 + the rest of the panel.
func TestComputeStats(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) // Friday
	tickets := []*ticket.Ticket{
		wrapDone("A", 2, 3, "2026-11-02", "2026-11-08", "2026-11-10"), // finished last week: 2 days
		wrapDone("B", 4, 4, "2026-11-02", "2026-11-16", "2026-11-18"), // finished this week: 2 days
		wrapDone("C", 4, 0, "2026-11-02", "2026-11-17", ""),           // in progress
		wrapDone("D", 2, 0, "2026-11-19", "", ""),                     // added this week
	}
	st := ComputeStats(tickets, NewEstimateContext(tickets, 0, nil), nil, "", now)

	if st.Window != "week" || st.PeriodStart != "2026-11-14" {
		t.Errorf("window %s from %s", st.Window, st.PeriodStart)
	}
	// Accuracy: (3+4) / (2+4) = 1.17, n=2; a week ago only A: 3/2 = 1.5.
	if a := st.AccuracyWrap; a.Value != 1.17 || a.N != 2 || !a.HasPrev || a.Prev != 1.5 {
		t.Errorf("wrap accuracy = %+v", a)
	}
	if st.AccuracyCode.N != 0 {
		t.Errorf("code accuracy = %+v, want no data", st.AccuracyCode)
	}
	// Complete: 6 of 12h now; then (D not yet created) 2 of 10h.
	if p := st.PercentComplete; p.Value != 50 || p.Prev != 20 || !p.HasPrev {
		t.Errorf("percent = %+v", p)
	}
	if st.TicketsDone != 2 || st.TicketsTotal != 4 || st.RemainingHours.Value != 6 {
		t.Errorf("done %d/%d remaining %v", st.TicketsDone, st.TicketsTotal, st.RemainingHours)
	}
	if f := st.Finished; f.Value != 1 || f.Prev != 1 || st.FinishedHours != 4 {
		t.Errorf("finished = %+v (%gh)", f, st.FinishedHours)
	}
	if c := st.CycleDays; c.N != 2 || c.Value < 2 || c.Value > 2.5 {
		t.Errorf("cycle = %+v", c)
	}
	if st.ScopeAdded != 1 || st.ScopeHours != 2 {
		t.Errorf("scope added %d (%gh)", st.ScopeAdded, st.ScopeHours)
	}
	html := RenderStatsHTML(st, "#schedule-vs-request")
	for _, want := range []string{"1.17x", "50%", "Cycle time", "Scope added", `href="#schedule-vs-request"`} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

// On a block project the period is the current block.
func TestComputeStatsBlockWindow(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	blocks := BuildBlocks(2, day("2026-11-02"), day("2026-11-30"))
	for i := range blocks {
		blocks[i].State = BlockUpcoming
	}
	blocks[0].State, blocks[1].State = BlockPast, BlockCurrent
	st := ComputeStats(nil, NewEstimateContext(nil, 0, nil), blocks, "", now)
	if st.Window != "block" || st.PeriodStart != "2026-11-16" || st.PrevStart != "2026-11-02" || st.WindowLabel != "this block (Nov 16-27)" {
		t.Errorf("block window = %s %s %s %q", st.Window, st.PeriodStart, st.PrevStart, st.WindowLabel)
	}
}


// HATE-kbq9: rework rate = hours on rework tickets / all hours logged, by
// category and who introduced it; rework stays out of accuracy.
func TestStatsRework(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	a := wrapDone("A", 4, 6, "2026-11-02", "2026-11-03", "2026-11-05")
	rw := wrapDone("R", 2, 2, "2026-11-10", "2026-11-11", "2026-11-12")
	rw.Tags = append(rw.Tags, "rework-of:A")
	rw.ReworkCategory, rw.IntroducedBy = "defect", "agent"
	desc := wrapDone("D", 1, 0, "2026-11-02", "", "")
	desc.Status, desc.CancellationReason, desc.CloseCategory = "closed", strp("not needed"), "descoped"
	tickets := []*ticket.Ticket{a, rw, desc}
	st := ComputeStats(tickets, NewEstimateContext(tickets, 0, nil), nil, "", now)
	if st.ReworkRate.Value != 25 || st.ReworkRate.N != 1 || st.ReworkAgent != 1 || st.ReworkByCategory["defect"].Tickets != 1 {
		t.Errorf("rework = %+v by %v agent %d", st.ReworkRate, st.ReworkByCategory, st.ReworkAgent)
	}
	if st.AccuracyWrap.N != 1 || st.AccuracyWrap.Value != 1.5 {
		t.Errorf("accuracy should exclude rework: %+v", st.AccuracyWrap)
	}
	if st.Descoped["descoped"] != 1 {
		t.Errorf("descoped = %v", st.Descoped)
	}
	if html := RenderStatsHTML(st, ""); !strings.Contains(html, "Rework") || !strings.Contains(html, "Defect (missed in testing)") || !strings.Contains(html, "Closed unfinished") {
		t.Error("rework / closed cards missing")
	}
}

// Force-closed as done counts as finished in the stats.
func TestStatsForceClosedDone(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	d := wrapDone("D", 4, 0, "2026-11-02", "", "")
	d.Status, d.CancellationReason, d.CloseCategory = "closed", strp("Done: shipped"), "done"
	d.Activity = append(d.Activity, ticket.Activity{Timestamp: "2026-11-18T10:00:00Z", Action: "force_closed", Detail: "not_started -> closed (Done): Done: shipped"})
	o := wrapDone("O", 4, 0, "2026-11-02", "", "")
	st := ComputeStats([]*ticket.Ticket{d, o}, NewEstimateContext([]*ticket.Ticket{d, o}, 0, nil), nil, "", now)
	if st.TicketsDone != 1 || st.TicketsTotal != 2 || st.PercentComplete.Value != 50 || st.Finished.Value != 1 {
		t.Errorf("done %d/%d %v%% finished %v", st.TicketsDone, st.TicketsTotal, st.PercentComplete.Value, st.Finished.Value)
	}
}

// Export to Excel sits next to Export to draw.io in the Gantt panel, keeping
// the ?start= of the draw.io link.
func TestGanttPanelExcelButton(t *testing.T) {
	out := renderGanttPanel(ganttFixture(), nil, "note", "/api/projects/P/gantt.drawio?start=2026-11-02")
	d, x := strings.Index(out, "Export to draw.io"), strings.Index(out, "Export to Excel")
	if d < 0 || x < d || !strings.Contains(out, `href="/api/projects/P/export.xlsx?start=2026-11-02"`) {
		t.Errorf("buttons: draw.io %d, excel %d", d, x)
	}
}
