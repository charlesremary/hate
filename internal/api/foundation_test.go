// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"hate/internal/merge"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// fullRouter serves the project, ticket and PM routes, like main.go.
func fullRouter() http.Handler {
	r := chi.NewRouter()
	RegisterProjectRoutes(r)
	RegisterTicketRoutes(r)
	return r
}

// HATE-2yqw tc2: 20 concurrent time logs on one ticket (with concurrent
// config writes and list calls in the mix) all land, the ticket JSON stays
// valid, every log is committed, and nothing deadlocks.
func TestConcurrentTimeLogs(t *testing.T) {
	_, root := setupEstimateProject(t)
	h := fullRouter()
	tk := ticket.BlankTicket("EST-1", "task", "Busy ticket", "a@x")
	tk.Status = "in_progress"
	if err := ticket.WriteTicket(root, tk); err != nil {
		t.Fatal(err)
	}
	git := initGitRepo(t, root)

	const n = 20
	codes := make(chan int, 3*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			code, _ := do(t, h, "POST", "/api/projects/est/tickets/EST-1/time",
				map[string]interface{}{"date": "2026-10-01", "hours": 0.25, "description": fmt.Sprintf("log %d", i), "author": "a@x"})
			codes <- code
		}(i)
		go func(i int) {
			defer wg.Done()
			code, _ := do(t, h, "PUT", "/api/projects/est/strict-time", map[string]interface{}{"strict_time_enforcement": i%2 == 0})
			codes <- code
		}(i)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects/est/tickets/", nil))
			codes <- rec.Code
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("concurrent requests did not finish (deadlock?)")
	}
	close(codes)
	for c := range codes {
		if c != http.StatusOK {
			t.Errorf("a request returned %d", c)
		}
	}

	data, err := os.ReadFile(ticket.TicketPath(root, "EST-1"))
	if err != nil {
		t.Fatal(err)
	}
	var got ticket.Ticket
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("ticket JSON corrupted: %v", err)
	}
	if len(got.TimeEntries) != n {
		t.Fatalf("time entries = %d, want %d", len(got.TimeEntries), n)
	}
	ids, descs := map[string]bool{}, map[string]bool{}
	for _, e := range got.TimeEntries {
		ids[e.ID], descs[e.Description] = true, true
	}
	if len(ids) != n || len(descs) != n {
		t.Errorf("entries not distinct: %d ids, %d descriptions", len(ids), len(descs))
	}
	if c := strings.Count(git("log", "--format=%s"), "EST-1: time logged"); c != n {
		t.Errorf("time-log commits = %d, want %d", c, n)
	}
	if _, err := ticket.ReadConfig(root); err != nil {
		t.Errorf("config corrupted: %v", err)
	}
	if st := git("status", "--porcelain", "--", "tickets"); st != "" {
		t.Errorf("uncommitted ticket changes: %s", st)
	}
}

// HATE-2yqw tc3: when the commit fails (the project is not a git repo), the
// API response carries commit_warning and the server log shows it; a
// successful commit carries no warning.
func TestCommitWarningSurfaced(t *testing.T) {
	h, root := setupEstimateProject(t) // GIT_DIR points nowhere: commits fail
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	code, body := do(t, h, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "x"})
	if code != http.StatusOK {
		t.Fatalf("create: %d %v", code, body)
	}
	warn, _ := body["commit_warning"].(string)
	if !strings.Contains(warn, "not a git repository") || body["id"] == nil {
		t.Errorf("create response = %v, want the ticket plus a commit_warning", body)
	}
	if !strings.Contains(buf.String(), "commit warning") || !strings.Contains(buf.String(), body["id"].(string)+": created") {
		t.Errorf("server log = %q", buf.String())
	}
	if _, err := ticket.ReadTicket(root, body["id"].(string)); err != nil {
		t.Error("the ticket should be saved even though the commit failed")
	}

	// A config write that commits reports it too.
	h2 := fullRouter()
	if code, body := do(t, h2, "PATCH", "/api/projects/est/info", map[string]interface{}{"project_name": "Renamed"}); code != http.StatusOK ||
		!strings.Contains(fmt.Sprint(body["commit_warning"]), "not a git repository") || body["project_name"] != "Renamed" {
		t.Errorf("rename: %d %v", code, body)
	}

	// In a git repo the same calls carry no warning.
	initGitRepo(t, root)
	code, body = do(t, h, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "y"})
	if _, has := body["commit_warning"]; code != http.StatusOK || has {
		t.Errorf("create in a repo: %d %v", code, body)
	}
}

