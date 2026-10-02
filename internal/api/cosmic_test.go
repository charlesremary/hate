// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/ticket"
)

// setupCosmicProjects points the app config at a temp projects root holding
// "est" (two features, nothing done) and "ref" (three finished reference
// features at 0.2, 0.3, 0.4 h/CFP), and returns a router with the project
// sub-routes. Git is pointed at a nonexistent dir so commits are no-ops.
func setupCosmicProjects(t *testing.T) (http.Handler, string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(base, "no-git"))
	oldPath := config.AppConfigPath
	config.AppConfigPath = filepath.Join(base, "app.json")
	t.Cleanup(func() { config.AppConfigPath = oldPath })

	projects := filepath.Join(base, "projects")
	app, _ := json.Marshal(map[string]interface{}{"projects_root": projects})
	if err := os.MkdirAll(projects, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.AppConfigPath, app, 0644); err != nil {
		t.Fatal(err)
	}

	mk := func(id, name string) string {
		root := filepath.Join(projects, id)
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
		if err := ticket.WriteConfig(root, ticket.DefaultConfig("c", name, id, "T")); err != nil {
			t.Fatal(err)
		}
		return root
	}
	mk1 := func(id, status string, tags []string) *ticket.Ticket {
		tk := ticket.BlankTicket(id, "task", id, "c@example.com")
		tk.Status = status
		tk.Tags = tags
		return tk
	}
	save := func(root string, tk *ticket.Ticket) {
		if err := ticket.WriteTicket(root, tk); err != nil {
			t.Fatal(err)
		}
	}
	feature := func(root, id string, cfp int, hours float64, status string, extra ...string) {
		save(root, mk1(id, "not_started", append([]string{fmt.Sprintf("cfp:%d", cfp)}, extra...)))
		child := mk1(id+"-1", status, []string{"parent:" + id, ticket.ClassFunctional})
		if hours > 0 {
			child.TimeEntries = []ticket.TimeEntry{{ID: "e" + id, Date: "2026-09-01", Hours: hours}}
		}
		save(root, child)
	}

	est := mk("est", "Estimate Me")
	feature(est, "T-1", 10, 0, "not_started", "calibration-slice")
	feature(est, "T-2", 20, 0, "not_started")
	h := 2.0
	wrap := mk1("T-3", "not_started", []string{ticket.ClassConfig})
	wrap.EstimateHours = &h
	save(est, wrap)

	ref := mk("ref", "Reference")
	feature(ref, "T-1", 10, 2, "complete")
	feature(ref, "T-2", 10, 3, "complete")
	feature(ref, "T-3", 10, 4, "closed")

	r := chi.NewRouter()
	r.Route("/api/projects/{projectId}", RegisterPMSubRoutes)
	return r, est
}

func TestCosmicEstimateAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)

	// GET with nothing saved: the defaults (manual baseline, Agentic, + own
	// features), the presets, the picker, and a range from the baseline alone.
	code, body := do(t, h, "GET", "/api/projects/est/cosmic", nil)
	if code != http.StatusOK {
		t.Fatalf("GET: %d %v", code, body)
	}
	if _, ok := body["estimate"]; ok {
		t.Error("old estimate field should be gone")
	}
	if body["slice_total"] != 1.0 || body["slice_done"] != 0.0 {
		t.Errorf("slice = %v/%v, want 0 of 1", body["slice_done"], body["slice_total"])
	}
	inputs := body["estimate_inputs"].(map[string]interface{})
	if inputs["min_cfp"] != 3.0 || inputs["count_unc_pct"] != 0.0 || inputs["ref_own"] != true ||
		inputs["ref_manual"] != true || inputs["defaults_applied"] != true || inputs["manual_preset"] != "agentic" ||
		fmt.Sprint(inputs["manual"]) != "map[high:1 likely:0.25 low:0.08]" ||
		len(inputs["ref_projects"].([]interface{})) != 0 {
		t.Errorf("default inputs = %v", inputs)
	}
	presets := body["manual_presets"].([]interface{})
	if len(presets) != 2 ||
		fmt.Sprint(presets[0]) != "map[high:1 id:agentic label:Agentic (Claude-assisted) likely:0.25 low:0.08]" ||
		fmt.Sprint(presets[1]) != "map[high:18 id:traditional label:Traditional (hand-coded) likely:12 low:8]" {
		t.Errorf("manual_presets = %v", presets)
	}
	avail := body["available_projects"].([]interface{})
	if len(avail) != 1 || avail[0].(map[string]interface{})["id"] != "ref" || avail[0].(map[string]interface{})["name"] != "Reference" {
		t.Errorf("available_projects = %v, want [ref]", avail)
	}
	mc := body["monte_carlo"].(map[string]interface{})
	code50 := mc["code"].(map[string]interface{})["p50"].(float64)
	if mc["ok"] != true || mc["manual_in_use"] != true || mc["p_manual"] != 1.0 || mc["n_own"] != 0.0 ||
		code50 < 7.5 || code50 > 10 { // 30 CFP x 0.25 = 7.5 at the median rate; the P50 of a skewed sum sits above it
		t.Errorf("default monte_carlo = %v", mc)
	}
	if cfg, err := ticket.ReadConfig(root); err != nil || cfg.EstimateRefManual != nil || cfg.EstimateManual != nil {
		t.Errorf("GET must not write the defaults (err %v)", err)
	}

	// Validation → 400.
	for _, bad := range []map[string]interface{}{
		{"min_cfp": 0},
		{"min_cfp": 2.5},
		{"count_unc_pct": 101},
		{"count_unc_pct": -1},
		{"ref_projects": []string{"ghost"}},
		{"ref_projects": []string{"est"}}, // itself
		{"ref_manual": true, "manual": map[string]float64{"low": 0.3, "likely": 0.2, "high": 1}},
		{"ref_manual": true, "manual": map[string]float64{"low": 0.1, "likely": 2, "high": 1}},
		{"ref_manual": true, "manual": map[string]float64{"low": 0, "likely": 0.2, "high": 1}},
	} {
		if code, body := do(t, h, "PUT", "/api/projects/est/cosmic-estimate", bad); code != http.StatusBadRequest || body["detail"] == nil {
			t.Errorf("PUT %v: %d %v, want 400", bad, code, body)
		}
	}

	// Valid PUT: borrow from ref, persist, and return the recomputed estimate.
	code, body = do(t, h, "PUT", "/api/projects/est/cosmic-estimate", map[string]interface{}{
		"ref_projects": []string{"ref", "ref"}, "ref_all": false, "ref_own": true, "min_cfp": 5, "count_unc_pct": 10,
	})
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %v", code, body)
	}
	inputs = body["estimate_inputs"].(map[string]interface{})
	if inputs["min_cfp"] != 5.0 || inputs["count_unc_pct"] != 10.0 || inputs["ref_own"] != true ||
		fmt.Sprint(inputs["ref_projects"]) != "[ref]" {
		t.Errorf("PUT inputs = %v", inputs)
	}
	mc = body["monte_carlo"].(map[string]interface{})
	if mc["ok"] != true || mc["n_borrowed"] != 3.0 || mc["n_own"] != 0.0 || mc["total_cfp"] != 30.0 ||
		mc["runs"] != 10000.0 || mc["seed"] != 42.0 || mc["widen_k"] != 1.5 {
		t.Errorf("PUT monte_carlo = %v", mc)
	}
	if pw := mc["platform_wrap"].(map[string]interface{}); pw["hours"] != 2.0 || pw["ticket_count"] != 1.0 {
		t.Errorf("platform_wrap = %v, want 2h over 1 ticket", pw)
	}
	if len(mc["histogram"].([]interface{})) != 20 {
		t.Errorf("histogram bins = %d, want 20", len(mc["histogram"].([]interface{})))
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EstimateRefOwn || len(cfg.EstimateRefProjects) != 1 || cfg.EstimateMinCFP == nil || *cfg.EstimateMinCFP != 5 ||
		cfg.EstimateCountUncPct == nil || *cfg.EstimateCountUncPct != 10 {
		t.Errorf("persisted config = %+v", cfg)
	}

	// GET reflects the saved inputs; nulls reset to the defaults.
	_, body = do(t, h, "GET", "/api/projects/est/cosmic", nil)
	if body["monte_carlo"].(map[string]interface{})["ok"] != true {
		t.Errorf("GET after PUT: monte_carlo = %v", body["monte_carlo"])
	}
	code, body = do(t, h, "PUT", "/api/projects/est/cosmic-estimate", map[string]interface{}{
		"ref_projects": []string{}, "ref_all": true, "ref_own": false, "min_cfp": nil, "count_unc_pct": nil,
	})
	inputs = body["estimate_inputs"].(map[string]interface{})
	if code != http.StatusOK || inputs["min_cfp"] != 3.0 || inputs["count_unc_pct"] != 0.0 || inputs["ref_all"] != true {
		t.Errorf("reset PUT: %d %v", code, inputs)
	}

	// Manual baseline round trip: Traditional, persisted and reported.
	code, body = do(t, h, "PUT", "/api/projects/est/cosmic-estimate", map[string]interface{}{
		"ref_projects": []string{}, "ref_all": false, "ref_own": true, "ref_manual": true,
		"manual": map[string]float64{"low": 8, "likely": 12, "high": 18},
	})
	if code != http.StatusOK {
		t.Fatalf("manual PUT: %d %v", code, body)
	}
	inputs = body["estimate_inputs"].(map[string]interface{})
	mc = body["monte_carlo"].(map[string]interface{})
	if inputs["ref_manual"] != true || inputs["manual_preset"] != "traditional" || inputs["defaults_applied"] != false ||
		mc["ok"] != true || mc["p_manual"] != 1.0 || mc["ref_median_rate"] != 12.0 {
		t.Errorf("manual PUT: inputs %v, monte_carlo ok=%v p_manual=%v median=%v", inputs, mc["ok"], mc["p_manual"], mc["ref_median_rate"])
	}
	cfg, _ = ticket.ReadConfig(root)
	if cfg.EstimateRefManual == nil || !*cfg.EstimateRefManual || cfg.EstimateManual == nil ||
		*cfg.EstimateManual != (ticket.ManualBaseline{Low: 8, Likely: 12, High: 18}) {
		t.Errorf("persisted manual = %v %v", cfg.EstimateRefManual, cfg.EstimateManual)
	}
	// A custom range; an omitted manual then keeps it.
	_, body = do(t, h, "PUT", "/api/projects/est/cosmic-estimate", map[string]interface{}{
		"ref_manual": true, "manual": map[string]float64{"low": 0.1, "likely": 0.3, "high": 0.9},
	})
	if inputs := body["estimate_inputs"].(map[string]interface{}); inputs["manual_preset"] != "custom" {
		t.Errorf("custom PUT inputs = %v", inputs)
	}
	_, body = do(t, h, "PUT", "/api/projects/est/cosmic-estimate", map[string]interface{}{"ref_manual": true})
	if inputs := body["estimate_inputs"].(map[string]interface{}); fmt.Sprint(inputs["manual"]) != "map[high:0.9 likely:0.3 low:0.1]" {
		t.Errorf("omitted manual should keep the saved range: %v", inputs["manual"])
	}

	// Explicitly unticking everything is saved as such: no reference selected.
	code, body = do(t, h, "PUT", "/api/projects/est/cosmic-estimate", map[string]interface{}{
		"ref_projects": []string{}, "ref_all": false, "ref_own": false, "ref_manual": false,
	})
	inputs = body["estimate_inputs"].(map[string]interface{})
	mc = body["monte_carlo"].(map[string]interface{})
	if code != http.StatusOK || inputs["ref_manual"] != false || inputs["ref_own"] != false || inputs["defaults_applied"] != false ||
		mc["ok"] != false || mc["error"] != "no reference selected" {
		t.Errorf("untick all: %d inputs %v ok=%v err=%v", code, inputs, mc["ok"], mc["error"])
	}
	_, body = do(t, h, "GET", "/api/projects/est/cosmic", nil)
	if mc := body["monte_carlo"].(map[string]interface{}); mc["error"] != "no reference selected" {
		t.Errorf("GET after untick: %v", mc["error"])
	}
}
