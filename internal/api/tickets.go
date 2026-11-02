// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package api provides HTTP handlers for the hate REST API.
// All JSON responses match the Python FastAPI format exactly.
// Error responses use {"detail": "message"}.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/pm"
	"hate/internal/service"
	"hate/internal/ticket"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// respondJSON writes a JSON response with the given status code.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// respondError writes a JSON error response matching FastAPI's {"detail": "..."} format.
func respondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"detail": message})
}

// getProjectRoot resolves a project path from projectID. Returns the path and true,
// or writes a 404 error response and returns ("", false).
func getProjectRoot(w http.ResponseWriter, projectID string) (string, bool) {
	path, err := service.ResolveProject(projectID)
	if err != nil {
		var amb *config.AmbiguousProjectError
		if errors.As(err, &amb) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail":     err.Error(),
				"candidates": amb.Candidates,
			})
			return "", false
		}
		respondError(w, http.StatusNotFound, err.Error())
		return "", false
	}
	return path, true
}

// respondTicket writes the ticket as JSON plus "commit_warning" when the
// auto-commit failed (the change itself is saved either way), and the computed
// "workflow" so the panel's Promote / Demote labels follow every change.
func respondTicket(w http.ResponseWriter, tk *ticket.Ticket, commitWarning string) {
	respondJSON(w, http.StatusOK, struct {
		*ticket.Ticket
		Workflow      *ticket.WorkflowView `json:"workflow,omitempty"`
		CommitWarning string               `json:"commit_warning,omitempty"`
	}{tk, ticket.TicketWorkflow(tk), commitWarning})
}

// respondResult writes a service mutation result (see respondTicket).
func respondResult(w http.ResponseWriter, res *service.Result) {
	respondTicket(w, res.Ticket, res.CommitWarning)
}

// respondServiceError maps a service error to the REST status and body the
// API has always used. fallback is the status for a plain rule failure or a
// missing ticket (most write handlers have answered 422 for both).
func respondServiceError(w http.ResponseWriter, err error, fallback int) {
	var estErr *ticket.EstimateValidationError
	var extErr *service.TimeExtensionError
	switch {
	case errors.Is(err, service.ErrProjectClosed):
		respondError(w, http.StatusLocked, err.Error())
	case errors.Is(err, ticket.ErrNeedsTimeLog):
		// Distinct signal so the UI pops the log-time form instead of just erroring.
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail":         err.Error(),
			"needs_time_log": true,
		})
	case errors.As(err, &estErr):
		// Estimation rule failed at first promote (class / estimate_hours / cfp:).
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail":           err.Error(),
			"estimate_invalid": true,
		})
	case errors.As(err, &extErr):
		respondJSON(w, http.StatusConflict, map[string]interface{}{
			"detail":               err.Error(),
			"needs_time_extension": true,
			"allotted_hours":       extErr.AllottedHours,
			"spent_hours":          extErr.SpentHours,
			"would_be_hours":       extErr.WouldBeHours,
		})
	case errors.Is(err, ticket.ErrEffortRetired):
		respondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrInternal):
		respondError(w, http.StatusInternalServerError, err.Error())
	default:
		respondError(w, fallback, err.Error())
	}
}

// respondTicketNotFound answers 404 "Ticket not found" for any failure but a
// closed project (comment and predecessor removal have always done this).
func respondTicketNotFound(w http.ResponseWriter, err error, ticketID string) {
	if errors.Is(err, service.ErrProjectClosed) {
		respondError(w, http.StatusLocked, err.Error())
		return
	}
	respondError(w, http.StatusNotFound, "Ticket not found: "+ticketID)
}

// decodeJSON decodes a JSON request body into dst. Returns false and writes
// a 422 error if decoding fails.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		respondError(w, http.StatusUnprocessableEntity, "Invalid request body: "+err.Error())
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Request body structs (matching Python Pydantic models)
// ---------------------------------------------------------------------------

// CreateTicketRequest matches the Python CreateTicketRequest model.
type CreateTicketRequest struct {
	Type             string   `json:"type"`
	Title            string   `json:"title"`
	Description      *string  `json:"description"`
	Priority         *string  `json:"priority"`
	Effort           *string  `json:"effort"` // retired: non-empty → 400
	EstimateHours    *float64 `json:"estimate_hours"`
	Assignee         *string  `json:"assignee"`
	Tags             []string `json:"tags"`
	Phase            *string  `json:"phase"`
	Predecessors     []string `json:"predecessors"`
	PlannedStartDate *string  `json:"planned_start_date"`
	DueDate          *string  `json:"due_date"`
	Severity         *string  `json:"severity"`
	Creator          *string  `json:"creator"`
	Hours            *float64 `json:"hours"`
	Attendees        *string  `json:"attendees"`
}

