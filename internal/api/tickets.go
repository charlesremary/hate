// Package api provides HTTP handlers for the hate REST API.
// All JSON responses match the Python FastAPI format exactly.
// Error responses use {"detail": "message"}.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
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
	path, err := config.GetProjectPath(projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found: "+projectID)
		return "", false
	}
	return path, true
}

// commitTicket auto-commits the ticket file and index after a mutation.
// Enforces project git identity first.
func commitTicket(repoRoot, ticketID, action string) {
	cfg, err := ticket.ReadConfig(repoRoot)
	if err == nil {
		ticket.EnsureProjectIdentity(repoRoot, cfg)
	}
	files := []string{
		ticket.TicketPath(repoRoot, ticketID),
		ticket.IndexPath(repoRoot),
	}
	ticket.GitCommit(repoRoot, files, ticketID+": "+action)
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
	Effort           *string  `json:"effort"`
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

// EditTicketRequest matches the Python EditTicketRequest model.
type EditTicketRequest struct {
	Field  string      `json:"field"`
	Value  interface{} `json:"value"`
	Author *string     `json:"author"`
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
		r.Get("/billing", handleGetBilling) // MUST be before /{ticketId}
		r.Get("/{ticketId}", handleGetTicket)
		r.Patch("/{ticketId}", handleEditTicket)
		r.Post("/{ticketId}/promote", handlePromoteTicket)
		r.Post("/{ticketId}/demote", handleDemoteTicket)
		r.Post("/{ticketId}/block", handleBlockTicket)
		r.Post("/{ticketId}/force-close", handleForceCloseTicket)
		r.Post("/{ticketId}/comment", handleAddComment)
		r.Post("/{ticketId}/time", handleAddTimeEntry)
		r.Delete("/{ticketId}/time/{entryId}", handleDeleteTimeEntry)
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

	// Regenerate index to ensure it includes all current fields
	_ = ticket.RegenerateIndex(root)

	idx, err := ticket.ReadIndex(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Apply filters from query params
	status := r.URL.Query().Get("status")
	ticketType := r.URL.Query().Get("type")
	assignee := r.URL.Query().Get("assignee")
	tag := r.URL.Query().Get("tag")
	phase := r.URL.Query().Get("phase")

	var filtered []ticket.IndexSummary
	for _, t := range idx.Tickets {
		if status != "" && t.Status != status {
			continue
		}
		if ticketType != "" && t.Type != ticketType {
			continue
		}
		if assignee != "" {
			a := ""
			if t.Assignee != nil {
				a = *t.Assignee
			}
			if a != assignee {
				continue
			}
		}
		if tag != "" {
			found := false
			for _, tg := range t.Tags {
				if tg == tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if phase != "" {
			p := ""
			if t.Phase != nil {
				p = *t.Phase
			}
			if p != phase {
				continue
			}
		}
		filtered = append(filtered, t)
	}

	if filtered == nil {
		filtered = []ticket.IndexSummary{}
	}
	respondJSON(w, http.StatusOK, filtered)
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

	creator := strVal(req.Creator)

	params := ticket.CreateTicketParams{
		Type:    req.Type,
		Title:   req.Title,
		Creator: creator,
	}
	if req.Priority != nil {
		params.Priority = *req.Priority
	}
	if req.Effort != nil {
		params.Effort = *req.Effort
	}
	if req.Assignee != nil {
		params.Assignee = *req.Assignee
	}
	if req.Description != nil {
		params.Description = *req.Description
	}
	if len(req.Tags) > 0 {
		params.Tags = req.Tags
	}
	if req.Phase != nil {
		params.Phase = *req.Phase
	}
	if len(req.Predecessors) > 0 {
		params.Predecessors = req.Predecessors
	}
	if req.PlannedStartDate != nil {
		params.PlannedStartDate = *req.PlannedStartDate
	}
	if req.DueDate != nil {
		params.DueDate = *req.DueDate
	}
	if req.Severity != nil {
		params.Severity = *req.Severity
	}
	if req.Hours != nil {
		params.Hours = *req.Hours
	}
	if req.Attendees != nil {
		params.Attendees = *req.Attendees
	}

	tk, err := ticket.CreateTicket(root, params)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, err.Error())
		} else {
			respondError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, tk.ID, "created")
	respondJSON(w, http.StatusOK, tk)
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

	idx, err := ticket.ReadIndex(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type billingEntry struct {
		TicketID    string  `json:"ticket_id"`
		TicketTitle string  `json:"ticket_title"`
		TicketPhase *string `json:"ticket_phase"`
		ID          string  `json:"id"`
		Date        string  `json:"date"`
		Hours       float64 `json:"hours"`
		Description string  `json:"description"`
		Author      string  `json:"author"`
		LoggedAt    string  `json:"logged_at"`
	}

	var entries []billingEntry
	for _, summary := range idx.Tickets {
		tk, err := ticket.ReadTicket(root, summary.ID)
		if err != nil {
			continue
		}
		for _, te := range tk.TimeEntries {
			if start != "" && te.Date < start {
				continue
			}
			if end != "" && te.Date > end {
				continue
			}
			entries = append(entries, billingEntry{
				TicketID:    tk.ID,
				TicketTitle: tk.Title,
				TicketPhase: tk.Phase,
				ID:          te.ID,
				Date:        te.Date,
				Hours:       te.Hours,
				Description: te.Description,
				Author:      te.Author,
				LoggedAt:    te.LoggedAt,
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

	var totalHours float64
	for _, e := range entries {
		totalHours += e.Hours
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"entries":     entries,
		"total_hours": totalHours,
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

	tk, err := ticket.ReadTicket(root, ticketID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Ticket not found: "+ticketID)
		return
	}
	respondJSON(w, http.StatusOK, tk)
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

	author := strVal(req.Author)
	tk, err := ticket.EditField(root, ticketID, req.Field, req.Value, author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, req.Field+" updated")
	respondJSON(w, http.StatusOK, tk)
}

// handlePromoteTicket handles POST /api/projects/{projectId}/tickets/{ticketId}/promote
func handlePromoteTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	author := r.URL.Query().Get("author")
	tk, err := ticket.Promote(root, ticketID, author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "status \u2192 "+tk.Status)
	respondJSON(w, http.StatusOK, tk)
}

// handleDemoteTicket handles POST /api/projects/{projectId}/tickets/{ticketId}/demote
func handleDemoteTicket(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	author := r.URL.Query().Get("author")
	tk, err := ticket.Demote(root, ticketID, author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "status \u2192 "+tk.Status)
	respondJSON(w, http.StatusOK, tk)
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
	tk, err := ticket.ChangeStatus(root, ticketID, "blocked", author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "status → blocked")
	respondJSON(w, http.StatusOK, tk)
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
	tk, err := ticket.ForceClose(root, ticketID, req.Reason, req.Author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "force-closed: "+req.Reason)
	respondJSON(w, http.StatusOK, tk)
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

	author := strVal(req.Author)
	tk, err := ticket.AddComment(root, ticketID, req.Message, author)
	if err != nil {
		respondError(w, http.StatusNotFound, "Ticket not found: "+ticketID)
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "comment added")
	respondJSON(w, http.StatusOK, tk)
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

	author := strVal(req.Author)
	tk, err := ticket.AddTimeEntry(root, ticketID, req.Date, req.Hours, req.Description, author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "time logged")
	respondJSON(w, http.StatusOK, tk)
}

// handleDeleteTimeEntry handles DELETE /api/projects/{projectId}/tickets/{ticketId}/time/{entryId}
func handleDeleteTimeEntry(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	entryID := chi.URLParam(r, "entryId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	author := r.URL.Query().Get("author")
	tk, err := ticket.DeleteTimeEntry(root, ticketID, entryID, author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "time entry deleted")
	respondJSON(w, http.StatusOK, tk)
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

	author := strVal(req.Author)
	tk, err := ticket.AddPredecessor(root, ticketID, req.PredecessorID, author)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "predecessor added: "+req.PredecessorID)
	respondJSON(w, http.StatusOK, tk)
}

// handleRemovePredecessor handles DELETE /api/projects/{projectId}/tickets/{ticketId}/predecessors/{predecessorId}
func handleRemovePredecessor(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	ticketID := chi.URLParam(r, "ticketId")
	predecessorID := chi.URLParam(r, "predecessorId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}

	author := r.URL.Query().Get("author")
	tk, err := ticket.RemovePredecessor(root, ticketID, predecessorID, author)
	if err != nil {
		respondError(w, http.StatusNotFound, "Ticket not found: "+ticketID)
		return
	}

	if err := ticket.RegenerateIndex(root); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	commitTicket(root, ticketID, "predecessor removed: "+predecessorID)
	respondJSON(w, http.StatusOK, tk)
}

// commitAttachment commits a ticket JSON change plus an attachment file with a
// descriptive message. Used by upload and delete — pass the on-disk path of
// the attachment file (or empty if no file change applies).
func commitAttachment(repoRoot, ticketID, attPath, action string) {
	cfg, err := ticket.ReadConfig(repoRoot)
	if err == nil {
		ticket.EnsureProjectIdentity(repoRoot, cfg)
	}
	files := []string{
		ticket.TicketPath(repoRoot, ticketID),
		ticket.IndexPath(repoRoot),
	}
	if attPath != "" {
		files = append(files, attPath)
	}
	ticket.GitCommit(repoRoot, files, ticketID+": "+action)
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
	commitAttachment(root, ticketID, dstPath, "attachment added: "+filename)
	respondJSON(w, http.StatusOK, tk)
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
	commitAttachment(root, ticketID, delPath, "attachment removed: "+att.Filename)
	respondJSON(w, http.StatusOK, tk)
}
