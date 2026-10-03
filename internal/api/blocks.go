// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// ---------------------------------------------------------------------------
// Planning blocks (Settings) and the work order (GET /ready)
// ---------------------------------------------------------------------------

// blockWeeksJSON is the GET/PUT block-weeks response: 2, 3, or null (no blocks).
func blockWeeksJSON(cfg *ticket.ProjectConfig) map[string]interface{} {
	var v interface{}
	if cfg.BlockWeeks != 0 {
		v = cfg.BlockWeeks
	}
	return map[string]interface{}{"block_weeks": v}
}

// getBlockWeeks handles GET /api/projects/{projectId}/block-weeks.
func getBlockWeeks(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, blockWeeksJSON(cfg))
}

// updateBlockWeeks handles PUT /api/projects/{projectId}/block-weeks.
// Body: {"block_weeks": 2|3|null}; null (or 0) turns blocks off. Anything else
// is a 400. Saves .tkt/config.json and commits it, only when it changed.
func updateBlockWeeks(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	var req struct {
		BlockWeeks *int `json:"block_weeks"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	weeks := 0
	if req.BlockWeeks != nil {
		weeks = *req.BlockWeeks
	}
	if !pm.ValidBlockWeeks(weeks) {
		respondError(w, http.StatusBadRequest, "block_weeks must be 2, 3, or null for no blocks")
		return
	}
	defer ticket.LockProject(root)()
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := blockWeeksJSON(cfg)
	if cfg.BlockWeeks == weeks {
		respondJSON(w, http.StatusOK, resp)
		return
	}
	cfg.BlockWeeks = weeks
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	msg := "planning blocks: none"
	if weeks != 0 {
		msg = fmt.Sprintf("planning blocks: %d weeks", weeks)
	}
	resp = blockWeeksJSON(cfg)
	if warn := ticket.CommitWarning(ticket.CommitFiles(root, []string{ticket.ConfigPath(root)}, msg)); warn != "" {
		resp["commit_warning"] = warn
	}
	respondJSON(w, http.StatusOK, resp)
}

// getBlocks handles GET /api/projects/{projectId}/blocks: the planning blocks
// [{n, label, start, end}] from block 1 to the later of the requested end and
// the projected finish, plus one; [] when the project doesn't plan in blocks.
// Read-only.
func getBlocks(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, pm.BlocksForProject(cfg, tickets, pm.ProjectEstimateContext(root, tickets, cfg), time.Now()))
}

// getReady handles GET /api/projects/{projectId}/ready: {ready, next, stages}
// from the shared dependency stages. Read-only.
func getReady(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
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
		cfg = nil
	}
	respondJSON(w, http.StatusOK, pm.ComputeReady(tickets, pm.ProjectEstimateContext(root, tickets, cfg)))
}
