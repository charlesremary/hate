// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"hate/internal/catalog"
)

// RegisterCatalogRoutes registers the org-level wrap-catalog API (HATE-k0gf) — a
// shared, optional suggested-tag vocabulary. It's org-level, so it lives above
// /api/projects/{id}.
func RegisterCatalogRoutes(r chi.Router) {
	r.Route("/api/catalog", func(r chi.Router) {
		r.Get("/", getCatalog)
		r.Post("/entries", createCatalogEntry)
		r.Put("/entries/{type}", updateCatalogEntry)
		r.Delete("/entries/{type}", deleteCatalogEntry)
	})
}

// getCatalog handles GET /api/catalog — returns the catalog, seeding defaults on
// first access.
func getCatalog(w http.ResponseWriter, r *http.Request) {
	c, err := catalog.Load()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, c)
}

// createCatalogEntry handles POST /api/catalog/entries.
func createCatalogEntry(w http.ResponseWriter, r *http.Request) {
	var e catalog.CatalogEntry
	if !decodeJSON(w, r, &e) {
		return
	}
	e.Type = strings.TrimSpace(e.Type)
	c, err := catalog.AddEntry(e)
	if err != nil {
		respondError(w, statusForCatalogErr(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, c)
}

// updateCatalogEntry handles PUT /api/catalog/entries/{type}.
func updateCatalogEntry(w http.ResponseWriter, r *http.Request) {
	typ := urlParam(r, "type")
	var e catalog.CatalogEntry
	if !decodeJSON(w, r, &e) {
		return
	}
	e.Type = strings.TrimSpace(e.Type)
	// Allow the body to omit type (keep the path's type) for convenience.
	if e.Type == "" {
		e.Type = typ
	}
	c, err := catalog.UpdateEntry(typ, e)
	if err != nil {
		respondError(w, statusForCatalogErr(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, c)
}

// deleteCatalogEntry handles DELETE /api/catalog/entries/{type}.
func deleteCatalogEntry(w http.ResponseWriter, r *http.Request) {
	typ := urlParam(r, "type")
	c, err := catalog.DeleteEntry(typ)
	if err != nil {
		respondError(w, statusForCatalogErr(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, c)
}

// statusForCatalogErr maps catalog errors to HTTP codes: not-found → 404,
// duplicate → 409, validation → 422, everything else → 500.
func statusForCatalogErr(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "already exists"):
		return http.StatusConflict
	case strings.Contains(msg, "must be") || strings.Contains(msg, "is required"):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}
