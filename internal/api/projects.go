// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// urlParam reads a chi URL parameter and percent-decodes it. chi matches on the
// raw (still-encoded) request path, so params like an email address with "@"
// arrive as "%40" — decode them before use.
func urlParam(r *http.Request, key string) string {
	v := chi.URLParam(r, key)
	if decoded, err := url.PathUnescape(v); err == nil {
		return decoded
	}
	return v
}

// ---------------------------------------------------------------------------
// Request body structs
// ---------------------------------------------------------------------------

// CreateProjectRequest matches the Python CreateProjectRequest model.
type CreateProjectRequest struct {
	FolderName  string `json:"folder_name"`
	Client      string `json:"client"`
	ProjectName string `json:"project_name"`
	ProjectID   string `json:"project_id"`
	Prefix      string `json:"prefix"`
}

// ResourceRequest matches the Python ResourceRequest model.
type ResourceRequest struct {
	Name                string   `json:"name"`
	Email               string   `json:"email"`
	GitUser             *string  `json:"git_user"`
	Role                *string  `json:"role"`
	DailyHoursAvailable *float64 `json:"daily_hours_available"`
}

// AppSettingsRequest matches the Python AppSettingsRequest model.
type AppSettingsRequest struct {
	ProjectsRoot *string                 `json:"projects_root"`
	Scheduler    *map[string]interface{} `json:"scheduler"`
	ShowBilling  *bool                   `json:"show_billing"`
	ShowCosmic   *bool                   `json:"show_cosmic"`
}

// GitIdentityRequest is the body for POST git-identity.
type GitIdentityRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// OpenProjectRequest is the body for POST /api/projects/open.
type OpenProjectRequest struct {
	Path string `json:"path"`
}

