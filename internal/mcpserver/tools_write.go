// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hate/internal/service"
	"hate/internal/ticket"
)

// write annotations: closed world; destructive only for force-close.
func write(title string, destructive, idempotent bool) *mcp.ToolAnnotations {
	d := falseP
	if destructive {
		d = trueP
	}
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: d, IdempotentHint: idempotent, OpenWorldHint: falseP}
}

// author is the explicit author, else the project's git identity (the one the
// UI commits as), else a fixed MCP marker.
func author(root, explicit string) string {
	if a := strings.TrimSpace(explicit); a != "" {
		return a
	}
	id := ticket.GitUserIdentity(root)
	if id["email"] != "" {
		return id["email"]
	}
	return "mcp@hate.local"
}

// resolve finds the project of a ticket (project optional).
func resolve(project, ticketID string) (string, error) {
	root, err := service.ResolveTicketProject(project, ticketID)
	return root, mapError(err)
}

// ---- inputs ----

type writeTicketIn struct {
	TicketID string `json:"ticket_id" jsonschema:"full ticket id, e.g. HATE-1a3s"`
	Project  string `json:"project,omitempty" jsonschema:"project id, folder or prefix; optional, defaults to the ticket id's prefix"`
	Author   string `json:"author,omitempty" jsonschema:"who is acting, for the activity log (default: the project's git identity)"`
}

type testCaseIn struct {
	Step     string `json:"step" jsonschema:"what the tester does"`
	Expected string `json:"expected" jsonschema:"the observable result that means pass"`
}

type newTicketIn struct {
	Ref              string       `json:"ref,omitempty" jsonschema:"a handle for this batch so later tickets can point at it (parent_ref, predecessor_refs)"`
	Type             string       `json:"type" jsonschema:"ticket type: task, dev_task, design_task, meeting, administration, ..."`
	Title            string       `json:"title" jsonschema:"verb-first outcome, e.g. Add CSV export to the billing tab"`
	Description      string       `json:"description,omitempty" jsonschema:"markdown: context, scope, rules, out of scope"`
	Priority         string       `json:"priority,omitempty" jsonschema:"low, medium, high or critical"`
	EstimateHours    *float64     `json:"estimate_hours,omitempty" jsonschema:"wrap (config / nonfunc) tickets only: hours in quarter-hour steps, >= 0.25"`
	Assignee         string       `json:"assignee,omitempty" jsonschema:"email of a project resource"`
	Tags             []string     `json:"tags,omitempty" jsonschema:"class tag (functional, config, nonfunc), cfp:N on a feature parent, type:..., see hate://guide?section=3"`
	Phase            string       `json:"phase,omitempty" jsonschema:"phase name for the rollup"`
	PlannedStartDate string       `json:"planned_start_date,omitempty" jsonschema:"YYYY-MM-DD"`
	DueDate          string       `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD"`
	ParentRef        string       `json:"parent_ref,omitempty" jsonschema:"ref of an earlier ticket in this batch to be the parent (adds parent:<id>)"`
	ParentID         string       `json:"parent_id,omitempty" jsonschema:"id of an existing ticket to be the parent (adds parent:<id>)"`
	PredecessorRefs  []string     `json:"predecessor_refs,omitempty" jsonschema:"refs of earlier tickets in this batch that must finish first"`
	Predecessors     []string     `json:"predecessors,omitempty" jsonschema:"ids of existing tickets that must finish first"`
	TestCases        []testCaseIn `json:"test_cases,omitempty" jsonschema:"acceptance tests for the ticket"`
}

type createTicketsIn struct {
	Project string        `json:"project" jsonschema:"project id, folder name or ticket prefix"`
	Tickets []newTicketIn `json:"tickets" jsonschema:"1 to 50 tickets, created in order"`
	Author  string        `json:"author,omitempty" jsonschema:"creator of every ticket (default: the project's git identity)"`
}

type updateFieldsIn struct {
	Title            *string  `json:"title,omitempty"`
	Description      *string  `json:"description,omitempty"`
	Priority         *string  `json:"priority,omitempty"`
	EstimateHours    *float64 `json:"estimate_hours,omitempty" jsonschema:"quarter-hour steps, >= 0.25 (wrap tickets); use clear_estimate_hours to remove it"`
	Assignee         *string  `json:"assignee,omitempty" jsonschema:"email; empty string unassigns"`
	Phase            *string  `json:"phase,omitempty" jsonschema:"empty string clears"`
	PlannedStartDate *string  `json:"planned_start_date,omitempty" jsonschema:"YYYY-MM-DD; empty string clears"`
	DueDate          *string  `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD; empty string clears"`
}

