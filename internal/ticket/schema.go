// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"fmt"
	"regexp"
	"time"
)

// SchemaVersion is the current schema version for tickets and config.
const SchemaVersion = "1.0.0"

// Enum slices matching the Python system exactly.
var (
	TicketTypes      = []string{"task", "dev_task", "design_task", "meeting", "administration"}
	Statuses         = []string{"not_started", "in_progress", "dev_complete", "qa_testing", "submitted_for_review", "approved", "complete", "closed", "rework", "blocked"}
	Priorities       = []string{"critical", "high", "medium", "low"}
	Efforts          = []string{"xs", "s", "m", "l", "xl"}
	DefectSeverities = []string{"critical", "major", "minor", "trivial"}
	AutoCompleteTypes = []string{"meeting", "administration"}

	ActivityActions = []string{
		"created",
		"status_changed",
		"comment",
		"assigned",
		"priority_changed",
		"effort_changed",
		"predecessor_added",
		"predecessor_removed",
		"tag_added",
		"tag_removed",
		"edited",
		"time_logged",
		"time_deleted",
	}

	// TypeSpecificFields maps ticket types to their type-specific field names.
	TypeSpecificFields = map[string][]string{
		"task":           {},
		"dev_task":       {},
		"design_task":    {},
		"meeting":        {"meeting_attendees"},
		"administration": {},
	}

	// EditableFields maps field names to their allowed enum values (nil means free-text).
	EditableFields = map[string][]string{
		"title":              nil,
		"description":        nil,
		"priority":           Priorities,
		"effort":             Efforts,
		"tags":               nil,
		"planned_start_date": nil,
		"due_date":           nil,
		"meeting_attendees":  nil,
	}
)

// BacklogTag marks a ticket as backlog — present in the project but out of
// committed scope. Backlog tickets are excluded from completion %, the
// baseline/schedule, and capacity checks. Removing the tag commits the ticket.
const BacklogTag = "backlog"

// IsBacklog reports whether the ticket carries the backlog tag.
func IsBacklog(t *Ticket) bool {
	for _, tag := range t.Tags {
		if tag == BacklogTag {
			return true
		}
	}
	return false
}

