// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hate/internal/catalog"
	"hate/internal/config"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// RegisterCatalogRoutes registers the org-level wrap-catalog API (HATE-k0gf) and
// the org-level wrap aggregation (HATE-2b1x). Both are org-level (shared across
// projects), so they live above /api/projects/{id}.
func RegisterCatalogRoutes(r chi.Router) {
	r.Route("/api/catalog", func(r chi.Router) {
		r.Get("/", getCatalog)
		r.Post("/entries", createCatalogEntry)
		r.Put("/entries/{type}", updateCatalogEntry)
		r.Delete("/entries/{type}", deleteCatalogEntry)
	})
	r.Get("/api/wrap-aggregate", getWrapAggregate)

	r.Route("/api/profiles", func(r chi.Router) {
		r.Get("/", listProfiles)
		r.Post("/", createProfile)
		r.Get("/{name}", getProfile)
		r.Post("/{name}/recompute", recomputeProfile)
		r.Delete("/{name}", deleteProfile)
	})

	r.Post("/api/estimate", postEstimate)
}

// wrapDataForProjects builds the per-project wrap data for the given project IDs
// (platform + tickets). An empty/nil id list means every project. Returns the
// data plus the IDs actually resolved.
func wrapDataForProjects(ids []string) ([]pm.WrapProjectData, []string) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var data []pm.WrapProjectData
	var resolved []string
	for _, p := range config.ListProjects() {
		if len(want) > 0 && !want[p.ID] {
			continue
		}
		cfg, err := ticket.ReadConfig(p.Path)
		if err != nil {
			continue
		}
		tickets, err := ticket.ReadAllTickets(p.Path)
		if err != nil {
			continue
		}
		data = append(data, pm.WrapProjectData{Platform: cfg.Platform, Tickets: tickets})
		resolved = append(resolved, p.ID)
	}
	return data, resolved
}

// getWrapAggregate handles GET /api/wrap-aggregate — pools wrap-tagged tickets
// across every project into measured hours-per-unit by (platform, type).
func getWrapAggregate(w http.ResponseWriter, r *http.Request) {
	cat, err := catalog.Load()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projects, _ := wrapDataForProjects(nil)
	respondJSON(w, http.StatusOK, pm.ComputeWrapAggregate(projects, cat))
}

// ProfileRequest is the body for POST /api/profiles.
type ProfileRequest struct {
	Name           string   `json:"name"`
	SourceProjects []string `json:"source_projects"`
}

// listProfiles handles GET /api/profiles.
func listProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := pm.ListProfiles()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, profiles)
}

// getProfile handles GET /api/profiles/{name}.
func getProfile(w http.ResponseWriter, r *http.Request) {
	p, err := pm.LoadProfile(urlParam(r, "name"))
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, p)
}

// createProfile handles POST /api/profiles — pools the named source projects (or
// all projects, if none given) into a cached profile using the current code
// constant and catalog.
func createProfile(w http.ResponseWriter, r *http.Request) {
	var req ProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respondError(w, http.StatusUnprocessableEntity, "profile name is required")
		return
	}
	if pm.ProfileSlug(req.Name) == "" {
		respondError(w, http.StatusUnprocessableEntity, "profile name must contain a letter or digit")
		return
	}
	p, err := buildAndSaveProfile(req.Name, req.SourceProjects)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, p)
}

// recomputeProfile handles POST /api/profiles/{name}/recompute — rebuilds the
// profile from the current tickets + code constant, keeping its source projects.
func recomputeProfile(w http.ResponseWriter, r *http.Request) {
	name := urlParam(r, "name")
	existing, err := pm.LoadProfile(name)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	p, err := buildAndSaveProfile(existing.Name, existing.SourceProjects)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, p)
}

// deleteProfile handles DELETE /api/profiles/{name}.
func deleteProfile(w http.ResponseWriter, r *http.Request) {
	if err := pm.DeleteProfile(urlParam(r, "name")); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"deleted": urlParam(r, "name")})
}

// buildAndSaveProfile pools the source projects and persists the profile.
func buildAndSaveProfile(name string, sources []string) (pm.Profile, error) {
	cat, err := catalog.Load()
	if err != nil {
		return pm.Profile{}, err
	}
	data, resolved := wrapDataForProjects(sources)
	constant := config.LoadConfig().CodeCFPConstant
	now := time.Now().UTC().Format(time.RFC3339)
	p := pm.BuildProfile(name, resolved, data, constant, cat, now)
	if err := pm.SaveProfile(p); err != nil {
		return pm.Profile{}, err
	}
	return p, nil
}

// postEstimate handles POST /api/estimate (HATE-y1wn).
func postEstimate(w http.ResponseWriter, r *http.Request) {
	var req pm.EstimateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var prof *pm.Profile
	if strings.TrimSpace(req.Profile) != "" {
		p, err := pm.LoadProfile(req.Profile)
		if err != nil {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		prof = p
	}
	cat, err := catalog.Load()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Fall back to the live aggregate when no profile is chosen.
	var liveAgg *pm.WrapAggregate
	if prof == nil {
		data, _ := wrapDataForProjects(nil)
		agg := pm.ComputeWrapAggregate(data, cat)
		liveAgg = &agg
	}
	constant := config.LoadConfig().CodeCFPConstant
	respondJSON(w, http.StatusOK, pm.ComputeEstimate(req, prof, liveAgg, cat, constant))
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
