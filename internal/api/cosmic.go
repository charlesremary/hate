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

// EstimateInputs are a project's Monte Carlo estimate inputs, with min_cfp and
// count_unc_pct as their effective values (defaults filled in).
type EstimateInputs struct {
	RefProjects []string `json:"ref_projects"`
	RefAll      bool     `json:"ref_all"`
	RefOwn      bool     `json:"ref_own"`
	MinCFP      int      `json:"min_cfp"`
	CountUncPct float64  `json:"count_unc_pct"`
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
	AvailableProjects []AvailableProject  `json:"available_projects"`
	MonteCarlo        pm.MonteCarloResult `json:"monte_carlo"`
}

// estimateInputsOf reads the effective estimate inputs from a project config.
func estimateInputsOf(cfg *ticket.ProjectConfig) EstimateInputs {
	in := EstimateInputs{
		RefProjects: []string{},
		MinCFP:      pm.EffectiveEstimateMinCFP(cfg),
		CountUncPct: pm.EffectiveCountUncPct(cfg),
	}
	if cfg != nil {
		if cfg.EstimateRefProjects != nil {
			in.RefProjects = cfg.EstimateRefProjects
		}
		in.RefAll = cfg.EstimateRefAll
		in.RefOwn = cfg.EstimateRefOwn
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
// the Monte Carlo estimate, its inputs, and the projects that can be referenced.
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
		AvailableProjects: availableProjects(projectID, root),
		MonteCarlo:        pm.ProjectMonteCarlo(projectID, root, tickets, cfg),
	})
}

// updateCosmicEstimate handles PUT /api/projects/{projectId}/cosmic-estimate.
// Body: {"ref_projects": [ids], "ref_all": bool, "ref_own": bool,
// "min_cfp": int|null, "count_unc_pct": number|null}. Nulls reset to the
// defaults. Validates (400), persists and commits the config, and returns the
// effective inputs with the recomputed Monte Carlo estimate.
func updateCosmicEstimate(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		RefProjects []string `json:"ref_projects"`
		RefAll      bool     `json:"ref_all"`
		RefOwn      bool     `json:"ref_own"`
		MinCFP      *float64 `json:"min_cfp"` // float so a fraction is a 400, not a decode error
		CountUncPct *float64 `json:"count_unc_pct"`
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
	cfg.EstimateMinCFP = minCFP
	cfg.EstimateCountUncPct = req.CountUncPct
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ticket.EnsureProjectIdentity(root, cfg)
	ticket.GitCommit(root, []string{ticket.ConfigPath(root)}, "estimate inputs")

	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"estimate_inputs": estimateInputsOf(cfg),
		"monte_carlo":     pm.ProjectMonteCarlo(projectID, root, tickets, cfg),
	})
}
