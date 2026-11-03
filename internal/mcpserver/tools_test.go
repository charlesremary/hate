// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hate/internal/config"
	"hate/internal/service"
	"hate/internal/ticket"
)

// setupProject points the app config at a temp projects root holding one git
// project "est" (prefix EST) and returns its root and a git runner.
func setupProject(t *testing.T) (string, func(...string) string) {
	t.Helper()
	base := t.TempDir()
	old := config.AppConfigPath
	config.AppConfigPath = filepath.Join(base, "app.json")
	t.Cleanup(func() { config.AppConfigPath = old })
	projects := filepath.Join(base, "projects")
	root := filepath.Join(projects, "est")
	app, _ := json.Marshal(map[string]any{"projects_root": projects})
	if err := os.WriteFile(config.AppConfigPath, app, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ticket.WriteConfig(root, ticket.DefaultConfig("c", "Est", "est", "EST")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	git("add", "-A")
	git("commit", "-q", "-m", "init")

	md, err := os.ReadFile("../../docs/ticketing-and-cfp-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	service.SetGuide(string(md))
	t.Cleanup(func() { service.SetGuide("") })
	return root, git
}

// connect returns a client session to a fresh server over in-memory transports.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := NewServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call runs a tool; it returns isError, the text content and the structured
// content decoded into a map.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (bool, string, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	out := map[string]any{}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return res.IsError, text, out
}

func mustOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	isErr, text, out := call(t, cs, name, args)
	if isErr {
		t.Fatalf("%s %v: error %s", name, args, text)
	}
	return out
}

func newTicket(t *testing.T, root string, p ticket.CreateTicketParams) string {
	t.Helper()
	res, err := service.CreateTicket(root, p)
	if err != nil {
		t.Fatal(err)
	}
	return res.Ticket.ID
}

var readTools = []string{"hate_get_project", "hate_get_ready", "hate_get_ticket", "hate_list_projects", "hate_list_tickets", "hate_search_tickets"}

// HATE-28gd tc3 (+ HATE-t546 annotations): stable order, hints as specified.
func TestToolsList(t *testing.T) {
	setupProject(t)
	cs := connect(t)
	list := func() []*mcp.Tool {
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Tools
	}
	a, b := list(), list()
	if len(a) != 19 {
		t.Errorf("%d tools, want 19", len(a))
	}
	ro := map[string]bool{}
	for _, n := range readTools {
		ro[n] = true
	}
	for i, tool := range a {
		if b[i].Name != tool.Name {
			t.Fatalf("order differs at %d: %s vs %s", i, tool.Name, b[i].Name)
		}
		an := tool.Annotations
		if an == nil || an.OpenWorldHint == nil || *an.OpenWorldHint {
			t.Errorf("%s: openWorldHint should be false", tool.Name)
			continue
		}
		if an.ReadOnlyHint != ro[tool.Name] {
			t.Errorf("%s: readOnlyHint = %v", tool.Name, an.ReadOnlyHint)
		}
		if !ro[tool.Name] {
			destructive := an.DestructiveHint != nil && *an.DestructiveHint
			if destructive != (tool.Name == "hate_force_close_ticket") {
				t.Errorf("%s: destructiveHint = %v", tool.Name, destructive)
			}
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: no outputSchema", tool.Name)
		}
	}
}

// HATE-28gd tc1, tc2: get_ticket infers the project; unknown ids are not_found.
func TestGetTicket(t *testing.T) {
	root, _ := setupProject(t)
	id := newTicket(t, root, ticket.CreateTicketParams{Type: "dev_task", Title: "A", Tags: []string{"functional"}})
	cs := connect(t)

	out := mustOK(t, cs, "hate_get_ticket", map[string]any{"ticket_id": id})
	if out["id"] != id || out["workflow"] == nil {
		t.Errorf("get_ticket: id %v workflow %v", out["id"], out["workflow"])
	}
	isErr, text, _ := call(t, cs, "hate_get_ticket", map[string]any{"ticket_id": "EST-zzzz"})
	if !isErr || !strings.Contains(text, `"error":"not_found"`) {
		t.Errorf("missing ticket: isError %v text %s", isErr, text)
	}
	isErr, text, _ = call(t, cs, "hate_get_ticket", map[string]any{"ticket_id": "NOPE-1234"})
	if !isErr || !strings.Contains(text, `"error":"not_found"`) {
		t.Errorf("unknown project: isError %v text %s", isErr, text)
	}
}

func TestReadTools(t *testing.T) {
	root, _ := setupProject(t)
	p := newTicket(t, root, ticket.CreateTicketParams{Type: "task", Title: "Feature X", Tags: []string{"cfp:4", "type:feature"}})
	a := newTicket(t, root, ticket.CreateTicketParams{Type: "dev_task", Title: "Code X", Tags: []string{"functional", "parent:" + p}})
	newTicket(t, root, ticket.CreateTicketParams{Type: "task", Title: "Later", Tags: []string{"backlog"}})
	cs := connect(t)

	projects := mustOK(t, cs, "hate_list_projects", nil)["projects"].([]any)
	if len(projects) != 1 || projects[0].(map[string]any)["prefix"] != "EST" {
		t.Errorf("projects = %v", projects)
	}
	if d := mustOK(t, cs, "hate_get_project", map[string]any{"project": "est"}); d["prefix"] != "EST" || d["resources"] == nil {
		t.Errorf("project = %v", d)
	}
	kids := mustOK(t, cs, "hate_list_tickets", map[string]any{"project": "EST", "parent": p})
	if kids["total"].(float64) != 1 || kids["tickets"].([]any)[0].(map[string]any)["id"] != a {
		t.Errorf("children = %v", kids)
	}
	if all := mustOK(t, cs, "hate_list_tickets", map[string]any{"project": "est", "include_backlog": false}); all["total"].(float64) != 2 {
		t.Errorf("without backlog: total %v, want 2", all["total"])
	}
	hits := mustOK(t, cs, "hate_search_tickets", map[string]any{"q": "code x"})["results"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["ticket_id"] != a {
		t.Errorf("search = %v", hits)
	}
	mustOK(t, cs, "hate_get_ready", map[string]any{"project": "est"})
}

// HATE-28gd tc4: the guide resource, whole and by section; the overview.
func TestResources(t *testing.T) {
	setupProject(t)
	cs := connect(t)
	read := func(uri string) *mcp.ResourceContents {
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("read %s: %v", uri, err)
		}
		if len(res.Contents) != 1 {
			t.Fatalf("read %s: %d contents", uri, len(res.Contents))
		}
		return res.Contents[0]
	}
	c := read("hate://guide?section=3")
	if c.MIMEType != "text/markdown" || !strings.HasPrefix(c.Text, "## 3. Tag conventions") {
		t.Errorf("section 3: %s %q", c.MIMEType, c.Text[:40])
	}
	if c := read("hate://guide"); !strings.Contains(c.Text, "## 12.") {
		t.Errorf("whole guide missing section 12")
	}
	if c := read("hate://projects/EST/overview"); c.MIMEType != "application/json" || !strings.Contains(c.Text, `"contacts":[]`) {
		t.Errorf("overview: %s %s", c.MIMEType, c.Text)
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "hate://guide?section=99"}); err == nil {
		t.Error("section 99: want an error")
	}
}

