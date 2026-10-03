// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"hate/internal/ticket"
)

func day(s string) time.Time { return parseDate(s) }

// HATE-evqw tc2: a requested start of Wed 2026-11-04 puts Block 01 on Mon
// 2026-11-02 through Fri 2026-11-13.
func TestBlockAnchorRequestedStart(t *testing.T) {
	anchor := BlockAnchor("2026-11-04", nil, day("2026-10-02"))
	if fmtDate(anchor) != "2026-11-02" {
		t.Fatalf("anchor = %s, want 2026-11-02", fmtDate(anchor))
	}
	bl := BuildBlocks(2, anchor, day("2026-11-20"))
	if len(bl) == 0 {
		t.Fatal("no blocks")
	}
	b := bl[0]
	if b.N != 1 || b.Start != "2026-11-02" || b.End != "2026-11-13" || b.Label != "Block 01 (Nov 2-13)" {
		t.Errorf("block 1 = %+v", b)
	}
	if bl[1].Start != "2026-11-16" || bl[1].End != "2026-11-27" {
		t.Errorf("block 2 = %+v", bl[1])
	}
}

// Block 1 falls back to the earliest planned start, then today; a weekend
// date goes back to the Monday before it.
func TestBlockAnchorFallbacks(t *testing.T) {
	early := &ticket.Ticket{ID: "A", PlannedStartDate: strp("2026-11-08")} // a Sunday
	later := &ticket.Ticket{ID: "B", PlannedStartDate: strp("2026-12-01")}
	backlog := &ticket.Ticket{ID: "C", PlannedStartDate: strp("2026-10-01"), Tags: []string{ticket.BacklogTag}}
	if got := fmtDate(BlockAnchor("", []*ticket.Ticket{later, early, backlog}, day("2026-10-02"))); got != "2026-11-02" {
		t.Errorf("planned-start anchor = %s, want 2026-11-02 (backlog ignored)", got)
	}
	if got := fmtDate(BlockAnchor("", nil, day("2026-10-02"))); got != "2026-09-28" {
		t.Errorf("today anchor = %s, want Mon 2026-09-28", got)
	}
}

// Labels across a month boundary, 3-week blocks, and coverage: through the
// block containing `until`, plus one.
func TestBuildBlocks(t *testing.T) {
	bl := BuildBlocks(3, day("2026-11-02"), day("2026-12-20"))
	// Nov 2-20, Nov 23-Dec 11, Dec 14-Jan 1 (contains Dec 20), +1.
	if len(bl) != 4 {
		t.Fatalf("got %d blocks, want 4: %+v", len(bl), bl)
	}
	if bl[0].End != "2026-11-20" || bl[1].Label != "Block 02 (Nov 23-Dec 11)" || bl[2].Label != "Block 03 (Dec 14-Jan 1)" {
		t.Errorf("blocks = %+v", bl)
	}
	if got := BuildBlocks(0, day("2026-11-02"), day("2027-01-01")); got == nil || len(got) != 0 {
		t.Errorf("no block length: %v, want []", got)
	}
	// until on the block's Saturday still counts as inside it.
	if got := BuildBlocks(2, day("2026-11-02"), day("2026-11-14")); len(got) != 2 {
		t.Errorf("until on the weekend: %d blocks, want 2", len(got))
	}
}

func TestParseBlockPhase(t *testing.T) {
	for in, want := range map[string]int{"Block 01 (Nov 2-13)": 1, "block 10": 10, "Block 7": 7, "01 - Build": 0, "Blocks": 0, "Block 0": 0} {
		n, ok := ParseBlockPhase(in)
		if (want > 0) != ok || n != want {
			t.Errorf("ParseBlockPhase(%q) = %d %v, want %d", in, n, ok, want)
		}
	}
}

// HATE-evqw tc4 (model): no block length → no blocks. With one, the list runs
// to the later of the requested end and the projected finish, plus one.
func TestBlocksForProject(t *testing.T) {
	tickets := []*ticket.Ticket{capT("A", 8, ""), capT("B", 8, "", "A")}
	ctx := NewEstimateContext(tickets, 0, nil)
	cfg := &ticket.ProjectConfig{RequestedStart: "2026-11-04", RequestedEnd: "2026-12-04"}
	if got := BlocksForProject(cfg, tickets, ctx, day("2026-10-02")); got == nil || len(got) != 0 {
		t.Errorf("unset block_weeks: %v, want []", got)
	}
	if got := BlocksForProject(nil, tickets, ctx, day("2026-10-02")); len(got) != 0 {
		t.Errorf("nil config: %v", got)
	}
	cfg.BlockWeeks = 2
	bl := BlocksForProject(cfg, tickets, ctx, day("2026-10-02"))
	// Nov 2-13, Nov 16-27, Nov 30-Dec 11 (contains Dec 4), +1 = 4.
	if len(bl) != 4 || bl[0].Start != "2026-11-02" || bl[3].Start != "2026-12-14" {
		t.Errorf("blocks to the requested end = %+v", bl)
	}
	// A projected finish past the requested end extends the list.
	big := []*ticket.Ticket{capT("X", 8*40, "")}
	bl = BlocksForProject(cfg, big, NewEstimateContext(big, 0, nil), day("2026-10-02"))
	// 40 working days from Wed Nov 4 ends Tue Dec 29: in block 5 (Dec 28-Jan 8), +1.
	if len(bl) != 6 {
		t.Errorf("blocks to the projected finish = %d, want 6: %+v", len(bl), bl)
	}
}

