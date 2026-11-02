// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"sort"
	"strings"

	"hate/internal/config"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// ProjectSummary is one project in ListProjects.
type ProjectSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Client      string `json:"client"`
	Prefix      string `json:"prefix"`
	Path        string `json:"path"`
	Closed      bool   `json:"closed"`
	TicketCount int    `json:"ticket_count"`
}

// ListProjects returns every registered, non-hidden project.
func ListProjects(includeClosed bool) []ProjectSummary {
	out := []ProjectSummary{}
	for _, p := range config.ListProjects() {
		s := ProjectSummary{ID: p.ID, Name: p.Name, Client: p.Client, Prefix: p.Prefix, Path: p.Path}
		if cfg, err := ticket.ReadConfig(p.Path); err == nil {
			s.Closed = cfg.IsClosed()
		}
		if s.Closed && !includeClosed {
			continue
		}
		s.TicketCount = countTickets(p.Path)
		out = append(out, s)
	}
	return out
}

// ProjectResource is a team member an agent can assign tickets to.
type ProjectResource struct {
	Name                string  `json:"name"`
	Email               string  `json:"email"`
	Role                string  `json:"role"`
	DailyHoursAvailable float64 `json:"daily_hours_available"`
}

// ProjectDetail is what an agent needs to know before working a project: the
// gates that apply (strict time, enforce-QA) and the phase convention
// (block_weeks, 0 = no blocks).
type ProjectDetail struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	Client                string            `json:"client"`
	Prefix                string            `json:"prefix"`
	Path                  string            `json:"path"`
	Closed                bool              `json:"closed"`
	BlockWeeks            int               `json:"block_weeks"`
	StrictTimeEnforcement bool              `json:"strict_time_enforcement"`
	EnforceQA             bool              `json:"enforce_qa"`
	TicketCount           int               `json:"ticket_count"`
	Resources             []ProjectResource `json:"resources"`
}

// GetProject reads a project's details.
func GetProject(root string) (*ProjectDetail, error) {
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		return nil, wrap(ErrInternal, err)
	}
	d := &ProjectDetail{
		ID: cfg.ProjectID, Name: cfg.ProjectName, Client: cfg.Client, Prefix: cfg.Prefix, Path: root,
		Closed: cfg.IsClosed(), BlockWeeks: cfg.BlockWeeks,
		StrictTimeEnforcement: cfg.StrictTimeEnforcement, EnforceQA: cfg.EnforceQA,
		Resources: []ProjectResource{},
	}
	d.TicketCount = countTickets(root)
	for _, r := range cfg.Resources {
		d.Resources = append(d.Resources, ProjectResource{Name: r.Name, Email: r.Email, Role: r.Role, DailyHoursAvailable: r.EffectiveDailyHours()})
	}
	return d, nil
}

// countTickets counts a project's ticket files without touching index.json
// (CurrentIndex rewrites it, and older repos still track that file).
func countTickets(root string) int {
	all, err := ticket.ReadAllTickets(root)
	if err != nil {
		return 0
	}
	return len(all)
}

// Ready is the project's work order: ready now, next, and every stage.
func Ready(root string) (pm.ReadyReport, error) {
	tickets, err := ticket.ReadAllTickets(root)
	if err != nil {
		return pm.ReadyReport{}, wrap(ErrInternal, err)
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		cfg = nil
	}
	return pm.ComputeReady(tickets, pm.ProjectEstimateContext(root, tickets, cfg)), nil
}

// Overview is a project's hand-maintained reference material.
type Overview struct {
	Contacts     []ticket.Contact     `json:"contacts"`
	Links        []ticket.Link        `json:"links"`
	Instructions []ticket.Instruction `json:"instructions"`
}

// GetOverview reads the Project Overview (contacts, links, instructions).
func GetOverview(root string) (*Overview, error) {
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		return nil, wrap(ErrInternal, err)
	}
	o := &Overview{Contacts: cfg.Contacts, Links: cfg.Links, Instructions: cfg.Instructions}
	if o.Contacts == nil {
		o.Contacts = []ticket.Contact{}
	}
	if o.Links == nil {
		o.Links = []ticket.Link{}
	}
	if o.Instructions == nil {
		o.Instructions = []ticket.Instruction{}
	}
	return o, nil
}

// Search limits.
const (
	SearchDefaultLimit = 20
	SearchMaxLimit     = 50
)

// SearchResult is one ticket hit from Search.
type SearchResult struct {
	ProjectID     string `json:"project_id"`
	ProjectName   string `json:"project_name"`
	ProjectPath   string `json:"project_path"`
	Prefix        string `json:"prefix"`
	TicketID      string `json:"ticket_id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Type          string `json:"type"`
	ClosedProject bool   `json:"closed_project"`

	rank int
	done bool
}

// searchRank scores a ticket against a lower-cased query: 0 exact ID, 1 ID
// prefix, 2 ID contains, 3 title contains, -1 no match.
func searchRank(q, id, title string) int {
	id = strings.ToLower(id)
	switch {
	case id == q:
		return 0
	case strings.HasPrefix(id, q):
		return 1
	case strings.Contains(id, q):
		return 2
	case strings.Contains(strings.ToLower(title), q):
		return 3
	}
	return -1
}

// Search finds tickets by ID or title across every registered, non-hidden
// project (closed ones included), ignoring case. Best match first; within a
// rank, open tickets before done ones, then by ID. limit <= 0 means the
// default; it is capped at SearchMaxLimit. Reads the ticket files directly
// and takes no locks.
func Search(q string, limit int) []SearchResult {
	q = strings.ToLower(strings.TrimSpace(q))
	if limit <= 0 {
		limit = SearchDefaultLimit
	}
	limit = min(limit, SearchMaxLimit)
	results := []SearchResult{}
	if q == "" {
		return results
	}
	for _, p := range config.ListProjects() {
		tickets, err := ticket.ReadAllTickets(p.Path)
		if err != nil {
			continue
		}
		closed := false
		if cfg, err := ticket.ReadConfig(p.Path); err == nil {
			closed = cfg.IsClosed()
		}
		for _, t := range tickets {
			rank := searchRank(q, t.ID, t.Title)
			if rank < 0 {
				continue
			}
			results = append(results, SearchResult{
				ProjectID: p.ID, ProjectName: p.Name, ProjectPath: p.Path, Prefix: p.Prefix,
				TicketID: t.ID, Title: t.Title, Status: t.Status, Type: t.Type, ClosedProject: closed,
				rank: rank, done: t.Status == "complete" || t.Status == "closed",
			})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.done != b.done {
			return !a.done
		}
		if a.TicketID != b.TicketID {
			return a.TicketID < b.TicketID
		}
		return a.ProjectID < b.ProjectID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
