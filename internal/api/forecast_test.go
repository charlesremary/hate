// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// initGitRepo turns root into a git repo with everything committed and
// returns a git runner (the cosmic setup points GIT_DIR at a dead dir).
func initGitRepo(t *testing.T, root string) func(args ...string) string {
	t.Helper()
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
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	return git
}

// HATE-5ux7 tc1/tc2: requested dates are validated, saved and committed.
func TestRequestedDatesAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)
	git := initGitRepo(t, root)
	const path = "/api/projects/est/requested-dates"

	if code, body := do(t, h, "GET", path, nil); code != http.StatusOK || body["requested_start"] != nil || body["requested_end"] != nil {
		t.Fatalf("GET unset: %d %v", code, body)
	}

	// tc1
	code, body := do(t, h, "PUT", path, map[string]interface{}{"requested_start": "2026-11-02", "requested_end": "2027-01-29"})
	if code != http.StatusOK || body["requested_start"] != "2026-11-02" || body["requested_end"] != "2027-01-29" {
		t.Fatalf("PUT: %d %v", code, body)
	}
	raw, _ := os.ReadFile(ticket.ConfigPath(root))
	if !strings.Contains(string(raw), `"requested_start": "2026-11-02"`) || !strings.Contains(string(raw), `"requested_end": "2027-01-29"`) {
		t.Errorf("config.json lacks the requested dates: %s", raw)
	}
	if log := git("log", "-1", "--format=%s"); !strings.Contains(log, "requested dates: start 2026-11-02, end 2027-01-29") {
		t.Errorf("last commit = %q", log)
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Errorf("config not committed: %s", st)
	}
	// Saving the same dates again commits nothing.
	before := git("rev-parse", "HEAD")
	do(t, h, "PUT", path, map[string]interface{}{"requested_start": "2026-11-02", "requested_end": "2027-01-29"})
	if git("rev-parse", "HEAD") != before {
		t.Error("an unchanged save made a commit")
	}
	// The target-date alias reads and sets the end, keeping the start.
	if code, body := do(t, h, "GET", "/api/projects/est/target-date", nil); code != http.StatusOK || body["target_date"] != "2027-01-29" {
		t.Errorf("alias GET: %d %v", code, body)
	}
	if code, body := do(t, h, "PUT", "/api/projects/est/target-date", map[string]interface{}{"target_date": "2027-02-26"}); code != http.StatusOK || body["target_date"] != "2027-02-26" {
		t.Errorf("alias PUT: %d %v", code, body)
	}
	if cfg, _ := ticket.ReadConfig(root); cfg.RequestedStart != "2026-11-02" || cfg.RequestedEnd != "2027-02-26" {
		t.Errorf("after alias PUT: start %q end %q", cfg.RequestedStart, cfg.RequestedEnd)
	}

	// tc2
	code, body = do(t, h, "PUT", path, map[string]interface{}{"requested_start": "2027-02-01", "requested_end": "2027-01-29"})
	if code != http.StatusBadRequest || !strings.Contains(body["detail"].(string), "after the requested end") {
		t.Errorf("start after end: %d %v", code, body)
	}
	if code, body = do(t, h, "PUT", path, map[string]interface{}{"requested_start": "Nov 2"}); code != http.StatusBadRequest || !strings.Contains(body["detail"].(string), "requested_start must be a date") {
		t.Errorf("bad date: %d %v", code, body)
	}
	if code, _ = do(t, h, "PUT", "/api/projects/est/target-date", map[string]interface{}{"target_date": "2026-10-01"}); code != http.StatusBadRequest {
		t.Errorf("alias end before start: %d, want 400", code)
	}
	if cfg, _ := ticket.ReadConfig(root); cfg.RequestedStart != "2026-11-02" || cfg.RequestedEnd != "2027-02-26" {
		t.Errorf("a rejected PUT changed the config: %q %q", cfg.RequestedStart, cfg.RequestedEnd)
	}
}

// HATE-5ux7 tc3: an old target_date reads as the requested end and still
// drives the Load table's over-by; saving migrates it to requested_end.
func TestLegacyTargetDateAsRequestedEnd(t *testing.T) {
	h, root := setupCosmicProjects(t)
	past := time.Now().AddDate(0, 0, -14).Format("2006-01-02")
	cfg, _ := ticket.ReadConfig(root)
	cfg.TargetDate = past
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	if code, body := do(t, h, "GET", "/api/projects/est/requested-dates", nil); code != http.StatusOK || body["requested_end"] != past || body["requested_start"] != nil {
		t.Fatalf("GET: %d %v", code, body)
	}
	html := getHTML(t, h, "/api/projects/est/dashboard")
	if !strings.Contains(html, "Working days to requested end") || !strings.Contains(html, `class="load-over"`) {
		t.Error("Load table lost its over-by with a legacy target date")
	}
	if !strings.Contains(html, "forecast-status") || !strings.Contains(html, "LATE") {
		t.Error("card missing for a legacy target date in the past")
	}
	if code, _ := do(t, h, "PUT", "/api/projects/est/requested-dates", map[string]interface{}{"requested_end": past}); code != http.StatusOK {
		t.Fatalf("PUT: %d", code)
	}
	raw, _ := os.ReadFile(ticket.ConfigPath(root))
	if strings.Contains(string(raw), "target_date") || !strings.Contains(string(raw), `"requested_end": "`+past+`"`) {
		t.Errorf("save did not migrate target_date: %s", raw)
	}
}