// ProjectPathRequest is the body for POST /api/projects/hide and /unhide.
type ProjectPathRequest struct {
	Path string `json:"path"`
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterProjectRoutes registers all project API routes on the given router.
func RegisterProjectRoutes(r chi.Router) {
	r.Route("/api/projects", func(r chi.Router) {
		r.Get("/", listProjects)
		r.Post("/", createProject)
		r.Get("/settings", getSettings)
		r.Put("/settings", updateSettings)
		r.Post("/open", openProject)
		r.Get("/hidden", listHiddenProjects)
		r.Post("/hide", hideProject)
		r.Post("/unhide", unhideProject)

		r.Route("/{projectId}", func(r chi.Router) {
			r.Get("/", getProject)
			r.Get("/sync-status", getSyncStatus)
			r.Post("/sync", syncProject)
			r.Get("/git-status", getGitStatus)
			r.Get("/git-identity", getGitIdentity)
			r.Post("/git-identity", setGitIdentity)
			r.Get("/resources", listResources)
			r.Post("/resources", addResource)
			r.Patch("/resources/{email}", updateResource)
			r.Delete("/resources/{email}", removeResource)
			r.Get("/whoami", whoami)
			r.Get("/effort-to-days", getEffortToDays)
			r.Put("/effort-to-days", updateEffortToDays)
			r.Get("/hour-budget", getHourBudget)
			r.Put("/hour-budget", updateHourBudget)
			r.Get("/strict-time", getStrictTime)
			r.Put("/strict-time", updateStrictTime)
			r.Get("/enforce-qa", getEnforceQA)
			r.Put("/enforce-qa", updateEnforceQA)
			r.Get("/overview", getOverview)
			r.Put("/overview", updateOverview)
			r.Post("/close", closeProject)
			r.Post("/reopen", reopenProject)
			r.Patch("/info", updateProjectInfo)

			// PM routes (co-located under {projectId})
			RegisterPMSubRoutes(r)
		})
	})
}

// ---------------------------------------------------------------------------
// Helper: enrich project with ticket_count, health, resource_count
// ---------------------------------------------------------------------------

func projectSummary(project map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range project {
		result[k] = v
	}
	result["ticket_count"] = float64(0)
	result["health"] = nil
	result["resource_count"] = float64(0)
	result["closed_at"] = ""

	path, _ := project["path"].(string)
	if path == "" {
		return result
	}

	// Ticket count from index.json
	idxPath := filepath.Join(path, "index.json")
	if data, err := os.ReadFile(idxPath); err == nil {
		var idx map[string]interface{}
		if json.Unmarshal(data, &idx) == nil {
			if tc, ok := idx["ticket_count"].(float64); ok {
				result["ticket_count"] = tc
			}
		}
	}

	// Health from latest snapshot
	snapshot, err := pm.LoadLatestSnapshot(path)
	if err == nil && snapshot != nil {
		result["health"] = snapshot.ComputedHealth
	}

	// Resource count + closed_at from .tkt/config.json
	tktCfgPath := filepath.Join(path, ".tkt", "config.json")
	if data, err := os.ReadFile(tktCfgPath); err == nil {
		var cfg map[string]interface{}
		if json.Unmarshal(data, &cfg) == nil {
			if resources, ok := cfg["resources"].([]interface{}); ok {
				result["resource_count"] = float64(len(resources))
			}
			if ca, ok := cfg["closed_at"].(string); ok {
				result["closed_at"] = ca
			}
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Handler implementations
// ---------------------------------------------------------------------------

// listProjects handles GET /api/projects
func listProjects(w http.ResponseWriter, r *http.Request) {
	projects := config.ListProjects()
	result := make([]interface{}, 0, len(projects))
	for _, p := range projects {
		pMap := map[string]interface{}{
			"id":     p.ID,
			"name":   p.Name,
			"client": p.Client,
			"prefix": p.Prefix,
			"path":   p.Path,
		}
		result = append(result, projectSummary(pMap))
	}
	respondJSON(w, http.StatusOK, result)
}

// createProject handles POST /api/projects
func createProject(w http.ResponseWriter, r *http.Request) {
	var req CreateProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Prefix == "" {
		req.Prefix = "TKT"
	}

	root := config.GetProjectsRoot()
	projectDir := filepath.Join(root, req.FolderName)

	if _, err := os.Stat(projectDir); err == nil {
		respondError(w, http.StatusConflict, "Directory already exists: "+req.FolderName)
		return
	}

	// Create structure
	tktDir := filepath.Join(projectDir, ".tkt")
	ticketsDir := filepath.Join(projectDir, "tickets")
	if err := os.MkdirAll(tktDir, 0o755); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.MkdirAll(ticketsDir, 0o755); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Write config
	cfg := ticket.DefaultConfig(req.Client, req.ProjectName, req.ProjectID, req.Prefix)
	if err := ticket.WriteConfig(projectDir, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Empty index
	if err := ticket.RegenerateIndex(projectDir); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"id":           req.ProjectID,
		"name":         req.ProjectName,
		"prefix":       req.Prefix,
		"path":         projectDir,
		"ticket_count": float64(0),
		"health":       nil,
	})
}

// openProject handles POST /api/projects/open. It registers an existing project
// folder (one containing .tkt/config.json) that may live outside the projects
// root — e.g. a repo a teammate cloned to an arbitrary location on disk.
func openProject(w http.ResponseWriter, r *http.Request) {
	var req OpenProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	path := strings.TrimSpace(req.Path)
	if path == "" {
		respondError(w, http.StatusUnprocessableEntity, "Project folder path is required.")
		return
	}

	// Expand a leading ~ and resolve to an absolute, clean path.
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path[1:], "/"))
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, "Invalid path: "+err.Error())
		return
	}
	abs = filepath.Clean(abs)

	// Must be a directory containing .tkt/config.json.
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		respondError(w, http.StatusNotFound, "Folder not found: "+abs)
		return
	}
	if _, err := os.Stat(filepath.Join(abs, ".tkt", "config.json")); err != nil {
		respondError(w, http.StatusUnprocessableEntity, "Not a tkt project — no .tkt/config.json in "+abs)
		return
	}

	if err := config.AddExtraProject(abs); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return the project summary so the UI can select it immediately.
	for _, p := range config.ListProjects() {
		if p.Path == abs {
			respondJSON(w, http.StatusOK, projectSummary(map[string]interface{}{
				"id":     p.ID,
				"name":   p.Name,
				"client": p.Client,
				"prefix": p.Prefix,
				"path":   p.Path,
			}))
			return
		}
	}
	respondError(w, http.StatusInternalServerError, "Project registered but could not be loaded.")
}

// listHiddenProjects handles GET /api/projects/hidden — projects the user has
// removed from tracking. They still exist on disk, just hidden from the UI.
func listHiddenProjects(w http.ResponseWriter, r *http.Request) {
	hidden := config.ListHiddenProjects()
	result := make([]interface{}, 0, len(hidden))
	for _, p := range hidden {
		result = append(result, map[string]interface{}{
			"id":     p.ID,
			"name":   p.Name,
			"client": p.Client,
			"prefix": p.Prefix,
			"path":   p.Path,
		})
	}
	respondJSON(w, http.StatusOK, result)
}