// dateRE validates YYYY-MM-DD format.
var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Activity represents a single activity log entry on a ticket.
type Activity struct {
	Timestamp string `json:"timestamp"`
	Author    string `json:"author"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
}

// TimeEntry represents a logged time entry on a ticket.
type TimeEntry struct {
	ID          string  `json:"id"`
	Date        string  `json:"date"`
	Hours       float64 `json:"hours"`
	Description string  `json:"description"`
	Author      string  `json:"author"`
	LoggedAt    string  `json:"logged_at"`
}

// Attachment represents a file attached to a ticket. The file itself is stored
// under <repo>/attachments/<ticket_id>/<attachment_id>-<filename> and committed
// to the project repo alongside the ticket JSON.
type Attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	UploadedAt  string `json:"uploaded_at"`
	UploadedBy  string `json:"uploaded_by"`
}

// Ticket is the unified ticket struct matching the Python flat schema exactly.
type Ticket struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Status        string   `json:"status"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Priority      string   `json:"priority"`
	Effort        *string  `json:"effort"`
	Tags          []string `json:"tags"`
	Phase         *string  `json:"phase"`
	Assignee      *string  `json:"assignee"`
	Creator       string   `json:"creator"`
	Predecessors  []string `json:"predecessors"`
	Repo          *string  `json:"repo"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	ClosedAt      *string  `json:"closed_at"`

	PlannedStartDate *string `json:"planned_start_date"`
	ActualStartDate  *string `json:"actual_start_date"`
	DueDate          *string `json:"due_date"`

	// Defect-specific fields
	DefectSeverity         *string `json:"defect_severity"`
	DefectReproSteps       *string `json:"defect_repro_steps"`
	DefectExpectedBehavior *string `json:"defect_expected_behavior"`
	DefectActualBehavior   *string `json:"defect_actual_behavior"`

	// Feature-specific fields
	FeatureAcceptanceCriteria *string `json:"feature_acceptance_criteria"`

	// Meeting-specific fields
	MeetingAttendees *string `json:"meeting_attendees"`

	// Time tracking
	TimeEntries []TimeEntry `json:"time_entries"`

	// Activity log
	Activity []Activity `json:"activity"`

	// Attachments are files uploaded against this ticket.
	Attachments []Attachment `json:"attachments"`

	// CancellationReason is set when a ticket is force-closed (skipped through
	// the workflow). Empty for normal completions.
	CancellationReason *string `json:"cancellation_reason,omitempty"`
}

// NowISO returns the current UTC time in ISO 8601 format matching Python's output.
func NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// ValidateDate validates a YYYY-MM-DD date string.
func ValidateDate(s string) error {
	if !dateRE.MatchString(s) {
		return fmt.Errorf("Invalid date format: '%s'. Expected YYYY-MM-DD", s)
	}
	_, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fmt.Errorf("Invalid date format: '%s'. Expected YYYY-MM-DD", s)
	}
	return nil
}

// ValidateEnum checks that value is in the allowed slice.
func ValidateEnum(field, value string, allowed []string) error {
	for _, a := range allowed {
		if a == value {
			return nil
		}
	}
	return fmt.Errorf("Invalid %s: '%s'. Must be one of: %s", field, value, joinStrings(allowed, ", "))
}

// BlankTicket creates a new ticket with all fields initialized, matching Python's blank_ticket().
func BlankTicket(id, ticketType, title, creator string) *Ticket {
	ts := NowISO()
	return &Ticket{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Type:          ticketType,
		Status:        "not_started",
		Title:         title,
		Description:   "",
		Priority:      "medium",
		Effort:        nil,
		Tags:          []string{},
		Phase:         nil,
		Assignee:      nil,
		Creator:       creator,
		Predecessors:  []string{},
		Repo:          nil,
		CreatedAt:     ts,
		UpdatedAt:     ts,
		ClosedAt:      nil,

		PlannedStartDate: nil,
		ActualStartDate:  nil,
		DueDate:          nil,

		DefectSeverity:         nil,
		DefectReproSteps:       nil,
		DefectExpectedBehavior: nil,
		DefectActualBehavior:   nil,

		FeatureAcceptanceCriteria: nil,

		MeetingAttendees: nil,

		TimeEntries: []TimeEntry{},
		Attachments: []Attachment{},
		Activity: []Activity{
			{
				Timestamp: ts,
				Author:    creator,
				Action:    "created",
				Detail:    "",
			},
		},
	}
}

// ValidateTicket validates a ticket and returns a list of error strings.
// An empty list means the ticket is valid.
func ValidateTicket(t *Ticket) []string {
	var errors []string

	if t.SchemaVersion == "" {
		errors = append(errors, "Missing required field: schema_version")
	}
	if t.ID == "" {
		errors = append(errors, "Missing required field: id")
	}
	if t.Type == "" {
		errors = append(errors, "Missing required field: type")
	}
	if t.Status == "" {
		errors = append(errors, "Missing required field: status")
	}
	if t.Title == "" {
		errors = append(errors, "Missing required field: title")
	}
	if t.CreatedAt == "" {
		errors = append(errors, "Missing required field: created_at")
	}
	if t.UpdatedAt == "" {
		errors = append(errors, "Missing required field: updated_at")
	}

	if len(errors) == 0 {
		if !Contains(TicketTypes, t.Type) {
			errors = append(errors, fmt.Sprintf("Invalid type: '%s'. Must be one of: %s", t.Type, joinStrings(TicketTypes, ", ")))
		}
		if !Contains(Statuses, t.Status) {
			errors = append(errors, fmt.Sprintf("Invalid status: '%s'. Must be one of: %s", t.Status, joinStrings(Statuses, ", ")))
		}
		if t.Priority != "" && !Contains(Priorities, t.Priority) {
			errors = append(errors, fmt.Sprintf("Invalid priority: '%s'", t.Priority))
		}
		if t.Effort != nil && *t.Effort != "" && !Contains(Efforts, *t.Effort) {
			errors = append(errors, fmt.Sprintf("Invalid effort: '%s'", *t.Effort))
		}
		if t.DefectSeverity != nil && *t.DefectSeverity != "" && !Contains(DefectSeverities, *t.DefectSeverity) {
			errors = append(errors, fmt.Sprintf("Invalid defect_severity: '%s'", *t.DefectSeverity))
		}
	}

	return errors
}

// IsAutoCompleteType returns true if the ticket type auto-completes on creation.
func IsAutoCompleteType(t string) bool {
	return Contains(AutoCompleteTypes, t)
}

// Contains checks if a string slice contains the given item.
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// joinStrings joins a slice with a separator.
func joinStrings(slice []string, sep string) string {
	result := ""
	for i, s := range slice {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

// StringPtr returns a pointer to the given string.
func StringPtr(s string) *string {
	return &s
}