type updateTicketIn struct {
	writeTicketIn
	Fields             *updateFieldsIn `json:"fields,omitempty" jsonschema:"fields to set; only the ones given change"`
	ClearEstimateHours bool            `json:"clear_estimate_hours,omitempty" jsonschema:"remove estimate_hours"`
	AddTags            []string        `json:"add_tags,omitempty" jsonschema:"tags to add (kept if already there)"`
	RemoveTags         []string        `json:"remove_tags,omitempty" jsonschema:"tags to remove"`
}

type reasonIn struct {
	writeTicketIn
	Reason string `json:"reason" jsonschema:"why, recorded on the ticket"`
}

type logTimeIn struct {
	writeTicketIn
	Hours            float64 `json:"hours" jsonschema:"hours worked, quarter-hour steps, > 0 and <= 24"`
	Description      string  `json:"description" jsonschema:"what was done (required: the promote gate needs a described entry)"`
	Date             string  `json:"date,omitempty" jsonschema:"YYYY-MM-DD (default today)"`
	ExtendAuthorized bool    `json:"extend_authorized,omitempty" jsonschema:"the user authorized logging past the ticket's allotment (strict time only)"`
	ExtendReason     string  `json:"extend_reason,omitempty" jsonschema:"why the extension was authorized"`
}

type commentIn struct {
	writeTicketIn
	Message string `json:"message" jsonschema:"comment text (markdown)"`
}

type editCommentIn struct {
	writeTicketIn
	CommentID string `json:"comment_id" jsonschema:"the comment's id (c1, c2, ...) from the ticket's activity"`
	Message   string `json:"message" jsonschema:"the new text"`
}

type addTestCasesIn struct {
	writeTicketIn
	Cases []testCaseIn `json:"cases" jsonschema:"1 to 20 test cases"`
}

type updateTestCaseIn struct {
	writeTicketIn
	CaseID   string  `json:"case_id" jsonschema:"test case id, e.g. tc2"`
	Step     *string `json:"step,omitempty"`
	Expected *string `json:"expected,omitempty"`
	Status   *string `json:"status,omitempty" jsonschema:"QA result: pass, fail, or empty string for untested"`
	Comment  *string `json:"comment,omitempty" jsonschema:"QA note: what happened / how to reproduce"`
}

type linkIn struct {
	writeTicketIn
	AddPredecessors    []string `json:"add_predecessors,omitempty" jsonschema:"ticket ids that must finish before this one"`
	RemovePredecessors []string `json:"remove_predecessors,omitempty" jsonschema:"predecessor links to remove"`
}

// ---- outputs ----

type createTicketsOut struct {
	Created       []service.BulkCreated `json:"created"`
	Tickets       []TicketSummary       `json:"tickets"`
	CommitWarning string                `json:"commit_warning,omitempty"`
}

type logTimeOut struct {
	TicketView
	LoggedHoursTotal float64  `json:"logged_hours_total"`
	AllottedHours    *float64 `json:"allotted_hours,omitempty"`
}

type addCommentOut struct {
	TicketID      string `json:"ticket_id"`
	CommentID     string `json:"comment_id"`
	ActivityCount int    `json:"activity_count"`
	CommitWarning string `json:"commit_warning,omitempty"`
}

type commentView struct {
	ID       string `json:"id"`
	Author   string `json:"author"`
	Message  string `json:"message"`
	EditedAt string `json:"edited_at"`
}

type editCommentOut struct {
	TicketID      string      `json:"ticket_id"`
	Comment       commentView `json:"comment"`
	CommitWarning string      `json:"commit_warning,omitempty"`
}

type testCasesOut struct {
	TicketID      string            `json:"ticket_id"`
	TestCases     []ticket.TestCase `json:"test_cases"`
	CommitWarning string            `json:"commit_warning,omitempty"`
}

type testCaseOut struct {
	TicketID      string          `json:"ticket_id"`
	TestCase      ticket.TestCase `json:"test_case"`
	CommitWarning string          `json:"commit_warning,omitempty"`
}

func validHours(h float64) bool {
	return h > 0 && h <= 24 && math.Abs(h*4-math.Round(h*4)) < 1e-9
}

