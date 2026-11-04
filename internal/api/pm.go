// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	r.Get("/export.xlsx", getExportXLSX)
	r.Head("/export.xlsx", getExportXLSX) // curl -I; net/http drops the body
	r.Post("/baseline", createBaseline)
	r.Post("/baseline-now", baselineFromTickets)
	r.Post("/rebaseline", rebaseline)
	r.Get("/baselines", listBaselines)
	r.Post("/report", generateReport)
	r.Get("/slip", listSlipEvents)
	r.Patch("/slip/{slipEventId}", resolveSlip)
	r.Get("/requested-dates", getRequestedDates)
	r.Put("/requested-dates", updateRequestedDates)
	r.Get("/target-date", getTargetDate) // alias for the requested end
	r.Put("/target-date", updateTargetDate)
	r.Get("/forecast", getForecast)
	r.Get("/rollup", getRollup)
	r.Get("/stats", getStats)
	r.Get("/block-weeks", getBlockWeeks)
	r.Put("/block-weeks", updateBlockWeeks)
	r.Get("/blocks", getBlocks)
	r.Get("/ready", getReady)
	r.Get("/hours-budget", getHoursBudget)
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

// getRollup handles GET /api/projects/{projectId}/rollup?by=lifecycle|block.
// Pure analysis: groups the current tickets by lifecycle or planning block and
// returns an hours-weighted percent-complete per group (blocks in order, with
// their dates). by defaults to block when the project plans in blocks, else
// lifecycle. Nothing is written.
func getRollup(w http.ResponseWriter, r *http.Request) {
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
	estCtx := pm.ProjectEstimateContext(root, tickets, cfg)
	by := r.URL.Query().Get("by")
	if by == "" {
		by = pm.RollupByLifecycle
		if cfg.BlockWeeks == 2 || cfg.BlockWeeks == 3 {
			by = pm.RollupByBlock
		}
	}
	report := pm.Rollup(tickets, estCtx, by)
	pm.ApplyBlockDates(&report, pm.BlocksForProject(cfg, tickets, estCtx, time.Now()))
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

// createSnapshot handles POST /api/projects/{projectId}/snapshot. Commits
// slip_events.json when the snapshot detected new slips.
func createSnapshot(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	snapshot, warn, err := pm.TakeSnapshot(projectID, root)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "No baseline") {
			respondError(w, http.StatusNotFound, err.Error())
		} else {
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, struct {
		*pm.Snapshot
		CommitWarning string `json:"commit_warning,omitempty"`
	}{snapshot, warn})
}

// getGanttDrawio handles GET /api/projects/{projectId}/gantt.drawio — the
// baselined Gantt exported as an editable draw.io file (download).
func getGanttDrawio(w http.ResponseWriter, r *http.Request) {
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
	snapshot, blocks, _ := ganttSchedule(r, projectID, root, tickets)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-gantt.drawio"`, projectID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(pm.RenderGanttDrawio(snapshot, blocks)))
}

// ganttSchedule returns the schedule the Gantt exports chart (draw.io and the
// Excel Gantt sheet): the latest baselined snapshot if there is one,
// otherwise the floating projected schedule from ?start= (default today), so
// export works pre-baseline too. Also the planning blocks and a note naming
// the schedule ("Baseline of <date>" / "Projected from <start>, not
// baselined").
func ganttSchedule(r *http.Request, projectID, root string, tickets []*ticket.Ticket) (*pm.Snapshot, []pm.Block, string) {
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
	blocks := pm.BlocksForProject(cfg, tickets, estCtx, time.Now())
	snapshot, err := pm.LoadLatestSnapshot(root)
	if err == nil && snapshot != nil {
		date := pm.ReadPlanStatus(root).BaselineDate
		if date == "" {
			date = snapshot.SnapshotDate
		}
		if len(date) > 10 {
			date = date[:10]
		}
		return snapshot, blocks, "Baseline of " + date
	}
	start := ganttStart(r)
	snapshot, _ = pm.ProjectSchedule(projectID, projectName, tickets, resources, estCtx, start)
	return snapshot, blocks, "Projected from " + start.Format("2006-01-02") + ", not baselined"
}

