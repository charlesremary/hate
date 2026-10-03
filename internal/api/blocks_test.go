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
	if len(rep.Stages) != 3 || rep.Stages[2].Tickets[0].ID != "T-c" || rep.Stages[2].Stage != 3 {
		t.Errorf("stages = %+v", rep.Stages)
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

// HATE-evqw: the phase rollup orders "Block NN" phases numerically and adds
// each block's dates.
func TestPhaseRollupBlocksAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)
	cfg, _ := ticket.ReadConfig(root)
	cfg.BlockWeeks, cfg.RequestedStart = 2, "2026-11-04"
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	ph := func(id, phase string) *ticket.Ticket {
		tk := ticket.BlankTicket(id, "task", id, "c@example.com")
		tk.Phase = &phase
		return tk
	}
	writeTickets(t, root, ph("T-10", "Block 10 (Mar 8-19)"), ph("T-02", "Block 02 (Nov 16-27)"), ph("T-01", "Block 01 (Nov 2-13)"))
	var rep pm.RollupReport
	getJSON(t, h, "/api/projects/est/phase-rollup", &rep)
	if len(rep.Phases) < 3 || rep.Phases[0].Block != 1 || rep.Phases[1].Block != 2 || rep.Phases[2].Block != 10 {
		t.Fatalf("phases = %+v", rep.Phases)
	}
	if p := rep.Phases[1]; p.BlockStart != "2026-11-16" || p.BlockEnd != "2026-11-27" {
		t.Errorf("Block 02 dates = %s..%s", p.BlockStart, p.BlockEnd)
	}
}