// blockGanttFixture: two people; A (alice) starts in block 1, B (bob) in
// block 2, C (alice) waits on A.
func blockGanttFixture() (*Snapshot, []Block) {
	snap := &Snapshot{
		ProjectID: "P", ProjectName: "Proj", SnapshotDate: "2026-11-02",
		CriticalPathIDs: []string{"A", "C"},
		Tasks: []SnapshotTask{
			{TaskID: "A", Title: "Alpha", Owner: "alice@x", Status: "not_started", Lane: "Alice", LaneRank: 0,
				Baseline: BaselineInfo{PlannedStart: "2026-11-02", PlannedEnd: "2026-11-06", PlannedDays: 5}},
			{TaskID: "B", Title: "Bravo", Owner: "bob@x", Status: "not_started", Lane: "Bob", LaneRank: 1,
				Baseline: BaselineInfo{PlannedStart: "2026-11-16", PlannedEnd: "2026-11-18", PlannedDays: 3}},
			{TaskID: "C", Title: "Charlie", Owner: "alice@x", Status: "not_started", Lane: "Alice", LaneRank: 0,
				Dependencies: []string{"A"},
				Baseline:     BaselineInfo{PlannedStart: "2026-11-09", PlannedEnd: "2026-11-10", PlannedDays: 2}},
		},
	}
	return snap, BuildBlocks(2, day("2026-11-02"), day("2026-11-20"))
}

// groupedRows renders ganttData's grouping as "group:id,id|group:id".
func groupedRows(snap *Snapshot, blocks []Block) string {
	rows, groups, _, _ := ganttData(snap, blocks)
	var parts []string
	cur := -1
	for _, r := range rows {
		if r.group != cur {
			parts = append(parts, groups[r.group].label+":"+r.task.TaskID)
			cur = r.group
		} else {
			parts[len(parts)-1] += "," + r.task.TaskID
		}
	}
	return strings.Join(parts, "|")
}

// HATE-evqw tc3 / HATE-43sr tc2: with blocks, the rows are grouped under block
// headers with block bands and labels; no stage headers; critical path kept.
func TestGanttGroupsByBlock(t *testing.T) {
	snap, blocks := blockGanttFixture()
	html := renderGanttPanel(snap, blocks, "note", "/x")
	for _, w := range []string{`class="gantt-block-band"`, `class="gantt-block-label"`, `Block 01 (Nov 2-13)`, `Block 02 (Nov 16-27)`,
		`#dc2626`, `id="gantt-cp"`, `planning block`} {
		if !strings.Contains(html, w) {
			t.Errorf("block Gantt missing %q", w)
		}
	}
	if strings.Contains(html, "Stage ") {
		t.Error("block Gantt still has stage headers")
	}
	// A and C (block 1) come before B (block 2), under the block headers.
	if got := groupedRows(snap, blocks); got != "Block 01 (Nov 2-13):A,C|Block 02 (Nov 16-27):B" {
		t.Errorf("rows = %s", got)
	}

	x := RenderGanttDrawio(snap, blocks)
	dec := xml.NewDecoder(strings.NewReader(x))
	for {
		if _, err := dec.Token(); err != nil {
			if err.Error() != "EOF" {
				t.Fatalf("draw.io not well-formed: %v", err)
			}
			break
		}
	}
	for _, w := range []string{`id="block0"`, `id="blocklbl0"`, `value="Block 01 (Nov 2-13)"`, `Block 01 (Nov 2-13) — 2 tasks`, `Block 02 (Nov 16-27) — 1 task,`} {
		if !strings.Contains(x, w) {
			t.Errorf("draw.io missing %q", w)
		}
	}
	if strings.Contains(x, "Stage ") {
		t.Error("draw.io still has stage headers")
	}
}

// HATE-43sr tc3 / HATE-evqw tc4: without blocks, rows are grouped by person
// (lane order) and there are no bands.
func TestGanttGroupsByPerson(t *testing.T) {
	snap, _ := blockGanttFixture()
	html := renderGanttPanel(snap, nil, "note", "/x")
	if strings.Contains(html, "gantt-block-band") || strings.Contains(html, "Block 01") || strings.Contains(html, "Stage ") {
		t.Error("no-blocks Gantt has bands or stages")
	}
	if got := groupedRows(snap, nil); got != "Alice:A,C|Bob:B" {
		t.Errorf("rows = %s", got)
	}
	if !strings.Contains(html, "Rows grouped by person") {
		t.Error("legend should say rows are grouped by person")
	}
}

// The projected snapshot carries the capacity lanes, so the Gantt's person
// groups match the schedule (resources in config order; parents last).
func TestProjectedGanttLanes(t *testing.T) {
	res := []ticket.Resource{{Email: "z@x", Name: "Zed"}, {Email: "a@x", Name: "Amy"}}
	tickets := []*ticket.Ticket{capT("T1", 4, "a@x"), capT("T2", 4, "z@x")}
	snap, _ := ProjectSchedule("P", "Proj", tickets, res, NewEstimateContext(tickets, 0, nil), schedStart)
	if got := groupedRows(snap, nil); got != "Zed:T2|Amy:T1" {
		t.Errorf("lane groups = %s, want Zed then Amy (config order)", got)
	}
}
