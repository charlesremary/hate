// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"fmt"
	"strings"
	"time"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// ReworkInput is what a rework needs (hate 3.0).
type ReworkInput struct {
	Category     string   `json:"category"`      // ticket.ReworkCategories
	IntroducedBy string   `json:"introduced_by"` // agent | human
	Reason       string   `json:"reason"`        // what's wrong, required
	BrokenBy     string   `json:"broken_by"`     // regression: the ticket whose change broke it (optional)
	Title        string   `json:"title"`         // default "Rework: <original title>"
	Estimate     *float64 `json:"estimate_hours"`
}

// ReworkResult is the new ticket plus the original (with its new comment).
type ReworkResult struct {
	Ticket        *ticket.Ticket `json:"ticket"`
	Original      *ticket.Ticket `json:"original"`
	CommitWarning string         `json:"commit_warning,omitempty"`
}

// Rework redoes a closed ticket as a new, linked one: closed tickets are
// never reopened, so the original's estimate, hours and close date stay as
// they were. The new ticket is tagged rework-of:<id>, copies the parent,
// class, lifecycle, assignee and priority, goes in the current block, and
// carries the category and who introduced the problem; the original gets a
// comment pointing at it. One commit.
func Rework(root, originalID string, in ReworkInput, author string) (*ReworkResult, error) {
	defer ticket.LockProject(root)()
	if err := checkOpen(root); err != nil {
		return nil, err
	}
	cat := strings.TrimSpace(strings.ToLower(in.Category))
	cat = strings.ReplaceAll(strings.ReplaceAll(cat, " ", "_"), "-", "_")
	if strings.Contains(cat, "changed") || strings.Contains(cat, "change_request") {
		return nil, invalid("A changed request is new scope, not rework: create a new ticket for it (and re-baseline if the plan changes).")
	}
	if !ticket.Contains(ticket.ReworkCategories, cat) {
		return nil, invalid("category is required: one of %s", strings.Join(ticket.ReworkCategories, ", "))
	}
	by := strings.TrimSpace(strings.ToLower(in.IntroducedBy))
	if !ticket.Contains(ticket.IntroducedByValues, by) {
		return nil, invalid("introduced_by is required: agent or human (who introduced the problem)")
	}
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 5 {
		return nil, invalid("reason is required (what's wrong, at least 5 characters)")
	}
	if err := ticket.ValidateEstimateHours(in.Estimate); err != nil {
		return nil, invalid("%v", err)
	}

	orig, err := ticket.ReadTicket(root, originalID)
	if err != nil {
		return nil, classify(err)
	}
	if !ticket.Contains(ticket.ClosedStatuses, orig.Status) {
		return nil, invalid("%s isn't finished (%s): change it directly, or demote it; rework is for closed tickets", orig.ID, orig.Status)
	}
	if ticket.IsDescoped(orig) {
		return nil, invalid("%s was force-closed as %s: it was never delivered, so there's nothing to rework", orig.ID, ticket.CloseCategoryLabels[orig.CloseCategory])
	}
	brokenBy := strings.TrimSpace(in.BrokenBy)
	if brokenBy != "" {
		if cat != "regression" {
			return nil, invalid("broken_by only applies to a regression")
		}
		if _, err := ticket.ReadTicket(root, brokenBy); err != nil {
			return nil, invalid("broken_by: %v", err)
		}
	}

	typ := orig.Type
	if _, ok := ticket.GetWorkflowForType(typ); !ok {
		typ = "task"
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Rework: " + orig.Title
	}
	tags := []string{ticket.ReworkTagPrefix + orig.ID}
	if p := ticket.ParentID(orig); p != "" {
		tags = append(tags, "parent:"+p)
	}
	if c := ticket.ClassOf(orig); c != "" {
		tags = append(tags, c)
	}
	params := ticket.CreateTicketParams{
		Type: typ, Title: title, Creator: author, Priority: orig.Priority, EstimateHours: in.Estimate, Tags: tags,
		Description: fmt.Sprintf("%s\n\nRework of %s (%s, introduced by %s).", reason, orig.ID, ticket.ReworkCategoryLabels[cat], by),
	}
	if orig.Assignee != nil {
		params.Assignee = *orig.Assignee
	}
	if orig.Lifecycle != nil {
		params.Lifecycle = *orig.Lifecycle
	}
	params.Block = currentBlock(root)

	nt, err := ticket.CreateTicket(root, params)
	if err != nil {
		return nil, classify(err)
	}
	nt.ReworkCategory, nt.IntroducedBy, nt.BrokenBy = cat, by, brokenBy
	if err := ticket.WriteTicket(root, nt); err != nil {
		return nil, wrap(ErrInternal, err)
	}
	orig, err = ticket.AddComment(root, orig.ID, fmt.Sprintf("Rework: %s (%s, introduced by %s): %s", nt.ID, ticket.ReworkCategoryLabels[cat], by, reason), author)
	if err != nil {
		return nil, classify(err)
	}
	if err := ticket.RegenerateIndex(root); err != nil {
		return nil, wrap(ErrInternal, err)
	}
	w := ticket.CommitWarning(ticket.CommitFiles(root,
		[]string{ticket.TicketPath(root, nt.ID), ticket.TicketPath(root, orig.ID)},
		fmt.Sprintf("%s: rework of %s (%s)", nt.ID, orig.ID, cat)))
	return &ReworkResult{Ticket: nt, Original: orig, CommitWarning: w}, nil
}

// currentBlock is the project's current planning block number (0 when it
// doesn't plan in blocks).
func currentBlock(root string) int {
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		return 0
	}
	tickets, _ := ticket.ReadAllTickets(root)
	for _, b := range pm.BlocksForProject(cfg, tickets, pm.ProjectEstimateContext(root, tickets, cfg), time.Now()) {
		if b.State == pm.BlockCurrent {
			return b.N
		}
	}
	return 0
}
