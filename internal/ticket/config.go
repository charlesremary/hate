// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
)

// Resource represents a team member in the project config.
type Resource struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	GitUser string `json:"git_user"`
	Role    string `json:"role"`
	// DailyHoursAvailable is the per-day capacity used by Check Schedule.
	// nil → assume DefaultDailyHours (8) so existing resources keep working
	// without migration.
	DailyHoursAvailable *float64 `json:"daily_hours_available,omitempty"`
}

// DefaultDailyHours is the assumed daily capacity for a resource that has no
// explicit daily_hours_available set.
const DefaultDailyHours = 8.0

// EffectiveDailyHours returns the resource's daily capacity, falling back to
// DefaultDailyHours when unset.
func (r Resource) EffectiveDailyHours() float64 {
	if r.DailyHoursAvailable != nil && *r.DailyHoursAvailable > 0 {
		return *r.DailyHoursAvailable
	}
	return DefaultDailyHours
}

// GitIdentity holds the git user identity for a project.
type GitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// TypeWorkflow defines the promote/demote transitions for one ticket type.
// A status mapped to "_previous" returns the ticket to whatever status it held
// before the current one (resolved from the activity log).
type TypeWorkflow struct {
	Promote map[string]string
	Demote  map[string]string
}

// ProjectConfig represents the .tkt/config.json file.
//
// Note: promotion workflows are no longer stored per-project — they are built in
// per ticket type (see TypeWorkflows). Any "workflow" block in an older config
// file is simply ignored on read and dropped on the next write.
type ProjectConfig struct {
	SchemaVersion string         `json:"schema_version"`
	Client        string         `json:"client"`
	ProjectName   string         `json:"project_name"`
	ProjectID     string         `json:"project_id"`
	Prefix        string         `json:"prefix"`
	NextSequence  int            `json:"next_sequence,omitempty"` // Legacy, kept for backward compat
	EffortToDays  map[string]int `json:"effort_to_days"`
	Repos         []string       `json:"repos"`
	AutoPush      bool           `json:"auto_push"`
	Resources     []Resource     `json:"resources"`
	GitIdentityV  *GitIdentity   `json:"git_identity,omitempty"`
	// ClosedAt is the ISO date (YYYY-MM-DD) the project was closed. Empty means
	// open. While closed, ticket-write endpoints reject mutations and the
	// project is filtered out of the sidebar by default.
	ClosedAt string `json:"closed_at,omitempty"`
	// EstimateHPerCFP / EstimateWrapPct hold the manual "initial estimate" inputs
	// for the COSMIC tab: a borrowed code rate (hours per CFP) and wrap %, used to
	// project total project hours from the total CFP before actuals exist. nil =
	// unset (no estimate entered yet).
	EstimateHPerCFP *float64 `json:"estimate_h_per_cfp,omitempty"`
	EstimateWrapPct *float64 `json:"estimate_wrap_pct,omitempty"`
}

// IsClosed reports whether the project's ClosedAt field is set.
func (c *ProjectConfig) IsClosed() bool {
	return c != nil && c.ClosedAt != ""
}

// DefaultEffortToDays maps effort sizes to estimated days.
var DefaultEffortToDays = map[string]int{
	"xs": 1,
	"s":  2,
	"m":  3,
	"l":  5,
	"xl": 8,
}

// ClosedStatuses stamp closed_at when reached; TerminalStatuses cannot be
// promoted from. Both are global — independent of ticket type.
var (
	ClosedStatuses   = []string{"complete", "closed"}
	TerminalStatuses = []string{"closed"}
)

