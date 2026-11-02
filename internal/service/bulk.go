// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"fmt"
	"os"
	"strings"

	"hate/internal/ticket"
)

// MaxBulkTickets caps one CreateTickets batch.
const MaxBulkTickets = 50

// BulkTicket is one ticket in a CreateTickets batch. Ref is a caller-chosen
// handle other tickets in the same batch use in ParentRef / PredecessorRefs
// before the real ids exist; a ref must name an EARLIER ticket in the batch.
type BulkTicket struct {
	Ref              string            `json:"ref,omitempty"`
	Type             string            `json:"type"`
	Title            string            `json:"title"`
	Description      string            `json:"description,omitempty"`
	Priority         string            `json:"priority,omitempty"`
	Effort           string            `json:"effort,omitempty"` // retired: non-empty is refused
	EstimateHours    *float64          `json:"estimate_hours,omitempty"`
	Assignee         string            `json:"assignee,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Phase            string            `json:"phase,omitempty"`
	PlannedStartDate string            `json:"planned_start_date,omitempty"`
	DueDate          string            `json:"due_date,omitempty"`
	Severity         string            `json:"severity,omitempty"`
	Hours            float64           `json:"hours,omitempty"`
	Attendees        string            `json:"attendees,omitempty"`
	ParentRef        string            `json:"parent_ref,omitempty"`
	ParentID         string            `json:"parent_id,omitempty"`
	PredecessorRefs  []string          `json:"predecessor_refs,omitempty"`
	Predecessors     []string          `json:"predecessors,omitempty"`
	TestCases        []ticket.TestCase `json:"test_cases,omitempty"`
}

// BulkCreated maps a batch ref to the id it got ("" ref when none was given).
type BulkCreated struct {
	Ref string `json:"ref"`
	ID  string `json:"id"`
}

// BulkResult is a created batch.
type BulkResult struct {
	Created       []BulkCreated
	Tickets       []*ticket.Ticket
	CommitWarning string
}

// CreateTickets creates up to MaxBulkTickets tickets in one locked write and
// one commit. Every ticket is validated before any is written; parent_ref /
// predecessor_refs resolve to the ids of earlier tickets in the batch
// (parent_ref adds the parent:<id> tag); inline test cases are added to their
// ticket. author is the creator of every ticket.
func CreateTickets(root string, batch []BulkTicket, author string) (*BulkResult, error) {
	defer ticket.LockProject(root)()
	if err := checkOpen(root); err != nil {
		return nil, err
	}
	if err := validateBatch(root, batch); err != nil {
		return nil, err
	}

	ids := map[string]string{} // ref -> id
	res := &BulkResult{}
	var paths []string
	rollback := func() {
		for _, p := range paths {
			os.Remove(p)
		}
	}
	for _, b := range batch {
		tags := append([]string{}, b.Tags...)
		parent := b.ParentID
		if b.ParentRef != "" {
			parent = ids[b.ParentRef]
		}
		if parent != "" && !ticket.Contains(tags, "parent:"+parent) {
			tags = append(tags, "parent:"+parent)
		}
		preds := append([]string{}, b.Predecessors...)
		for _, r := range b.PredecessorRefs {
			preds = append(preds, ids[r])
		}
		tk, err := ticket.CreateTicket(root, ticket.CreateTicketParams{
			Type: b.Type, Title: b.Title, Creator: author, Priority: b.Priority,
			EstimateHours: b.EstimateHours, Assignee: b.Assignee, Tags: tags, Phase: b.Phase,
			Predecessors: preds, PlannedStartDate: b.PlannedStartDate, DueDate: b.DueDate,
			Severity: b.Severity, Description: b.Description, Hours: b.Hours, Attendees: b.Attendees,
		})
		if err != nil {
			rollback()
			return nil, classify(err)
		}
		paths = append(paths, ticket.TicketPath(root, tk.ID))
		if len(b.TestCases) > 0 {
			if tk, err = ticket.AddTestCases(root, tk.ID, b.TestCases, author); err != nil {
				rollback()
				return nil, classify(err)
			}
		}
		if b.Ref != "" {
			ids[b.Ref] = tk.ID
		}
		res.Created = append(res.Created, BulkCreated{Ref: b.Ref, ID: tk.ID})
		res.Tickets = append(res.Tickets, tk)
	}
	if err := ticket.RegenerateIndex(root); err != nil {
		return nil, wrap(ErrInternal, err)
	}
	created := make([]string, len(res.Created))
	for i, c := range res.Created {
		created[i] = c.ID
	}
	res.CommitWarning = ticket.CommitWarning(ticket.CommitFiles(root, paths, "created "+strings.Join(created, ", ")))
	return res, nil
}

// validateBatch checks the whole batch before anything is written.
func validateBatch(root string, batch []BulkTicket) error {
	bad := func(i int, format string, a ...interface{}) error {
		return &Error{Kind: ErrInvalidInput, Msg: fmt.Sprintf("tickets[%d]: ", i) + fmt.Sprintf(format, a...)}
	}
	if len(batch) == 0 {
		return &Error{Kind: ErrInvalidInput, Msg: "tickets: give at least one ticket"}
	}
	if len(batch) > MaxBulkTickets {
		return &Error{Kind: ErrInvalidInput, Msg: fmt.Sprintf("tickets: at most %d per batch (got %d)", MaxBulkTickets, len(batch))}
	}
	exists := func(id string) bool {
		_, err := os.Stat(ticket.TicketPath(root, id))
		return err == nil
	}
	seen := map[string]bool{}
	for i, b := range batch {
		if strings.TrimSpace(b.Type) == "" {
			return bad(i, "type is required")
		}
		if strings.TrimSpace(b.Title) == "" {
			return bad(i, "title is required")
		}
		if strings.TrimSpace(b.Effort) != "" {
			return wrap(ErrInvalidInput, ticket.ErrEffortRetired)
		}
		if err := ticket.ValidateEstimateHours(b.EstimateHours); err != nil {
			return bad(i, "%v", err)
		}
		for _, d := range []string{b.PlannedStartDate, b.DueDate} {
			if d != "" {
				if err := ticket.ValidateDate(d); err != nil {
					return bad(i, "%v", err)
				}
			}
		}
		if b.ParentRef != "" && b.ParentID != "" {
			return bad(i, "give parent_ref or parent_id, not both")
		}
		if b.ParentRef != "" && !seen[b.ParentRef] {
			return bad(i, "parent_ref %q must name an earlier ticket in the batch", b.ParentRef)
		}
		if b.ParentID != "" && !exists(b.ParentID) {
			return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("tickets[%d]: parent ticket not found: %s", i, b.ParentID)}
		}
		for _, r := range b.PredecessorRefs {
			if !seen[r] {
				return bad(i, "predecessor_ref %q must name an earlier ticket in the batch", r)
			}
		}
		for _, p := range b.Predecessors {
			if !exists(p) {
				return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("tickets[%d]: Predecessor ticket not found: %s", i, p)}
			}
		}
		for j, tc := range b.TestCases {
			if strings.TrimSpace(tc.Step) == "" || strings.TrimSpace(tc.Expected) == "" {
				return bad(i, "test_cases[%d]: step and expected are required", j)
			}
		}
		if b.Ref != "" {
			if seen[b.Ref] {
				return bad(i, "duplicate ref %q", b.Ref)
			}
			seen[b.Ref] = true
		}
	}
	return nil
}