// HATE-t546 tc1, tc2: promote is gated on logged time; logging unblocks it.
func TestPromoteFlow(t *testing.T) {
	root, _ := setupProject(t)
	id := newTicket(t, root, ticket.CreateTicketParams{Type: "dev_task", Title: "A", Tags: []string{"functional"}})
	if _, err := ticket.ChangeStatus(root, id, "in_progress", "a"); err != nil {
		t.Fatal(err)
	}
	cs := connect(t)

	isErr, text, _ := call(t, cs, "hate_promote_ticket", map[string]any{"ticket_id": id})
	if !isErr || !strings.Contains(text, `"error":"needs_time_log"`) || !strings.Contains(text, "hate_log_time") {
		t.Fatalf("promote without time: %v %s", isErr, text)
	}
	if tk, _ := ticket.ReadTicket(root, id); tk.Status != "in_progress" {
		t.Errorf("status %s, want in_progress", tk.Status)
	}
	lt := mustOK(t, cs, "hate_log_time", map[string]any{"ticket_id": id, "hours": 0.5, "description": "wired handler"})
	if lt["logged_hours_total"].(float64) != 0.5 {
		t.Errorf("logged_hours_total = %v", lt["logged_hours_total"])
	}
	out := mustOK(t, cs, "hate_promote_ticket", map[string]any{"ticket_id": id})
	if out["status"] != "dev_complete" {
		t.Errorf("status after promote = %v, want dev_complete", out["status"])
	}
	// Author defaults to the project's git identity.
	tk, _ := ticket.ReadTicket(root, id)
	if last := tk.Activity[len(tk.Activity)-1]; last.Author != "test@example.com" {
		t.Errorf("author = %q, want the git identity", last.Author)
	}
	// Bad hours are refused before anything is written.
	if isErr, text, _ := call(t, cs, "hate_log_time", map[string]any{"ticket_id": id, "hours": 0.3, "description": "x"}); !isErr || !strings.Contains(text, "invalid_input") {
		t.Errorf("0.3h: %v %s", isErr, text)
	}
}

