// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package service holds the ticket operations shared by every transport (the
// REST API in internal/api and the MCP server). Each mutating function is the
// project lock's outermost entry point (see internal/ticket/lock.go): it takes
// ticket.LockProject once, refuses writes to a closed project, calls the
// internal/ticket mutator, regenerates the index and auto-commits. Callers
// must not hold the lock.
package service

import (
	"math"
	"strings"

	"hate/internal/config"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// Result is a mutated ticket plus the auto-commit warning ("" when the commit
// succeeded; the change is saved either way).
type Result struct {
	Ticket        *ticket.Ticket
	CommitWarning string
}

// ResolveProject finds a project's root folder by project id or folder name.
func ResolveProject(ref string) (string, error) {
	path, err := config.GetProjectPath(ref)
	if err != nil {
		return "", &Error{Kind: ErrNotFound, Msg: "Project not found: " + ref, Cause: err}
	}
	return path, nil
}

// checkOpen refuses writes to a closed project. The caller holds the lock.
func checkOpen(root string) error {
	if cfg, err := ticket.ReadConfig(root); err == nil && cfg.IsClosed() {
		return &Error{Kind: ErrProjectClosed, Msg: ErrProjectClosed.Error()}
	}
	return nil
}

// commit commits the ticket file (index.json is derived and never committed)
// and returns the commit warning. The caller holds the lock.
func commit(root, ticketID, action string) string {
	return ticket.CommitWarning(ticket.CommitFiles(root, []string{ticket.TicketPath(root, ticketID)}, ticketID+": "+action))
}

// mutate runs one ticket mutation as a lock entry point. fn returns the
// changed ticket and the commit action ("time logged", "status → done", ...).
// regen regenerates index.json before committing.
func mutate(root, ticketID string, regen bool, fn func() (*ticket.Ticket, string, error)) (*Result, error) {
	defer ticket.LockProject(root)()
	if err := checkOpen(root); err != nil {
		return nil, err
	}
	tk, action, err := fn()
	if err != nil {
		return nil, err
	}
	if regen {
		if err := ticket.RegenerateIndex(root); err != nil {
			return nil, wrap(ErrInternal, err)
		}
	}
	if ticketID == "" {
		ticketID = tk.ID
	}
	return &Result{Ticket: tk, CommitWarning: commit(root, ticketID, action)}, nil
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// ListFilter narrows ListTickets; empty fields match everything.
type ListFilter struct {
	Status   string
	Type     string
	Assignee string
	Tag      string
	Phase    string
}

// ListItem is one row of ListTickets: the index summary plus the hours-at-risk
// flag and the computed estimate.
type ListItem struct {
	ticket.IndexSummary
	AtRisk         bool    `json:"at_risk"`
	EstimatedHours float64 `json:"estimated_hours"`
	EstimateSource string  `json:"estimate_source"`
}

// ListTickets returns the project's tickets matching f, in index order.
func ListTickets(root string, f ListFilter) ([]ListItem, error) {
	// Built from the ticket files, never read back from index.json, so a failed
	// index write can't empty the list (under the lock, so a list can't
	// overwrite the index a concurrent edit just wrote).
	unlock := ticket.LockProject(root)
	idx, err := ticket.CurrentIndex(root)
	unlock()
	if err != nil {
		return nil, wrap(ErrInternal, err)
	}

	filtered := []ticket.IndexSummary{}
	for _, t := range idx.Tickets {
		if f.Status != "" && t.Status != f.Status {
			continue
		}
		if f.Type != "" && t.Type != f.Type {
			continue
		}
		if f.Assignee != "" && strVal(t.Assignee) != f.Assignee {
			continue
		}
		if f.Tag != "" && !ticket.Contains(t.Tags, f.Tag) {
			continue
		}
		if f.Phase != "" && strVal(t.Phase) != f.Phase {
			continue
		}
		filtered = append(filtered, t)
	}

	// Flag wrap tickets whose logged hours are >= 90% of their allotment, using
	// the same rule as the PM dashboard's hours-at-risk watchlist, so the ticket
	// gutter and the watchlist always agree. Wrap allotments don't depend on the
	// reference rate, so the context skips computing it.
	var effortToDays map[string]float64
	if cfg, cErr := ticket.ReadConfig(root); cErr == nil && cfg != nil {
		effortToDays = cfg.EffortToDays
	}
	var full []*ticket.Ticket
	for _, s := range filtered {
		if tk, rErr := ticket.ReadTicket(root, s.ID); rErr == nil {
			full = append(full, tk)
		}
	}
	atRisk := make(map[string]bool)
	for _, row := range pm.ComputeHoursAtRisk(full, pm.NewEstimateContext(full, 0, effortToDays)) {
		atRisk[row.TicketID] = true
	}

	// Each ticket's computed estimate, from the whole project so a filtered
	// list still finds parents and siblings.
	est := Estimates(root)
	items := make([]ListItem, len(filtered))
	for i, s := range filtered {
		e := est(s.ID)
		items[i] = ListItem{IndexSummary: s, AtRisk: atRisk[s.ID], EstimatedHours: e.Hours, EstimateSource: e.Source}
	}
	return items, nil
}

// Estimate is a ticket's computed estimate (pm.EstimatedHours), rounded to
// hundredths.
type Estimate struct {
	Hours  float64
	Source string
}

// Estimates returns a lookup of each ticket's computed estimate (wrap
// estimate, converted legacy effort, or a code ticket's share of its feature)
// across the whole project. Unknown ids give a zero value.
func Estimates(root string) func(id string) Estimate {
	all, err := ticket.ReadAllTickets(root)
	cfg, cErr := ticket.ReadConfig(root)
	if err != nil || cErr != nil {
		return func(string) Estimate { return Estimate{} }
	}
	ctx := pm.ProjectEstimateContext(root, all, cfg)
	byID := make(map[string]Estimate, len(all))
	for _, t := range all {
		h, src := pm.EstimatedHours(t, ctx)
		byID[t.ID] = Estimate{Hours: math.Round(h*100) / 100, Source: src}
	}
	return func(id string) Estimate { return byID[id] }
}

// Detail is a full ticket with its computed estimate and workflow.
type Detail struct {
	*ticket.Ticket
	EstimatedHours float64              `json:"estimated_hours"`
	EstimateSource string               `json:"estimate_source"`
	Workflow       *ticket.WorkflowView `json:"workflow,omitempty"`
}

// GetTicket reads one ticket with its estimate and workflow ("workflow" is
// computed on read, never stored; types with no workflow have none).
func GetTicket(root, ticketID string) (*Detail, error) {
	tk, err := ticket.ReadTicket(root, ticketID)
	if err != nil {
		return nil, &Error{Kind: ErrNotFound, Msg: "Ticket not found: " + ticketID, Cause: err}
	}
	e := Estimates(root)(ticketID)
	return &Detail{Ticket: tk, EstimatedHours: e.Hours, EstimateSource: e.Source, Workflow: ticket.TicketWorkflow(tk)}, nil
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

// CreateTicket creates one ticket. A non-empty params.Effort is refused
// (effort sizing is retired); estimate_hours must be a quarter-hour multiple.
func CreateTicket(root string, params ticket.CreateTicketParams) (*Result, error) {
	return mutate(root, "", true, func() (*ticket.Ticket, string, error) {
		if strings.TrimSpace(params.Effort) != "" {
			return nil, "", wrap(ErrInvalidInput, ticket.ErrEffortRetired)
		}
		if err := ticket.ValidateEstimateHours(params.EstimateHours); err != nil {
			return nil, "", wrap(ErrInvalidInput, err)
		}
		tk, err := ticket.CreateTicket(root, params)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "created", nil
	})
}

// EditField sets one field on a ticket.
func EditField(root, ticketID, field string, value interface{}, author string) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.EditField(root, ticketID, field, value, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, field + " updated", nil
	})
}

// Promote moves a ticket to its next status. Gates: leaving a work status
// needs a described time entry logged since entering it
// (ticket.ErrNeedsTimeLog); the first promote out of not_started checks the
// estimation rules (*ticket.EstimateValidationError).
func Promote(root, ticketID, author string) (*Result, error) {
	return statusChange(root, ticketID, func() (*ticket.Ticket, error) { return ticket.Promote(root, ticketID, author) })
}

// Demote moves a ticket to its previous status.
func Demote(root, ticketID, author string) (*Result, error) {
	return statusChange(root, ticketID, func() (*ticket.Ticket, error) { return ticket.Demote(root, ticketID, author) })
}

func statusChange(root, ticketID string, fn func() (*ticket.Ticket, error)) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := fn()
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "status → " + tk.Status, nil
	})
}

