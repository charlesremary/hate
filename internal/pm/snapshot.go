// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"hate/internal/ticket"
)

// ---------------------------------------------------------------------------
// PM artifact paths
// ---------------------------------------------------------------------------

// PMDir returns the path to the PM artifacts directory: <projectRoot>/.tkt/pm/
func PMDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".tkt", "pm")
}

// BaselinePath returns the path to baseline.json.
func BaselinePath(projectRoot string) string {
	return filepath.Join(PMDir(projectRoot), "baseline.json")
}

// SlipEventsPath returns the path to slip_events.json.
func SlipEventsPath(projectRoot string) string {
	return filepath.Join(PMDir(projectRoot), "slip_events.json")
}

// SnapshotsDir returns the path to the snapshots directory.
func SnapshotsDir(projectRoot string) string {
	return filepath.Join(PMDir(projectRoot), "snapshots")
}

// LatestSnapshotPath returns the path to the most recent snapshot file, or empty string if none exist.
func LatestSnapshotPath(projectRoot string) string {
	sdir := SnapshotsDir(projectRoot)
	entries, err := os.ReadDir(sdir)
	if err != nil {
		return ""
	}
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			jsonFiles = append(jsonFiles, filepath.Join(sdir, e.Name()))
		}
	}
	if len(jsonFiles) == 0 {
		return ""
	}
	sort.Strings(jsonFiles)
	return jsonFiles[len(jsonFiles)-1]
}

// LoadLatestSnapshot loads the most recent snapshot, or returns nil if none exist.
func LoadLatestSnapshot(projectRoot string) (*Snapshot, error) {
	path := LatestSnapshotPath(projectRoot)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot: %w", err)
	}
	return &snap, nil
}

// ---------------------------------------------------------------------------
// Status mapping: tkt -> pm_engine
// ---------------------------------------------------------------------------

var tktToPMStatus = map[string]string{
	"not_started":          "not_started",
	"in_progress":          "in_progress",
	"dev_complete":         "in_progress",
	"qa_testing":           "in_progress",
	"submitted_for_review": "in_progress",
	"approved":             "in_progress",
	"rework":               "in_progress",
	"blocked":              "blocked",
	"complete":             "complete",
	"closed":               "complete",
}

