// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"hate/internal/config"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// setupPlanProject is the cosmic "est" project with T-3 due in 5 days, as a
// git repo with everything committed.
func setupPlanProject(t *testing.T) (http.Handler, string, func(args ...string) string) {
	t.Helper()
	h, root := setupCosmicProjects(t)
	setDue(t, root, "T-3", time.Now().AddDate(0, 0, 5))
	git := initGitRepo(t, root)
	return h, root, git
}

func setDue(t *testing.T, root, id string, due time.Time) {
	t.Helper()
	tk, err := ticket.ReadTicket(root, id)
	if err != nil {
		t.Fatal(err)
	}
	d := due.Format("2006-01-02")
	tk.DueDate = &d
	if err := ticket.WriteTicket(root, tk); err != nil {
		t.Fatal(err)
	}
}

// slipT3 moves T-3's due date `days` past its current baseline end and commits
// the ticket, so later git status checks only see what the plan code left.
func slipT3(t *testing.T, root string, git func(...string) string, days int) {
	t.Helper()
	data, err := os.ReadFile(pm.BaselinePath(root))
	if err != nil {
		t.Fatal(err)
	}
	var b pm.Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	for _, bt := range b.Tasks {
		if bt.TaskID == "T-3" {
			end, _ := time.Parse("2006-01-02", bt.PlannedEnd)
			setDue(t, root, "T-3", end.AddDate(0, 0, days))
			git("commit", "-q", "-am", "move T-3")
			return
		}
	}
	t.Fatal("T-3 not in the baseline")
}

func doList(t *testing.T, h http.Handler, path string) (int, []map[string]interface{}) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var out []map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func snapshotFiles(t *testing.T, root string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(pm.SnapshotsDir(root), "*.json"))
	return m
}