// Block marks a ticket blocked with an optional reason. The workflow has no
// transition into "blocked", so this is the one way in; promote/demote leave it.
func Block(root, ticketID, reason, author string) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.BlockTicket(root, ticketID, reason, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "status → blocked", nil
	})
}

// ForceClose closes a ticket skipping the workflow, recording the reason.
func ForceClose(root, ticketID, reason, author string) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.ForceClose(root, ticketID, reason, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "force-closed: " + reason, nil
	})
}

// AddComment appends a comment to a ticket's activity.
func AddComment(root, ticketID, message, author string) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.AddComment(root, ticketID, message, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "comment added", nil
	})
}

// LogTimeParams is one time entry to log.
type LogTimeParams struct {
	Date        string
	Hours       float64
	Description string
	Author      string
	// ExtendAuthorized / ExtendReason authorize a log past a wrap ticket's
	// hours allotment under strict time enforcement.
	ExtendAuthorized bool
	ExtendReason     string
}

// LogTime logs time on a ticket. Under strict time enforcement a log that
// would push a wrap ticket past its hours allotment fails with
// *TimeExtensionError unless ExtendAuthorized is set with a non-empty reason.
// Functional and unclassed tickets are never gated (code is estimated per
// feature, not per ticket).
func LogTime(root, ticketID string, p LogTimeParams) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		extendReason := ""
		if cfg, err := ticket.ReadConfig(root); err == nil && cfg.StrictTimeEnforcement {
			if tk, err := ticket.ReadTicket(root, ticketID); err == nil {
				allot, _ := pm.WrapAllotment(tk, pm.NewEstimateContext(nil, 0, cfg.EffortToDays))
				if allot > 0 { // non-wrap or unestimated tickets have no allotment to enforce
					var spent float64
					for _, te := range tk.TimeEntries {
						spent += te.Hours
					}
					if spent+p.Hours > allot {
						reason := strings.TrimSpace(p.ExtendReason)
						if !p.ExtendAuthorized || reason == "" {
							return nil, "", &TimeExtensionError{TicketID: ticketID, AllottedHours: allot, SpentHours: spent, WouldBeHours: spent + p.Hours}
						}
						extendReason = reason
					}
				}
			}
		}
		tk, err := ticket.AddTimeEntry(root, ticketID, p.Date, p.Hours, p.Description, p.Author, extendReason)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "time logged", nil
	})
}