// TypeWorkflows holds the built-in promotion workflow for each ticket type that
// has one. Types not present here (meeting, administration) auto-complete on
// creation and have no promote/demote path. "_previous" returns a blocked ticket
// to its prior status.
var TypeWorkflows = map[string]TypeWorkflow{
	// Generic task: a short path with no dev/QA cycle.
	"task": {
		Promote: map[string]string{
			"not_started": "in_progress",
			"in_progress": "complete",
			"complete":    "closed",
			"blocked":     "_previous",
		},
		Demote: map[string]string{
			"in_progress": "not_started",
			"complete":    "in_progress",
			"blocked":     "_previous",
		},
	},
	// Dev task: full dev + QA cycle, with a rework loop off QA testing.
	"dev_task": {
		Promote: map[string]string{
			"not_started":  "in_progress",
			"in_progress":  "dev_complete",
			"dev_complete": "qa_testing",
			"qa_testing":   "complete",
			"complete":     "closed",
			"rework":       "qa_testing",
			"blocked":      "_previous",
		},
		Demote: map[string]string{
			"in_progress":  "not_started",
			"dev_complete": "in_progress",
			"qa_testing":   "rework",
			"complete":     "qa_testing",
			"blocked":      "_previous",
		},
	},
	// Design task: a review + approval cycle ending in closed.
	"design_task": {
		Promote: map[string]string{
			"not_started":          "in_progress",
			"in_progress":          "submitted_for_review",
			"submitted_for_review": "approved",
			"approved":             "closed",
			"blocked":              "_previous",
		},
		Demote: map[string]string{
			"in_progress":          "not_started",
			"submitted_for_review": "in_progress",
			"approved":             "submitted_for_review",
			"blocked":              "_previous",
		},
	},
}

// GetWorkflowForType returns the built-in promotion workflow for a ticket type.
// The second return value is false for types with no workflow (meeting,
// administration), which auto-complete on creation.
func GetWorkflowForType(ticketType string) (TypeWorkflow, bool) {
	wf, ok := TypeWorkflows[ticketType]
	return wf, ok
}

// DefaultConfig returns a new default project config matching Python's default_config().
func DefaultConfig(client, projectName, projectID, prefix string) *ProjectConfig {
	if prefix == "" {
		prefix = "TKT"
	}
	etd := make(map[string]int)
	for k, v := range DefaultEffortToDays {
		etd[k] = v
	}
	return &ProjectConfig{
		SchemaVersion: SchemaVersion,
		Client:        client,
		ProjectName:   projectName,
		ProjectID:     projectID,
		Prefix:        prefix,
		EffortToDays:  etd,
		Repos:         []string{},
		AutoPush:      false,
		Resources:     []Resource{},
	}
}

// ConfigPath returns the path to .tkt/config.json in the given repo root.
func ConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".tkt", "config.json")
}

// ReadConfig reads and parses the project config from .tkt/config.json.
func ReadConfig(repoRoot string) (*ProjectConfig, error) {
	path := ConfigPath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return &cfg, nil
}

// WriteConfig writes the project config to .tkt/config.json.
func WriteConfig(repoRoot string, cfg *ProjectConfig) error {
	path := ConfigPath(repoRoot)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// NextTicketID allocates the next ticket ID by incrementing the sequence counter.
const base36Chars = "0123456789abcdefghijklmnopqrstuvwxyz"

// GenerateTicketID creates a random base36 ticket ID like "AMPL-7k3x".
// Checks for collisions against existing ticket files, retries up to 10 times.
func GenerateTicketID(repoRoot string) (string, error) {
	cfg, err := ReadConfig(repoRoot)
	if err != nil {
		return "", err
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "TKT"
	}
	tdir := TicketsDir(repoRoot)
	for attempt := 0; attempt < 10; attempt++ {
		suffix := make([]byte, 4)
		for i := range suffix {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(base36Chars))))
			if err != nil {
				return "", fmt.Errorf("random generation failed: %w", err)
			}
			suffix[i] = base36Chars[n.Int64()]
		}
		ticketID := fmt.Sprintf("%s-%s", prefix, string(suffix))
		// Check for collision
		path := filepath.Join(tdir, ticketID+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return ticketID, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique ticket ID after 10 attempts")
}

// GetStatuses returns the full list of valid ticket statuses. The status set is
// global; promotion *paths* through it are what vary by ticket type.
func GetStatuses() []string {
	return Statuses
}
