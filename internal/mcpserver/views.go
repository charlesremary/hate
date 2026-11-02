// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"hate/internal/service"
	"hate/internal/ticket"
)

// TicketSummary is one ticket in a list.
type TicketSummary struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	Priority       string   `json:"priority"`
	Assignee       string   `json:"assignee"`
	Phase          string   `json:"phase"`
	Tags           []string `json:"tags"`
	Parent         string   `json:"parent"`
	Class          string   `json:"class"`
	EstimateHours  *float64 `json:"estimate_hours"`
	EstimatedHours float64  `json:"estimated_hours"`
	EstimateSource string   `json:"estimate_source"`
	AtRisk         bool     `json:"at_risk"`
}

func summaryFromItem(it service.ListItem) TicketSummary {
	s := TicketSummary{
		ID: it.ID, Title: it.Title, Type: it.Type, Status: it.Status, Priority: it.Priority,
		Assignee: deref(it.Assignee), Phase: deref(it.Phase), Tags: it.Tags, Class: it.Class,
		EstimateHours: it.EstimateHours, EstimatedHours: it.EstimatedHours, EstimateSource: it.EstimateSource,
		AtRisk: it.AtRisk,
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}
	for _, tg := range s.Tags {
		if len(tg) > len("parent:") && tg[:len("parent:")] == "parent:" {
			s.Parent = tg[len("parent:"):]
		}
	}
	return s
}

func summaryFromTicket(t *ticket.Ticket, est func(string) service.Estimate) TicketSummary {
	e := est(t.ID)
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	return TicketSummary{
		ID: t.ID, Title: t.Title, Type: t.Type, Status: t.Status, Priority: t.Priority,
		Assignee: deref(t.Assignee), Phase: deref(t.Phase), Tags: tags, Parent: ticket.ParentID(t),
		Class: ticket.ClassOf(t), EstimateHours: t.EstimateHours, EstimatedHours: e.Hours, EstimateSource: e.Source,
	}
}

// TicketView is a full ticket plus its computed estimate, its workflow
// (promote_to / demote_to / path) and, after a write, the commit warning.
type TicketView struct {
	*ticket.Ticket
	EstimatedHours float64              `json:"estimated_hours"`
	EstimateSource string               `json:"estimate_source"`
	Workflow       *ticket.WorkflowView `json:"workflow,omitempty"`
	CommitWarning  string               `json:"commit_warning,omitempty"`
}

func viewFromDetail(d *service.Detail) TicketView {
	return TicketView{Ticket: d.Ticket, EstimatedHours: d.EstimatedHours, EstimateSource: d.EstimateSource, Workflow: d.Workflow}
}

// viewFromResult builds the view of a mutated ticket, recomputing its estimate.
func viewFromResult(root string, res *service.Result) TicketView {
	e := service.Estimates(root)(res.Ticket.ID)
	return TicketView{Ticket: res.Ticket, EstimatedHours: e.Hours, EstimateSource: e.Source,
		Workflow: ticket.TicketWorkflow(res.Ticket), CommitWarning: res.CommitWarning}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