// DeleteTimeEntry removes one time entry.
func DeleteTimeEntry(root, ticketID, entryID, author string) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.DeleteTimeEntry(root, ticketID, entryID, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "time entry deleted", nil
	})
}

// AddTestCase adds one test case.
func AddTestCase(root, ticketID, step, expected, author string) (*Result, error) {
	return mutate(root, ticketID, false, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.AddTestCase(root, ticketID, step, expected, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "add test case", nil
	})
}

// AddTestCases adds several test cases in one write.
func AddTestCases(root, ticketID string, cases []ticket.TestCase, author string) (*Result, error) {
	return mutate(root, ticketID, false, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.AddTestCases(root, ticketID, cases, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "add test cases (bulk)", nil
	})
}

// UpdateTestCase edits a test case or records its pass/fail result.
func UpdateTestCase(root, ticketID, caseID string, upd ticket.TestCaseUpdate, author string) (*Result, error) {
	return mutate(root, ticketID, false, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.UpdateTestCase(root, ticketID, caseID, upd, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "update test case", nil
	})
}

// DeleteTestCase removes a test case.
func DeleteTestCase(root, ticketID, caseID, author string) (*Result, error) {
	return mutate(root, ticketID, false, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.DeleteTestCase(root, ticketID, caseID, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "delete test case", nil
	})
}

// AddPredecessor links predecessorID as a predecessor of ticketID.
func AddPredecessor(root, ticketID, predecessorID, author string) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.AddPredecessor(root, ticketID, predecessorID, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "predecessor added: " + predecessorID, nil
	})
}

// RemovePredecessor unlinks a predecessor.
func RemovePredecessor(root, ticketID, predecessorID, author string) (*Result, error) {
	return mutate(root, ticketID, true, func() (*ticket.Ticket, string, error) {
		tk, err := ticket.RemovePredecessor(root, ticketID, predecessorID, author)
		if err != nil {
			return nil, "", classify(err)
		}
		return tk, "predecessor removed: " + predecessorID, nil
	})
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