// hideProject handles POST /api/projects/hide — removes a project from the UI.
// The project folder is left untouched on disk.
func hideProject(w http.ResponseWriter, r *http.Request) {
	var req ProjectPathRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		respondError(w, http.StatusUnprocessableEntity, "Project path is required.")
		return
	}
	if err := config.HideProject(path); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"hidden": filepath.Clean(path)})
}

// unhideProject handles POST /api/projects/unhide — restores a hidden project.
func unhideProject(w http.ResponseWriter, r *http.Request) {
	var req ProjectPathRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		respondError(w, http.StatusUnprocessableEntity, "Project path is required.")
		return
	}
	if err := config.UnhideProject(path); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"restored": filepath.Clean(path)})
}

// getSettings handles GET /api/projects/settings
func getSettings(w http.ResponseWriter, r *http.Request) {
	cfg := config.LoadConfig()
	respondJSON(w, http.StatusOK, cfg)
}

// updateSettings handles PUT /api/projects/settings
func updateSettings(w http.ResponseWriter, r *http.Request) {
	var req AppSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg := config.LoadConfig()
	if req.ProjectsRoot != nil {
		cfg.ProjectsRoot = *req.ProjectsRoot
	}
	if req.ShowBilling != nil {
		cfg.ShowBilling = *req.ShowBilling
	}
	if req.ShowCosmic != nil {
		cfg.ShowCosmic = *req.ShowCosmic
	}
	if req.Scheduler != nil {
		sched := *req.Scheduler
		if enabled, ok := sched["enabled"].(bool); ok {
			cfg.Scheduler.Enabled = enabled
		}
		if interval, ok := sched["interval_hours"].(float64); ok {
			cfg.Scheduler.IntervalHours = int(interval)
		}
		if projects, ok := sched["projects"]; ok {
			cfg.Scheduler.Projects = projects
		}
	}
	if err := config.SaveConfig(cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, cfg)
}

// getProject handles GET /api/projects/{projectId}
func getProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	projects := config.ListProjects()
	for _, p := range projects {
		if p.ID == projectID {
			pMap := map[string]interface{}{
				"id":     p.ID,
				"name":   p.Name,
				"prefix": p.Prefix,
				"path":   p.Path,
			}
			respondJSON(w, http.StatusOK, projectSummary(pMap))
			return
		}
	}
	respondError(w, http.StatusNotFound, "Project not found: "+projectID)
}

// getSyncStatus handles GET /api/projects/{projectId}/sync-status
func getSyncStatus(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, ticket.GitFetchStatus(root))
}

// syncProject handles POST /api/projects/{projectId}/sync
func syncProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err == nil {
		ticket.EnsureProjectIdentity(root, cfg)
	}

	result := ticket.GitSync(root)
	if success, _ := result["success"].(bool); !success {
		msg, _ := result["message"].(string)
		if msg == "" {
			msg = "Sync failed"
		}
		respondError(w, http.StatusConflict, msg)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// getGitStatus handles GET /api/projects/{projectId}/git-status
func getGitStatus(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, ticket.GitStatus(root))
}

// getGitIdentity handles GET /api/projects/{projectId}/git-identity
func getGitIdentity(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"configured": cfg.GitIdentityV,
		"current":    ticket.GitUserIdentity(root),
	})
}

// setGitIdentity handles POST /api/projects/{projectId}/git-identity
func setGitIdentity(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req GitIdentityRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Name)
	email := strings.TrimSpace(req.Email)
	if name == "" || email == "" {
		respondError(w, http.StatusUnprocessableEntity, "Both name and email are required.")
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cfg.GitIdentityV = &ticket.GitIdentity{
		Name:  name,
		Email: email,
	}
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Apply immediately
	ticket.SetGitIdentity(root, name, email)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"name":  name,
		"email": email,
	})
}

// listResources handles GET /api/projects/{projectId}/resources
func listResources(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resources := cfg.Resources
	if resources == nil {
		resources = []ticket.Resource{}
	}
	respondJSON(w, http.StatusOK, resources)
}

// addResource handles POST /api/projects/{projectId}/resources
func addResource(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req ResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Check for duplicate email
	for _, res := range cfg.Resources {
		if res.Email == req.Email {
			respondError(w, http.StatusConflict, "Resource already exists: "+req.Email)
			return
		}
	}

	gitUser := ""
	if req.GitUser != nil {
		gitUser = *req.GitUser
	}
	role := "developer"
	if req.Role != nil && *req.Role != "" {
		role = *req.Role
	}

	cfg.Resources = append(cfg.Resources, ticket.Resource{
		Name:                req.Name,
		Email:               req.Email,
		GitUser:             gitUser,
		Role:                role,
		DailyHoursAvailable: req.DailyHoursAvailable,
	})

	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, cfg.Resources)
}

