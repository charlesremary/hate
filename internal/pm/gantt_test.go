// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"encoding/xml"
	"strings"
	"testing"
)

func strptr(s string) *string { return &s }

// ganttFixture builds a snapshot: A → B (both critical), B slips 3 days, and a
// milestone M.
func ganttFixture() *Snapshot {
	return &Snapshot{
		ProjectID:       "TEST",
		ProjectName:     "Test Project",
		SnapshotDate:    "2026-08-07",
		CriticalPathIDs: []string{"A", "B"},
		Tasks: []SnapshotTask{
			{TaskID: "A", Title: "Foundation", Status: "complete", IsCriticalPath: true,
				Baseline: BaselineInfo{PlannedStart: "2026-08-01", PlannedEnd: "2026-08-05", PlannedDays: 5}},
			{TaskID: "B", Title: "Build on A", Status: "in_progress", IsCriticalPath: true,
				Dependencies: []string{"A"},
				Baseline:     BaselineInfo{PlannedStart: "2026-08-06", PlannedEnd: "2026-08-10", PlannedDays: 5},
				Current:      CurrentInfo{ProjectedEnd: strptr("2026-08-13")}}, // +3d slip
			{TaskID: "M", Title: "Ship", IsMilestone: true,
				Baseline: BaselineInfo{PlannedStart: "2026-08-14", PlannedEnd: "2026-08-14"}},
		},
	}
}

func TestRenderGanttPanel(t *testing.T) {
	svg := renderGanttPanel(ganttFixture(), "Baselined schedule — read-only.", "/api/projects/TEST/gantt.drawio")
	wants := []string{
		`class="gantt-svg"`,
		`/api/projects/TEST/gantt.drawio`, // export button href
		`Export to draw.io`,
		`#dc2626`, // critical-path red stroke
		`#7c3aed`, // milestone diamond fill
		`+3d`,     // slip label
		`today`,   // today marker
	}
	for _, w := range wants {
		if !strings.Contains(svg, w) {
			t.Errorf("gantt SVG missing %q", w)
		}
	}
	// Bars are ordered by planned start: A's bar must appear before B's in source.
	if strings.Index(svg, "Foundation") > strings.Index(svg, "Build on A") {
		t.Error("rows not ordered by planned start (A should precede B)")
	}
}

func TestRenderGanttDrawioIsValidXML(t *testing.T) {
	out := RenderGanttDrawio(ganttFixture())

	// Well-formedness: decode every token, expect no error.
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("draw.io XML is not well-formed: %v", err)
		}
	}

	wants := []string{
		`<mxfile`,
		`<diagram name="Test Project Gantt">`,
		`source="t0" target="t1"`, // dependency edge A→B
		`rhombus`,                 // milestone
		`+3d`,                     // slip cell
		`Jan 2006`,                // month label format artifact? no — check a real month
	}
	// The fixture spans Aug 2026; assert the month label instead of the format string.
	wants[len(wants)-1] = "Aug 2026"
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("draw.io export missing %q", w)
		}
	}
}
