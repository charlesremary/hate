// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"encoding/json"
	"errors"

	"hate/internal/config"
	"hate/internal/service"
	"hate/internal/ticket"
)

// toolError is a business-rule failure returned to the model as a tool
// execution error (isError: true) whose text is a JSON object with an
// "error" code the model can act on (docs/plan-mcp-server.md section 5.4).
type toolError map[string]any

func (e toolError) Error() string {
	b, _ := json.Marshal(map[string]any(e))
	return string(b)
}

func invalid(detail string) error {
	return toolError{"error": "invalid_input", "detail": detail}
}

// mapError turns a service error into a toolError.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var amb *config.AmbiguousProjectError
	var est *ticket.EstimateValidationError
	var ext *service.TimeExtensionError
	switch {
	case ticket.IsNewerFormat(err):
		return toolError{"error": "project_newer_format", "detail": err.Error(),
			"next": "tell the user to update hate (brew upgrade --cask hate) and restart it; reads still work"}
	case errors.As(err, &amb):
		return toolError{"error": "ambiguous_project", "detail": err.Error(), "candidates": amb.Candidates}
	case errors.Is(err, service.ErrProjectClosed):
		return toolError{"error": "project_closed", "detail": err.Error()}
	case errors.Is(err, ticket.ErrNeedsTimeLog):
		return toolError{"error": "needs_time_log", "detail": err.Error(),
			"next": "call hate_log_time with the hours and a description of the work, then retry"}
	case errors.As(err, &est):
		return toolError{"error": "estimate_invalid", "detail": err.Error(),
			"next": "fix the class tag / estimate_hours / parent cfp: with hate_update_ticket (see hate://guide?section=3), then retry"}
	case errors.As(err, &ext):
		return toolError{"error": "needs_time_extension", "detail": err.Error(),
			"allotted_hours": ext.AllottedHours, "spent_hours": ext.SpentHours, "would_be_hours": ext.WouldBeHours,
			"next": "ask the user to authorize, then retry with extend_authorized: true and an extend_reason"}
	case errors.Is(err, service.ErrConflict):
		return toolError{"error": "conflict", "detail": err.Error()}
	case errors.Is(err, service.ErrNotFound):
		return toolError{"error": "not_found", "detail": err.Error()}
	case errors.Is(err, ticket.ErrEffortRetired), errors.Is(err, service.ErrInvalidInput):
		return toolError{"error": "invalid_input", "detail": err.Error()}
	}
	return toolError{"error": "internal", "detail": err.Error()}
}