// HATE-5ux7 tc4 / HATE-j0ju tc6: clearing both dates removes the fields and the
// card becomes the set-dates hint.
func TestClearRequestedDates(t *testing.T) {
	h, root := setupCosmicProjects(t)
	if code, _ := do(t, h, "PUT", "/api/projects/est/requested-dates", map[string]interface{}{"requested_start": "2026-11-02", "requested_end": "2027-01-29"}); code != http.StatusOK {
		t.Fatal(code)
	}
	code, body := do(t, h, "PUT", "/api/projects/est/requested-dates", map[string]interface{}{"requested_start": nil, "requested_end": nil})
	if code != http.StatusOK || body["requested_start"] != nil || body["requested_end"] != nil {
		t.Fatalf("clear: %d %v", code, body)
	}
	raw, _ := os.ReadFile(ticket.ConfigPath(root))
	if strings.Contains(string(raw), "requested_") || strings.Contains(string(raw), "target_date") {
		t.Errorf("cleared config still has dates: %s", raw)
	}
	html := getHTML(t, h, "/api/projects/est/dashboard")
	if !strings.Contains(html, "forecast-hint") || strings.Contains(html, "forecast-status") {
		t.Error("dashboard should show the set-dates hint, not the card")
	}
	if _, err := os.Stat(pm.ForecastHistoryPath(root)); !os.IsNotExist(err) {
		t.Error("history recorded without a requested end")
	}
}

// HATE-j0ju: GET /forecast returns the card as JSON; the card is at the top of
// both dashboard flavors.
func TestForecastAPIAndPlacement(t *testing.T) {
	h, _ := setupCosmicProjects(t)
	start := time.Now().Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 42).Format("2006-01-02")
	if code, _ := do(t, h, "PUT", "/api/projects/est/requested-dates", map[string]interface{}{"requested_start": start, "requested_end": end}); code != http.StatusOK {
		t.Fatal(code)
	}
	code, body := do(t, h, "GET", "/api/projects/est/forecast", nil)
	if code != http.StatusOK {
		t.Fatalf("forecast: %d %v", code, body)
	}
	for _, k := range []string{"requested_start", "requested_end", "actual_start", "likely_finish", "p85_finish", "finish_variance_days", "remaining_hours", "working_days_left", "needs_per_day", "has_per_day", "hours_to_cut", "status", "history"} {
		if _, ok := body[k]; !ok {
			t.Errorf("forecast JSON missing %q", k)
		}
	}
	if body["requested_end"] != end || body["status"] != pm.ForecastOnTrack {
		t.Errorf("forecast = %v", body)
	}

	html := getHTML(t, h, "/api/projects/est/dashboard")
	if i, j := strings.Index(html, "Schedule vs request"), strings.Index(html, `<div class="cards">`); i < 0 || j < 0 || i > j {
		t.Errorf("pre-baseline card not at the top (card %d, cards %d)", i, j)
	}
	if code, body := do(t, h, "POST", "/api/projects/est/baseline-now", nil); code != http.StatusOK {
		t.Fatalf("baseline-now: %d %v", code, body)
	}
	if code, body := do(t, h, "POST", "/api/projects/est/snapshot", nil); code != http.StatusOK {
		t.Fatalf("snapshot: %d %v", code, body)
	}
	html = getHTML(t, h, "/api/projects/est/dashboard")
	if i, j := strings.Index(html, "Schedule vs request"), strings.Index(html, `<div class="tabs">`); i < 0 || j < 0 || i > j {
		t.Errorf("baselined card not at the top (card %d, tabs %d)", i, j)
	}
}

// HATE-kpr0 tc1: loading the dashboard twice on the same day with no changes
// leaves one history entry and one commit; GET /forecast shares the helper.
func TestForecastHistoryDashboardTwice(t *testing.T) {
	h, root := setupCosmicProjects(t)
	git := initGitRepo(t, root)
	end := time.Now().AddDate(0, 0, 42).Format("2006-01-02")
	if code, _ := do(t, h, "PUT", "/api/projects/est/requested-dates", map[string]interface{}{"requested_end": end}); code != http.StatusOK {
		t.Fatal(code)
	}
	getHTML(t, h, "/api/projects/est/dashboard")
	getHTML(t, h, "/api/projects/est/dashboard")
	hist, err := pm.ReadForecastHistory(root)
	if err != nil || len(hist) != 1 || hist[0].RequestedEnd != end {
		t.Fatalf("history = %+v (%v), want 1 entry", hist, err)
	}
	if n := strings.Count(git("log", "--format=%s"), "forecast history"); n != 1 {
		t.Errorf("forecast history commits = %d, want 1", n)
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Errorf("uncommitted: %s", st)
	}
	code, body := do(t, h, "GET", "/api/projects/est/forecast", nil)
	if code != http.StatusOK || len(body["history"].([]interface{})) != 1 {
		t.Errorf("forecast history = %v", body["history"])
	}
	if n := strings.Count(git("log", "--format=%s"), "forecast history"); n != 1 {
		t.Errorf("GET /forecast added a commit: %d", n)
	}
}
