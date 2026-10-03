// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"fmt"
	"math"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// EstimateInputs are a project's Monte Carlo estimate inputs, as their
// effective values (defaults filled in). With no saved reference inputs the
// defaults are the manual baseline (Agentic) + own features, and
// DefaultsApplied is true.
type EstimateInputs struct {
	RefProjects     []string              `json:"ref_projects"`
	RefAll          bool                  `json:"ref_all"`
	RefOwn          bool                  `json:"ref_own"`
	RefManual       bool                  `json:"ref_manual"`
	Manual          ticket.ManualBaseline `json:"manual"`
	ManualPreset    string                `json:"manual_preset"` // agentic | traditional | custom
	DefaultsApplied bool                  `json:"defaults_applied"`
	MinCFP          int                   `json:"min_cfp"`
	CountUncPct     float64               `json:"count_unc_pct"`
}

// AvailableProject is another known project, offered as a reference.
type AvailableProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CosmicResponse is the GET /cosmic payload: the calibration report plus the
// Monte Carlo estimate and its inputs.
type CosmicResponse struct {
	pm.CosmicReport
	EstimateInputs    EstimateInputs      `json:"estimate_inputs"`
	ManualPresets     []pm.ManualPreset   `json:"manual_presets"`
	AvailableProjects []AvailableProject  `json:"available_projects"`
	MonteCarlo        pm.MonteCarloResult `json:"monte_carlo"`
}

// estimateInputsOf reads the effective estimate inputs from a project config.
func estimateInputsOf(cfg *ticket.ProjectConfig) EstimateInputs {
	refs := pm.EffectiveEstimateRefs(cfg)
	in := EstimateInputs{
		RefProjects:     []string{},
		RefAll:          refs.All,
		RefOwn:          refs.Own,
		RefManual:       refs.Manual,
		Manual:          refs.ManualBaseline,
		ManualPreset:    pm.ManualPresetID(refs.ManualBaseline),
		DefaultsApplied: refs.DefaultsApplied,
		MinCFP:          pm.EffectiveEstimateMinCFP(cfg),
		CountUncPct:     pm.EffectiveCountUncPct(cfg),
	}
	if refs.Projects != nil {
		in.RefProjects = refs.Projects
	}
	return in
}

// availableProjects lists the known projects other than this one.
func availableProjects(projectID, root string) []AvailableProject {
	out := []AvailableProject{}
	self := filepath.Clean(root)
	for _, p := range config.ListProjects() {
		if p.ID == projectID || filepath.Clean(p.Path) == self {
			continue
		}
		out = append(out, AvailableProject{ID: p.ID, Name: p.Name})
	}
	return out
}

// getCosmic handles GET /api/projects/{projectId}/cosmic.
// Returns the COSMIC calibration report (per-feature rollups + project aggregate),
// the Monte Carlo estimate, its inputs, the manual baseline presets, and the
// projects that can be referenced.
func getCosmic(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		cfg = &ticket.ProjectConfig{}
	}
	respondJSON(w, http.StatusOK, CosmicResponse{
		CosmicReport:      pm.ComputeCosmic(tickets),
		EstimateInputs:    estimateInputsOf(cfg),
		ManualPresets:     pm.ManualPresets,
		AvailableProjects: availableProjects(projectID, root),
		MonteCarlo:        pm.ProjectMonteCarlo(projectID, root, tickets, cfg),
	})
}

// updateCosmicEstimate handles PUT /api/projects/{projectId}/cosmic-estimate.
// Body: {"ref_projects": [ids], "ref_all": bool, "ref_own": bool,
// "ref_manual": bool, "manual": {"low", "likely", "high"} (h/CFP, optional),
// "min_cfp": int|null, "count_unc_pct": number|null}. Nulls reset to the
// defaults; an omitted manual keeps the saved range (or the default preset).
// Validates (400; manual needs 0 < low <= likely <= high), persists and
// commits the config, and returns the effective inputs with the recomputed
// Monte Carlo estimate.
func updateCosmicEstimate(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	defer ticket.LockProject(root)()
	var req struct {
		RefProjects []string               `json:"ref_projects"`
		RefAll      bool                   `json:"ref_all"`
		RefOwn      bool                   `json:"ref_own"`
		RefManual   bool                   `json:"ref_manual"`
		Manual      *ticket.ManualBaseline `json:"manual"`
		MinCFP      *float64               `json:"min_cfp"` // float so a fraction is a 400, not a decode error
		CountUncPct *float64               `json:"count_unc_pct"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var minCFP *int
	if req.MinCFP != nil {
		if *req.MinCFP < 1 || *req.MinCFP != math.Trunc(*req.MinCFP) {
			respondError(w, http.StatusBadRequest, "min_cfp must be a whole number >= 1")
			return
		}
		v := int(*req.MinCFP)
		minCFP = &v
	}
	if req.CountUncPct != nil && (*req.CountUncPct < 0 || *req.CountUncPct > 100) {
		respondError(w, http.StatusBadRequest, "count_unc_pct must be between 0 and 100")
		return
	}
	if req.Manual != nil && !pm.ValidManualBaseline(*req.Manual) {
		respondError(w, http.StatusBadRequest, "manual: 0 < low <= likely <= high required")
		return
	}
	known := map[string]bool{}
	for _, p := range availableProjects(projectID, root) {
		known[p.ID] = true
	}
	refs := []string{}
	seen := map[string]bool{}
	for _, id := range req.RefProjects {
		if !known[id] {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("unknown reference project: %q", id))
			return
		}
		if !seen[id] {
			seen[id] = true
			refs = append(refs, id)
		}
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.EstimateRefProjects = refs
	if len(refs) == 0 {
		cfg.EstimateRefProjects = nil
	}
	cfg.EstimateRefAll = req.RefAll
	cfg.EstimateRefOwn = req.RefOwn
	refManual := req.RefManual
	cfg.EstimateRefManual = &refManual
	if req.Manual != nil {
		m := *req.Manual
		cfg.EstimateManual = &m
	}
	cfg.EstimateMinCFP = minCFP
	cfg.EstimateCountUncPct = req.CountUncPct
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	warn := ticket.CommitWarning(ticket.CommitFiles(root, []string{ticket.ConfigPath(root)}, "estimate inputs"))

	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]interface{}{
		"estimate_inputs": estimateInputsOf(cfg),
		"monte_carlo":     pm.ProjectMonteCarlo(projectID, root, tickets, cfg),
	}
	if warn != "" {
		resp["commit_warning"] = warn
	}
	respondJSON(w, http.StatusOK, resp)
}
