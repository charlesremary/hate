// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
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
// Requested dates (Settings) and the Schedule vs request forecast
// ---------------------------------------------------------------------------

// dateOrNull maps an unset date to JSON null.
func dateOrNull(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// requestedDatesJSON is the GET/PUT requested-dates response.
func requestedDatesJSON(cfg *ticket.ProjectConfig) map[string]interface{} {
	return map[string]interface{}{
		"requested_start": dateOrNull(cfg.RequestedStart),
		"requested_end":   dateOrNull(cfg.EffectiveRequestedEnd()),
	}
}

// parseOptionalDate trims a nullable date; "" means clear. A non-empty value
// must be YYYY-MM-DD.
func parseOptionalDate(v *string, field string) (string, error) {
	if v == nil {
		return "", nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", fmt.Errorf("%s must be a date (YYYY-MM-DD) or null to clear", field)
	}
	return s, nil
}

// saveRequestedDates validates start <= end, writes the config (requested_end
// replaces the legacy target_date) and commits it, only when something changed.
// Returns the config and an HTTP status + message on failure.
func saveRequestedDates(root, start, end, commitMsg string) (*ticket.ProjectConfig, int, string) {
	if start != "" && end != "" && start > end {
		return nil, http.StatusBadRequest, fmt.Sprintf("requested start (%s) is after the requested end (%s)", start, end)
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	if cfg.RequestedStart == start && cfg.RequestedEnd == end && cfg.TargetDate == "" {
		return cfg, 0, ""
	}
	cfg.RequestedStart, cfg.RequestedEnd, cfg.TargetDate = start, end, ""
	if err := ticket.WriteConfig(root, cfg); err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	ticket.EnsureProjectIdentity(root, cfg)
	ticket.GitCommit(root, []string{ticket.ConfigPath(root)}, commitMsg)
	return cfg, 0, ""
}

// getRequestedDates handles GET /api/projects/{projectId}/requested-dates.
// Returns {"requested_start", "requested_end"} (null when unset; an old
// target_date reads as the requested end).
func getRequestedDates(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, requestedDatesJSON(cfg))
}

// updateRequestedDates handles PUT /api/projects/{projectId}/requested-dates.
// Body: {"requested_start": "YYYY-MM-DD"|null, "requested_end": ...}; null (or
// "", or omitted) clears a date. Bad dates and start after end are 400s.
// Commits the config when it changes.
func updateRequestedDates(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	var req struct {
		RequestedStart *string `json:"requested_start"`
		RequestedEnd   *string `json:"requested_end"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	start, err := parseOptionalDate(req.RequestedStart, "requested_start")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	end, err := parseOptionalDate(req.RequestedEnd, "requested_end")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	msg := "clear requested dates"
	if start != "" || end != "" {
		msg = fmt.Sprintf("requested dates: start %s, end %s", orDash(start), orDash(end))
	}
	cfg, code, detail := saveRequestedDates(root, start, end, msg)
	if code != 0 {
		respondError(w, code, detail)
		return
	}
	respondJSON(w, http.StatusOK, requestedDatesJSON(cfg))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// getTargetDate handles GET /api/projects/{projectId}/target-date — an alias
// for the requested end. Returns {"target_date": "YYYY-MM-DD"|null}.
func getTargetDate(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"target_date": dateOrNull(cfg.EffectiveRequestedEnd())})
}

// updateTargetDate handles PUT /api/projects/{projectId}/target-date — an
// alias that sets only the requested end (the requested start is kept).
// Body: {"target_date": "YYYY-MM-DD"|null}.
func updateTargetDate(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	var req struct {
		TargetDate *string `json:"target_date"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	end, err := parseOptionalDate(req.TargetDate, "target_date")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	cur, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	msg := "target date " + end
	if end == "" {
		msg = "clear target date"
	}
	cfg, code, detail := saveRequestedDates(root, cur.RequestedStart, end, msg)
	if code != 0 {
		respondError(w, code, detail)
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"target_date": dateOrNull(cfg.EffectiveRequestedEnd())})
}

// computeForecast computes the Schedule vs request card for a project and
// records it in the forecast history (committing the file when it changed).
// Shared by the dashboard and GET /forecast. cfg may be nil.
func computeForecast(projectID, root string, tickets []*ticket.Ticket, cfg *ticket.ProjectConfig) (pm.ForecastReport, []pm.ForecastHistoryEntry) {
	now := time.Now()
	rep := pm.ProjectForecast(projectID, root, tickets, cfg, now)
	hist, _, err := pm.RecordForecastHistory(root, rep, now)
	if err != nil {
		log.Printf("forecast history (%s): %v", projectID, err)
	}
	if hist == nil {
		hist = []pm.ForecastHistoryEntry{}
	}
	return rep, hist
}

// forecastCardHTML renders the card for the top of the PM dashboard.
func forecastCardHTML(projectID, root string, tickets []*ticket.Ticket, cfg *ticket.ProjectConfig) string {
	rep, hist := computeForecast(projectID, root, tickets, cfg)
	return pm.RenderForecastHTML(rep, hist)
}

// getForecast handles GET /api/projects/{projectId}/forecast: the Schedule vs
// request card as JSON plus the forecast history. Records the history like the
// dashboard does.
func getForecast(w http.ResponseWriter, r *http.Request) {
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
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		cfg = nil
	}
	rep, hist := computeForecast(projectID, root, tickets, cfg)
	respondJSON(w, http.StatusOK, struct {
		pm.ForecastReport
		History []pm.ForecastHistoryEntry `json:"history"`
	}{rep, hist})
}
