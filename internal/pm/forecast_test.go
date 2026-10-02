// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"hate/internal/ticket"
)

// forecastFixture: one resource (8 h/day) with one 20-CFP code ticket; at a
// 2 h/CFP rate that's 40h = 5 business days from Wed 2026-07-22, so the likely
// finish is Tue 07-28. With a P85 factor of 1.5 it's 60h = 7.5 days, finishing
// Fri 07-31.
func forecastFixture() ([]*ticket.Ticket, []ticket.Resource, EstimateContext) {
	res := []ticket.Resource{{Email: "a@x", DailyHoursAvailable: dh(8)}}
	code := &ticket.Ticket{ID: "F1", Title: "F1", Type: "dev_task", Status: "not_started", Priority: "medium",
		Tags: []string{ticket.ClassFunctional, "cfp:20"}, Assignee: strp("a@x")}
	tickets := []*ticket.Ticket{code}
	return tickets, res, NewEstimateContext(tickets, 2, nil)
}

// HATE-j0ju tc1: a requested end well after the P85 finish is ON TRACK with a
// negative finish variance.
func TestForecastOnTrack(t *testing.T) {
	tickets, res, ctx := forecastFixture()
	rep := ComputeForecast(tickets, res, ctx, 1.5, schedStart, "", "2026-08-14")
	if rep.LikelyFinish != "2026-07-28" || rep.P85Finish != "2026-07-31" {
		t.Fatalf("likely/p85 = %s / %s, want 2026-07-28 / 2026-07-31", rep.LikelyFinish, rep.P85Finish)
	}
	if rep.Status != ForecastOnTrack {
		t.Errorf("status = %q, want ON TRACK", rep.Status)
	}
	// Jul 29..Aug 14 = 13 business days early.
	if rep.FinishVarianceDays != -13 || rep.P85FinishVarianceDays != -10 {
		t.Errorf("finish variance = %d / %d, want -13 / -10", rep.FinishVarianceDays, rep.P85FinishVarianceDays)
	}
	// 40h over 18 working days (Jul 22..Aug 14) vs 8 h/day: fits with spare.
	if rep.RemainingHours != 40 || rep.WorkingDaysLeft != 18 || rep.HasPerDay != 8 || rep.HoursToCut != 0 || rep.SpareHours != 104 {
		t.Errorf("needs vs has = %+v", rep)
	}
	html := RenderForecastHTML(rep, nil)
	for _, w := range []string{"Schedule vs request", "ON TRACK", "#dcfce7", "Tue Jul 28, 2026", "Fri Jul 31, 2026", "13 business days early", "104.0h spare"} {
		if !strings.Contains(html, w) {
			t.Errorf("card missing %q", w)
		}
	}
}

// HATE-j0ju tc2: likely before the requested end, P85 after → AT RISK.
func TestForecastAtRisk(t *testing.T) {
	tickets, res, ctx := forecastFixture()
	rep := ComputeForecast(tickets, res, ctx, 1.5, schedStart, "", "2026-07-29")
	if rep.Status != ForecastAtRisk {
		t.Fatalf("status = %q, want AT RISK (likely %s, p85 %s)", rep.Status, rep.LikelyFinish, rep.P85Finish)
	}
	if rep.FinishVarianceDays != -1 || rep.P85FinishVarianceDays != 2 {
		t.Errorf("variances = %d / %d, want -1 / +2", rep.FinishVarianceDays, rep.P85FinishVarianceDays)
	}
	if html := RenderForecastHTML(rep, nil); !strings.Contains(html, "AT RISK") || !strings.Contains(html, "#fef3c7") {
		t.Error("card missing the amber AT RISK badge")
	}
	// No Monte Carlo result (factor 1): P85 = likely, so on track.
	rep = ComputeForecast(tickets, res, ctx, MCP85Factor(MonteCarloResult{}), schedStart, "", "2026-07-29")
	if rep.P85Finish != rep.LikelyFinish || rep.Status != ForecastOnTrack || rep.P85Factor != 1 {
		t.Errorf("no MC: p85 %s likely %s status %s factor %g", rep.P85Finish, rep.LikelyFinish, rep.Status, rep.P85Factor)
	}
	if f := MCP85Factor(MonteCarloResult{OK: true, Code: MCPercentiles{P50: 100, P85: 130}}); f != 1.3 {
		t.Errorf("MCP85Factor = %g, want 1.3", f)
	}
}

