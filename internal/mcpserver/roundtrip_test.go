// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hate/internal/api"
	"hate/internal/ticket"
)

// HATE-ramw: the agent loop end to end at the protocol level, over the
// in-memory transport and over Streamable HTTP on the full router (REST and
// /mcp together, as main.go mounts them), cross-checked through REST.

// fullServer serves the app's REST routes plus /mcp, like main.go.
func fullServer(t *testing.T) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	api.RegisterProjectRoutes(r)
	api.RegisterTicketRoutes(r)
	api.RegisterPMRoutes(r)
	api.RegisterSearchRoutes(r)
	api.RegisterGuideRoutes(r)
	r.Handle(Path, Handler(0))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// connectHTTP connects a go-sdk client to srv's /mcp. version "" = latest.
func connectHTTP(t *testing.T, srv *httptest.Server, version string) *mcp.ClientSession {
	t.Helper()
	tr := &mcp.StreamableClientTransport{Endpoint: srv.URL + Path, DisableStandaloneSSE: true}
	var opts *mcp.ClientSessionOptions
	if version != "" {
		opts = &mcp.ClientSessionOptions{ProtocolVersion: version}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "roundtrip", Version: "1"}, nil).Connect(context.Background(), tr, opts)
	if err != nil {
		t.Fatalf("connect %s: %v", version, err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// restGet GETs a REST path and decodes the JSON object.
func restGet(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return out
}

// agentLoop is what an agent does with a feature: find the project, create
// the feature and its children, add test cases, start work, log time,
// promote, set the phase. Returns the child it worked.
func agentLoop(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	projects := mustOK(t, cs, "hate_list_projects", nil)["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("projects = %v", projects)
	}
	prefix := projects[0].(map[string]any)["prefix"].(string)

	out := mustOK(t, cs, "hate_create_tickets", map[string]any{
		"project": prefix,
		"author":  "agent@x",
		"tickets": []map[string]any{
			{"ref": "p", "type": "task", "title": "Export billing to CSV", "tags": []string{"type:feature", "cfp:6"}},
			{"ref": "code", "type": "dev_task", "title": "Add the CSV endpoint", "parent_ref": "p", "tags": []string{"functional"}},
			{"ref": "deploy", "type": "task", "title": "Deploy the export", "parent_ref": "p", "tags": []string{"nonfunc"},
				"estimate_hours": 1, "predecessor_refs": []string{"code"}},
		},
	})
	child := out["created"].([]any)[1].(map[string]any)["id"].(string)

	kids := mustOK(t, cs, "hate_list_tickets", map[string]any{"project": prefix, "parent": out["created"].([]any)[0].(map[string]any)["id"]})
	if kids["total"].(float64) != 2 {
		t.Fatalf("children = %v", kids)
	}
	mustOK(t, cs, "hate_add_test_cases", map[string]any{"ticket_id": child,
		"cases": []map[string]any{{"step": "GET /billing.csv", "expected": "a CSV with one row per entry"}}})

	if st := mustOK(t, cs, "hate_promote_ticket", map[string]any{"ticket_id": child})["status"]; st != "in_progress" {
		t.Fatalf("start work: status %v", st)
	}
	isErr, text, _ := call(t, cs, "hate_promote_ticket", map[string]any{"ticket_id": child})
	if !isErr || !strings.Contains(text, "needs_time_log") {
		t.Fatalf("promote without time: %v %s", isErr, text)
	}
	mustOK(t, cs, "hate_log_time", map[string]any{"ticket_id": child, "hours": 1.25, "description": "wrote the endpoint"})
	if st := mustOK(t, cs, "hate_promote_ticket", map[string]any{"ticket_id": child})["status"]; st != "dev_complete" {
		t.Fatalf("finish work: status %v", st)
	}
	up := mustOK(t, cs, "hate_update_ticket", map[string]any{"ticket_id": child, "fields": map[string]any{"phase": "02 - Build"}})
	if up["phase"] != "02 - Build" {
		t.Fatalf("phase = %v", up["phase"])
	}
	ready := mustOK(t, cs, "hate_get_ready", map[string]any{"project": prefix})
	if !strings.Contains(fmt.Sprint(ready["ready"]), "Deploy the export") {
		t.Errorf("deploy should be ready once code is done: %v", ready["ready"])
	}
	return child
}

func TestRoundTripInMemory(t *testing.T) {
	root, _ := setupProject(t)
	child := agentLoop(t, connect(t))
	tk, _ := ticket.ReadTicket(root, child)
	if tk.Status != "dev_complete" || len(tk.TimeEntries) != 1 || len(tk.TestCases) != 1 {
		t.Errorf("on disk: status %s, %d time entries, %d test cases", tk.Status, len(tk.TimeEntries), len(tk.TestCases))
	}
}

func TestRoundTripHTTP(t *testing.T) {
	_, git := setupProject(t)
	srv := fullServer(t)
	cs := connectHTTP(t, srv, "")
	child := agentLoop(t, cs)

	// Read it back over REST: the same ticket the tools wrote.
	viaMCP := mustOK(t, cs, "hate_get_ticket", map[string]any{"ticket_id": child})
	viaREST := restGet(t, srv, "/api/projects/est/tickets/"+child)
	for _, k := range []string{"status", "phase", "title", "estimated_hours"} {
		if fmt.Sprint(viaREST[k]) != fmt.Sprint(viaMCP[k]) {
			t.Errorf("%s: REST %v, MCP %v", k, viaREST[k], viaMCP[k])
		}
	}
	if viaREST["phase"] != "02 - Build" {
		t.Errorf("REST phase = %v", viaREST["phase"])
	}
	if te := viaREST["time_entries"].([]any); len(te) != 1 {
		t.Errorf("REST time entries = %v", te)
	}
	// Every write was committed: nothing left in the working tree.
	if st := git("status", "--porcelain", "tickets"); st != "" {
		t.Errorf("uncommitted ticket files: %s", st)
	}
}

// A client on the previous protocol revision (initialize handshake) still
// gets the full tool set from the stateless server.
func TestRoundTripLegacyProtocol(t *testing.T) {
	setupProject(t)
	cs := connectHTTP(t, fullServer(t), "2025-11-25")
	if ir := cs.InitializeResult(); ir == nil || ir.ProtocolVersion != "2025-11-25" {
		t.Fatalf("initialize result = %+v, want protocol 2025-11-25", ir)
	}
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 19 {
		t.Errorf("%d tools over 2025-11-25, want 19", len(res.Tools))
	}
	mustOK(t, cs, "hate_list_projects", nil)
}

// 20 parallel time logs over MCP alongside REST comments on the same ticket:
// the project lock is shared, so every entry lands and each write is its own
// commit (mirrors TestConcurrentTimeLogs on the REST side).
func TestRoundTripConcurrentWrites(t *testing.T) {
	root, git := setupProject(t)
	id := newTicket(t, root, ticket.CreateTicketParams{Type: "task", Title: "Busy"})
	before := git("rev-parse", "HEAD")
	srv := fullServer(t)
	cs := connectHTTP(t, srv, "")

	var wg sync.WaitGroup
	errs := make(chan string, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "hate_log_time",
				Arguments: map[string]any{"ticket_id": id, "hours": 0.25, "description": fmt.Sprintf("slice %d", i)}})
			if err != nil || res.IsError {
				errs <- fmt.Sprintf("log %d: %v %v", i, err, res)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			body := strings.NewReader(fmt.Sprintf(`{"message":"rest %d"}`, i))
			resp, err := http.Post(srv.URL+"/api/projects/est/tickets/"+id+"/comment", "application/json", body)
			if err != nil {
				errs <- err.Error()
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Sprintf("comment %d: %d", i, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	tk, _ := ticket.ReadTicket(root, id)
	comments := 0
	for _, a := range tk.Activity {
		if a.Action == "comment" {
			comments++
		}
	}
	if len(tk.TimeEntries) != 20 || comments != 20 {
		t.Errorf("time entries %d, comments %d, want 20 and 20", len(tk.TimeEntries), comments)
	}
	if n := git("rev-list", "--count", before+"..HEAD"); n != "40" {
		t.Errorf("%s new commits, want 40", n)
	}
	if logs := git("log", "--format=%s", before+"..HEAD", "--grep", "time logged"); strings.Count(logs, "time logged") != 20 {
		t.Errorf("time-logged commits:\n%s", logs)
	}
}