// EditTicketRequest is a PATCH body: either one {field, value} (the original
// Python EditTicketRequest shape) or a multi-field edit {fields, add_tags,
// remove_tags} applied all-or-nothing in one commit.
type EditTicketRequest struct {
	Field      string                 `json:"field"`
	Value      interface{}            `json:"value"`
	Fields     map[string]interface{} `json:"fields"`
	AddTags    []string               `json:"add_tags"`
	RemoveTags []string               `json:"remove_tags"`
	Author     *string                `json:"author"`
}

// CommentRequest matches the Python CommentRequest model.
type CommentRequest struct {
	Message string  `json:"message"`
	Author  *string `json:"author"`
}

// TimeEntryRequest matches the Python TimeEntryRequest model.
type TimeEntryRequest struct {
	Date        string  `json:"date"`
	Hours       float64 `json:"hours"`
	Description string  `json:"description"`
	Author      *string `json:"author"`
	// ExtendAuthorized / ExtendReason authorize a log past a wrap ticket's hours
	// allotment under strict time enforcement (see handleAddTimeEntry).
	ExtendAuthorized bool   `json:"extend_authorized"`
	ExtendReason     string `json:"extend_reason"`
}

// PredecessorRequest matches the Python PredecessorRequest model.
type PredecessorRequest struct {
	PredecessorID string  `json:"predecessor_id"`
	Author        *string `json:"author"`
}

// ---------------------------------------------------------------------------
// Helper to get optional string value
// ---------------------------------------------------------------------------

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterTicketRoutes registers all ticket API routes on the given router.
func RegisterTicketRoutes(r chi.Router) {
	r.Route("/api/projects/{projectId}/tickets", func(r chi.Router) {
		r.Use(blockWritesWhenProjectClosed)
		r.Get("/", handleListTickets)
		r.Post("/", handleCreateTicket)
		r.Post("/bulk", handleCreateTicketsBulk)
		r.Get("/billing", handleGetBilling) // MUST be before /{ticketId}
		r.Get("/{ticketId}", handleGetTicket)
		r.Patch("/{ticketId}", handleEditTicket)
		r.Post("/{ticketId}/promote", handlePromoteTicket)
		r.Post("/{ticketId}/demote", handleDemoteTicket)
		r.Post("/{ticketId}/block", handleBlockTicket)
		r.Post("/{ticketId}/force-close", handleForceCloseTicket)
		r.Post("/{ticketId}/comment", handleAddComment)
		r.Patch("/{ticketId}/comments/{commentId}", handleEditComment)
		r.Post("/{ticketId}/time", handleAddTimeEntry)
		r.Delete("/{ticketId}/time/{entryId}", handleDeleteTimeEntry)
		r.Post("/{ticketId}/test-cases", handleAddTestCase)
		r.Post("/{ticketId}/test-cases/bulk", handleAddTestCasesBulk)
		r.Patch("/{ticketId}/test-cases/{caseId}", handleUpdateTestCase)
		r.Delete("/{ticketId}/test-cases/{caseId}", handleDeleteTestCase)
		r.Post("/{ticketId}/predecessors", handleAddPredecessor)
		r.Delete("/{ticketId}/predecessors/{predecessorId}", handleRemovePredecessor)
		r.Post("/{ticketId}/attachments", handleUploadAttachment)
		r.Get("/{ticketId}/attachments/{attachmentId}", handleDownloadAttachment)
		r.Delete("/{ticketId}/attachments/{attachmentId}", handleDeleteAttachment)
	})
}

