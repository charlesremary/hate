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
// Returns the COSMIC calibration report (per-feature rollups + project aggregate).
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
	respondJSON(w, http.StatusOK, pm.ComputeCosmic(tickets))
}
