// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CreateTicketParams holds all parameters for creating a new ticket.
type CreateTicketParams struct {
	Type             string
	Title            string
	Creator          string
	Priority         string
	Effort           string
	Assignee         string
	Tags             []string
	Phase            string
	Predecessors     []string
	PlannedStartDate string
	DueDate          string
	Severity         string
	Description      string
	Hours            float64
	Attendees        string
}

// TicketsDir returns the path to the tickets directory.
func TicketsDir(repoRoot string) string {
	return filepath.Join(repoRoot, "tickets")
}

// TicketPath returns the path to a specific ticket JSON file.
func TicketPath(repoRoot, ticketID string) string {
	return filepath.Join(TicketsDir(repoRoot), ticketID+".json")
}

// ReadTicket reads a single ticket by ID. Returns error if not found.
func ReadTicket(repoRoot, ticketID string) (*Ticket, error) {
	path := TicketPath(repoRoot, ticketID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Ticket not found: %s", ticketID)
		}
		return nil, err
	}
	var t Ticket
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("failed to parse ticket %s: %w", ticketID, err)
	}
	// Ensure slices are not nil for JSON compatibility
	if t.Tags == nil {
		t.Tags = []string{}
	}
	if t.Predecessors == nil {
		t.Predecessors = []string{}
	}
	if t.TimeEntries == nil {
		t.TimeEntries = []TimeEntry{}
	}
	if t.Activity == nil {
		t.Activity = []Activity{}
	}
	if t.Attachments == nil {
		t.Attachments = []Attachment{}
	}
	return &t, nil
}

// ReadAllTickets reads all ticket files sorted by ID.
func ReadAllTickets(repoRoot string) ([]*Ticket, error) {
	tdir := TicketsDir(repoRoot)
	entries, err := os.ReadDir(tdir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Ticket{}, nil
		}
		return nil, err
	}

	var tickets []*Ticket
	var filenames []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			filenames = append(filenames, e.Name())
		}
	}
	sort.Strings(filenames)

	for _, fname := range filenames {
		path := filepath.Join(tdir, fname)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var t Ticket
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		if t.Tags == nil {
			t.Tags = []string{}
		}
		if t.Predecessors == nil {
			t.Predecessors = []string{}
		}
		if t.TimeEntries == nil {
			t.TimeEntries = []TimeEntry{}
		}
		if t.Activity == nil {
			t.Activity = []Activity{}
		}
		if t.Attachments == nil {
			t.Attachments = []Attachment{}
		}
		tickets = append(tickets, &t)
	}
	if tickets == nil {
		tickets = []*Ticket{}
	}
	return tickets, nil
}