// HATE-j0ju tc3: likely after the requested end → LATE, positive variance,
// needs > has, hours to cut shown.
func TestForecastLate(t *testing.T) {
	tickets, res, ctx := forecastFixture()
	rep := ComputeForecast(tickets, res, ctx, 1.5, schedStart, "", "2026-07-24")
	if rep.Status != ForecastLate || rep.FinishVarianceDays != 2 {
		t.Fatalf("status %q variance %d, want LATE +2", rep.Status, rep.FinishVarianceDays)
	}
	// 40h over Wed-Fri (3 days) = 13.3 h/day vs 8; cut 40 - 24 = 16h.
	if rep.WorkingDaysLeft != 3 || rep.NeedsPerDay != 13.3 || rep.NeedsPerDay <= rep.HasPerDay || rep.HoursToCut != 16 || rep.SpareHours != 0 {
		t.Errorf("needs vs has = %+v", rep)
	}
	html := RenderForecastHTML(rep, nil)
	for _, w := range []string{"LATE", "#fee2e2", "+2 business days late", "13.3 h/day", "cut 16.0h"} {
		if !strings.Contains(html, w) {
			t.Errorf("card missing %q", w)
		}
	}
}

// HATE-j0ju tc4: a requested start in the future moves the schedule start;
// with no work started the actual start is blank.
func TestForecastFutureStart(t *testing.T) {
	tickets, res, ctx := forecastFixture()
	rep := ComputeForecast(tickets, res, ctx, 1, schedStart, "2026-08-01", "2026-08-31") // Sat → Mon 08-03
	if rep.ScheduleStart != "2026-08-03" || rep.LikelyFinish != "2026-08-07" {
		t.Errorf("schedule %s..%s, want 2026-08-03..2026-08-07", rep.ScheduleStart, rep.LikelyFinish)
	}
	if rep.ActualStart != "" || rep.StartVarianceDays != nil {
		t.Errorf("actual start = %q (%v), want blank", rep.ActualStart, rep.StartVarianceDays)
	}
	if html := RenderForecastHTML(rep, nil); !strings.Contains(html, "not started") {
		t.Error("card should say not started")
	}
	// A requested start in the past: the schedule runs from today.
	rep = ComputeForecast(tickets, res, ctx, 1, schedStart, "2026-07-01", "2026-08-31")
	if rep.ScheduleStart != "2026-07-22" {
		t.Errorf("schedule start = %s, want today 2026-07-22", rep.ScheduleStart)
	}
}

// HATE-j0ju tc5: a ticket moved to in_progress on 2026-11-04 sets the actual
// start, with the variance against the requested start.
func TestForecastActualStart(t *testing.T) {
	tickets, res, ctx := forecastFixture()
	tickets[0].Activity = []ticket.Activity{
		{Timestamp: "2026-11-01T09:00:00Z", Action: "created"},
		{Timestamp: "2026-11-04T10:00:00Z", Action: "status_changed", Detail: "not_started -> in_progress"},
		{Timestamp: "2026-11-06T10:00:00Z", Action: "status_changed", Detail: "blocked -> in_progress"},
	}
	today := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	rep := ComputeForecast(tickets, res, ctx, 1, today, "2026-11-02", "2026-12-31")
	if rep.ActualStart != "2026-11-04" || rep.StartVarianceDays == nil || *rep.StartVarianceDays != 2 {
		t.Fatalf("actual start %q variance %v, want 2026-11-04 +2", rep.ActualStart, rep.StartVarianceDays)
	}
	if html := RenderForecastHTML(rep, nil); !strings.Contains(html, "Wed Nov 4, 2026") || !strings.Contains(html, "+2 business days late") {
		t.Error("card missing the actual start / start variance")
	}
	// An earlier time entry wins; a cancelled ticket's doesn't count.
	other := capT("W1", 2, "a@x")
	other.TimeEntries = []ticket.TimeEntry{{Date: "2026-10-30", Hours: 1}}
	cancelled := capT("X1", 2, "a@x")
	cancelled.Status = "closed"
	cancelled.CancellationReason = strp("descoped")
	cancelled.TimeEntries = []ticket.TimeEntry{{Date: "2026-10-01", Hours: 1}}
	all := append(tickets, other, cancelled)
	if as := ActualStart(all); fmtDate(as) != "2026-10-30" {
		t.Errorf("ActualStart = %s, want 2026-10-30", fmtDate(as))
	}
}

