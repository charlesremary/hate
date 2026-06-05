// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// TemplateTask represents a single task in a WBS template.
type TemplateTask struct {
	TemplateTaskID string   `json:"template_task_id"`
	Title          string   `json:"title"`
	BaselineDays   int      `json:"baseline_days"`
	Sequence       int      `json:"sequence"`
	Dependencies   []string `json:"dependencies"`
	Notes          string   `json:"notes"`
	OwnerTeam      string   `json:"owner_team"`
	IsMilestone    bool     `json:"is_milestone,omitempty"`
}

// Template represents a WBS template.
type Template struct {
	TemplateID      string         `json:"template_id"`
	TemplateVersion string         `json:"template_version"`
	DisplayName     string         `json:"display_name"`
	Description     string         `json:"description"`
	Tasks           []TemplateTask `json:"tasks"`
}

// KickoffParams holds the parameters for a WBS kickoff.
type KickoffParams struct {
	ProjectID           string
	ProjectName         string
	TemplateID          string
	StartDate           time.Time
	OwnerAssignments    map[string]interface{}
	DurationAdjustments map[string]interface{}
	CreatedBy           string
}

// ---------------------------------------------------------------------------
// Business day helpers
// ---------------------------------------------------------------------------

func addBusinessDays(start time.Time, days int) time.Time {
	current := start
	added := 0
	for added < days {
		current = current.AddDate(0, 0, 1)
		if current.Weekday() != time.Saturday && current.Weekday() != time.Sunday {
			added++
		}
	}
	return current
}

// ---------------------------------------------------------------------------
// Schedule computation
// ---------------------------------------------------------------------------

type scheduledTask struct {
	TemplateTaskID string
	StartDate      time.Time
	EndDate        time.Time
	PlannedDays    int
	Owner          string
}