// blockWritesWhenProjectClosed rejects any non-GET/HEAD request to a
// /api/projects/{projectId}/tickets/* route when the project is closed.
// Reads/lookups still go through so the closed project remains viewable.
func blockWritesWhenProjectClosed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		projectID := chi.URLParam(r, "projectId")
		root, ok := getProjectRoot(w, projectID)
		if !ok {
			return
		}
		cfg, err := ticket.ReadConfig(root)
		if err == nil && cfg.IsClosed() {
			respondError(w, http.StatusLocked, "Project is closed — reopen it to make changes.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Handler implementations
// ---------------------------------------------------------------------------

// handleListTickets handles GET /api/projects/{projectId}/tickets
func handleListTickets(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	q := r.URL.Query()
	items, err := service.ListTickets(root, service.ListFilter{
		Status: q.Get("status"), Type: q.Get("type"), Assignee: q.Get("assignee"),
		Tag: q.Get("tag"), Phase: q.Get("phase"), Parent: q.Get("parent"),
	})
	if err != nil {
		respondServiceError(w, err, http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, items)
}

// handleCreateTicket handles POST /api/projects/{projectId}/tickets
func handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req CreateTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	params := ticket.CreateTicketParams{
		Type:             req.Type,
		Title:            req.Title,
		Creator:          strVal(req.Creator),
		Priority:         strVal(req.Priority),
		Effort:           strVal(req.Effort),
		EstimateHours:    req.EstimateHours,
		Assignee:         strVal(req.Assignee),
		Description:      strVal(req.Description),
		Phase:            strVal(req.Phase),
		PlannedStartDate: strVal(req.PlannedStartDate),
		DueDate:          strVal(req.DueDate),
		Severity:         strVal(req.Severity),
		Attendees:        strVal(req.Attendees),
	}
	if len(req.Tags) > 0 {
		params.Tags = req.Tags
	}
	if len(req.Predecessors) > 0 {
		params.Predecessors = req.Predecessors
	}
	if req.Hours != nil {
		params.Hours = *req.Hours
	}

	res, err := service.CreateTicket(root, params)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleCreateTicketsBulk handles POST /api/projects/{projectId}/tickets/bulk:
// {"tickets": [...], "author": "..."} (max 50). Tickets may reference earlier
// ones in the batch by "ref" (parent_ref, predecessor_refs) and carry inline
// test_cases. All are validated before any is written; one commit.
func handleCreateTicketsBulk(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		Tickets []service.BulkTicket `json:"tickets"`
		Author  string               `json:"author"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := service.CreateTickets(root, req.Tickets, req.Author)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"created":        res.Created,
		"tickets":        res.Tickets,
		"commit_warning": res.CommitWarning,
	})
}

// handleGetBilling handles GET /api/projects/{projectId}/tickets/billing
func handleGetBilling(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")

	idx, err := ticket.CurrentIndex(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// A wrap ticket's hours allotment drives the normal/override split: hours up
	// to its estimate are "normal"; everything beyond is "override". Functional
	// and unclassed tickets have no per-ticket allotment (all hours normal).
	var effortToDays map[string]float64
	if cfg, cErr := ticket.ReadConfig(root); cErr == nil && cfg != nil {
		effortToDays = cfg.EffortToDays
	}
	estCtx := pm.NewEstimateContext(nil, 0, effortToDays)

	type billingEntry struct {
		TicketID      string  `json:"ticket_id"`
		TicketTitle   string  `json:"ticket_title"`
		TicketPhase   *string `json:"ticket_phase"`
		ID            string  `json:"id"`
		Date          string  `json:"date"`
		Hours         float64 `json:"hours"`          // normal (within-allotment) portion
		OverrideHours float64 `json:"override_hours"` // over-allotment portion
		ExtendReason  string  `json:"extend_reason"`  // recorded authorization for override hours, if any
		Description   string  `json:"description"`
		Author        string  `json:"author"`
		LoggedAt      string  `json:"logged_at"`
	}

	var entries []billingEntry
	for _, summary := range idx.Tickets {
		tk, err := ticket.ReadTicket(root, summary.ID)
		if err != nil {
			continue
		}

		// Allotment for this ticket (0 when unsized → all hours are normal, since
		// there is no budget to exceed).
		allot, _ := pm.WrapAllotment(tk, estCtx)

		// The split is cumulative over ALL of a ticket's entries in chronological
		// order, computed *before* the date filter — so a filtered view can't reset
		// the allotment and mislabel later over-budget hours as normal.
		ordered := make([]ticket.TimeEntry, len(tk.TimeEntries))
		copy(ordered, tk.TimeEntries)
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].Date != ordered[j].Date {
				return ordered[i].Date < ordered[j].Date
			}
			return ordered[i].LoggedAt < ordered[j].LoggedAt
		})

		cum := 0.0
		for _, te := range ordered {
			normal, over := te.Hours, 0.0
			if allot > 0 {
				before, after := cum, cum+te.Hours
				switch {
				case after <= allot:
					normal, over = te.Hours, 0
				case before >= allot:
					normal, over = 0, te.Hours
				default:
					normal, over = allot-before, after-allot
				}
			}
			cum += te.Hours

			if start != "" && te.Date < start {
				continue
			}
			if end != "" && te.Date > end {
				continue
			}
			reason := ""
			if over > 0 {
				reason = te.ExtendReason
			}
			entries = append(entries, billingEntry{
				TicketID:      tk.ID,
				TicketTitle:   tk.Title,
				TicketPhase:   tk.Phase,
				ID:            te.ID,
				Date:          te.Date,
				Hours:         normal,
				OverrideHours: over,
				ExtendReason:  reason,
				Description:   te.Description,
				Author:        te.Author,
				LoggedAt:      te.LoggedAt,
			})
		}
	}

	// Sort by date
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Date < entries[j].Date
	})

	if entries == nil {
		entries = []billingEntry{}
	}

	var totalNormal, totalOverride float64
	for _, e := range entries {
		totalNormal += e.Hours
		totalOverride += e.OverrideHours
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"entries":              entries,
		"total_hours":          totalNormal + totalOverride,
		"total_normal_hours":   totalNormal,
		"total_override_hours": totalOverride,
	})
}

// handleGetTicket handles GET /api/projects/{projectId}/tickets/{ticketId}
func handleGetTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	d, err := service.GetTicket(root, ticketID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, d)
}

// handleEditTicket handles PATCH /api/projects/{projectId}/tickets/{ticketId}
func handleEditTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req EditTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var res *service.Result
	var err error
	if req.Fields != nil || req.AddTags != nil || req.RemoveTags != nil {
		if req.Field != "" {
			respondError(w, http.StatusUnprocessableEntity, "give either field/value or fields/add_tags/remove_tags, not both")
			return
		}
		res, err = service.EditFields(root, ticketID, req.Fields, req.AddTags, req.RemoveTags, strVal(req.Author))
	} else {
		res, err = service.EditField(root, ticketID, req.Field, req.Value, strVal(req.Author))
	}
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// statusAuthor returns the author for a status change: the JSON body's
// "author" when present and non-empty, else ?author=. A missing, empty or
// non-JSON body is tolerated, so callers using either convention are recorded.
func statusAuthor(r *http.Request) string {
	var req struct {
		Author string `json:"author"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Author != "" {
		return req.Author
	}
	return r.URL.Query().Get("author")
}

// handlePromoteTicket handles POST /api/projects/{projectId}/tickets/{ticketId}/promote
func handlePromoteTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	res, err := service.Promote(root, ticketID, statusAuthor(r))
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleDemoteTicket handles POST /api/projects/{projectId}/tickets/{ticketId}/demote
func handleDemoteTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	res, err := service.Demote(root, ticketID, statusAuthor(r))
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleBlockTicket handles POST /api/projects/{projectId}/tickets/{ticketId}/block.
// The promote/demote workflow has no transition *into* "blocked" (it only ever maps
// blocked -> _previous), so this is the one entry point for marking a ticket blocked.
// Leaving the blocked state is still done with promote/demote.
func handleBlockTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	author := r.URL.Query().Get("author")
	// Optional {reason, author} body — the reason is captured on the ticket and
	// shown on the PM dashboard's blocked-tickets list. Body is tolerated as empty.
	var req struct {
		Reason string `json:"reason"`
		Author string `json:"author"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Author != "" {
		author = req.Author
	}
	res, err := service.Block(root, ticketID, req.Reason, author)
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleAddTestCase handles POST /api/projects/{projectId}/tickets/{ticketId}/test-cases
func handleAddTestCase(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		Step     string `json:"step"`
		Expected string `json:"expected"`
		Author   string `json:"author"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := service.AddTestCase(root, ticketID, req.Step, req.Expected, req.Author)
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleAddTestCasesBulk handles POST /api/projects/{projectId}/tickets/{ticketId}/test-cases/bulk
// Body: {"cases": [{"step": "...", "expected": "..."}], "author": "..."}. The
// low-friction path for agents and the paste-to-author box — all cases in one write.
func handleAddTestCasesBulk(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req struct {
		Cases []struct {
			Step     string `json:"step"`
			Expected string `json:"expected"`
		} `json:"cases"`
		Author string `json:"author"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cases := make([]ticket.TestCase, len(req.Cases))
	for i, c := range req.Cases {
		cases[i] = ticket.TestCase{Step: c.Step, Expected: c.Expected}
	}
	res, err := service.AddTestCases(root, ticketID, cases, req.Author)
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleUpdateTestCase handles PATCH /api/projects/{projectId}/tickets/{ticketId}/test-cases/{caseId}
func handleUpdateTestCase(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	caseID := chi.URLParam(r, "caseId")
	var req struct {
		Step     *string `json:"step"`
		Expected *string `json:"expected"`
		Status   *string `json:"status"`
		Comment  *string `json:"comment"`
		Author   *string `json:"author"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := service.UpdateTestCase(root, ticketID, caseID, ticket.TestCaseUpdate{
		Step: req.Step, Expected: req.Expected, Status: req.Status, Comment: req.Comment,
	}, strVal(req.Author))
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleDeleteTestCase handles DELETE /api/projects/{projectId}/tickets/{ticketId}/test-cases/{caseId}
func handleDeleteTestCase(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	caseID := chi.URLParam(r, "caseId")
	res, err := service.DeleteTestCase(root, ticketID, caseID, r.URL.Query().Get("author"))
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleForceCloseTicket handles POST /api/projects/{projectId}/tickets/{ticketId}/force-close.
// Skips the workflow and lands the ticket at "closed" with a recorded reason.
func handleForceCloseTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
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
	if req.Author == "" {
		req.Author = r.URL.Query().Get("author")
	}
	res, err := service.ForceClose(root, ticketID, req.Reason, req.Author)
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleAddComment handles POST /api/projects/{projectId}/tickets/{ticketId}/comment
func handleAddComment(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req CommentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := service.AddComment(root, ticketID, req.Message, strVal(req.Author))
	if err != nil {
		respondTicketNotFound(w, err, ticketID)
		return
	}
	respondResult(w, res)
}

// handleEditComment handles PATCH /api/projects/{projectId}/tickets/{ticketId}/comments/{commentId}:
// {message, author?}. Unknown ticket or comment id -> 404; empty message or a
// non-comment entry -> 422.
func handleEditComment(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req CommentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := service.EditComment(root, ticketID, chi.URLParam(r, "commentId"), req.Message, strVal(req.Author))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleAddTimeEntry handles POST /api/projects/{projectId}/tickets/{ticketId}/time
func handleAddTimeEntry(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req TimeEntryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := service.LogTime(root, ticketID, service.LogTimeParams{
		Date: req.Date, Hours: req.Hours, Description: req.Description, Author: strVal(req.Author),
		ExtendAuthorized: req.ExtendAuthorized, ExtendReason: req.ExtendReason,
	})
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleDeleteTimeEntry handles DELETE /api/projects/{projectId}/tickets/{ticketId}/time/{entryId}
func handleDeleteTimeEntry(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	entryID := chi.URLParam(r, "entryId")
	res, err := service.DeleteTimeEntry(root, ticketID, entryID, r.URL.Query().Get("author"))
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleAddPredecessor handles POST /api/projects/{projectId}/tickets/{ticketId}/predecessors
func handleAddPredecessor(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	var req PredecessorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := service.AddPredecessor(root, ticketID, req.PredecessorID, strVal(req.Author))
	if err != nil {
		respondServiceError(w, err, http.StatusUnprocessableEntity)
		return
	}
	respondResult(w, res)
}

// handleRemovePredecessor handles DELETE /api/projects/{projectId}/tickets/{ticketId}/predecessors/{predecessorId}
func handleRemovePredecessor(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	predecessorID := chi.URLParam(r, "predecessorId")
	res, err := service.RemovePredecessor(root, ticketID, predecessorID, r.URL.Query().Get("author"))
	if err != nil {
		respondTicketNotFound(w, err, ticketID)
		return
	}
	respondResult(w, res)
}

// commitAttachment commits a ticket JSON change plus an attachment file with a
// descriptive message. Used by upload and delete — pass the on-disk path of
// the attachment file (or empty if no file change applies). Returns the commit
// warning. The caller holds the project lock.
func commitAttachment(repoRoot, ticketID, attPath, action string) string {
	files := []string{ticket.TicketPath(repoRoot, ticketID)}
	if attPath != "" {
		files = append(files, attPath)
	}
	return ticket.CommitWarning(ticket.CommitFiles(repoRoot, files, ticketID+": "+action))
}

// handleUploadAttachment handles POST /api/projects/{projectId}/tickets/{ticketId}/attachments.
// Accepts a multipart form with a single "file" part, capped at MaxAttachmentSize.
func handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	// Make sure the ticket exists before we accept any bytes.
	if _, err := ticket.ReadTicket(root, ticketID); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	// Cap the entire request body. ParseMultipartForm's own size argument only
	// controls in-memory parsing, not the upload total.
	r.Body = http.MaxBytesReader(w, r.Body, ticket.MaxAttachmentSize+1024*1024)
	if err := r.ParseMultipartForm(8 * 1024 * 1024); err != nil {
		respondError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload too large or malformed: %v", err))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "missing 'file' form part")
		return
	}
	defer file.Close()
	if header.Size > ticket.MaxAttachmentSize {
		respondError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file too large (max %d bytes)", ticket.MaxAttachmentSize))
		return
	}
	author := r.FormValue("author")

	filename := ticket.SanitizeAttachmentFilename(header.Filename)
	attID, err := ticket.GenerateAttachmentID()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := ticket.AttachmentsDir(root, ticketID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dstPath := ticket.AttachmentPath(root, ticketID, attID, filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	written, copyErr := io.Copy(dst, file)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(dstPath)
		respondError(w, http.StatusInternalServerError, "failed to write attachment")
		return
	}
	if written > ticket.MaxAttachmentSize {
		os.Remove(dstPath)
		respondError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file too large (max %d bytes)", ticket.MaxAttachmentSize))
		return
	}

	att := ticket.Attachment{
		ID:          attID,
		Filename:    filename,
		Size:        written,
		ContentType: ticket.GuessContentType(filename),
		UploadedAt:  ticket.NowISO(),
		UploadedBy:  author,
	}
	// The file has its own unique path; only the ticket update and the commit
	// need the lock (not the upload itself).
	defer ticket.LockProject(root)()
	tk, err := ticket.AppendAttachment(root, ticketID, att, author)
	if err != nil {
		os.Remove(dstPath)
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondTicket(w, tk, commitAttachment(root, ticketID, dstPath, "attachment added: "+filename))
}

// handleDownloadAttachment handles GET /api/projects/{projectId}/tickets/{ticketId}/attachments/{attachmentId}.
// Streams the file with a sanitized Content-Disposition; allowed during read-only (closed) state.
func handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	attID := chi.URLParam(r, "attachmentId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	t, err := ticket.ReadTicket(root, ticketID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	att, found := ticket.FindAttachment(t, attID)
	if !found {
		respondError(w, http.StatusNotFound, "Attachment not found: "+attID)
		return
	}
	path := ticket.AttachmentPath(root, ticketID, att.ID, att.Filename)
	f, err := os.Open(path)
	if err != nil {
		respondError(w, http.StatusNotFound, "Attachment file missing on disk")
		return
	}
	defer f.Close()
	// Inline-rendered types (images, markdown, text, PDF) are viewable in
	// the browser; everything else downloads.
	disposition := "attachment"
	if strings.HasPrefix(att.ContentType, "image/") || strings.HasPrefix(att.ContentType, "text/") || att.ContentType == "application/pdf" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", att.Size))
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=\"%s\"", disposition, att.Filename))
	io.Copy(w, f)
}

// handleDeleteAttachment handles DELETE /api/projects/{projectId}/tickets/{ticketId}/attachments/{attachmentId}.
func handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	attID := chi.URLParam(r, "attachmentId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	defer ticket.LockProject(root)()
	author := r.URL.Query().Get("author")
	// Resolve the file path before removing so we can include it in the commit.
	t, err := ticket.ReadTicket(root, ticketID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	att, found := ticket.FindAttachment(t, attID)
	if !found {
		respondError(w, http.StatusNotFound, "Attachment not found: "+attID)
		return
	}
	delPath := ticket.AttachmentPath(root, ticketID, att.ID, att.Filename)
	tk, err := ticket.RemoveAttachment(root, ticketID, attID, author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondTicket(w, tk, commitAttachment(root, ticketID, delPath, "attachment removed: "+att.Filename))
}