// HATE-t546 tc3: a feature and its child in one call and one commit.
func TestCreateTickets(t *testing.T) {
	root, git := setupProject(t)
	before := git("rev-parse", "HEAD")
	cs := connect(t)
	out := mustOK(t, cs, "hate_create_tickets", map[string]any{
		"project": "EST",
		"tickets": []map[string]any{
			{"ref": "p", "type": "task", "title": "Build X", "tags": []string{"cfp:4", "type:feature"}},
			{"ref": "c", "type": "dev_task", "title": "Code X", "parent_ref": "p", "tags": []string{"functional"},
				"test_cases": []map[string]any{{"step": "s1", "expected": "e1"}, {"step": "s2", "expected": "e2"}}},
		},
	})
	created := out["created"].([]any)
	if len(created) != 2 {
		t.Fatalf("created = %v", created)
	}
	pid := created[0].(map[string]any)["id"].(string)
	cid := created[1].(map[string]any)["id"].(string)
	if n := git("rev-list", "--count", before+"..HEAD"); n != "1" {
		t.Errorf("%s new commits, want 1", n)
	}
	if msg := git("log", "-1", "--format=%s"); !strings.Contains(msg, pid) || !strings.Contains(msg, cid) {
		t.Errorf("commit %q should name both ids", msg)
	}
	child, _ := ticket.ReadTicket(root, cid)
	if !ticket.Contains(child.Tags, "parent:"+pid) || len(child.TestCases) != 2 {
		t.Errorf("child tags %v, %d test cases", child.Tags, len(child.TestCases))
	}
	if kid := out["tickets"].([]any)[1].(map[string]any); kid["parent"] != pid || kid["class"] != "functional" {
		t.Errorf("summary = %v", kid)
	}
}

// HATE-t546 tc4: a closed project refuses writes, reads still work.
func TestClosedProject(t *testing.T) {
	root, _ := setupProject(t)
	id := newTicket(t, root, ticket.CreateTicketParams{Type: "task", Title: "A"})
	cfg, _ := ticket.ReadConfig(root)
	cfg.ClosedAt = "2026-11-02"
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := ticket.ReadTicket(root, id)
	cs := connect(t)
	isErr, text, _ := call(t, cs, "hate_add_comment", map[string]any{"ticket_id": id, "message": "hi"})
	if !isErr || !strings.Contains(text, `"error":"project_closed"`) {
		t.Errorf("comment on closed project: %v %s", isErr, text)
	}
	after, _ := ticket.ReadTicket(root, id)
	if len(after.Activity) != len(before.Activity) {
		t.Error("activity changed")
	}
	mustOK(t, cs, "hate_get_ticket", map[string]any{"ticket_id": id})
}

// The remaining write tools, one happy path each.
func TestOtherWriteTools(t *testing.T) {
	root, _ := setupProject(t)
	a := newTicket(t, root, ticket.CreateTicketParams{Type: "dev_task", Title: "A"})
	b := newTicket(t, root, ticket.CreateTicketParams{Type: "dev_task", Title: "B"})
	cs := connect(t)

	up := mustOK(t, cs, "hate_update_ticket", map[string]any{"ticket_id": a,
		"fields": map[string]any{"phase": "02 - Build", "estimate_hours": 1.5}, "add_tags": []string{"config"}})
	if up["phase"] != "02 - Build" || up["estimate_hours"].(float64) != 1.5 {
		t.Errorf("update = phase %v estimate %v", up["phase"], up["estimate_hours"])
	}
	if up := mustOK(t, cs, "hate_update_ticket", map[string]any{"ticket_id": a, "clear_estimate_hours": true}); up["estimate_hours"] != nil {
		t.Errorf("clear estimate: %v", up["estimate_hours"])
	}
	cm := mustOK(t, cs, "hate_add_comment", map[string]any{"ticket_id": a, "message": "teh fix"})
	ec := mustOK(t, cs, "hate_edit_comment", map[string]any{"ticket_id": a, "comment_id": cm["comment_id"], "message": "the fix"})
	if ec["comment"].(map[string]any)["message"] != "the fix" {
		t.Errorf("edit comment = %v", ec)
	}
	tcs := mustOK(t, cs, "hate_add_test_cases", map[string]any{"ticket_id": a, "cases": []map[string]any{{"step": "s", "expected": "e"}}})
	tcID := tcs["test_cases"].([]any)[0].(map[string]any)["id"]
	if u := mustOK(t, cs, "hate_update_test_case", map[string]any{"ticket_id": a, "case_id": tcID, "status": "pass"}); u["test_case"].(map[string]any)["status"] != "pass" {
		t.Errorf("update test case = %v", u)
	}
	if l := mustOK(t, cs, "hate_link_tickets", map[string]any{"ticket_id": b, "add_predecessors": []string{a}}); len(l["predecessors"].([]any)) != 1 {
		t.Errorf("link = %v", l["predecessors"])
	}
	if bl := mustOK(t, cs, "hate_block_ticket", map[string]any{"ticket_id": b, "reason": "waiting on A"}); bl["status"] != "blocked" {
		t.Errorf("block = %v", bl["status"])
	}
	if dm := mustOK(t, cs, "hate_demote_ticket", map[string]any{"ticket_id": b}); dm["status"] == "blocked" {
		t.Errorf("demote left blocked")
	}
	if isErr, _, _ := call(t, cs, "hate_force_close_ticket", map[string]any{"ticket_id": b, "reason": "no"}); !isErr {
		t.Error("force close with a 2-char reason should fail")
	}
	if fc := mustOK(t, cs, "hate_force_close_ticket", map[string]any{"ticket_id": b, "reason": "duplicate of A"}); fc["status"] != "closed" {
		t.Errorf("force close = %v", fc["status"])
	}
}