func computeSchedule(templateTasks []TemplateTask, params KickoffParams) map[string]*scheduledTask {
	scheduled := map[string]*scheduledTask{}

	// Sort by sequence
	sorted := make([]TemplateTask, len(templateTasks))
	copy(sorted, templateTasks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Sequence < sorted[j].Sequence
	})

	for _, tmplTask := range sorted {
		ttid := tmplTask.TemplateTaskID
		deps := tmplTask.Dependencies

		var earliestStart time.Time
		if len(deps) > 0 {
			var depEndDates []time.Time
			for _, dep := range deps {
				if s, ok := scheduled[dep]; ok {
					depEndDates = append(depEndDates, s.EndDate)
				}
			}
			if len(depEndDates) > 0 {
				// Find max
				maxEnd := depEndDates[0]
				for _, d := range depEndDates[1:] {
					if d.After(maxEnd) {
						maxEnd = d
					}
				}
				earliestStart = maxEnd.AddDate(0, 0, 1)
				// Skip weekends
				for earliestStart.Weekday() == time.Saturday || earliestStart.Weekday() == time.Sunday {
					earliestStart = earliestStart.AddDate(0, 0, 1)
				}
			} else {
				earliestStart = params.StartDate
			}
		} else {
			earliestStart = params.StartDate
		}

		baseDays := tmplTask.BaselineDays
		if baseDays <= 0 {
			baseDays = 3
		}

		adjustment := 1.0
		if params.DurationAdjustments != nil {
			if adj, ok := params.DurationAdjustments[ttid]; ok {
				switch v := adj.(type) {
				case float64:
					adjustment = v
				case int:
					adjustment = float64(v)
				}
			}
		}
		adjustedDays := int(float64(baseDays)*adjustment + 0.5)
		if adjustedDays < 1 {
			adjustedDays = 1
		}

		endDate := addBusinessDays(earliestStart, adjustedDays-1)

		owner := params.CreatedBy
		if params.OwnerAssignments != nil {
			if o, ok := params.OwnerAssignments[ttid]; ok {
				if s, ok := o.(string); ok {
					owner = s
				}
			}
		}

		scheduled[ttid] = &scheduledTask{
			TemplateTaskID: ttid,
			StartDate:      earliestStart,
			EndDate:        endDate,
			PlannedDays:    adjustedDays,
			Owner:          owner,
		}
	}

	return scheduled
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// RunWBS generates a WBS from a template and writes baseline.json. Returns the baseline.
func RunWBS(params KickoffParams, projectRoot, templateDir string) (*Baseline, error) {
	outputDir := PMDir(projectRoot)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create PM directory: %w", err)
	}

	// Load template
	templatePath := filepath.Join(templateDir, params.TemplateID+".json")
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("template not found: %s", templatePath)
	}
	var tmpl Template
	if err := json.Unmarshal(templateData, &tmpl); err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	// Compute schedule
	scheduled := computeSchedule(tmpl.Tasks, params)

	// Build template lookup
	templateByID := map[string]TemplateTask{}
	for _, t := range tmpl.Tasks {
		templateByID[t.TemplateTaskID] = t
	}

	// Order by start date
	type taskEntry struct {
		sched *scheduledTask
		tmpl  TemplateTask
	}
	var ordered []taskEntry
	for ttid, sched := range scheduled {
		ordered = append(ordered, taskEntry{sched: sched, tmpl: templateByID[ttid]})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].sched.StartDate.Before(ordered[j].sched.StartDate)
	})

	// Assign ticket IDs
	type ticketEntry struct {
		TaskID               string
		Title                string
		Owner                string
		StartDate            time.Time
		EndDate              time.Time
		PlannedDays          int
		IsMilestone          bool
		TemplateTaskID       string
		ResolvedDependencies []string
	}

	tmplToTicket := map[string]string{}
	var tickets []ticketEntry
	for i, entry := range ordered {
		taskID := fmt.Sprintf("%s-%d", params.ProjectID, i+1)
		tmplToTicket[entry.sched.TemplateTaskID] = taskID
		tickets = append(tickets, ticketEntry{
			TaskID:               taskID,
			Title:                entry.tmpl.Title,
			Owner:                entry.sched.Owner,
			StartDate:            entry.sched.StartDate,
			EndDate:              entry.sched.EndDate,
			PlannedDays:          entry.sched.PlannedDays,
			IsMilestone:          entry.tmpl.IsMilestone,
			TemplateTaskID:       entry.sched.TemplateTaskID,
			ResolvedDependencies: entry.tmpl.Dependencies,
		})
	}

	// Resolve template dep IDs to ticket IDs
	for i := range tickets {
		var resolved []string
		for _, dep := range tickets[i].ResolvedDependencies {
			if tid, ok := tmplToTicket[dep]; ok {
				resolved = append(resolved, tid)
			} else {
				resolved = append(resolved, dep)
			}
		}
		if resolved == nil {
			resolved = []string{}
		}
		tickets[i].ResolvedDependencies = resolved
	}

	// Build baseline tasks
	var baselineTasks []BaselineTask
	var maxEnd time.Time
	for _, t := range tickets {
		if t.EndDate.After(maxEnd) {
			maxEnd = t.EndDate
		}
		baselineTasks = append(baselineTasks, BaselineTask{
			TaskID:         t.TaskID,
			Title:          t.Title,
			Owner:          t.Owner,
			PlannedStart:   fmtDate(t.StartDate),
			PlannedEnd:     fmtDate(t.EndDate),
			PlannedDays:    t.PlannedDays,
			Dependencies:   t.ResolvedDependencies,
			IsMilestone:    t.IsMilestone,
			TemplateTaskID: t.TemplateTaskID,
		})
	}

	baseline := &Baseline{
		CreatedDate: fmtDate(time.Now()),
		CreatedBy:   params.CreatedBy,
		ProjectID:   params.ProjectID,
		ProjectName: params.ProjectName,
		ProjectType: params.TemplateID,
		PlannedStart: fmtDate(params.StartDate),
		PlannedEnd:  fmtDate(maxEnd),
		Tasks:       baselineTasks,
	}

	// Write baseline
	outPath := filepath.Join(outputDir, "baseline.json")
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal baseline: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write baseline: %w", err)
	}

	return baseline, nil
}
