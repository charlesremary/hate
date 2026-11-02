// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/ticket"
)

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 50
)

// SearchResult is one ticket hit from GET /api/search.
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

// RegisterSearchRoutes registers the cross-project ticket search.
func RegisterSearchRoutes(r chi.Router) {
	r.Get("/api/search", handleSearch)
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

// handleSearch handles GET /api/search?q=&limit= — search-as-you-type over
// every registered, non-hidden project (closed ones included) by ticket ID
// and title. Reads the ticket files directly and takes no locks: it's
// read-only and runs on every keystroke.
func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := searchDefaultLimit
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, searchMaxLimit)
	}
	results := []SearchResult{}
	if q == "" {
		respondJSON(w, http.StatusOK, results)
		return
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

	// Best match first; within a rank, open tickets before done ones, then by ID.
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
	respondJSON(w, http.StatusOK, results)
}
