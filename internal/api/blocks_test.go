// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// getJSON GETs path and decodes the body into out.
func getJSON(t *testing.T, h http.Handler, path string, out interface{}) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("GET %s: %v (%s)", path, err, rec.Body.String())
	}
}

// HATE-evqw tc1: block_weeks 2 is saved in .tkt/config.json and committed;
// bad values are 400s; null turns blocks off (tc4).
func TestBlockWeeksAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)
	git := initGitRepo(t, root)
	const path = "/api/projects/est/block-weeks"

	if code, body := do(t, h, "GET", path, nil); code != http.StatusOK || body["block_weeks"] != nil {
		t.Fatalf("GET unset: %d %v", code, body)
	}
	for _, bad := range []interface{}{1, 4, -2} {
		if code, _ := do(t, h, "PUT", path, map[string]interface{}{"block_weeks": bad}); code != http.StatusBadRequest {
			t.Errorf("PUT %v: %d, want 400", bad, code)
		}
	}
	code, body := do(t, h, "PUT", path, map[string]interface{}{"block_weeks": 2})
	if code != http.StatusOK || body["block_weeks"] != 2.0 || body["commit_warning"] != nil {
		t.Fatalf("PUT 2: %d %v", code, body)
	}
	raw, _ := os.ReadFile(ticket.ConfigPath(root))
	if !strings.Contains(string(raw), `"block_weeks": 2`) {
		t.Errorf("config.json lacks block_weeks: %s", raw)
	}
	if log := git("log", "-1", "--format=%s"); log != "planning blocks: 2 weeks" {
		t.Errorf("last commit = %q", log)
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Errorf("config not committed: %s", st)
	}
	// Same value again: no new commit.
	head := git("rev-parse", "HEAD")
	do(t, h, "PUT", path, map[string]interface{}{"block_weeks": 2})
	if git("rev-parse", "HEAD") != head {
		t.Error("an unchanged save made a commit")
	}
	if code, body = do(t, h, "PUT", path, map[string]interface{}{"block_weeks": nil}); code != http.StatusOK || body["block_weeks"] != nil {
		t.Errorf("PUT null: %d %v", code, body)
	}
	if cfg, _ := ticket.ReadConfig(root); cfg.BlockWeeks != 0 {
		t.Errorf("block_weeks after null = %d", cfg.BlockWeeks)
	}
	if log := git("log", "-1", "--format=%s"); log != "planning blocks: none" {
		t.Errorf("last commit = %q", log)
	}
}

// HATE-evqw tc2/tc4: GET /blocks with requested start Wed 2026-11-04 starts
// Block 01 on Mon 2026-11-02 and ends it Fri 2026-11-13; with no blocks set it
// is an empty list.
func TestBlocksAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)
	var blocks []pm.Block
	getJSON(t, h, "/api/projects/est/blocks", &blocks)
	if blocks == nil || len(blocks) != 0 {
		t.Fatalf("no blocks set: %v, want []", blocks)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects/est/blocks", nil))
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("body = %q, want []", rec.Body.String())
	}

	cfg, _ := ticket.ReadConfig(root)
	cfg.BlockWeeks, cfg.RequestedStart, cfg.RequestedEnd = 2, "2026-11-04", "2027-01-29"
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	getJSON(t, h, "/api/projects/est/blocks", &blocks)
	if len(blocks) < 7 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if b := blocks[0]; b.N != 1 || b.Start != "2026-11-02" || b.End != "2026-11-13" || b.Label != "Block 01 (Nov 2-13)" {
		t.Errorf("block 1 = %+v", b)
	}
	// Covers the requested end (Fri 2027-01-29 is in block 7, Jan 25-Feb 5), plus one.
	if last := blocks[len(blocks)-1]; last.N != 8 || last.Start != "2027-02-08" {
		t.Errorf("last block = %+v (of %d)", last, len(blocks))
	}
}

// writeTickets saves tickets into the project and returns them.
func writeTickets(t *testing.T, root string, ts ...*ticket.Ticket) {
	t.Helper()
	for _, tk := range ts {
		if err := ticket.WriteTicket(root, tk); err != nil {
			t.Fatal(err)
		}
	}
}

// HATE-43sr tc5: GET /ready on a chain A -> B -> C with A open is ready=[A],
// next=[B].
func TestReadyAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)
	// Clear the fixture's tickets so only the chain is open.
	if err := os.RemoveAll(ticket.TicketsDir(root)); err != nil {
		t.Fatal(err)
	}
	mk := func(id string, preds ...string) *ticket.Ticket {
		tk := ticket.BlankTicket(id, "task", "Ticket "+id, "c@example.com")
		hrs := 2.0
		tk.EstimateHours = &hrs
		tk.Tags = []string{ticket.ClassConfig}
		tk.Predecessors = preds
		return tk
	}
	writeTickets(t, root, mk("T-a"), mk("T-b", "T-a"), mk("T-c", "T-b"))
	var rep pm.ReadyReport
	getJSON(t, h, "/api/projects/est/ready", &rep)
	if len(rep.Ready) != 1 || rep.Ready[0].ID != "T-a" || len(rep.Next) != 1 || rep.Next[0].ID != "T-b" {
		t.Fatalf("ready=%+v next=%+v", rep.Ready, rep.Next)
	}
	if len(rep.Waves) != 3 || rep.Waves[2].Tickets[0].ID != "T-c" || rep.Waves[2].Wave != 3 {
		t.Errorf("waves = %+v", rep.Waves)
	}
}