// HATE-j0ju tc6: no requested end → the card is a hint to set dates in Settings.
func TestForecastNoRequestedEnd(t *testing.T) {
	tickets, res, ctx := forecastFixture()
	rep := ComputeForecast(tickets, res, ctx, 1.5, schedStart, "", "")
	if rep.Status != "" || rep.LikelyFinish != "2026-07-28" {
		t.Errorf("no end: status %q likely %s", rep.Status, rep.LikelyFinish)
	}
	html := RenderForecastHTML(rep, nil)
	if !strings.Contains(html, "forecast-hint") || !strings.Contains(html, "Settings") || strings.Contains(html, "forecast-status") {
		t.Errorf("hint HTML = %s", html)
	}
}

// Strings in the card are escaped: a junk date never reaches the page raw.
func TestForecastEscapes(t *testing.T) {
	rep := ForecastReport{RequestedEnd: "2026-08-14", LikelyFinish: "<b>x</b>", P85Finish: "<b>x</b>", Status: ForecastOnTrack, P85Factor: 1}
	if html := RenderForecastHTML(rep, nil); strings.Contains(html, "<b>x</b>") {
		t.Error("unescaped date in card")
	}
}

func hentry(date, likely, p85 string, rem float64) ForecastHistoryEntry {
	return ForecastHistoryEntry{Date: date, LikelyFinish: likely, P85Finish: p85, RequestedEnd: "2026-08-14", RemainingHours: rem}
}

// HATE-kpr0 tc1-tc3 (pure): unchanged → nothing; same day → replaced; new day
// and different → appended; new day but same → nothing.
func TestMergeForecastHistory(t *testing.T) {
	h, changed := MergeForecastHistory(nil, hentry("2026-07-22", "2026-07-28", "2026-07-31", 40))
	if !changed || len(h) != 1 {
		t.Fatalf("first: %v %d", changed, len(h))
	}
	if h2, changed := MergeForecastHistory(h, hentry("2026-07-22", "2026-07-28", "2026-07-31", 40)); changed || len(h2) != 1 {
		t.Errorf("unchanged same day: %v %d", changed, len(h2))
	}
	h, changed = MergeForecastHistory(h, hentry("2026-07-22", "2026-07-29", "2026-08-03", 44))
	if !changed || len(h) != 1 || h[0].LikelyFinish != "2026-07-29" {
		t.Errorf("same-day change should replace: %v %+v", changed, h)
	}
	if h2, changed := MergeForecastHistory(h, hentry("2026-07-23", "2026-07-29", "2026-08-03", 44)); changed || len(h2) != 1 {
		t.Errorf("new day, same forecast: %v %d", changed, len(h2))
	}
	h, changed = MergeForecastHistory(h, hentry("2026-07-23", "2026-07-30", "2026-08-03", 40))
	if !changed || len(h) != 2 || h[1].Date != "2026-07-23" {
		t.Errorf("new day change should append: %v %+v", changed, h)
	}
}

