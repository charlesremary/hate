// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// ---------------------------------------------------------------------------
// Request body structs
// ---------------------------------------------------------------------------

// BaselineRequest matches the Python BaselineRequest model.
type BaselineRequest struct {
	ProjectName         string                 `json:"project_name"`
	TemplateID          string                 `json:"template_id"`
	StartDate           string                 `json:"start_date"`
	OwnerAssignments    map[string]interface{} `json:"owner_assignments"`
	DurationAdjustments map[string]interface{} `json:"duration_adjustments"`
	CreatedBy           *string                `json:"created_by"`
}

// SlipResolveRequest matches the Python SlipResolveRequest model.
type SlipResolveRequest struct {
	ReasonCategory  string  `json:"reason_category"`
	ReasonNarrative string  `json:"reason_narrative"`
	AcknowledgedBy  *string `json:"acknowledged_by"`
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterPMRoutes is kept for main.go compat but is now a no-op.
// PM routes are registered via RegisterPMSubRoutes inside the project route tree.
func RegisterPMRoutes(r chi.Router) {}

// RegisterPMSubRoutes registers PM routes inside a {projectId} sub-router.
func RegisterPMSubRoutes(r chi.Router) {
	r.Get("/snapshot", getSnapshot)
	r.Post("/snapshot", createSnapshot)
	r.Get("/dashboard", getDashboard)
	r.Get("/gantt.drawio", getGanttDrawio)
	r.Post("/baseline", createBaseline)
	r.Post("/baseline-now", baselineFromTickets)
	r.Post("/report", generateReport)
	r.Get("/slip", listSlipEvents)
	r.Patch("/slip/{slipEventId}", resolveSlip)
	r.Get("/target-date", getTargetDate)
	r.Put("/target-date", updateTargetDate)
	r.Get("/phase-rollup", getPhaseRollup)
	r.Get("/test-summary", getTestSummary)
	r.Get("/cosmic", getCosmic)
	r.Put("/cosmic-estimate", updateCosmicEstimate)
}

// getTestSummary handles GET /api/projects/{projectId}/test-summary.
// Per-ticket QA test-case tallies plus the cases themselves, for the Test cases tab.
func getTestSummary(w http.ResponseWriter, r *http.Request) {
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
	respondJSON(w, http.StatusOK, pm.ComputeTestSummary(tickets))
}

// getPhaseRollup handles GET /api/projects/{projectId}/phase-rollup.
// Pure analysis: groups the current tickets by phase and returns an
// hours-weighted percent-complete per phase. Nothing is written.
func getPhaseRollup(w http.ResponseWriter, r *http.Request) {
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
	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	report := pm.PhaseRollup(tickets, pm.ProjectEstimateContext(root, tickets, cfg))
	respondJSON(w, http.StatusOK, report)
}

// getTargetDate handles GET /api/projects/{projectId}/target-date.
// Returns {"target_date": "YYYY-MM-DD"|null}.
func getTargetDate(w http.ResponseWriter, r *http.Request) {
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
	respondJSON(w, http.StatusOK, map[string]interface{}{"target_date": targetDateJSON(cfg.TargetDate)})
}

// updateTargetDate handles PUT /api/projects/{projectId}/target-date.
// Body: {"target_date": "YYYY-MM-DD"|null}. null (or "") clears it; any other
// value must be a valid date (400). Persists and commits the config when it
// changes.
func updateTargetDate(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		TargetDate *string `json:"target_date"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	target := ""
	if req.TargetDate != nil {
		target = strings.TrimSpace(*req.TargetDate)
	}
	if target != "" {
		if _, err := time.Parse("2006-01-02", target); err != nil {
			respondError(w, http.StatusBadRequest, "target_date must be a date (YYYY-MM-DD) or null to clear")
			return
		}
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.TargetDate != target {
		cfg.TargetDate = target
		if err := ticket.WriteConfig(root, cfg); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		ticket.EnsureProjectIdentity(root, cfg)
		msg := "target date " + target
		if target == "" {
			msg = "clear target date"
		}
		ticket.GitCommit(root, []string{ticket.ConfigPath(root)}, msg)
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"target_date": targetDateJSON(cfg.TargetDate)})
}

// targetDateJSON maps an unset target date to JSON null.
func targetDateJSON(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ---------------------------------------------------------------------------
// Handler implementations
// ---------------------------------------------------------------------------

// getSnapshot handles GET /api/projects/{projectId}/snapshot
func getSnapshot(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	snapshot, err := pm.LoadLatestSnapshot(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if snapshot == nil {
		respondError(w, http.StatusNotFound, "No snapshots yet. Run POST /snapshot first.")
		return
	}
	respondJSON(w, http.StatusOK, snapshot)
}

// createSnapshot handles POST /api/projects/{projectId}/snapshot
func createSnapshot(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	snapshot, err := pm.RunSnapshot(projectID, root)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "No baseline") {
			respondError(w, http.StatusNotFound, err.Error())
		} else {
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, snapshot)
}

// getGanttDrawio handles GET /api/projects/{projectId}/gantt.drawio — the
// baselined Gantt exported as an editable draw.io file (download).
func getGanttDrawio(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	// Baselined snapshot if there is one; otherwise the floating projected
	// schedule from ?start= (default today), so export works pre-baseline too.
	snapshot, err := pm.LoadLatestSnapshot(root)
	if err != nil || snapshot == nil {
		tickets, terr := ticket.ReadAllTickets(root)
		if terr != nil {
			respondError(w, http.StatusInternalServerError, terr.Error())
			return
		}
		projectName := projectID
		cfg, cerr := ticket.ReadConfig(root)
		var resources []ticket.Resource
		if cerr == nil {
			if cfg.ProjectName != "" {
				projectName = cfg.ProjectName
			}
			resources = cfg.Resources
		} else {
			cfg = nil
		}
		estCtx := pm.ProjectEstimateContext(root, tickets, cfg)
		snapshot, _ = pm.ProjectSchedule(projectID, projectName, tickets, resources, estCtx, ganttStart(r))
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-gantt.drawio"`, projectID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(pm.RenderGanttDrawio(snapshot)))
}