// updateResource handles PATCH /api/projects/{projectId}/resources/{email}.
// The {email} path param identifies the existing resource; the body carries the
// new values (the email itself may change).
func updateResource(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	email := urlParam(r, "email")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req ResourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	idx := -1
	for i, res := range cfg.Resources {
		if res.Email == email {
			idx = i
			break
		}
	}
	if idx == -1 {
		respondError(w, http.StatusNotFound, "Resource not found: "+email)
		return
	}

	// If the email is changing, make sure the new one isn't already taken.
	if req.Email != email {
		for i, res := range cfg.Resources {
			if i != idx && res.Email == req.Email {
				respondError(w, http.StatusConflict, "Resource already exists: "+req.Email)
				return
			}
		}
	}

	gitUser := ""
	if req.GitUser != nil {
		gitUser = *req.GitUser
	}
	role := "developer"
	if req.Role != nil && *req.Role != "" {
		role = *req.Role
	}

	// Preserve the existing daily_hours_available unless the caller is
	// explicitly setting it (so name/role edits don't blow away the field).
	dailyHours := cfg.Resources[idx].DailyHoursAvailable
	if req.DailyHoursAvailable != nil {
		dailyHours = req.DailyHoursAvailable
	}
	cfg.Resources[idx] = ticket.Resource{
		Name:                req.Name,
		Email:               req.Email,
		GitUser:             gitUser,
		Role:                role,
		DailyHoursAvailable: dailyHours,
	}

	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, cfg.Resources)
}

// removeResource handles DELETE /api/projects/{projectId}/resources/{email}
func removeResource(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	email := urlParam(r, "email")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var filtered []ticket.Resource
	found := false
	for _, res := range cfg.Resources {
		if res.Email == email {
			found = true
			continue
		}
		filtered = append(filtered, res)
	}

	if !found {
		respondError(w, http.StatusNotFound, "Resource not found: "+email)
		return
	}

	if filtered == nil {
		filtered = []ticket.Resource{}
	}
	cfg.Resources = filtered
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, filtered)
}

// whoami handles GET /api/projects/{projectId}/whoami
func whoami(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Prefer the project's configured git identity over the live git config,
	// matching how the "Working as" banner resolves identity. Otherwise a repo
	// with no local git identity falls through to the global config, which may
	// be a different person entirely.
	identity := ticket.GitUserIdentity(root)
	if cfg.GitIdentityV != nil && cfg.GitIdentityV.Email != "" {
		identity = map[string]string{
			"name":  cfg.GitIdentityV.Name,
			"email": cfg.GitIdentityV.Email,
		}
	}

	identityEmail := identity["email"]
	identityName := identity["name"]

	var matched *ticket.Resource
	for _, res := range cfg.Resources {
		if res.Email == identityEmail || res.GitUser == identityName {
			r := res
			matched = &r
			break
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"git_identity": identity,
		"resource":     matched,
	})
}

// validEffortSizes is the canonical t-shirt size set the UI exposes.
var validEffortSizes = map[string]bool{"xs": true, "s": true, "m": true, "l": true, "xl": true}

// getEffortToDays handles GET /api/projects/{projectId}/effort-to-days.
// Returns the project's effort_to_days map, falling back to defaults for any
// missing keys so the UI always renders a value for every size.
func getEffortToDays(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make(map[string]float64, len(ticket.DefaultEffortToDays))
	for k, v := range ticket.DefaultEffortToDays {
		out[k] = v
	}
	for k, v := range cfg.EffortToDays {
		if validEffortSizes[k] {
			out[k] = v
		}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"effort_to_days": out,
		"defaults":       ticket.DefaultEffortToDays,
	})
}