// getExportXLSX handles GET /api/projects/{projectId}/export.xlsx — the
// project as an Excel workbook (download): a Tickets sheet with every ticket
// and a Gantt sheet charting the same schedule as gantt.drawio.
func getExportXLSX(w http.ResponseWriter, r *http.Request) {
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
	cfg, cerr := ticket.ReadConfig(root)
	if cerr != nil {
		cfg = nil
	}
	estCtx := pm.ProjectEstimateContext(root, tickets, cfg)
	snapshot, blocks, note := ganttSchedule(r, projectID, root, tickets)
	data, err := pm.ProjectWorkbook(tickets, estCtx, snapshot, blocks, note)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.xlsx"`, projectID, time.Now().Format("2006-01-02")))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
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

// getDashboard handles GET /api/projects/{projectId}/dashboard. With a
// baseline and no snapshot for today, it takes today's snapshot first.
func getDashboard(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	// With a Git account: bring in teammates' changes first (skipped when a
	// pull ran in the last minute), so the snapshot and slips see them.
	SyncManager.BeforeDashboard(root)
	if _, err := pm.AutoSnapshot(projectID, root); err != nil {
		log.Printf("auto-snapshot (%s): %v", projectID, err)
	}
	stripHTML := pm.RenderPlanStripHTML(projectID, pm.ReadPlanStatus(root))

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
			targetDate = cfg.EffectiveRequestedEnd()
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
		blocks := pm.BlocksForProject(cfg, tickets, estCtx, time.Now())
		reportsHTML := pm.RenderProjectedGanttHTML(projectID, projectName, tickets, resources, estCtx, start, exportURL, blocks) +
			pm.RenderLoadHTML(pm.ComputeLoad(tickets, resources, estCtx, time.Now(), targetDate)) +
			pm.RenderHoursBudgetHTML(pm.ComputeHoursBudget(tickets, resources, workHours, adminHours, qaHours)) +
			pm.RenderHoursAtRiskHTML(pm.ComputeHoursAtRisk(tickets, estCtx)) +
			pm.RenderBlockedHTML(pm.ComputeBlocked(tickets)) +
			pm.RenderTestSummaryLineHTML(pm.ComputeTestSummary(tickets)) +
			pm.RenderEstimateVarianceHTML(pm.ComputeEstimateVariance(tickets, estCtx)) +
			pm.RenderOverridesHTML(pm.ComputeOverrides(tickets)) +
			pm.RenderProjectCostHTML(pm.ComputeProjectCost(tickets))
		statsHTML := pm.RenderStatsHTML(projectStats(root, tickets, cfg, estCtx, blocks), "#schedule-vs-request")
		html := pm.GenerateSimpleDashboard(tickets, projectID, projectName, stripHTML+forecastCardHTML(projectID, root, tickets, cfg), statsHTML, reportsHTML)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(html))
		return
	}

	snapshot, err := pm.LoadLatestSnapshot(root)
	if err != nil || snapshot == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		html := "<!DOCTYPE html><html><head><meta charset=\"UTF-8\"></head><body style=\"font-family:-apple-system,sans-serif;background:#f4f5f7\">" +
			stripHTML + "<p style=\"margin:20px 24px\">No snapshot yet. Take one from the Plan strip.</p></body></html>"
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
		targetDate = cfg.EffectiveRequestedEnd()
		workHours = cfg.EffectiveWorkHours()
		adminHours = cfg.AdminHours
		qaHours = cfg.QAHours
	} else {
		cfg = nil
	}
	estCtx := pm.ProjectEstimateContext(root, costTickets, cfg)
	reportsHTML := pm.RenderLoadHTML(pm.ComputeLoad(costTickets, resources, estCtx, time.Now(), targetDate)) +
		pm.RenderHoursBudgetHTML(pm.ComputeHoursBudget(costTickets, resources, workHours, adminHours, qaHours)) +
		pm.RenderHoursAtRiskHTML(pm.ComputeHoursAtRisk(costTickets, estCtx)) +
		pm.RenderBlockedHTML(pm.ComputeBlocked(costTickets)) +
		pm.RenderTestSummaryLineHTML(pm.ComputeTestSummary(costTickets)) +
		pm.RenderEstimateVarianceHTML(pm.ComputeEstimateVariance(costTickets, estCtx)) +
		pm.RenderOverridesHTML(pm.ComputeOverrides(costTickets)) +
		pm.RenderProjectCostHTML(pm.ComputeProjectCost(costTickets))
	dashBlocks := pm.BlocksForProject(cfg, costTickets, estCtx, time.Now())
	statsHTML := pm.RenderStatsHTML(projectStats(root, costTickets, cfg, estCtx, dashBlocks), "#schedule-vs-request")
	html := pm.GenerateDashboard(snapshot, dashBlocks, stripHTML+forecastCardHTML(projectID, root, costTickets, cfg)+statsHTML, reportsHTML)
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

	// Guard: replacing a baseline goes through POST /rebaseline (with a reason).
	if pm.BaselineExists(root) {
		respondError(w, http.StatusConflict, "Baseline already exists. Use POST /rebaseline to replace it.")
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

	unlock := ticket.LockProject(root)
	baseline, err := pm.RunWBS(params, root, templateDir)
	warn := ""
	if err == nil {
		warn = pm.CommitBaseline(root, fmt.Sprintf("baseline: %s from template %s", req.ProjectName, req.TemplateID))
	}
	unlock()
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, err.Error())
		} else {
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, struct {
		*pm.Baseline
		CommitWarning string `json:"commit_warning,omitempty"`
	}{baseline, warn})
}

