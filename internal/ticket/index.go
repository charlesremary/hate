package ticket

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// IndexSummary is the summary representation of a ticket in the index.
type IndexSummary struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Status       string   `json:"status"`
	Title        string   `json:"title"`
	Priority     string   `json:"priority"`
	Effort       *string  `json:"effort"`
	Phase        *string  `json:"phase"`
	Assignee     *string  `json:"assignee"`
	Predecessors []string `json:"predecessors"`
	Repo         *string  `json:"repo"`
	Tags         []string `json:"tags"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	ClosedAt         *string `json:"closed_at"`
	PlannedStartDate *string `json:"planned_start_date"`
	DueDate          *string `json:"due_date"`
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
			ID:           t.ID,
			Type:         t.Type,
			Status:       t.Status,
			Title:        t.Title,
			Priority:     priority,
			Effort:       t.Effort,
			Phase:        t.Phase,
			Assignee:     t.Assignee,
			Predecessors: predecessors,
			Repo:         t.Repo,
			Tags:         tags,
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

// RegenerateIndex rebuilds index.json from all ticket files.
func RegenerateIndex(repoRoot string) error {
	tickets, err := ReadAllTickets(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to read tickets: %w", err)
	}
	idx := buildIndex(tickets)
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}
	data = append(data, '\n')
	path := IndexPath(repoRoot)
	return os.WriteFile(path, data, 0644)
}

// ReadIndex reads the current index. Returns empty index if file is missing.
func ReadIndex(repoRoot string) (*Index, error) {
	path := IndexPath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			idx := buildIndex([]*Ticket{})
			return idx, nil
		}
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("failed to parse index: %w", err)
	}
	return &idx, nil
}
