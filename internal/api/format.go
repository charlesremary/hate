// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"hate/internal/ticket"
)

// respondNewerFormat answers 409 for a project a newer hate changed, with
// newer_format: true so the UI can show the upgrade banner.
func respondNewerFormat(w http.ResponseWriter, err error) {
	respondJSON(w, http.StatusConflict, map[string]interface{}{
		"detail":       err.Error(),
		"newer_format": true,
	})
}

// writeAllowedOnNewer lists the project routes that still work on a project
// a newer hate changed: syncing (pull only; Sync refuses to merge or push
// local changes into a newer project) and the local git identity.
var writeAllowedOnNewer = []string{"/sync", "/autosync", "/git-identity"}

// blockWritesWhenNewerFormat rejects every non-GET request to a
// /api/projects/{projectId}/* route when a newer hate changed the project.
// Reads go through: the project stays viewable.
func blockWritesWhenNewerFormat(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/projects/"+chi.URLParam(r, "projectId"))
		for _, p := range writeAllowedOnNewer {
			if rest == p || strings.HasPrefix(rest, p+"/") {
				next.ServeHTTP(w, r)
				return
			}
		}
		root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
		if !ok {
			return
		}
		if err := ticket.CheckWritable(root); err != nil {
			respondNewerFormat(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
