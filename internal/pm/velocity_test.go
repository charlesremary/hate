// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"testing"
	"time"
)

// vtask is a baseline task with planned business days ending on end.
func vtask(id, status, start, end string, days int, deps ...string) SnapshotTask {
	return SnapshotTask{
		TaskID:       id,
		Status:       status,
		Dependencies: deps,
		Baseline:     BaselineInfo{PlannedStart: start, PlannedEnd: end, PlannedDays: days},
	}
}

// Velocity is planned days done ÷ planned days due by today, so being on plan
// reads 1.0 whatever the team size or weekends (HATE-12zw).
func TestScheduleVelocity(t *testing.T) {
	today := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) // a Wednesday
	cases := []struct {
		name  string
		tasks []SnapshotTask
		want  float64
	}{
		{"three people in parallel, on plan", []SnapshotTask{
			vtask("A", "complete", "2026-10-05", "2026-10-16", 10),
			vtask("B", "complete", "2026-10-05", "2026-10-16", 10),
			vtask("C", "complete", "2026-10-05", "2026-10-16", 10),
			vtask("D", "not_started", "2026-10-19", "2026-10-30", 10, "A"),
			vtask("E", "not_started", "2026-10-19", "2026-10-30", 10, "B"),
			vtask("F", "not_started", "2026-10-19", "2026-10-30", 10, "C"),
		}, 1.0},
		{"one lane across weekends, on plan", []SnapshotTask{
			vtask("A", "complete", "2026-10-05", "2026-10-09", 5),
			vtask("B", "complete", "2026-10-12", "2026-10-16", 5, "A"),
			vtask("C", "in_progress", "2026-10-19", "2026-10-23", 5, "B"),
		}, 1.0},
		{"half the due work done", []SnapshotTask{
			vtask("A", "complete", "2026-10-05", "2026-10-09", 5),
			vtask("B", "complete", "2026-10-05", "2026-10-09", 5),
			vtask("C", "in_progress", "2026-10-12", "2026-10-16", 5),
			vtask("D", "not_started", "2026-10-12", "2026-10-16", 5),
		}, 0.5},
		{"nothing due yet", []SnapshotTask{
			vtask("A", "not_started", "2026-10-26", "2026-10-30", 5),
		}, 1.0},
		{"ahead of plan", []SnapshotTask{
			vtask("A", "complete", "2026-10-12", "2026-10-16", 5),
			vtask("B", "complete", "2026-10-26", "2026-10-30", 5, "A"),
		}, 2.0},
	}
	for _, c := range cases {
		if got := scheduleVelocity(c.tasks, today); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: velocity = %v, want %v", c.name, got, c.want)
		}
	}
}

// With velocity 1.0 a remaining task keeps its planned days; at 0.5 they double.
func TestRecalcProjectedEndsUsesVelocity(t *testing.T) {
	today := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	onPlan := []SnapshotTask{
		vtask("A", "complete", "2026-10-05", "2026-10-16", 10),
		vtask("B", "complete", "2026-10-05", "2026-10-16", 10),
		vtask("C", "complete", "2026-10-05", "2026-10-16", 10),
		vtask("D", "not_started", "2026-10-19", "2026-10-30", 10),
	}
	recalcProjectedEnds(onPlan, today)
	if got, want := *onPlan[3].Current.ProjectedEnd, fmtDate(addBusinessDaysToDate(today, 10)); got != want {
		t.Errorf("on plan: D projected end = %s, want %s (10 business days)", got, want)
	}

	behind := []SnapshotTask{
		vtask("A", "complete", "2026-10-05", "2026-10-09", 5),
		vtask("B", "not_started", "2026-10-05", "2026-10-09", 5),
	}
	recalcProjectedEnds(behind, today)
	if got, want := *behind[1].Current.ProjectedEnd, fmtDate(addBusinessDaysToDate(today, 10)); got != want {
		t.Errorf("velocity 0.5: B projected end = %s, want %s (5 days doubled)", got, want)
	}
}