// HATE-kpr0 tc1-tc3 (on disk, in git): one entry and one commit for repeated
// identical computations; a same-day change replaces; a new day appends; no
// requested end records nothing.
func TestRecordForecastHistoryGit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_DIR", "")
	os.Unsetenv("GIT_DIR")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	git("commit", "-q", "--allow-empty", "-m", "init")
	commits := func() int { return strings.Count(git("log", "--format=%s"), "forecast history") }

	rep := ForecastReport{RequestedEnd: "2026-08-14", LikelyFinish: "2026-07-28", P85Finish: "2026-07-31", RemainingHours: 40}
	day1 := schedStart
	if _, changed, err := RecordForecastHistory(root, ForecastReport{LikelyFinish: "2026-07-28"}, day1); err != nil || changed {
		t.Fatalf("no requested end recorded: %v %v", changed, err)
	}
	if _, err := os.Stat(ForecastHistoryPath(root)); !os.IsNotExist(err) {
		t.Error("history file written without a requested end")
	}
	for i := 0; i < 2; i++ {
		if _, _, err := RecordForecastHistory(root, rep, day1); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := ReadForecastHistory(root)
	if len(h) != 1 || commits() != 1 {
		t.Fatalf("after two same-day records: %d entries, %d commits, want 1/1", len(h), commits())
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Errorf("history not committed: %s", st)
	}

	rep.LikelyFinish = "2026-07-29"
	RecordForecastHistory(root, rep, day1.Add(5*time.Hour))
	h, _ = ReadForecastHistory(root)
	if len(h) != 1 || h[0].LikelyFinish != "2026-07-29" || commits() != 2 {
		t.Errorf("same-day change: %+v, %d commits", h, commits())
	}

	rep.P85Finish = "2026-08-04"
	RecordForecastHistory(root, rep, day1.AddDate(0, 0, 1))
	h, _ = ReadForecastHistory(root)
	if len(h) != 2 || h[1].Date != "2026-07-23" || commits() != 3 {
		t.Errorf("new day: %+v, %d commits", h, commits())
	}
}

// HATE-kpr0 tc4/tc5: the trend needs 2+ entries; it draws likely, P85 and a
// dashed requested-end line.
func TestForecastTrend(t *testing.T) {
	rep := ForecastReport{RequestedEnd: "2026-08-14", LikelyFinish: "2026-07-28", P85Finish: "2026-07-31", Status: ForecastOnTrack, P85Factor: 1.5}
	one := []ForecastHistoryEntry{hentry("2026-07-22", "2026-07-28", "2026-07-31", 40)}
	if html := RenderForecastHTML(rep, one); strings.Contains(html, "<svg") {
		t.Error("trend drawn with 1 entry")
	}
	two := append(one, hentry("2026-07-23", "2026-07-30", "2026-08-05", 38))
	html := RenderForecastHTML(rep, two)
	for _, w := range []string{`class="forecast-trend"`, `class="trend-likely"`, `class="trend-p85"`, `class="trend-requested"`, `stroke-dasharray`, "Finish-date trend"} {
		if !strings.Contains(html, w) {
			t.Errorf("trend missing %q", w)
		}
	}
}

// Concurrent records (parallel dashboard loads) leave a valid file with one
// entry per day.
func TestRecordForecastHistoryConcurrent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_DIR", root+"/no-git") // no repo: the commit just fails
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			rep := ForecastReport{RequestedEnd: "2026-08-14", LikelyFinish: "2026-07-28", P85Finish: "2026-07-31", RemainingHours: float64(i)}
			if _, _, err := RecordForecastHistory(root, rep, schedStart.AddDate(0, 0, i%2)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	h, err := ReadForecastHistory(root)
	if err != nil || len(h) == 0 || len(h) > 20 {
		t.Fatalf("history after concurrent writes: %d entries (%v)", len(h), err)
	}
	for i, e := range h {
		if i > 0 && e.Date == h[i-1].Date {
			t.Errorf("two consecutive entries for %s", e.Date)
		}
	}
}
