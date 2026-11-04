// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"hate/internal/service"
	"hate/internal/ticket"
)

// respondNewerFormat answers 409 for a project whose format this hate can't
// write: newer_format (a newer hate changed it: upgrade) or needs_migration
// (older: apply the one-time update), so the UI can show the right banner.
func respondNewerFormat(w http.ResponseWriter, err error) {
	respondJSON(w, http.StatusConflict, map[string]interface{}{
		"detail":          err.Error(),
		"newer_format":    ticket.IsNewerFormat(err),
		"needs_migration": ticket.IsNeedsMigration(err),
	})
}

// writeAllowedOnNewer lists the project routes that still work on a project
// this hate can't write: syncing (Sync refuses to merge local changes across
// formats), the local git identity, and the one-time update itself.
var writeAllowedOnNewer = []string{"/sync", "/autosync", "/git-identity", "/migration"}

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

// getMigration handles GET /api/projects/{projectId}/migration: the preview
// of the one-time project update (?blocks_from_phase=true|false overrides the
// default rule). Writes nothing.
func getMigration(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	plan, err := service.PlanMigration(root, migrateOpts(r.URL.Query().Get("blocks_from_phase")))
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondJSON(w, http.StatusOK, plan)
}

// applyMigration handles POST /api/projects/{projectId}/migration
// {blocks_from_phase?, author?}: runs the update and commits it once.
func applyMigration(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	var req struct {
		BlocksFromPhase *bool  `json:"blocks_from_phase"`
		Author          string `json:"author"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	author := strings.TrimSpace(req.Author)
	if author == "" {
		cfg, _ := ticket.ReadConfig(root)
		if id := ticket.EffectiveIdentity(root, cfg); id.Email != "" {
			author = id.Email
		} else {
			author = "hate"
		}
	}
	plan, err := service.ApplyMigration(root, ticket.MigrateOptions{BlocksFromPhase: req.BlocksFromPhase}, author)
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondJSON(w, http.StatusOK, plan)
}

func migrateOpts(q string) ticket.MigrateOptions {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "true", "1", "yes":
		t := true
		return ticket.MigrateOptions{BlocksFromPhase: &t}
	case "false", "0", "no":
		f := false
		return ticket.MigrateOptions{BlocksFromPhase: &f}
	}
	return ticket.MigrateOptions{}
}