// TktTicketsToPMFormat converts tkt Ticket structs to the map format pm_engine expects.
func TktTicketsToPMFormat(tickets []*ticket.Ticket) map[string]map[string]interface{} {
	result := make(map[string]map[string]interface{})
	for _, t := range tickets {
		owner := ""
		if t.Assignee != nil {
			owner = *t.Assignee
		}
		if owner == "" {
			owner = t.Creator
		}

		status := t.Status
		if mapped, ok := tktToPMStatus[status]; ok {
			status = mapped
		}

		var dueDate interface{}
		if t.DueDate != nil {
			dueDate = *t.DueDate
		}

		var actualStart interface{}
		if t.ActualStartDate != nil {
			actualStart = *t.ActualStartDate
		}

		var actualEnd interface{}
		if t.ClosedAt != nil && len(*t.ClosedAt) >= 10 {
			s := (*t.ClosedAt)[:10]
			actualEnd = s
		} else if status == "complete" && len(t.UpdatedAt) >= 10 {
			// Fallback: use updated_at for completed tasks without closed_at
			actualEnd = t.UpdatedAt[:10]
		}

		result[t.ID] = map[string]interface{}{
			"task_id":      t.ID,
			"title":        t.Title,
			"owner":        owner,
			"status":       status,
			"due_date":     dueDate,
			"actual_start": actualStart,
			"actual_end":   actualEnd,
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Read all tickets from disk
// ---------------------------------------------------------------------------

// ReadAllTickets reads all ticket JSON files from the tickets/ directory.
func ReadAllTickets(projectRoot string) ([]*ticket.Ticket, error) {
	ticketsDir := filepath.Join(projectRoot, "tickets")
	entries, err := os.ReadDir(ticketsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read tickets directory: %w", err)
	}

	var tickets []*ticket.Ticket
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	sort.Strings(jsonFiles)

	for _, name := range jsonFiles {
		path := filepath.Join(ticketsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read ticket %s: %w", name, err)
		}
		var ticket ticket.Ticket
		if err := json.Unmarshal(data, &ticket); err != nil {
			return nil, fmt.Errorf("failed to parse ticket %s: %w", name, err)
		}
		tickets = append(tickets, &ticket)
	}
	return tickets, nil
}

// ---------------------------------------------------------------------------
// Run snapshot
// ---------------------------------------------------------------------------

// RunSnapshot builds and writes a new snapshot from tkt ticket files.
func RunSnapshot(projectID, projectRoot string) (*Snapshot, error) {
	today := time.Now()
	todayStr := today.Format("2006-01-02")

	// Load baseline
	bp := BaselinePath(projectRoot)
	data, err := os.ReadFile(bp)
	if err != nil {
		return nil, fmt.Errorf("no baseline found at %s. Run WBS generator first to create baseline.json", bp)
	}
	var baseline Baseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, fmt.Errorf("failed to parse baseline: %w", err)
	}

	// Read current tickets
	tickets, err := ReadAllTickets(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read tickets: %w", err)
	}
	currentTasks := TktTicketsToPMFormat(tickets)

	// Load existing slip events
	sep := SlipEventsPath(projectRoot)
	var slipEvents []SlipEvent
	if sepData, err := os.ReadFile(sep); err == nil {
		if err := json.Unmarshal(sepData, &slipEvents); err != nil {
			slipEvents = []SlipEvent{}
		}
	}

	// Tag baseline tasks with project_id
	for i := range baseline.Tasks {
		baseline.Tasks[i].ProjectID = projectID
	}

	// Detect new slip events
	newEvents := DetectSlipEvents(baseline.Tasks, currentTasks, slipEvents)
	if len(newEvents) > 0 {
		slipEvents = append(slipEvents, newEvents...)
		if err := os.MkdirAll(filepath.Dir(sep), 0755); err != nil {
			return nil, fmt.Errorf("failed to create slip events directory: %w", err)
		}
		slipData, err := json.MarshalIndent(slipEvents, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal slip events: %w", err)
		}
		if err := os.WriteFile(sep, slipData, 0644); err != nil {
			return nil, fmt.Errorf("failed to write slip events: %w", err)
		}
	}

	// Build snapshot
	snapshot := BuildSnapshot(baseline, currentTasks, slipEvents, today)

	// Enrich with critical path
	EnrichSnapshotWithCriticalPath(&snapshot)

	// Write snapshot
	sdir := SnapshotsDir(projectRoot)
	if err := os.MkdirAll(sdir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create snapshots directory: %w", err)
	}
	outPath := filepath.Join(sdir, todayStr+".json")
	snapData, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal snapshot: %w", err)
	}
	if err := os.WriteFile(outPath, snapData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write snapshot: %w", err)
	}

	return &snapshot, nil
}

// ---------------------------------------------------------------------------
// Create baseline from tickets
// ---------------------------------------------------------------------------

// CreateBaselineFromTickets creates a baseline.json from the current state of tkt tickets.
func CreateBaselineFromTickets(projectRoot, projectID, projectName, createdBy string) (*Baseline, error) {
	bp := BaselinePath(projectRoot)
	if _, err := os.Stat(bp); err == nil {
		return nil, fmt.Errorf("baseline already exists. Cannot re-baseline")
	}

	tickets, err := ReadAllTickets(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read tickets: %w", err)
	}
	// Backlog tickets are out of committed scope — never part of the baseline,
	// so they don't affect the projected end date, slip, or health.
	committed := make([]*ticket.Ticket, 0, len(tickets))
	for _, t := range tickets {
		if !ticket.IsBacklog(t) {
			committed = append(committed, t)
		}
	}
	tickets = committed
	if len(tickets) == 0 {
		return nil, fmt.Errorf("no committed tickets to baseline (all are backlog). Untag some, or create non-backlog tickets first")
	}

	// Read config for effort_to_days
	effortToDays := map[string]float64{"xs": 1, "s": 2, "m": 3, "l": 5, "xl": 8}
	cfg, err := ticket.ReadConfig(projectRoot)
	if err == nil && cfg.EffortToDays != nil {
		effortToDays = cfg.EffortToDays
	}

	today := time.Now()
	todayStr := today.Format("2006-01-02")

	var baselineTasks []BaselineTask
	for _, t := range tickets {
		taskID := t.ID

		// Determine planned start
		plannedStartStr := todayStr
		if t.ActualStartDate != nil && *t.ActualStartDate != "" {
			plannedStartStr = *t.ActualStartDate
		} else if t.PlannedStartDate != nil && *t.PlannedStartDate != "" {
			plannedStartStr = *t.PlannedStartDate
		}

		// Determine planned days from effort or default. Effort-days may be
		// fractional (quarter-day granularity), but the baseline schedules on
		// whole calendar days (AddDate / business-day loops), so round to the
		// nearest day, floored at 1 so a sized ticket always spans a day.
		plannedDays := 5
		if t.Effort != nil && *t.Effort != "" {
			if d, ok := effortToDays[*t.Effort]; ok {
				plannedDays = int(math.Round(d))
				if plannedDays < 1 {
					plannedDays = 1
				}
			}
		}

		// Determine planned end
		var plannedEndStr string
		if t.DueDate != nil && *t.DueDate != "" {
			plannedEndStr = *t.DueDate
			// Recalculate planned_days from date range
			ps := parseDate(plannedStartStr)
			pe := parseDate(plannedEndStr)
			if !ps.IsZero() && !pe.IsZero() {
				d := int(pe.Sub(ps).Hours() / 24)
				if d < 1 {
					d = 1
				}
				plannedDays = d
			}
		} else {
			ps := parseDate(plannedStartStr)
			if !ps.IsZero() {
				plannedEndStr = fmtDate(ps.AddDate(0, 0, plannedDays))
			} else {
				plannedEndStr = fmtDate(today.AddDate(0, 0, plannedDays))
			}
		}

		deps := t.Predecessors
		if deps == nil {
			deps = []string{}
		}

		owner := createdBy
		if t.Assignee != nil && *t.Assignee != "" {
			owner = *t.Assignee
		} else if t.Creator != "" {
			owner = t.Creator
		}

		baselineTasks = append(baselineTasks, BaselineTask{
			TaskID:       taskID,
			Title:        t.Title,
			Owner:        owner,
			PlannedStart: plannedStartStr,
			PlannedEnd:   plannedEndStr,
			PlannedDays:  plannedDays,
			Dependencies: deps,
			IsMilestone:  false,
		})
	}

	// Compute project-level dates
	projectStart := todayStr
	projectEnd := todayStr
	if len(baselineTasks) > 0 {
		allStarts := make([]string, len(baselineTasks))
		allEnds := make([]string, len(baselineTasks))
		for i, t := range baselineTasks {
			allStarts[i] = t.PlannedStart
			allEnds[i] = t.PlannedEnd
		}
		sort.Strings(allStarts)
		sort.Strings(allEnds)
		projectStart = allStarts[0]
		projectEnd = allEnds[len(allEnds)-1]
	}

	baseline := &Baseline{
		CreatedDate:  todayStr,
		CreatedBy:    createdBy,
		ProjectID:    projectID,
		ProjectName:  projectName,
		ProjectType:  "ad-hoc",
		PlannedStart: projectStart,
		PlannedEnd:   projectEnd,
		Tasks:        baselineTasks,
	}

	// Write baseline
	if err := os.MkdirAll(filepath.Dir(bp), 0755); err != nil {
		return nil, fmt.Errorf("failed to create PM directory: %w", err)
	}
	bdata, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal baseline: %w", err)
	}
	bdata = append(bdata, '\n')
	if err := os.WriteFile(bp, bdata, 0644); err != nil {
		return nil, fmt.Errorf("failed to write baseline: %w", err)
	}

	return baseline, nil
}
