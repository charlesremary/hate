// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// getCosmic handles GET /api/projects/{projectId}/cosmic.
// Returns the COSMIC calibration report (per-feature rollups + project aggregate),
// with the persisted manual initial-estimate projection attached.
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
	report := pm.ComputeCosmic(tickets)
	if cfg, err := ticket.ReadConfig(root); err == nil {
		report.Estimate = pm.BuildCosmicEstimate(report.Aggregate.TotalCFP, cfg.EstimateHPerCFP, cfg.EstimateWrapPct)
	} else {
		report.Estimate = pm.BuildCosmicEstimate(report.Aggregate.TotalCFP, nil, nil)
	}
	respondJSON(w, http.StatusOK, report)
}

// updateCosmicEstimate handles PUT /api/projects/{projectId}/cosmic-estimate.
// Body: {"h_per_cfp": <float|null>, "wrap_pct": <float|null>}. Persists the manual
// estimate inputs on the project; nulls clear them. Returns the recomputed estimate.
func updateCosmicEstimate(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		HPerCFP *float64 `json:"h_per_cfp"`
		WrapPct *float64 `json:"wrap_pct"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.HPerCFP != nil && *req.HPerCFP < 0 {
		respondError(w, http.StatusBadRequest, "h_per_cfp must be >= 0")
		return
	}
	if req.WrapPct != nil && *req.WrapPct < 0 {
		respondError(w, http.StatusBadRequest, "wrap_pct must be >= 0")
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.EstimateHPerCFP = req.HPerCFP
	cfg.EstimateWrapPct = req.WrapPct
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	totalCFP := pm.ComputeCosmic(tickets).Aggregate.TotalCFP
	respondJSON(w, http.StatusOK, pm.BuildCosmicEstimate(totalCFP, cfg.EstimateHPerCFP, cfg.EstimateWrapPct))
}
