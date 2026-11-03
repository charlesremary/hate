// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"

	"hate/internal/fsutil"
)

// IndexSummary is the summary representation of a ticket in the index.
type IndexSummary struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Status           string   `json:"status"`
	Title            string   `json:"title"`
	Priority         string   `json:"priority"`
	Effort           *string  `json:"effort"` // retired, read-only legacy
	EstimateHours    *float64 `json:"estimate_hours,omitempty"`
	Class            string   `json:"class"` // functional | config | nonfunc | ""
	Lifecycle        *string  `json:"lifecycle"`
	Block            int      `json:"block,omitempty"`
	Blocked          *BlockedFlag `json:"blocked,omitempty"`
	CloseCategory    string   `json:"close_category,omitempty"`
	ReworkCategory   string   `json:"rework_category,omitempty"`
	IntroducedBy     string   `json:"introduced_by,omitempty"`
	Assignee         *string  `json:"assignee"`
	Predecessors     []string `json:"predecessors"`
	Repo             *string  `json:"repo"`
	Tags             []string `json:"tags"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	ClosedAt         *string  `json:"closed_at"`
	PlannedStartDate *string  `json:"planned_start_date"`
	DueDate          *string  `json:"due_date"`
}

// Index is the top-level index.json structure.
type Index struct {
	SchemaVersion string         `json:"schema_version"`
	GeneratedAt   string         `json:"generated_at"`
	TicketCount   int            `json:"ticket_count"`
	Tickets       []IndexSummary `json:"tickets"`
}

// buildIndex creates an Index from a list of tickets.
func buildIndex(tickets []*Ticket) *Index {
	summaries := make([]IndexSummary, 0, len(tickets))
	for _, t := range tickets {
		priority := t.Priority
		if priority == "" {
			priority = "medium"
		}
		tags := t.Tags
		if tags == nil {
			tags = []string{}
		}
		predecessors := t.Predecessors
		if predecessors == nil {
			predecessors = []string{}
		}
		summaries = append(summaries, IndexSummary{
			ID:               t.ID,
			Type:             t.Type,
			Status:           t.Status,
			Title:            t.Title,
			Priority:         priority,
			Effort:           t.Effort,
			EstimateHours:    t.EstimateHours,
			Class:            ClassOf(t),
			Lifecycle:        t.Lifecycle,
			Block:            t.Block,
			Blocked:          t.Blocked,
			CloseCategory:    t.CloseCategory,
			ReworkCategory:   t.ReworkCategory,
			IntroducedBy:     t.IntroducedBy,
			Assignee:         t.Assignee,
			Predecessors:     predecessors,
			Repo:             t.Repo,
			Tags:             tags,
			CreatedAt:        t.CreatedAt,
			UpdatedAt:        t.UpdatedAt,
			ClosedAt:         t.ClosedAt,
			PlannedStartDate: t.PlannedStartDate,
			DueDate:          t.DueDate,
		})
	}
	return &Index{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   NowISO(),
		TicketCount:   len(tickets),
		Tickets:       summaries,
	}
}

// IndexPath returns the path to index.json.
func IndexPath(repoRoot string) string {
	return filepath.Join(repoRoot, "index.json")
}

// CurrentIndex builds the index from the ticket files and refreshes
// index.json as a side effect. Nothing in hate reads index.json back to show
// data: it is a convenience file for outside tools, so a failed write (on
// Windows, a rename refused while Defender or a sync client holds the file
// open) is logged and never hides tickets. It errors only when the ticket
// files can't be read. The atomic write makes it safe without the project lock.
func CurrentIndex(repoRoot string) (*Index, error) {
	tickets, err := ReadAllTickets(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read tickets: %w", err)
	}
	idx := buildIndex(tickets)
	writeIndex(repoRoot, idx)
	return idx, nil
}

// RegenerateIndex refreshes index.json after a write. Like CurrentIndex, a
// failed index.json write is logged, not returned, so it can't fail a request
// (or skip its commit) after the ticket itself was saved.
func RegenerateIndex(repoRoot string) error {
	_, err := CurrentIndex(repoRoot)
	return err
}

// writeIndex writes index.json atomically, logging any failure.
func writeIndex(repoRoot string, idx *Index) {
	path := IndexPath(repoRoot)
	data, err := json.MarshalIndent(idx, "", "  ")
	if err == nil {
		data = append(data, '\n')
		err = fsutil.WriteFileAtomic(path, data, 0644)
	}
	if err != nil {
		log.Printf("index.json write failed (%s): %v", path, err)
	}
}