// ganttStart resolves the projection start date from ?start=YYYY-MM-DD,
// defaulting to today.
func ganttStart(r *http.Request) time.Time {
	if s := r.URL.Query().Get("start"); s != "" {
		if d, err := time.Parse("2006-01-02", s); err == nil {
			return d
		}
	}
	return time.Now()
}

// getDashboard handles GET /api/projects/{projectId}/dashboard
func getDashboard(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	// If no baseline exists, show the pre-baseline simple dashboard
	if !pm.BaselineExists(root) {
		tickets, err := ticket.ReadAllTickets(root)
		if err != nil {
			tickets = []*ticket.Ticket{}
		}
		cfg, err := ticket.ReadConfig(root)
		projectName := projectID
		var resources []ticket.Resource
		var workHours, adminHours, qaHours *float64
		targetDate := ""
		if err == nil {
			if cfg.ProjectName != "" {
				projectName = cfg.ProjectName
			}
			resources = cfg.Resources
			targetDate = cfg.TargetDate
			workHours = cfg.EffectiveWorkHours()
			adminHours = cfg.AdminHours
			qaHours = cfg.QAHours
		} else {
			cfg = nil
		}
		estCtx := pm.ProjectEstimateContext(root, tickets, cfg)
		start := ganttStart(r)
		exportURL := fmt.Sprintf("/api/projects/%s/gantt.drawio", projectID)
		if s := r.URL.Query().Get("start"); s != "" {
			exportURL += "?start=" + s
		}
		reportsHTML := pm.RenderProjectedGanttHTML(projectID, projectName, tickets, resources, estCtx, start, exportURL) +
			pm.RenderExecPlanHTML(tickets, resources, estCtx) +
			pm.RenderLoadHTML(pm.ComputeLoad(tickets, resources, estCtx, time.Now(), targetDate)) +
			pm.RenderHoursBudgetHTML(pm.ComputeHoursBudget(tickets, workHours, adminHours, qaHours)) +
			pm.RenderHoursAtRiskHTML(pm.ComputeHoursAtRisk(tickets, estCtx)) +
			pm.RenderBlockedHTML(pm.ComputeBlocked(tickets)) +
			pm.RenderTestSummaryLineHTML(pm.ComputeTestSummary(tickets)) +
			pm.RenderEstimateVarianceHTML(pm.ComputeEstimateVariance(tickets, estCtx)) +
			pm.RenderOverridesHTML(pm.ComputeOverrides(tickets)) +
			pm.RenderProjectCostHTML(pm.ComputeProjectCost(tickets))
		html := pm.GenerateSimpleDashboard(tickets, projectID, projectName, reportsHTML)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(html))
		return
	}

	snapshot, err := pm.LoadLatestSnapshot(root)
	if err != nil || snapshot == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		html := "<html><body><p>No snapshot yet. " +
			"<a href='/api/projects/" + projectID + "/snapshot' " +
			"onclick=\"fetch(this.href,{method:'POST'});return false;\">Run snapshot</a></p></body></html>"
		w.Write([]byte(html))
		return
	}

	costTickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		costTickets = []*ticket.Ticket{}
	}
	var resources []ticket.Resource
	var workHours, adminHours, qaHours *float64
	targetDate := ""
	cfg, cfgErr := ticket.ReadConfig(root)
	if cfgErr == nil {
		resources = cfg.Resources
		targetDate = cfg.TargetDate
		workHours = cfg.EffectiveWorkHours()
		adminHours = cfg.AdminHours
		qaHours = cfg.QAHours
	} else {
		cfg = nil
	}
	estCtx := pm.ProjectEstimateContext(root, costTickets, cfg)
	reportsHTML := pm.RenderExecPlanHTML(costTickets, resources, estCtx) +
		pm.RenderLoadHTML(pm.ComputeLoad(costTickets, resources, estCtx, time.Now(), targetDate)) +
		pm.RenderHoursBudgetHTML(pm.ComputeHoursBudget(costTickets, workHours, adminHours, qaHours)) +
		pm.RenderHoursAtRiskHTML(pm.ComputeHoursAtRisk(costTickets, estCtx)) +
		pm.RenderBlockedHTML(pm.ComputeBlocked(costTickets)) +
		pm.RenderTestSummaryLineHTML(pm.ComputeTestSummary(costTickets)) +
		pm.RenderEstimateVarianceHTML(pm.ComputeEstimateVariance(costTickets, estCtx)) +
		pm.RenderOverridesHTML(pm.ComputeOverrides(costTickets)) +
		pm.RenderProjectCostHTML(pm.ComputeProjectCost(costTickets))
	html := pm.GenerateDashboard(snapshot, reportsHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

// createBaseline handles POST /api/projects/{projectId}/baseline
func createBaseline(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	// Guard: baseline is immutable
	if pm.BaselineExists(root) {
		respondError(w, http.StatusConflict, "Baseline already exists. Cannot re-baseline.")
		return
	}

	var req BaselineRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, "Invalid start_date. Expected YYYY-MM-DD.")
		return
	}

	createdBy := "pm@company.com"
	if req.CreatedBy != nil && *req.CreatedBy != "" {
		createdBy = *req.CreatedBy
	}

	// Get template directory from config or use default
	templateDir := "templates"

	params := pm.KickoffParams{
		ProjectID:           projectID,
		ProjectName:         req.ProjectName,
		TemplateID:          req.TemplateID,
		StartDate:           startDate,
		OwnerAssignments:    req.OwnerAssignments,
		DurationAdjustments: req.DurationAdjustments,
		CreatedBy:           createdBy,
	}

	baseline, err := pm.RunWBS(params, root, templateDir)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, err.Error())
		} else {
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, baseline)
}