// HATE-lp54 tc1/tc2 + HATE-w0in tc1 through the API: in a repo that still
// tracks index.json, promoting a legacy "feature" ticket succeeds, adds one
// "stop tracking" commit, and leaves git status clean of index.json.
func TestLegacyProjectFirstEdit(t *testing.T) {
	h, root := setupEstimateProject(t)
	f := ticket.BlankTicket("EST-f", "task", "Old feature", "a@x")
	f.Type = "feature"
	f.Status = "open"
	data, _ := json.MarshalIndent(f, "", "  ")
	if err := os.MkdirAll(ticket.TicketsDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ticket.TicketPath(root, "EST-f"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ticket.RegenerateIndex(root); err != nil {
		t.Fatal(err)
	}
	git := initGitRepo(t, root) // commits everything, index.json included
	if git("ls-files", "index.json") != "index.json" {
		t.Fatal("setup: index.json should be tracked")
	}

	code, body := do(t, h, "POST", "/api/projects/est/tickets/EST-f/promote?author=pm@x", nil)
	if code != http.StatusOK || body["type"] != "dev_task" || body["status"] != "in_progress" {
		t.Fatalf("promote legacy feature: %d %v", code, body)
	}
	if _, has := body["commit_warning"]; has {
		t.Errorf("unexpected commit warning: %v", body["commit_warning"])
	}
	subjects := strings.Split(git("log", "--format=%s"), "\n")
	if len(subjects) != 3 || subjects[1] != ticket.IndexUntrackMessage || !strings.HasPrefix(subjects[0], "EST-f: status") {
		t.Errorf("log = %q", subjects)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "tickets/EST-f.json" {
		t.Errorf("promote commit = %q", files)
	}
	if st := git("status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("git status = %q, want clean (index.json ignored)", st)
	}
	raw, _ := os.ReadFile(ticket.TicketPath(root, "EST-f"))
	var stored ticket.Ticket
	_ = json.Unmarshal(raw, &stored)
	notes := ""
	for _, a := range stored.Activity {
		if a.Action == ticket.ActionNormalized {
			notes += a.Detail + ";"
		}
	}
	if stored.Type != "dev_task" || notes != "normalized legacy type feature -> dev_task;normalized legacy status open -> not_started;" {
		t.Errorf("ticket file not normalized: type %s, notes %q", stored.Type, notes)
	}
}

// HATE-wn0y tc1 end to end: two clones of a project (the PM's machine and
// Chuck's) each take a snapshot that detects the same slip. They write the
// identical event, and merging their slip_events.json gives one event.
func TestSameSlipDetectedOnTwoMachines(t *testing.T) {
	h, root, git := setupPlanProject(t)
	if code, _ := do(t, h, "POST", "/api/projects/est/baseline-now", nil); code != http.StatusOK {
		t.Fatal(code)
	}
	slipT3(t, root, git, 6)
	base, _ := os.ReadFile(pm.SlipEventsPath(root)) // nil: no events yet
	other := filepath.Join(t.TempDir(), "est")
	if out, err := exec.Command("git", "clone", "-q", root, other).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	for _, r := range []string{root, other} {
		if _, warn, err := pm.TakeSnapshot("est", r); err != nil || warn != "" {
			t.Fatalf("snapshot in %s: %v %q", r, err, warn)
		}
	}
	mine, _ := os.ReadFile(pm.SlipEventsPath(root))
	theirs, _ := os.ReadFile(pm.SlipEventsPath(other))
	if len(mine) == 0 || string(mine) != string(theirs) {
		t.Fatalf("machines disagree:\n%s\n---\n%s", mine, theirs)
	}
	res, err := merge.ResolveFile(".tkt/pm/slip_events.json", base, mine, theirs)
	if err != nil || res.NeedsAttention {
		t.Fatal(err, res.Note)
	}
	var evs []pm.SlipEvent
	if err := json.Unmarshal(res.Merged, &evs); err != nil || len(evs) != 1 || evs[0].TaskID != "T-3" {
		t.Errorf("merged = %s", res.Merged)
	}
}