// HATE-478u: GET /hours-budget carries the pools and the per-contributor split.
func TestHoursBudgetAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)
	if err := os.RemoveAll(ticket.TicketsDir(root)); err != nil {
		t.Fatal(err)
	}
	a := ticket.BlankTicket("T-a", "task", "Work", "c@example.com")
	a.TimeEntries = []ticket.TimeEntry{
		{Author: "bob@x.com", Bucket: "work", Hours: 3},
		{Author: "sally@x.com", Bucket: "qa", Hours: 5},
	}
	m := ticket.BlankTicket("T-m", "meeting", "Sync", "c@example.com")
	m.TimeEntries = []ticket.TimeEntry{{Author: "bob@x.com", Hours: 1}} // legacy -> admin
	writeTickets(t, root, a, m)

	var b pm.HoursBudget
	getJSON(t, h, "/api/projects/est/hours-budget", &b)
	if b.Work.Spent != 3 || b.Admin.Spent != 1 || b.QA.Spent != 5 {
		t.Errorf("pools = %+v / %+v / %+v", b.Work, b.Admin, b.QA)
	}
	if len(b.ByContributor) != 2 || b.ByContributor[0].Name != "sally" || b.ByContributor[0].QA != 5 ||
		b.ByContributor[1].Author != "bob@x.com" || b.ByContributor[1].Work != 3 || b.ByContributor[1].Admin != 1 || b.ByContributor[1].Total != 4 {
		t.Errorf("by_contributor = %+v", b.ByContributor)
	}
	if html := getHTML(t, h, "/api/projects/est/dashboard"); !strings.Contains(html, "Logged by contributor") {
		t.Error("dashboard hours budget card has no contributor breakdown")
	}
}

// HATE-43sr tc1 + HATE-evqw tc3: the PM dashboard has no Execution plan card
// and no stage headers; with blocks set its Gantt has block bands, without
// them it groups by person.
func TestDashboardBlocksNoExecPlan(t *testing.T) {
	h, root := setupCosmicProjects(t)
	html := getHTML(t, h, "/api/projects/est/dashboard")
	for _, bad := range []string{"Execution plan", "parallel stages", ">Stage 1<", "Stage 1 "} {
		if strings.Contains(html, bad) {
			t.Errorf("dashboard still has %q", bad)
		}
	}
	if strings.Contains(html, "gantt-block-band") {
		t.Error("bands without blocks")
	}
	if !strings.Contains(html, "Rows grouped by person") {
		t.Error("no-blocks Gantt should group by person")
	}

	cfg, _ := ticket.ReadConfig(root)
	cfg.BlockWeeks = 2
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	html = getHTML(t, h, "/api/projects/est/dashboard")
	if !strings.Contains(html, "gantt-block-band") || !strings.Contains(html, "gantt-block-label") || !strings.Contains(html, "Block 01 (") {
		t.Error("block Gantt missing bands / labels")
	}
	if strings.Contains(html, "Execution plan") {
		t.Error("Execution plan card is back")
	}
	x := getHTML(t, h, "/api/projects/est/gantt.drawio")
	if !strings.Contains(x, `id="block0"`) || strings.Contains(x, "Stage ") {
		t.Error("draw.io export: want block bands, no stages")
	}
}

// HATE-a7i1: on a block project the rollup defaults to blocks, in number
// order, with each block's dates; ?by=lifecycle groups by kind of work.
func TestRollupBlocksAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)
	cfg, _ := ticket.ReadConfig(root)
	cfg.BlockWeeks, cfg.RequestedStart = 2, "2026-11-04"
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	bl := func(id string, block int, lc string) *ticket.Ticket {
		tk := ticket.BlankTicket(id, "task", id, "c@example.com")
		tk.Block = block
		if lc != "" {
			tk.Lifecycle = &lc
		}
		return tk
	}
	writeTickets(t, root, bl("T-10", 10, ""), bl("T-02", 2, "Build"), bl("T-01", 1, "Design"))
	var rep pm.RollupReport
	getJSON(t, h, "/api/projects/est/rollup", &rep)
	if rep.By != "block" || len(rep.Groups) < 3 || rep.Groups[0].Block != 1 || rep.Groups[1].Block != 2 || rep.Groups[2].Block != 10 {
		t.Fatalf("groups = %+v", rep.Groups)
	}
	if g := rep.Groups[1]; g.BlockStart != "2026-11-16" || g.BlockEnd != "2026-11-27" || !strings.HasPrefix(g.Label, "Block 02 (") {
		t.Errorf("Block 02 = %s..%s %q", g.BlockStart, g.BlockEnd, g.Label)
	}
	var lc pm.RollupReport
	getJSON(t, h, "/api/projects/est/rollup?by=lifecycle", &lc)
	if lc.By != "lifecycle" || len(lc.Groups) < 2 || lc.Groups[0].Key != "Build" {
		t.Errorf("by lifecycle = %+v", lc.Groups)
	}
}