// baselineFromTickets handles POST /api/projects/{projectId}/baseline-now
func baselineFromTickets(w http.ResponseWriter, r *http.Request) {
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

	projectName := projectID
	if cfg.ProjectName != "" {
		projectName = cfg.ProjectName
	}

	baseline, err := pm.CreateBaselineFromTickets(root, projectID, projectName, "")
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			respondError(w, http.StatusConflict, err.Error())
		} else {
			respondError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, baseline)
}

// generateReport handles POST /api/projects/{projectId}/report
func generateReport(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "Reports not yet implemented in Go version")
}

// listSlipEvents handles GET /api/projects/{projectId}/slip
func listSlipEvents(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	events, err := pm.ReadSlipEvents(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, events)
}

// resolveSlip handles PATCH /api/projects/{projectId}/slip/{slipEventId}
func resolveSlip(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	slipEventID := chi.URLParam(r, "slipEventId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req SlipResolveRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if !pm.IsValidSlipCategory(req.ReasonCategory) {
		respondError(w, http.StatusUnprocessableEntity,
			"Invalid reason_category. Must be one of: "+strings.Join(pm.ValidSlipCategories, ", "))
		return
	}

	events, err := pm.ReadSlipEvents(root)
	if err != nil {
		respondError(w, http.StatusNotFound, "No slip events file found.")
		return
	}
	if len(events) == 0 {
		respondError(w, http.StatusNotFound, "No slip events file found.")
		return
	}

	acknowledgedBy := "api-user"
	if req.AcknowledgedBy != nil && *req.AcknowledgedBy != "" {
		acknowledgedBy = *req.AcknowledgedBy
	}

	found := false
	todayStr := time.Now().Format("2006-01-02")
	for i := range events {
		if events[i].SlipEventID == slipEventID {
			events[i].ReasonCategory = &req.ReasonCategory
			events[i].ReasonNarrative = &req.ReasonNarrative
			events[i].Status = "resolved"
			events[i].AcknowledgedBy = &acknowledgedBy
			events[i].AcknowledgedDate = &todayStr
			found = true
			break
		}
	}

	if !found {
		respondError(w, http.StatusNotFound, "Slip event not found: "+slipEventID)
		return
	}

	if err := pm.WriteSlipEvents(root, events); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"updated": slipEventID,
		"status":  "resolved",
	})
}