// WriteTicket validates and writes a ticket to disk.
func WriteTicket(repoRoot string, t *Ticket) error {
	errors := ValidateTicket(t)
	if len(errors) > 0 {
		return fmt.Errorf("Invalid ticket: %s", strings.Join(errors, "; "))
	}
	path := TicketPath(repoRoot, t.ID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create tickets directory: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal ticket: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// CreateTicket creates a new ticket and writes it to disk.
func CreateTicket(repoRoot string, params CreateTicketParams) (*Ticket, error) {
	t := BlankTicket(params.Type, params.Type, params.Title, params.Creator)
	// Fix: the first param should be the ticket ID, not the type.
	// We need to get the next ticket ID first.
	// Actually, looking at the Python code, create_ticket takes ticket_id as a param.
	// The caller is responsible for calling next_ticket_id.
	// But our params struct doesn't have an ID field. Let's allocate one.
	ticketID, err := GenerateTicketID(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ticket ID: %w", err)
	}
	t = BlankTicket(ticketID, params.Type, params.Title, params.Creator)

	if params.Priority != "" {
		t.Priority = params.Priority
	}
	if params.Effort != "" {
		t.Effort = StringPtr(params.Effort)
	}
	if params.Assignee != "" {
		t.Assignee = StringPtr(params.Assignee)
	}
	if len(params.Tags) > 0 {
		t.Tags = params.Tags
	}
	if params.Phase != "" {
		t.Phase = &params.Phase
	}
	if params.Description != "" {
		t.Description = params.Description
	}
	if len(params.Predecessors) > 0 {
		for _, predID := range params.Predecessors {
			predPath := TicketPath(repoRoot, predID)
			if _, err := os.Stat(predPath); os.IsNotExist(err) {
				return nil, fmt.Errorf("Predecessor ticket not found: %s", predID)
			}
		}
		t.Predecessors = params.Predecessors
	}
	if params.Severity != "" && params.Type == "defect" {
		t.DefectSeverity = StringPtr(params.Severity)
	}
	if params.PlannedStartDate != "" {
		if err := ValidateDate(params.PlannedStartDate); err != nil {
			return nil, err
		}
		t.PlannedStartDate = StringPtr(params.PlannedStartDate)
	}
	if params.DueDate != "" {
		if err := ValidateDate(params.DueDate); err != nil {
			return nil, err
		}
		t.DueDate = StringPtr(params.DueDate)
	}
	if params.Attendees != "" && params.Type == "meeting" {
		t.MeetingAttendees = StringPtr(params.Attendees)
	}

	// Handle auto-complete types (meeting, administration)
	if IsAutoCompleteType(params.Type) {
		t.Status = "complete"
		t.ClosedAt = StringPtr(NowISO())
		if params.Hours > 0 {
			hours := roundToQuarter(params.Hours)
			date := NowISO()[:10]
			entryID := fmt.Sprintf("t%d", len(t.TimeEntries)+1)
			t.TimeEntries = append(t.TimeEntries, TimeEntry{
				ID:          entryID,
				Date:        date,
				Hours:       hours,
				Description: params.Title,
				Author:      params.Creator,
				LoggedAt:    NowISO(),
			})
			addActivity(t, params.Creator, "time_logged", fmt.Sprintf("%.2fh on %s: %s", hours, date, params.Title))
		}
		addActivity(t, params.Creator, "status_changed", "not_started -> complete")
	}

	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// addActivity appends an activity entry and updates the timestamp.
func addActivity(t *Ticket, author, action, detail string) {
	t.Activity = append(t.Activity, Activity{
		Timestamp: NowISO(),
		Author:    author,
		Action:    action,
		Detail:    detail,
	})
	t.UpdatedAt = NowISO()
}

// ForceClose moves a ticket directly to the terminal "closed" status, skipping
// the type's promote/demote workflow. Used for tickets that are dropped,
// duplicated, scoped out, etc. — distinct from a real completion. A reason is
// required and stored on the ticket; an activity entry is recorded.
//
// Refuses if the ticket is already at a terminal status (closed).
func ForceClose(repoRoot, ticketID, reason, author string) (*Ticket, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 5 {
		return nil, fmt.Errorf("reason is required (min 5 characters)")
	}
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	if Contains(TerminalStatuses, t.Status) {
		return nil, fmt.Errorf("ticket is already %s", t.Status)
	}
	oldStatus := t.Status
	t.Status = "closed"
	closedAt := NowISO()
	t.ClosedAt = &closedAt
	r := reason
	t.CancellationReason = &r
	addActivity(t, author, "force_closed", fmt.Sprintf("%s -> closed: %s", oldStatus, reason))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// ChangeStatus changes a ticket's status directly (bypassing the promote/demote
// path). Used for transitions the workflow has no path into — e.g. "blocked".
func ChangeStatus(repoRoot, ticketID, newStatus, author string) (*Ticket, error) {
	if err := ValidateEnum("status", newStatus, GetStatuses()); err != nil {
		return nil, err
	}

	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	oldStatus := t.Status
	if oldStatus == newStatus {
		return t, nil
	}
	t.Status = newStatus
	if newStatus == "in_progress" && t.ActualStartDate == nil {
		date := NowISO()[:10]
		t.ActualStartDate = &date
	}
	if Contains(ClosedStatuses, newStatus) {
		closedAt := NowISO()
		t.ClosedAt = &closedAt
	} else if Contains(ClosedStatuses, oldStatus) && !Contains(ClosedStatuses, newStatus) {
		t.ClosedAt = nil
	}
	addActivity(t, author, "status_changed", fmt.Sprintf("%s -> %s", oldStatus, newStatus))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// resolvePrevious finds the status before the current one by scanning the activity log.
func resolvePrevious(t *Ticket) string {
	for i := len(t.Activity) - 1; i >= 0; i-- {
		entry := t.Activity[i]
		if entry.Action == "status_changed" {
			detail := entry.Detail
			if strings.Contains(detail, " -> ") {
				parts := strings.SplitN(detail, " -> ", 2)
				prev := parts[0]
				if prev != t.Status {
					return prev
				}
			}
		}
	}
	return "not_started"
}

// ErrNeedsTimeLog is returned by Promote when leaving a work status without any
// time (with a description) logged since entering it. The API surfaces this as a
// distinct signal so the UI can pop the log-time form.
var ErrNeedsTimeLog = errors.New("log time (with a description) for this work before promoting")

// workStatuses are the statuses where actual work happens — you can't promote out
// of one without logging time for that work first. Always on, no bypass except
// force-close (which skips the workflow entirely, with a recorded reason).
var workStatuses = map[string]bool{
	"in_progress": true,
	"qa_testing":  true,
	"rework":      true,
}

// statusEnteredAt returns the ISO timestamp the ticket entered its current status
// (the most recent status_changed activity into it), falling back to created_at.
func statusEnteredAt(t *Ticket) string {
	suffix := "-> " + t.Status
	for i := len(t.Activity) - 1; i >= 0; i-- {
		a := t.Activity[i]
		if a.Action == "status_changed" && strings.HasSuffix(a.Detail, suffix) {
			return a.Timestamp
		}
	}
	return t.CreatedAt
}

// hasTimeLoggedSince reports whether any described, non-zero time entry was logged
// at/after the given ISO timestamp. ISO 8601 UTC strings compare lexically.
func hasTimeLoggedSince(t *Ticket, since string) bool {
	for _, te := range t.TimeEntries {
		if te.Hours > 0 && strings.TrimSpace(te.Description) != "" && te.LoggedAt >= since {
			return true
		}
	}
	return false
}

// Promote moves a ticket to the next status in its type's workflow.
func Promote(repoRoot, ticketID, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}

	wf, ok := GetWorkflowForType(t.Type)
	if !ok {
		return nil, fmt.Errorf("Tickets of type '%s' have no promotion workflow", t.Type)
	}

	current := t.Status
	if Contains(TerminalStatuses, current) {
		return nil, fmt.Errorf("Cannot promote from '%s' -- terminal status", current)
	}

	// Gate: leaving a work status requires time (with a description) logged since
	// entering it — so the calibration data can't go missing. force-close bypasses.
	if workStatuses[current] && !hasTimeLoggedSince(t, statusEnteredAt(t)) {
		return nil, ErrNeedsTimeLog
	}

	nextStatus, ok := wf.Promote[current]
	if !ok {
		return nil, fmt.Errorf("Cannot promote a %s from '%s' -- no promote transition defined", t.Type, current)
	}

	if nextStatus == "_previous" {
		nextStatus = resolvePrevious(t)
	}

	t.Status = nextStatus
	if nextStatus == "in_progress" && t.ActualStartDate == nil {
		date := NowISO()[:10]
		t.ActualStartDate = &date
	}
	if Contains(ClosedStatuses, nextStatus) {
		closedAt := NowISO()
		t.ClosedAt = &closedAt
	}
	addActivity(t, author, "status_changed", fmt.Sprintf("%s -> %s", current, nextStatus))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// Demote moves a ticket to the previous status in its type's workflow.
func Demote(repoRoot, ticketID, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}

	wf, ok := GetWorkflowForType(t.Type)
	if !ok {
		return nil, fmt.Errorf("Tickets of type '%s' have no promotion workflow", t.Type)
	}

	current := t.Status
	prevStatus, ok := wf.Demote[current]
	if !ok {
		return nil, fmt.Errorf("Cannot demote a %s from '%s' -- no demote transition defined", t.Type, current)
	}

	if prevStatus == "_previous" {
		prevStatus = resolvePrevious(t)
	}

	t.Status = prevStatus
	if Contains(ClosedStatuses, current) && !Contains(ClosedStatuses, prevStatus) {
		t.ClosedAt = nil
	}
	addActivity(t, author, "status_changed", fmt.Sprintf("%s -> %s", current, prevStatus))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// AssignTicket assigns a ticket to a person.
func AssignTicket(repoRoot, ticketID, assignee, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	old := "unassigned"
	if t.Assignee != nil {
		old = *t.Assignee
	}
	t.Assignee = StringPtr(assignee)
	addActivity(t, author, "assigned", fmt.Sprintf("%s -> %s", old, assignee))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// AddComment adds a comment to a ticket.
func AddComment(repoRoot, ticketID, message, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	addActivity(t, author, "comment", message)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// EditField edits a single field on a ticket.
func EditField(repoRoot, ticketID, field string, value interface{}, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}

	// Validate date fields
	if (field == "planned_start_date" || field == "due_date") && value != nil {
		if s, ok := value.(string); ok && s != "" {
			if err := ValidateDate(s); err != nil {
				return nil, err
			}
		}
	}

	oldValue := getFieldValue(t, field)
	if err := setFieldValue(t, field, value); err != nil {
		return nil, err
	}
	addActivity(t, author, "edited", fmt.Sprintf("%s: %v -> %v", field, formatFieldValue(oldValue), formatFieldValue(value)))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// getFieldValue retrieves a field value from a ticket by name.
func getFieldValue(t *Ticket, field string) interface{} {
	switch field {
	case "type":
		return t.Type
	case "title":
		return t.Title
	case "description":
		return t.Description
	case "priority":
		return t.Priority
	case "effort":
		if t.Effort != nil {
			return *t.Effort
		}
		return nil
	case "tags":
		return t.Tags
	case "phase":
		if t.Phase != nil {
			return *t.Phase
		}
		return nil
	case "assignee":
		if t.Assignee != nil {
			return *t.Assignee
		}
		return nil
	case "planned_start_date":
		if t.PlannedStartDate != nil {
			return *t.PlannedStartDate
		}
		return nil
	case "due_date":
		if t.DueDate != nil {
			return *t.DueDate
		}
		return nil
	case "defect_severity":
		if t.DefectSeverity != nil {
			return *t.DefectSeverity
		}
		return nil
	case "defect_repro_steps":
		if t.DefectReproSteps != nil {
			return *t.DefectReproSteps
		}
		return nil
	case "defect_expected_behavior":
		if t.DefectExpectedBehavior != nil {
			return *t.DefectExpectedBehavior
		}
		return nil
	case "defect_actual_behavior":
		if t.DefectActualBehavior != nil {
			return *t.DefectActualBehavior
		}
		return nil
	case "feature_acceptance_criteria":
		if t.FeatureAcceptanceCriteria != nil {
			return *t.FeatureAcceptanceCriteria
		}
		return nil
	case "meeting_attendees":
		if t.MeetingAttendees != nil {
			return *t.MeetingAttendees
		}
		return nil
	default:
		return nil
	}
}

// setFieldValue sets a field on a ticket by name.
func setFieldValue(t *Ticket, field string, value interface{}) error {
	switch field {
	case "type":
		if s, ok := value.(string); ok {
			t.Type = s
			return nil
		}
		return fmt.Errorf("type must be a string")
	case "title":
		if s, ok := value.(string); ok {
			t.Title = s
			return nil
		}
		return fmt.Errorf("title must be a string")
	case "description":
		if s, ok := value.(string); ok {
			t.Description = s
			return nil
		}
		return fmt.Errorf("description must be a string")
	case "priority":
		if s, ok := value.(string); ok {
			t.Priority = s
			return nil
		}
		return fmt.Errorf("priority must be a string")
	case "effort":
		if value == nil {
			t.Effort = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.Effort = StringPtr(s)
			return nil
		}
		return fmt.Errorf("effort must be a string")
	case "tags":
		if value == nil {
			t.Tags = []string{}
			return nil
		}
		switch v := value.(type) {
		case []string:
			t.Tags = v
			return nil
		case []interface{}:
			tags := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					tags = append(tags, s)
				}
			}
			t.Tags = tags
			return nil
		}
		return fmt.Errorf("tags must be a list of strings")
	case "phase":
		if value == nil {
			t.Phase = nil
			return nil
		}
		if s, ok := value.(string); ok {
			if s == "" {
				t.Phase = nil
			} else {
				t.Phase = StringPtr(s)
			}
			return nil
		}
		return fmt.Errorf("phase must be a string")
	case "assignee":
		if value == nil {
			t.Assignee = nil
			return nil
		}
		if s, ok := value.(string); ok {
			if s == "" {
				t.Assignee = nil
			} else {
				t.Assignee = StringPtr(s)
			}
			return nil
		}
		return fmt.Errorf("assignee must be a string")
	case "planned_start_date":
		if value == nil {
			t.PlannedStartDate = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.PlannedStartDate = StringPtr(s)
			return nil
		}
		return fmt.Errorf("planned_start_date must be a string")
	case "due_date":
		if value == nil {
			t.DueDate = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.DueDate = StringPtr(s)
			return nil
		}
		return fmt.Errorf("due_date must be a string")
	case "defect_severity":
		if value == nil {
			t.DefectSeverity = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.DefectSeverity = StringPtr(s)
			return nil
		}
		return fmt.Errorf("defect_severity must be a string")
	case "defect_repro_steps":
		if value == nil {
			t.DefectReproSteps = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.DefectReproSteps = StringPtr(s)
			return nil
		}
		return fmt.Errorf("defect_repro_steps must be a string")
	case "defect_expected_behavior":
		if value == nil {
			t.DefectExpectedBehavior = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.DefectExpectedBehavior = StringPtr(s)
			return nil
		}
		return fmt.Errorf("defect_expected_behavior must be a string")
	case "defect_actual_behavior":
		if value == nil {
			t.DefectActualBehavior = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.DefectActualBehavior = StringPtr(s)
			return nil
		}
		return fmt.Errorf("defect_actual_behavior must be a string")
	case "feature_acceptance_criteria":
		if value == nil {
			t.FeatureAcceptanceCriteria = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.FeatureAcceptanceCriteria = StringPtr(s)
			return nil
		}
		return fmt.Errorf("feature_acceptance_criteria must be a string")
	case "meeting_attendees":
		if value == nil {
			t.MeetingAttendees = nil
			return nil
		}
		if s, ok := value.(string); ok {
			t.MeetingAttendees = StringPtr(s)
			return nil
		}
		return fmt.Errorf("meeting_attendees must be a string")
	default:
		return fmt.Errorf("Unknown field: %s", field)
	}
}

// formatFieldValue formats a value for activity log detail, mimicking Python's repr.
func formatFieldValue(v interface{}) string {
	if v == nil {
		return "None"
	}
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("'%s'", val)
	case []string:
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// roundToQuarter rounds a float to the nearest 0.25.
func roundToQuarter(hours float64) float64 {
	return math.Round(hours/0.25) * 0.25
}

// AddTimeEntry adds a time entry to a ticket. extendReason is non-empty only
// when the entry was authorized past the ticket's allotment under strict time
// enforcement; it is recorded on the entry and in the activity detail.
func AddTimeEntry(repoRoot, ticketID, date string, hours float64, description, author, extendReason string) (*Ticket, error) {
	if err := ValidateDate(date); err != nil {
		return nil, err
	}
	if hours <= 0 {
		return nil, fmt.Errorf("Hours must be greater than 0")
	}
	hours = roundToQuarter(hours)

	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	entryID := fmt.Sprintf("t%d", len(t.TimeEntries)+1)
	t.TimeEntries = append(t.TimeEntries, TimeEntry{
		ID:           entryID,
		Date:         date,
		Hours:        hours,
		Description:  description,
		Author:       author,
		LoggedAt:     NowISO(),
		ExtendReason: extendReason,
	})
	detail := fmt.Sprintf("%gh on %s: %s", hours, date, description)
	if extendReason != "" {
		detail += fmt.Sprintf(" (extended past allotment — authorized: %s)", extendReason)
	}
	addActivity(t, author, "time_logged", detail)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// DeleteTimeEntry deletes a time entry from a ticket.
func DeleteTimeEntry(repoRoot, ticketID, entryID, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}

	var found *TimeEntry
	foundIdx := -1
	for i, e := range t.TimeEntries {
		if e.ID == entryID {
			found = &t.TimeEntries[i]
			foundIdx = i
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("Time entry not found: %s", entryID)
	}

	detail := fmt.Sprintf("%gh on %s: %s", found.Hours, found.Date, found.Description)
	t.TimeEntries = append(t.TimeEntries[:foundIdx], t.TimeEntries[foundIdx+1:]...)
	addActivity(t, author, "time_deleted", detail)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// AddPredecessor adds a predecessor link to a ticket.
func AddPredecessor(repoRoot, ticketID, predecessorID, author string) (*Ticket, error) {
	predPath := TicketPath(repoRoot, predecessorID)
	if _, err := os.Stat(predPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Predecessor ticket not found: %s", predecessorID)
	}

	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	if Contains(t.Predecessors, predecessorID) {
		return t, nil
	}
	t.Predecessors = append(t.Predecessors, predecessorID)
	addActivity(t, author, "predecessor_added", predecessorID)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// RemovePredecessor removes a predecessor link from a ticket.
func RemovePredecessor(repoRoot, ticketID, predecessorID, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	if !Contains(t.Predecessors, predecessorID) {
		return t, nil
	}
	newPreds := make([]string, 0, len(t.Predecessors))
	for _, p := range t.Predecessors {
		if p != predecessorID {
			newPreds = append(newPreds, p)
		}
	}
	t.Predecessors = newPreds
	addActivity(t, author, "predecessor_removed", predecessorID)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}
