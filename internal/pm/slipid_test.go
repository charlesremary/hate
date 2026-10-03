// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"strings"
	"testing"
	"time"
)

// HATE-wn0y: slip event ids are derived from baseline + ticket + revised due
// date, so the same slip detected twice gets the same id; old sequential ids
// stay valid and still block a duplicate for their task.
func TestDeterministicSlipEventIDs(t *testing.T) {
	b := Baseline{CreatedDate: "2026-09-01", CreatedBy: "a@x", ProjectID: "p", PlannedStart: "2026-09-01", PlannedEnd: "2026-10-01",
		Tasks: []BaselineTask{{TaskID: "T-1", PlannedEnd: "2026-09-20", ProjectID: "p"}, {TaskID: "T-2", PlannedEnd: "2026-09-25", ProjectID: "p"}}}
	cur := map[string]map[string]interface{}{
		"T-1": {"due_date": "2026-09-25"},
		"T-2": {"due_date": "2026-09-28"},
	}
	day := time.Date(2026, 9, 21, 9, 0, 0, 0, time.Local)
	key := BaselineKey(b)
	a := DetectSlipEvents(key, b.Tasks, cur, nil, day)
	again := DetectSlipEvents(BaselineKey(b), b.Tasks, cur, nil, day.Add(3*time.Hour))
	if len(a) != 2 || len(again) != 2 {
		t.Fatalf("events: %d / %d", len(a), len(again))
	}
	for i := range a {
		if a[i].SlipEventID != again[i].SlipEventID || a[i].DetectedDate != again[i].DetectedDate {
			t.Errorf("event %d differs: %+v vs %+v", i, a[i], again[i])
		}
		if !strings.HasPrefix(a[i].SlipEventID, "SE-p-") || len(a[i].SlipEventID) != len("SE-p-")+10 {
			t.Errorf("id format: %s", a[i].SlipEventID)
		}
	}
	if a[0].SlipEventID == a[1].SlipEventID {
		t.Error("two different slips share an id")
	}
	if a[0].SlipEventID != SlipEventID("p", key, "T-1", "2026-09-25") {
		t.Error("SlipEventID doesn't match the detected id")
	}
	// A different revised date or baseline is a different slip.
	if SlipEventID("p", key, "T-1", "2026-09-26") == a[0].SlipEventID {
		t.Error("revised date not part of the id")
	}
	b2 := b
	b2.CreatedDate = "2026-09-15"
	if SlipEventID("p", BaselineKey(b2), "T-1", "2026-09-25") == a[0].SlipEventID {
		t.Error("baseline not part of the id")
	}

	// An existing old-style unresolved event (SE-p-001) still counts: no
	// duplicate for T-1, and nothing is renumbered.
	old := []SlipEvent{{SlipEventID: "SE-p-001", TaskID: "T-1", ProjectID: "p", Status: "unresolved", SlipDays: 5}}
	n := DetectSlipEvents(key, b.Tasks, cur, old, day)
	if len(n) != 1 || n[0].TaskID != "T-2" || n[0].SlipEventID == "SE-p-001" {
		t.Errorf("with an old event: %+v", n)
	}
	// An event already recorded under its deterministic id is not re-added.
	rec := append(old, a[1])
	rec[1].Status = "resolved"
	rec[1].SlipDays = 0 // explains nothing, so T-2 still has unexplained slip
	if n := DetectSlipEvents(key, b.Tasks, cur, rec, day); len(n) != 0 {
		t.Errorf("re-detected an existing id: %+v", n)
	}
}
