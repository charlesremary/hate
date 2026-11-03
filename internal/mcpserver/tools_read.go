// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hate/internal/pm"
	"hate/internal/service"
	"hate/internal/ticket"
)

var falseP = func() *bool { b := false; return &b }()
var trueP = func() *bool { b := true; return &b }()

// readOnly annotations: no side effects, safe to repeat, closed world.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: falseP}
}

// ---- inputs ----

type projectIn struct {
	Project string `json:"project" jsonschema:"project id, folder name or ticket prefix (case-insensitive), e.g. HATE"`
}

type ticketIn struct {
	TicketID string `json:"ticket_id" jsonschema:"full ticket id, e.g. HATE-1a3s"`
	Project  string `json:"project,omitempty" jsonschema:"project id, folder or prefix; optional, defaults to the ticket id's prefix"`
}

type listProjectsIn struct {
	IncludeClosed *bool `json:"include_closed,omitempty" jsonschema:"include closed projects (default true)"`
}

type searchIn struct {
	Q     string `json:"q" jsonschema:"text to match against ticket id and title, case-insensitive (1-200 chars)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results, 1-50 (default 20)"`
}

type listTicketsIn struct {
	Project        string `json:"project" jsonschema:"project id, folder name or ticket prefix"`
	Status         string `json:"status,omitempty" jsonschema:"only this status, e.g. in_progress"`
	Type           string `json:"type,omitempty" jsonschema:"only this ticket type, e.g. dev_task"`
	Assignee       string `json:"assignee,omitempty" jsonschema:"only tickets assigned to this email"`
	Tag            string `json:"tag,omitempty" jsonschema:"only tickets with this exact tag"`
	Lifecycle      string `json:"lifecycle,omitempty" jsonschema:"only this lifecycle (kind of work), e.g. Build"`
	Block          int    `json:"block,omitempty" jsonschema:"only tickets planned into this block number"`
	Parent         string `json:"parent,omitempty" jsonschema:"only the children of this feature (tickets tagged parent:<id>)"`
	IncludeBacklog *bool  `json:"include_backlog,omitempty" jsonschema:"include tickets tagged backlog (default true)"`
	Limit          int    `json:"limit,omitempty" jsonschema:"max tickets returned (default 200); total counts all matches"`
}

// ---- outputs ----

type listProjectsOut struct {
	Projects []service.ProjectSummary `json:"projects"`
}

type searchOut struct {
	Results []service.SearchResult `json:"results"`
}

type listTicketsOut struct {
	Tickets []TicketSummary `json:"tickets"`
	Total   int             `json:"total"`
}

// ---- registration ----

func registerReadTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "hate_list_projects",
		Description: "List the hate projects on this machine: id, name, client, ticket prefix, folder, closed flag and ticket count. Start here to find the project for a piece of work.",
		Annotations: readOnly("List projects"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listProjectsIn) (*mcp.CallToolResult, listProjectsOut, error) {
		include := in.IncludeClosed == nil || *in.IncludeClosed
		return nil, listProjectsOut{Projects: service.ListProjects(include)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "hate_get_project",
		Description: "Project details an agent needs before working it: prefix, closed flag, block_weeks (0 = no planning blocks; 2 or 3 = plan tickets into numbered blocks), " +
			"strict_time_enforcement (time logs past a wrap ticket's estimate need authorization), enforce_qa, ticket count, and the team (assignable emails).",
		Annotations: readOnly("Get project"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in projectIn) (*mcp.CallToolResult, *service.ProjectDetail, error) {
		root, err := service.ResolveProject(in.Project)
		if err != nil {
			return nil, nil, mapError(err)
		}
		d, err := service.GetProject(root)
		return nil, d, mapError(err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "hate_get_stats",
		Description: "The PM Dashboard's live project numbers, each with the value a week (or a block) earlier: % complete (hours-weighted), remaining hours, " +
			"estimate accuracy (actual / estimate; wrap per ticket, code per finished feature, with sample size), tickets finished this period, " +
			"cycle time (days start to done), blocked now, scope added since the baseline, and the test pass rate. Use it for a status summary.",
		Annotations: readOnly("Get project stats"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in projectIn) (*mcp.CallToolResult, *pm.ProjectStats, error) {
		root, err := service.ResolveProject(in.Project)
		if err != nil {
			return nil, nil, mapError(err)
		}
		st, err := service.Stats(root)
		if err != nil {
			return nil, nil, mapError(err)
		}
		return nil, &st, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "hate_search_tickets",
		Description: "Search tickets across every project by id or title (case-insensitive). Ranked: exact id, id prefix, id contains, title contains; open tickets first. " +
			"Use it to find a ticket when you know part of its id or title but not its project.",
		Annotations: readOnly("Search tickets"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
		if len(in.Q) == 0 || len(in.Q) > 200 {
			return nil, searchOut{}, invalid("q must be 1-200 characters")
		}
		if in.Limit < 0 || in.Limit > service.SearchMaxLimit {
			return nil, searchOut{}, invalid("limit must be 1-50")
		}
		return nil, searchOut{Results: service.Search(in.Q, in.Limit)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "hate_list_tickets",
		Description: "List a project's tickets with optional filters (status, type, assignee, tag, lifecycle, block, parent). Each row has the class tag, estimate_hours, " +
			"the computed estimated_hours and at_risk. Use parent=<feature id> to review a feature's children.",
		Annotations: readOnly("List tickets"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listTicketsIn) (*mcp.CallToolResult, listTicketsOut, error) {
		root, err := service.ResolveProject(in.Project)
		if err != nil {
			return nil, listTicketsOut{}, mapError(err)
		}
		items, err := service.ListTickets(root, service.ListFilter{
			Status: in.Status, Type: in.Type, Assignee: in.Assignee, Tag: in.Tag, Lifecycle: in.Lifecycle, Block: in.Block, Parent: in.Parent,
		})
		if err != nil {
			return nil, listTicketsOut{}, mapError(err)
		}
		includeBacklog := in.IncludeBacklog == nil || *in.IncludeBacklog
		limit := in.Limit
		if limit <= 0 {
			limit = 200
		}
		out := listTicketsOut{Tickets: []TicketSummary{}}
		for _, it := range items {
			if !includeBacklog && ticket.Contains(it.Tags, "backlog") {
				continue
			}
			out.Total++
			if len(out.Tickets) < limit {
				out.Tickets = append(out.Tickets, summaryFromItem(it))
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "hate_get_ticket",
		Description: "Read one ticket in full: description, tags, test cases, time entries, activity (comments have ids), attachments metadata, the computed estimate, " +
			"and workflow {promote_to, demote_to, path}: read promote_to before promoting. project is optional (taken from the id's prefix).",
		Annotations: readOnly("Get ticket"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ticketIn) (*mcp.CallToolResult, *TicketView, error) {
		root, err := service.ResolveTicketProject(in.Project, in.TicketID)
		if err != nil {
			return nil, nil, mapError(err)
		}
		d, err := service.GetTicket(root, in.TicketID)
		if err != nil {
			return nil, nil, mapError(err)
		}
		v := viewFromDetail(d)
		return nil, &v, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "hate_get_ready",
		Description: "The project's work order from predecessor links: ready (open, unblocked, all predecessors done; work these), next (unlocked once ready finishes) " +
			"and every open work ticket by dependency wave (wave 1 = nothing open in front of it) with hours. Feature parents and backlog are left out.",
		Annotations: readOnly("Get ready work"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in projectIn) (*mcp.CallToolResult, pm.ReadyReport, error) {
		root, err := service.ResolveProject(in.Project)
		if err != nil {
			return nil, pm.ReadyReport{}, mapError(err)
		}
		rep, err := service.Ready(root)
		return nil, rep, mapError(err)
	})
}
