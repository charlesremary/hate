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
	r.Post("/check-conflicts", checkScheduleConflicts)
	r.Post("/balance", balanceProject)
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
// effort-weighted percent-complete per phase. Nothing is written.
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
	effortToDays := cfg.EffortToDays
	if effortToDays == nil {
		effortToDays = ticket.DefaultEffortToDays
	}
	report := pm.PhaseRollup(tickets, effortToDays)
	respondJSON(w, http.StatusOK, report)
}

// balanceProject handles POST /api/projects/{projectId}/balance.
// Body: {"apply": bool, "author": "email"}. When apply=false (default), runs
// the algorithm and returns the proposed changes — nothing is written. When
// apply=true, writes the new dates to each affected ticket and commits the
// whole batch in one git commit.
func balanceProject(w http.ResponseWriter, r *http.Request) {
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
	if cfg.IsClosed() {
		respondError(w, http.StatusLocked, "Project is closed — reopen it to balance.")
		return
	}
	var req struct {
		Apply  bool   `json:"apply"`
		Author string `json:"author"`
	}
	// Body is optional — a bare POST runs in preview mode.
	if r.ContentLength > 0 {
		_ = decodeJSON(w, r, &req)
	}
	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	effortToDays := cfg.EffortToDays
	if effortToDays == nil {
		effortToDays = ticket.DefaultEffortToDays
	}
	// Project start: earliest existing planned_start across non-terminal
	// tickets, or today if none.
	var earliest time.Time
	for _, t := range tickets {
		if t.Status == "closed" || t.Status == "complete" {
			continue
		}
		if t.PlannedStartDate == nil || *t.PlannedStartDate == "" {
			continue
		}
		d, err := time.Parse("2006-01-02", *t.PlannedStartDate)
		if err == nil && (earliest.IsZero() || d.Before(earliest)) {
			earliest = d
		}
	}
	if earliest.IsZero() {
		earliest = time.Now().UTC()
	}
	report := pm.BalanceProject(tickets, cfg.Resources, effortToDays, earliest)

	if !req.Apply {
		respondJSON(w, http.StatusOK, report)
		return
	}
	if report.CycleDetected {
		respondError(w, http.StatusUnprocessableEntity,
			"Cannot apply: predecessor cycle detected. Resolve the cycle and re-run.")
		return
	}
	paths, err := pm.ApplyBalance(root, report, req.Author)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ticket.EnsureProjectIdentity(root, cfg)
	files := append(paths, ticket.IndexPath(root))
	ticket.GitCommit(root, files, fmt.Sprintf("balance project (%d tickets rescheduled)", report.TicketsAffected))
	respondJSON(w, http.StatusOK, report)
}

// checkScheduleConflicts handles POST /api/projects/{projectId}/check-conflicts.
// Reads the current ticket list + resources from disk and runs the capacity
// check. Does not persist anything — pure analysis.
func checkScheduleConflicts(w http.ResponseWriter, r *http.Request) {
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
	effortToDays := cfg.EffortToDays
	if effortToDays == nil {
		effortToDays = ticket.DefaultEffortToDays
	}
	report := pm.CheckScheduleConflicts(tickets, cfg.Resources, effortToDays, time.Time{})
	respondJSON(w, http.StatusOK, report)
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
	snapshot, err := pm.LoadLatestSnapshot(root)
	if err != nil || snapshot == nil {
		respondError(w, http.StatusNotFound, "No snapshot yet — create a baseline and run a snapshot first.")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-gantt.drawio"`, projectID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(pm.RenderGanttDrawio(snapshot)))
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
		var effortToDays map[string]float64
		var workHours, adminHours, qaHours *float64
		if err == nil {
			if cfg.ProjectName != "" {
				projectName = cfg.ProjectName
			}
			effortToDays = cfg.EffortToDays
			workHours = cfg.EffectiveWorkHours()
			adminHours = cfg.AdminHours
			qaHours = cfg.QAHours
		}
		reportsHTML := pm.RenderExecPlanHTML(tickets, effortToDays) +
			pm.RenderHoursBudgetHTML(pm.ComputeHoursBudget(tickets, workHours, adminHours, qaHours)) +
			pm.RenderHoursAtRiskHTML(pm.ComputeHoursAtRisk(tickets, effortToDays)) +
			pm.RenderBlockedHTML(pm.ComputeBlocked(tickets)) +
			pm.RenderTestSummaryLineHTML(pm.ComputeTestSummary(tickets)) +
			pm.RenderEstimateVarianceHTML(pm.ComputeEstimateVariance(tickets, effortToDays)) +
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
	var effortToDays map[string]float64
	var workHours, adminHours, qaHours *float64
	if cfg, err := ticket.ReadConfig(root); err == nil {
		effortToDays = cfg.EffortToDays
		workHours = cfg.EffectiveWorkHours()
		adminHours = cfg.AdminHours
		qaHours = cfg.QAHours
	}
	reportsHTML := pm.RenderExecPlanHTML(costTickets, effortToDays) +
		pm.RenderHoursBudgetHTML(pm.ComputeHoursBudget(costTickets, workHours, adminHours, qaHours)) +
		pm.RenderHoursAtRiskHTML(pm.ComputeHoursAtRisk(costTickets, effortToDays)) +
		pm.RenderBlockedHTML(pm.ComputeBlocked(costTickets)) +
		pm.RenderTestSummaryLineHTML(pm.ComputeTestSummary(costTickets)) +
		pm.RenderEstimateVarianceHTML(pm.ComputeEstimateVariance(costTickets, effortToDays)) +
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