// HATE-4pk1 tc1/tc2/tc3/tc5, HATE-y16z tc1/tc2: Baseline now, a detected slip
// and a resolution are each committed (only those paths); snapshots stay out
// of git; the Plan strip shows the state.
func TestPlanAuditTrailCommits(t *testing.T) {
	h, root, git := setupPlanProject(t)
	const base = "/api/projects/est"
	today := time.Now().Format("2006-01-02")

	// y16z tc1: no baseline.
	html := getHTML(t, h, base+"/dashboard")
	if !strings.Contains(html, `id="plan-strip"`) || !strings.Contains(html, "No baseline.") || !strings.Contains(html, "planBaselineNow(this)") {
		t.Fatal("pre-baseline strip missing No baseline / Baseline now")
	}
	if strings.Contains(html, "Ready to Baseline?") {
		t.Error("the old Baseline Now section is still there")
	}
	if i, j := strings.Index(html, `id="plan-strip"`), strings.Index(html, "Schedule vs request"); i > j {
		t.Error("strip should sit above the Schedule vs request card")
	}
	if len(snapshotFiles(t, root)) != 0 {
		t.Error("auto-snapshot ran without a baseline")
	}

	// tc1: Baseline now is committed with the snapshots .gitignore.
	if code, body := do(t, h, "POST", base+"/baseline-now", nil); code != http.StatusOK || body["created_by"] != "test@example.com" {
		t.Fatalf("baseline-now: %d %v", code, body)
	}
	if msg := git("log", "-1", "--format=%s"); !strings.HasPrefix(msg, "baseline: 5 tickets, planned end ") {
		t.Errorf("baseline commit = %q", msg)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != ".tkt/pm/.gitignore\n.tkt/pm/baseline.json" {
		t.Errorf("baseline commit files = %q", files)
	}
	if gi, _ := os.ReadFile(pm.PMGitignorePath(root)); string(gi) != "snapshots/\n" {
		t.Errorf(".gitignore = %q", gi)
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Errorf("uncommitted after baseline: %s", st)
	}
	if code, _ := do(t, h, "POST", base+"/baseline-now", nil); code != http.StatusConflict {
		t.Errorf("second baseline-now: %d, want 409", code)
	}

	// y16z tc2: the dashboard takes today's snapshot and the strip shows it all.
	html = getHTML(t, h, base+"/dashboard")
	ps := pm.ReadPlanStatus(root)
	for _, want := range []string{
		"Baseline <strong>" + today + "</strong> by test@example.com",
		"5 tickets",
		"planned end <strong>" + ps.PlannedEnd + "</strong>",
		"Last snapshot <strong>" + today + "</strong>",
		"(auto)",
		"0 unresolved slips",
		"planSnapshot(this)",
		"planRebaseline(this)",
		`href="#slip-ledger"`,
		`id="slip-ledger"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("baselined strip missing %q", want)
		}
	}
	if strings.Contains(html, "snapshot-prompt-overlay") || strings.Contains(html, "No baseline.") {
		t.Error("baselined dashboard still has the old snapshot prompt / no-baseline state")
	}
	if i, j := strings.Index(html, `id="plan-strip"`), strings.Index(html, "Schedule vs request"); i < 0 || i > j {
		t.Error("strip should sit above the Schedule vs request card")
	}

	// tc2: a snapshot that detects a slip commits slip_events.json.
	slipT3(t, root, git, 10)
	if code, body := do(t, h, "POST", base+"/snapshot", nil); code != http.StatusOK || body["unresolved_slip_events"] != 1.0 {
		t.Fatalf("snapshot: %d %v", code, body["unresolved_slip_events"])
	}
	if msg := git("log", "-1", "--format=%s"); msg != "snapshot "+today+": 1 new slip event" {
		t.Errorf("slip commit = %q", msg)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != ".tkt/pm/slip_events.json" {
		t.Errorf("slip commit files = %q", files)
	}
	// tc5: snapshots are ignored.
	if st := git("status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("git status after snapshots: %s", st)
	}
	if ls := git("ls-files", ".tkt/pm/snapshots"); ls != "" {
		t.Errorf("snapshots tracked: %s", ls)
	}
	// A snapshot that finds nothing new commits nothing.
	head := git("rev-parse", "HEAD")
	do(t, h, "POST", base+"/snapshot", nil)
	if git("rev-parse", "HEAD") != head {
		t.Error("an unchanged snapshot made a commit")
	}
	if html := getHTML(t, h, base+"/dashboard"); !strings.Contains(html, "1 unresolved slip<") {
		t.Error("strip should show 1 unresolved slip")
	}

	// tc3: resolving commits the resolution.
	code, body := do(t, h, "PATCH", base+"/slip/SE-est-001", map[string]interface{}{"reason_category": "client_delay", "reason_narrative": "waiting on the client"})
	if code != http.StatusOK {
		t.Fatalf("resolve: %d %v", code, body)
	}
	if msg := git("log", "-1", "--format=%s"); msg != "slip SE-est-001 resolved: client_delay" {
		t.Errorf("resolve commit = %q", msg)
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Errorf("uncommitted after resolve: %s", st)
	}
	evs, _ := pm.ReadSlipEvents(root)
	if len(evs) != 1 || evs[0].Status != "resolved" || *evs[0].AcknowledgedBy != "test@example.com" {
		t.Errorf("events = %+v", evs)
	}
}

// HATE-4pk1 tc4: opening the dashboard repeatedly (also concurrently) on one
// day with a baseline and no snapshot gives one snapshot file and no duplicate
// slip events.
func TestAutoSnapshotOncePerDay(t *testing.T) {
	h, root, git := setupPlanProject(t)
	if code, _ := do(t, h, "POST", "/api/projects/est/baseline-now", nil); code != http.StatusOK {
		t.Fatal(code)
	}
	slipT3(t, root, git, 4) // a slip waiting to be detected

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects/est/dashboard", nil))
		}()
	}
	wg.Wait()
	getHTML(t, h, "/api/projects/est/dashboard")
	getHTML(t, h, "/api/projects/est/dashboard")

	if files := snapshotFiles(t, root); len(files) != 1 || filepath.Base(files[0]) != time.Now().Format("2006-01-02")+".json" {
		t.Errorf("snapshot files = %v, want today's only", files)
	}
	evs, _ := pm.ReadSlipEvents(root)
	if len(evs) != 1 {
		t.Errorf("slip events = %d, want 1", len(evs))
	}
	if n := strings.Count(git("log", "--format=%s"), "new slip event"); n != 1 {
		t.Errorf("slip commits = %d, want 1", n)
	}
	if st := git("status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("uncommitted: %s", st)
	}
	if snap, _ := pm.LoadLatestSnapshot(root); snap.GeneratedBy != "auto" {
		t.Errorf("generated_by = %q, want auto", snap.GeneratedBy)
	}
}

// HATE-ciml tc1/tc4/tc5, HATE-y16z tc3: re-baseline archives the old baseline
// with the reason, closes unresolved slips as "rebaseline", creates the new
// baseline, and commits it all in one commit; GET /baselines lists the archive.
func TestRebaseline(t *testing.T) {
	h, root, git := setupPlanProject(t)
	const base = "/api/projects/est"
	today := time.Now().Format("2006-01-02")
	if code, _ := do(t, h, "POST", base+"/baseline-now", nil); code != http.StatusOK {
		t.Fatal(code)
	}
	oldBaseline, _ := os.ReadFile(pm.BaselinePath(root))
	slipT3(t, root, git, 10)
	do(t, h, "POST", base+"/snapshot", nil)
	movedDue := ""
	if tk, _ := ticket.ReadTicket(root, "T-3"); tk.DueDate != nil {
		movedDue = *tk.DueDate
	}
	commitsBefore := git("rev-list", "--count", "HEAD")

	// tc1 + tc4
	code, body := do(t, h, "POST", base+"/rebaseline", map[string]interface{}{"reason": "  client moved scope  "})
	if code != http.StatusOK {
		t.Fatalf("rebaseline: %d %v", code, body)
	}
	if body["archive_id"] != today+"-1" || body["archive_path"] != ".tkt/pm/baselines/"+today+"-1.json" || body["closed_slip_events"] != 1.0 {
		t.Errorf("rebaseline response = %v", body)
	}
	if n := git("rev-list", "--count", "HEAD"); n != incr(commitsBefore) {
		t.Errorf("commits %s -> %s, want exactly one more", commitsBefore, n)
	}
	if msg := git("log", "-1", "--format=%s"); msg != "re-baseline: client moved scope" {
		t.Errorf("commit = %q", msg)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != ".tkt/pm/baseline.json\n.tkt/pm/baselines/"+today+"-1.json\n.tkt/pm/slip_events.json" {
		t.Errorf("commit files = %q", files)
	}
	if st := git("status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("uncommitted after re-baseline: %s", st)
	}
	var arc pm.BaselineArchive
	data, _ := os.ReadFile(filepath.Join(pm.BaselinesDir(root), today+"-1.json"))
	if err := json.Unmarshal(data, &arc); err != nil {
		t.Fatal(err)
	}
	var archived, old pm.Baseline
	_ = json.Unmarshal(arc.Baseline, &archived)
	_ = json.Unmarshal(oldBaseline, &old)
	if arc.Reason != "client moved scope" || arc.ArchivedBy != "test@example.com" || arc.ArchivedAt == "" ||
		archived.PlannedEnd != old.PlannedEnd || len(archived.Tasks) != len(old.Tasks) {
		t.Errorf("archive = %+v", arc)
	}
	var nb pm.Baseline
	data, _ = os.ReadFile(pm.BaselinePath(root))
	_ = json.Unmarshal(data, &nb)
	for _, bt := range nb.Tasks {
		if bt.TaskID == "T-3" && bt.PlannedEnd != movedDue {
			t.Errorf("new baseline T-3 end = %s, want the moved due %s", bt.PlannedEnd, movedDue)
		}
	}
	evs, _ := pm.ReadSlipEvents(root)
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	e := evs[0]
	if e.Status != "resolved" || *e.ReasonCategory != "rebaseline" || *e.ReasonNarrative != "client moved scope" ||
		*e.AcknowledgedBy != "test@example.com" || *e.AcknowledgedDate != today || e.SupersededBy == nil || *e.SupersededBy != today+"-1" {
		t.Errorf("event after re-baseline = %+v", e)
	}

	// y16z tc3: the dashboard shows the new baseline (today's snapshot was retaken).
	html := getHTML(t, h, base+"/dashboard")
	if !strings.Contains(html, "planned end <strong>"+nb.PlannedEnd+"</strong>") || !strings.Contains(html, "0 unresolved slips") ||
		!strings.Contains(html, "1 earlier baseline") || !strings.Contains(html, "(re-baseline)") {
		t.Error("dashboard does not show the new baseline")
	}

	// Slips against the new baseline are detected (old events don't mask them).
	slipT3(t, root, git, 3)
	if code, body := do(t, h, "POST", base+"/snapshot", nil); code != http.StatusOK || body["unresolved_slip_events"] != 1.0 {
		t.Errorf("slip after re-baseline: %d %v", code, body["unresolved_slip_events"])
	}
	evs, _ = pm.ReadSlipEvents(root)
	if len(evs) != 2 || evs[1].SlipEventID != "SE-est-002" || evs[1].SlipDays != 3 || evs[1].SupersededBy != nil {
		t.Errorf("events = %+v", evs)
	}

	// tc5: two re-baselines, two archive entries in order.
	if code, body := do(t, h, "POST", base+"/rebaseline", map[string]interface{}{"reason": "second change"}); code != http.StatusOK || body["archive_id"] != today+"-2" {
		t.Fatalf("second rebaseline: %d %v", code, body)
	}
	code, list := doList(t, h, base+"/baselines")
	if code != http.StatusOK || len(list) != 2 {
		t.Fatalf("baselines: %d %v", code, list)
	}
	if list[0]["id"] != today+"-1" || list[0]["reason"] != "client moved scope" ||
		list[1]["id"] != today+"-2" || list[1]["reason"] != "second change" || list[1]["task_count"] != 5.0 {
		t.Errorf("baselines = %v", list)
	}
	if st := git("status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("uncommitted: %s", st)
	}
}

func incr(n string) string {
	i, _ := strconv.Atoi(n)
	return strconv.Itoa(i + 1)
}

// HATE-ciml tc2/tc3: a short reason is 400; no baseline is 409; nothing written.
func TestRebaselineValidation(t *testing.T) {
	h, root, git := setupPlanProject(t)
	const path = "/api/projects/est/rebaseline"
	head := git("rev-parse", "HEAD")
	for _, reason := range []string{"", "   ", "abcd"} {
		if code, body := do(t, h, "POST", path, map[string]interface{}{"reason": reason}); code != http.StatusBadRequest || !strings.Contains(body["detail"].(string), "at least 5 characters") {
			t.Errorf("reason %q: %d %v, want 400", reason, code, body)
		}
	}
	if code, body := do(t, h, "POST", path, map[string]interface{}{"reason": "client moved scope"}); code != http.StatusConflict {
		t.Errorf("no baseline: %d %v, want 409", code, body)
	}
	if git("rev-parse", "HEAD") != head || pm.BaselineExists(root) {
		t.Error("a rejected re-baseline wrote something")
	}
	if code, _ := do(t, h, "POST", "/api/projects/est/baseline-now", nil); code != http.StatusOK {
		t.Fatal(code)
	}
	head = git("rev-parse", "HEAD")
	if code, _ := do(t, h, "POST", path, map[string]interface{}{"reason": "nope"}); code != http.StatusBadRequest {
		t.Errorf("short reason with a baseline: %d", code)
	}
	if git("rev-parse", "HEAD") != head {
		t.Error("a rejected re-baseline made a commit")
	}
	if _, err := os.Stat(pm.BaselinesDir(root)); !os.IsNotExist(err) {
		t.Error("a rejected re-baseline created the archive")
	}
	if code, list := doList(t, h, "/api/projects/est/baselines"); code != http.StatusOK || len(list) != 0 {
		t.Errorf("empty archive: %d %v", code, list)
	}
}

// HATE-y16z tc4 / HATE-fcc7: no Snapshot button in the project header; Help
// covers the Plan strip; the version is 1.0.9.
func TestHeaderAndHelp(t *testing.T) {
	idx, err := os.ReadFile("../../static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile("../../static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(idx), "btn-run-snapshot") || strings.Contains(string(js), "btn-run-snapshot") {
		t.Error("the header Snapshot button (or its handler) is still there")
	}
	for _, want := range []string{"Plan strip", "Re-baseline", "once a day", ".tkt/pm/baselines/"} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("Help does not mention %q", want)
		}
	}
	if config.AppVersion != "1.0.9" {
		t.Errorf("AppVersion = %s", config.AppVersion)
	}
}