// ticketTool registers a tool whose handler gets the resolved project root.
func ticketTool[In interface{ ref() writeTicketIn }, Out any](s *mcp.Server, t *mcp.Tool, h func(root string, in In) (Out, error)) {
	mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		r := in.ref()
		root, err := resolve(r.Project, r.TicketID)
		if err != nil {
			return nil, zero, err
		}
		out, err := h(root, in)
		if err != nil {
			return nil, zero, mapError(err)
		}
		return nil, out, nil
	})
}

func (w writeTicketIn) ref() writeTicketIn { return w }

func registerWriteTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "hate_create_tickets",
		Description: "Create 1-50 tickets in one write and one commit, e.g. a feature parent and its children. Give each a ref; children point at the parent with parent_ref " +
			"(adds parent:<id>) and at earlier siblings with predecessor_refs; test_cases go inline. Every ticket is validated before any is written. " +
			"Tag rules (hate://guide?section=3): the parent carries cfp:N and type:feature, no estimate_hours; each child carries exactly one class tag; " +
			"config/nonfunc children carry estimate_hours in quarter hours; functional children carry none.",
		Annotations: write("Create tickets", false, false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createTicketsIn) (*mcp.CallToolResult, createTicketsOut, error) {
		root, err := service.ResolveProject(in.Project)
		if err != nil {
			return nil, createTicketsOut{}, mapError(err)
		}
		batch := make([]service.BulkTicket, len(in.Tickets))
		for i, t := range in.Tickets {
			cases := make([]ticket.TestCase, len(t.TestCases))
			for j, c := range t.TestCases {
				cases[j] = ticket.TestCase{Step: c.Step, Expected: c.Expected}
			}
			batch[i] = service.BulkTicket{
				Ref: t.Ref, Type: t.Type, Title: t.Title, Description: t.Description, Priority: t.Priority,
				EstimateHours: t.EstimateHours, Assignee: t.Assignee, Tags: t.Tags, Phase: t.Phase,
				PlannedStartDate: t.PlannedStartDate, DueDate: t.DueDate, ParentRef: t.ParentRef, ParentID: t.ParentID,
				PredecessorRefs: t.PredecessorRefs, Predecessors: t.Predecessors, TestCases: cases,
			}
		}
		res, err := service.CreateTickets(root, batch, author(root, in.Author))
		if err != nil {
			return nil, createTicketsOut{}, mapError(err)
		}
		est := service.Estimates(root)
		out := createTicketsOut{Created: res.Created, Tickets: []TicketSummary{}, CommitWarning: res.CommitWarning}
		for _, t := range res.Tickets {
			out.Tickets = append(out.Tickets, summaryFromTicket(t, est))
		}
		return nil, out, nil
	})

	ticketTool(s, &mcp.Tool{
		Name: "hate_update_ticket",
		Description: "Change several fields and tags of one ticket at once (title, description, priority, estimate_hours, assignee, phase, dates; add_tags / remove_tags). " +
			"All-or-nothing: one bad value and nothing changes. One activity entry per field, one commit. Status changes use promote / demote / block.",
		Annotations: write("Update ticket", false, true),
	}, func(root string, in updateTicketIn) (TicketView, error) {
		fields := map[string]interface{}{}
		if f := in.Fields; f != nil {
			set := func(name string, v *string) {
				if v != nil {
					fields[name] = *v
				}
			}
			set("title", f.Title)
			set("description", f.Description)
			set("priority", f.Priority)
			set("assignee", f.Assignee)
			set("phase", f.Phase)
			set("planned_start_date", f.PlannedStartDate)
			set("due_date", f.DueDate)
			if f.EstimateHours != nil {
				fields["estimate_hours"] = *f.EstimateHours
			}
		}
		if in.ClearEstimateHours {
			if _, both := fields["estimate_hours"]; both {
				return TicketView{}, invalid("give estimate_hours or clear_estimate_hours, not both")
			}
			fields["estimate_hours"] = nil
		}
		if len(fields) == 0 && len(in.AddTags) == 0 && len(in.RemoveTags) == 0 {
			return TicketView{}, invalid("nothing to change: give fields, clear_estimate_hours, add_tags or remove_tags")
		}
		res, err := service.EditFields(root, in.TicketID, fields, in.AddTags, in.RemoveTags, author(root, in.Author))
		if err != nil {
			return TicketView{}, err
		}
		return viewFromResult(root, res), nil
	})

	ticketTool(s, &mcp.Tool{
		Name: "hate_promote_ticket",
		Description: "Move a ticket to its next status (the ticket's workflow.promote_to). Leaving a work status (in_progress, qa_testing, rework) needs a described time " +
			"entry logged since entering it: call hate_log_time first, or you get needs_time_log. The first promote out of not_started checks the class / " +
			"estimate_hours / cfp: rules (estimate_invalid; see hate://guide?section=3).",
		Annotations: write("Promote ticket", false, false),
	}, func(root string, in writeTicketIn) (TicketView, error) {
		res, err := service.Promote(root, in.TicketID, author(root, in.Author))
		if err != nil {
			return TicketView{}, err
		}
		return viewFromResult(root, res), nil
	})

	ticketTool(s, &mcp.Tool{
		Name:        "hate_demote_ticket",
		Description: "Move a ticket back one status (workflow.demote_to), e.g. QA found a problem.",
		Annotations: write("Demote ticket", false, false),
	}, func(root string, in writeTicketIn) (TicketView, error) {
		res, err := service.Demote(root, in.TicketID, author(root, in.Author))
		if err != nil {
			return TicketView{}, err
		}
		return viewFromResult(root, res), nil
	})

	ticketTool(s, &mcp.Tool{
		Name:        "hate_block_ticket",
		Description: "Mark a ticket blocked with the reason (shown on the PM dashboard). Promote or demote leaves blocked.",
		Annotations: write("Block ticket", false, true),
	}, func(root string, in reasonIn) (TicketView, error) {
		if strings.TrimSpace(in.Reason) == "" {
			return TicketView{}, invalid("reason is required")
		}
		res, err := service.Block(root, in.TicketID, in.Reason, author(root, in.Author))
		if err != nil {
			return TicketView{}, err
		}
		return viewFromResult(root, res), nil
	})

	ticketTool(s, &mcp.Tool{
		Name: "hate_force_close_ticket",
		Description: "Close a ticket skipping its workflow, recording why. This DESCOPES the ticket (dropped, duplicate, cancelled); it is not how you finish work, " +
			"which is promote. Confirm with the user before using it.",
		Annotations: write("Force-close ticket", true, true),
	}, func(root string, in reasonIn) (TicketView, error) {
		if len(strings.TrimSpace(in.Reason)) < 5 {
			return TicketView{}, invalid("reason must be at least 5 characters")
		}
		res, err := service.ForceClose(root, in.TicketID, in.Reason, author(root, in.Author))
		if err != nil {
			return TicketView{}, err
		}
		return viewFromResult(root, res), nil
	})

	ticketTool(s, &mcp.Tool{
		Name: "hate_log_time",
		Description: "Log time on a ticket (quarter-hour steps, with a description of the work). Needed before promoting out of a work status. Under strict time " +
			"enforcement, a log past a wrap ticket's allotment returns needs_time_extension: ask the user, then retry with extend_authorized and extend_reason.",
		Annotations: write("Log time", false, false),
	}, func(root string, in logTimeIn) (logTimeOut, error) {
		if !validHours(in.Hours) {
			return logTimeOut{}, invalid("hours must be > 0, <= 24, in quarter-hour steps (0.25, 0.5, ...)")
		}
		if strings.TrimSpace(in.Description) == "" {
			return logTimeOut{}, invalid("description is required")
		}
		date := in.Date
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}
		res, err := service.LogTime(root, in.TicketID, service.LogTimeParams{
			Date: date, Hours: in.Hours, Description: in.Description, Author: author(root, in.Author),
			ExtendAuthorized: in.ExtendAuthorized, ExtendReason: in.ExtendReason,
		})
		if err != nil {
			return logTimeOut{}, err
		}
		out := logTimeOut{TicketView: viewFromResult(root, res)}
		for _, te := range res.Ticket.TimeEntries {
			out.LoggedHoursTotal += te.Hours
		}
		if c := ticket.ClassOf(res.Ticket); ticket.IsWrapClass(c) && res.Ticket.EstimateHours != nil {
			v := *res.Ticket.EstimateHours
			out.AllottedHours = &v
		}
		return out, nil
	})

	ticketTool(s, &mcp.Tool{
		Name:        "hate_add_comment",
		Description: "Add a comment (markdown) to a ticket's activity log. Returns the new comment's id, which hate_edit_comment takes.",
		Annotations: write("Add comment", false, false),
	}, func(root string, in commentIn) (addCommentOut, error) {
		if strings.TrimSpace(in.Message) == "" {
			return addCommentOut{}, invalid("message is required")
		}
		res, err := service.AddComment(root, in.TicketID, in.Message, author(root, in.Author))
		if err != nil {
			return addCommentOut{}, err
		}
		act := res.Ticket.Activity
		return addCommentOut{TicketID: res.Ticket.ID, CommentID: act[len(act)-1].ID, ActivityCount: len(act), CommitWarning: res.CommitWarning}, nil
	})

	ticketTool(s, &mcp.Tool{
		Name: "hate_edit_comment",
		Description: "Replace a comment's text (fix a typo or a wrong statement). The previous text is kept in a comment_edited activity entry. " +
			"Only comments can be edited; they can't be deleted.",
		Annotations: write("Edit comment", false, true),
	}, func(root string, in editCommentIn) (editCommentOut, error) {
		res, err := service.EditComment(root, in.TicketID, in.CommentID, in.Message, author(root, in.Author))
		if err != nil {
			return editCommentOut{}, err
		}
		out := editCommentOut{TicketID: res.Ticket.ID, CommitWarning: res.CommitWarning}
		for _, a := range res.Ticket.Activity {
			if a.ID == in.CommentID {
				out.Comment = commentView{ID: a.ID, Author: a.Author, Message: a.Detail, EditedAt: a.EditedAt}
			}
		}
		return out, nil
	})

	ticketTool(s, &mcp.Tool{
		Name:        "hate_add_test_cases",
		Description: "Add 1-20 acceptance test cases (step + expected) to a ticket in one write.",
		Annotations: write("Add test cases", false, false),
	}, func(root string, in addTestCasesIn) (testCasesOut, error) {
		if len(in.Cases) == 0 || len(in.Cases) > 20 {
			return testCasesOut{}, invalid("give 1 to 20 cases")
		}
		cases := make([]ticket.TestCase, len(in.Cases))
		for i, c := range in.Cases {
			if strings.TrimSpace(c.Step) == "" || strings.TrimSpace(c.Expected) == "" {
				return testCasesOut{}, invalid("every case needs a step and an expected result")
			}
			cases[i] = ticket.TestCase{Step: c.Step, Expected: c.Expected}
		}
		res, err := service.AddTestCases(root, in.TicketID, cases, author(root, in.Author))
		if err != nil {
			return testCasesOut{}, err
		}
		return testCasesOut{TicketID: res.Ticket.ID, TestCases: res.Ticket.TestCases, CommitWarning: res.CommitWarning}, nil
	})

	ticketTool(s, &mcp.Tool{
		Name:        "hate_update_test_case",
		Description: "Edit a test case's step / expected, or record its QA result (status pass, fail, or empty for untested) with a comment.",
		Annotations: write("Update test case", false, true),
	}, func(root string, in updateTestCaseIn) (testCaseOut, error) {
		if in.Status != nil && *in.Status != "" && *in.Status != "pass" && *in.Status != "fail" {
			return testCaseOut{}, invalid(`status must be "pass", "fail" or ""`)
		}
		res, err := service.UpdateTestCase(root, in.TicketID, in.CaseID, ticket.TestCaseUpdate{
			Step: in.Step, Expected: in.Expected, Status: in.Status, Comment: in.Comment,
		}, author(root, in.Author))
		if err != nil {
			return testCaseOut{}, err
		}
		out := testCaseOut{TicketID: res.Ticket.ID, CommitWarning: res.CommitWarning}
		for _, c := range res.Ticket.TestCases {
			if c.ID == in.CaseID {
				out.TestCase = c
			}
		}
		return out, nil
	})

	ticketTool(s, &mcp.Tool{
		Name:        "hate_link_tickets",
		Description: "Add or remove predecessor links: a predecessor must finish before this ticket is ready (drives hate_get_ready and the schedule).",
		Annotations: write("Link tickets", false, true),
	}, func(root string, in linkIn) (TicketView, error) {
		if len(in.AddPredecessors) == 0 && len(in.RemovePredecessors) == 0 {
			return TicketView{}, invalid("give add_predecessors or remove_predecessors")
		}
		a := author(root, in.Author)
		var res *service.Result
		var err error
		for _, p := range in.AddPredecessors {
			if res, err = service.AddPredecessor(root, in.TicketID, p, a); err != nil {
				return TicketView{}, err
			}
		}
		for _, p := range in.RemovePredecessors {
			if res, err = service.RemovePredecessor(root, in.TicketID, p, a); err != nil {
				return TicketView{}, err
			}
		}
		return viewFromResult(root, res), nil
	})
}