// readOptionalAuthor reads an optional {"author": ...} body (empty or absent
// is fine).
func readOptionalAuthor(r *http.Request) string {
	var req struct {
		Author string `json:"author"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	return req.Author
}

// baselineFromTickets handles POST /api/projects/{projectId}/baseline-now.
// Optional body {"author"}; defaults to the project's git identity. Commits
// baseline.json (and .tkt/pm/.gitignore).
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

	author := pm.ResolveAuthor(root, readOptionalAuthor(r))
	baseline, warn, err := pm.BaselineNow(root, projectID, projectName, author)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			respondError(w, http.StatusConflict, err.Error())
		} else {
			respondError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, struct {
		*pm.Baseline
		CommitWarning string `json:"commit_warning,omitempty"`
	}{baseline, warn})
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

	defer ticket.LockProject(root)()
	events, err := pm.ReadSlipEvents(root)
	if err != nil {
		respondError(w, http.StatusNotFound, "No slip events file found.")
		return
	}
	if len(events) == 0 {
		respondError(w, http.StatusNotFound, "No slip events file found.")
		return
	}

	requestedBy := ""
	if req.AcknowledgedBy != nil {
		requestedBy = *req.AcknowledgedBy
	}
	acknowledgedBy := pm.ResolveAuthor(root, requestedBy)

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
	resp := map[string]interface{}{
		"updated": slipEventID,
		"status":  "resolved",
	}
	if warn := pm.CommitSlipEvents(root, fmt.Sprintf("slip %s resolved: %s", slipEventID, req.ReasonCategory)); warn != "" {
		resp["commit_warning"] = warn
	}
	respondJSON(w, http.StatusOK, resp)
}

// rebaseline handles POST /api/projects/{projectId}/rebaseline.
// Body: {"reason": "...", "author"?}. The reason must be at least 5 characters
// (400); 409 without a baseline. Archives the current baseline to
// .tkt/pm/baselines/<date>-<n>.json, closes its unresolved slip events as
// "rebaseline", baselines the current tickets and commits it all in one commit.
func rebaseline(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
		Author string `json:"author"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := pm.ValidRebaselineReason(req.Reason); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectName := projectID
	if cfg, err := ticket.ReadConfig(root); err == nil && cfg.ProjectName != "" {
		projectName = cfg.ProjectName
	}
	res, err := pm.Rebaseline(root, projectID, projectName, req.Reason, pm.ResolveAuthor(root, req.Author))
	switch {
	case errors.Is(err, pm.ErrNoBaseline):
		respondError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, pm.ErrRebaselineReason):
		respondError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// listBaselines handles GET /api/projects/{projectId}/baselines: the archived
// (replaced) baselines, oldest first.
func listBaselines(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	list, err := pm.ListBaselineArchive(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, list)
}

// projectStats computes the stats panel for a project (blocks may be nil:
// they're computed here).
func projectStats(root string, tickets []*ticket.Ticket, cfg *ticket.ProjectConfig, estCtx pm.EstimateContext, blocks []pm.Block) pm.ProjectStats {
	if blocks == nil {
		blocks = pm.BlocksForProject(cfg, tickets, estCtx, time.Now())
	}
	return pm.ComputeStats(tickets, estCtx, blocks, pm.ReadPlanStatus(root).BaselineDate, time.Now())
}

// getStats handles GET /api/projects/{projectId}/stats: the PM Dashboard's
// live numbers (delivery, estimate accuracy, flow, tests, scope added) with
// trends. Read-only.
func getStats(w http.ResponseWriter, r *http.Request) {
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
	respondJSON(w, http.StatusOK, projectStats(root, tickets, cfg, pm.ProjectEstimateContext(root, tickets, cfg), nil))
}