// updateEffortToDays handles PUT /api/projects/{projectId}/effort-to-days.
// Only the canonical sizes (xs, s, m, l, xl) are accepted; values must be > 0.
func updateEffortToDays(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		EffortToDays map[string]float64 `json:"effort_to_days"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.EffortToDays) == 0 {
		respondError(w, http.StatusBadRequest, "effort_to_days is required")
		return
	}
	cleaned := make(map[string]float64, len(req.EffortToDays))
	for k, v := range req.EffortToDays {
		key := strings.ToLower(strings.TrimSpace(k))
		if !validEffortSizes[key] {
			respondError(w, http.StatusBadRequest, "unknown effort size: "+k)
			return
		}
		if v < 0.25 {
			respondError(w, http.StatusBadRequest, "value for "+key+" must be >= 0.25")
			return
		}
		// Snap to the nearest quarter-day so stored values stay on the grid
		// regardless of floating-point jitter from the client.
		cleaned[key] = math.Round(v*4) / 4
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.EffortToDays = cleaned
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"effort_to_days": cleaned,
		"defaults":       ticket.DefaultEffortToDays,
	})
}

// getHourBudget handles GET /api/projects/{projectId}/hour-budget.
// Returns the two hour pools (null when unset); work_hours migrates a legacy
// max_hours value so old projects keep their cap.
func getHourBudget(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"work_hours":  cfg.EffectiveWorkHours(),
		"admin_hours": cfg.AdminHours,
		"qa_hours":    cfg.QAHours,
	})
}

// updateHourBudget handles PUT /api/projects/{projectId}/hour-budget.
// Body: {"work_hours": <number|null>, "admin_hours": <number|null>}. A positive
// number sets a pool; null clears it; ≤ 0 is rejected. Saving clears any legacy
// max_hours so it stops shadowing the new fields.
func updateHourBudget(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		WorkHours  *float64 `json:"work_hours"`
		AdminHours *float64 `json:"admin_hours"`
		QAHours    *float64 `json:"qa_hours"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	for name, v := range map[string]*float64{"work_hours": req.WorkHours, "admin_hours": req.AdminHours, "qa_hours": req.QAHours} {
		if v != nil && *v <= 0 {
			respondError(w, http.StatusBadRequest, name+" must be greater than 0 (or null to clear)")
			return
		}
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.WorkHours = req.WorkHours
	cfg.AdminHours = req.AdminHours
	cfg.QAHours = req.QAHours
	cfg.MaxHours = nil // migrated into WorkHours; stop shadowing
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"work_hours":  cfg.WorkHours,
		"admin_hours": cfg.AdminHours,
		"qa_hours":    cfg.QAHours,
	})
}

// getStrictTime handles GET /api/projects/{projectId}/strict-time.
func getStrictTime(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"strict_time_enforcement": cfg.StrictTimeEnforcement})
}

// updateStrictTime handles PUT /api/projects/{projectId}/strict-time.
// Body: {"strict_time_enforcement": bool}.
func updateStrictTime(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		StrictTimeEnforcement bool `json:"strict_time_enforcement"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.StrictTimeEnforcement = req.StrictTimeEnforcement
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"strict_time_enforcement": cfg.StrictTimeEnforcement})
}

// getEnforceQA handles GET /api/projects/{projectId}/enforce-qa.
func getEnforceQA(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"enforce_qa": cfg.EnforceQA})
}

// updateEnforceQA handles PUT /api/projects/{projectId}/enforce-qa.
// Body: {"enforce_qa": bool}.
func updateEnforceQA(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		EnforceQA bool `json:"enforce_qa"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.EnforceQA = req.EnforceQA
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"enforce_qa": cfg.EnforceQA})
}

// updateProjectInfo handles PATCH /api/projects/{projectId}/info. Currently
// only the display name is editable — project_id, prefix, folder name, and
// ticket IDs are immutable by design.
func updateProjectInfo(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		ProjectName *string `json:"project_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.IsClosed() {
		respondError(w, http.StatusLocked, "Project is closed — reopen it to make changes.")
		return
	}
	if req.ProjectName != nil {
		name := strings.TrimSpace(*req.ProjectName)
		if name == "" {
			respondError(w, http.StatusBadRequest, "project_name cannot be empty")
			return
		}
		if len(name) > 120 {
			respondError(w, http.StatusBadRequest, "project_name is too long (max 120 chars)")
			return
		}
		cfg.ProjectName = name
	}
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ticket.EnsureProjectIdentity(root, cfg)
	ticket.GitCommit(root, []string{ticket.ConfigPath(root)}, "rename project: "+cfg.ProjectName)
	respondJSON(w, http.StatusOK, cfg)
}

// closeProject handles POST /api/projects/{projectId}/close. Stamps
// closed_at = today (UTC date) in the project config. No-op if already closed.
func closeProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.ClosedAt == "" {
		cfg.ClosedAt = time.Now().UTC().Format("2006-01-02")
		if err := ticket.WriteConfig(root, cfg); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"closed_at": cfg.ClosedAt})
}

// reopenProject handles POST /api/projects/{projectId}/reopen. Clears
// closed_at. No-op if already open.
func reopenProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.ClosedAt != "" {
		cfg.ClosedAt = ""
		if err := ticket.WriteConfig(root, cfg); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"closed_at": ""})
}
