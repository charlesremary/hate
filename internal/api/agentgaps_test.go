// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hate/internal/service"
	"hate/internal/ticket"
)

// HATE-wla6: the REST endpoints agents were missing (parent filter,
// multi-field edit, prefix lookup, bulk create, guide).

func createT(t *testing.T, h http.Handler, body map[string]interface{}) string {
	t.Helper()
	code, out := do(t, h, "POST", "/api/projects/est/tickets/", body)
	if code != http.StatusOK {
		t.Fatalf("create %v: %d %v", body, code, out)
	}
	return out["id"].(string)
}

func listIDs(t *testing.T, h http.Handler, query string) []string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/projects/est/tickets/?"+query, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list ?%s: %d %s", query, rec.Code, rec.Body.String())
	}
	var items []map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &items)
	ids := []string{}
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	return ids
}

// tc1: ?parent= returns exactly the children of a feature.
func TestListByParent(t *testing.T) {
	h, _ := setupEstimateProject(t)
	p := createT(t, h, map[string]interface{}{"type": "task", "title": "Feature", "tags": []string{"cfp:4"}})
	a := createT(t, h, map[string]interface{}{"type": "dev_task", "title": "A", "tags": []string{"parent:" + p, "functional"}})
	createT(t, h, map[string]interface{}{"type": "dev_task", "title": "B"})
	if ids := listIDs(t, h, "parent="+p); len(ids) != 1 || ids[0] != a {
		t.Errorf("?parent=%s = %v, want [%s]", p, ids, a)
	}
}

// tc2 + success path: multi-field edit is all-or-nothing.
func TestMultiFieldEdit(t *testing.T) {
	h, root := setupEstimateProject(t)
	a := createT(t, h, map[string]interface{}{"type": "dev_task", "title": "A", "tags": []string{"x"}})
	url := "/api/projects/est/tickets/" + a

	code, _ := do(t, h, "PATCH", url, map[string]interface{}{"fields": map[string]interface{}{"estimate_hours": 0.3}, "add_tags": []string{"config"}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad estimate: %d, want 422", code)
	}
	tk, _ := ticket.ReadTicket(root, a)
	if ticket.Contains(tk.Tags, "config") || tk.EstimateHours != nil {
		t.Fatalf("failed edit changed the ticket: tags %v estimate %v", tk.Tags, tk.EstimateHours)
	}

	code, out := do(t, h, "PATCH", url, map[string]interface{}{
		"fields":   map[string]interface{}{"estimate_hours": 1.5, "phase": "02 - Build", "title": "A2"},
		"add_tags": []string{"config"}, "remove_tags": []string{"x"}, "author": "agent@x",
	})
	if code != http.StatusOK {
		t.Fatalf("multi edit: %d %v", code, out)
	}
	tk, _ = ticket.ReadTicket(root, a)
	if tk.Title != "A2" || tk.Phase == nil || *tk.Phase != "02 - Build" || tk.EstimateHours == nil || *tk.EstimateHours != 1.5 {
		t.Errorf("fields not applied: %+v", tk)
	}
	if len(tk.Tags) != 1 || tk.Tags[0] != "config" {
		t.Errorf("tags = %v, want [config]", tk.Tags)
	}
	edits := 0
	for _, act := range tk.Activity {
		if act.Action == "edited" {
			edits++
		}
	}
	if edits != 4 {
		t.Errorf("edited activity entries = %d, want 4 (3 fields + tags)", edits)
	}

	// The single-field body still works; mixing both shapes is refused.
	if code, out := do(t, h, "PATCH", url, map[string]interface{}{"field": "priority", "value": "high"}); code != http.StatusOK {
		t.Errorf("single-field edit: %d %v", code, out)
	}
	if code, _ := do(t, h, "PATCH", url, map[string]interface{}{"field": "priority", "value": "low", "fields": map[string]interface{}{"title": "z"}}); code != http.StatusUnprocessableEntity {
		t.Errorf("mixed shapes: %d, want 422", code)
	}
	if code, _ := do(t, h, "PATCH", url, map[string]interface{}{"fields": map[string]interface{}{"status": "done"}}); code != http.StatusUnprocessableEntity {
		t.Errorf("status via fields: %d, want 422", code)
	}
}

// Prefix lookup: "EST" and "est" find the project whose prefix is EST; a
// prefix two projects share is 409 with candidates.
func TestProjectByPrefix(t *testing.T) {
	h, root := setupEstimateProject(t)
	// Rename the project id so only the prefix can match "ESTPRE".
	cfg, _ := ticket.ReadConfig(root)
	cfg.ProjectID = "estimation"
	cfg.Prefix = "ESTPRE"
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	if code, out := do(t, h, "GET", "/api/projects/estpre/tickets/", nil); code != http.StatusOK {
		t.Fatalf("by prefix: %d %v", code, out)
	}

	other := filepath.Join(filepath.Dir(root), "est2")
	if err := ticket.WriteConfig(other, ticket.DefaultConfig("c", "Est2", "est2", "ESTPRE")); err != nil {
		t.Fatal(err)
	}
	code, out := do(t, h, "GET", "/api/projects/ESTPRE/tickets/", nil)
	if code != http.StatusConflict {
		t.Fatalf("shared prefix: %d %v, want 409", code, out)
	}
	if c, _ := out["candidates"].([]interface{}); len(c) != 2 {
		t.Errorf("candidates = %v, want 2", out["candidates"])
	}
	if code, _ := do(t, h, "GET", "/api/projects/NOPE/tickets/", nil); code != http.StatusNotFound {
		t.Errorf("unknown: %d, want 404", code)
	}
}

// tc3: bulk create with refs, inline test cases and a single commit.
func TestBulkCreate(t *testing.T) {
	h, root := setupEstimateProject(t)
	git := initGitRepo(t, root)
	before := git("rev-list", "--count", "HEAD")

	code, out := do(t, h, "POST", "/api/projects/est/tickets/bulk", map[string]interface{}{
		"author": "agent@x",
		"tickets": []map[string]interface{}{
			{"ref": "p", "type": "task", "title": "Build X", "tags": []string{"cfp:4"}},
			{"ref": "c", "type": "dev_task", "title": "Code X", "parent_ref": "p", "tags": []string{"functional"},
				"test_cases": []map[string]string{{"step": "s", "expected": "e"}}},
			{"type": "task", "title": "Deploy X", "parent_ref": "p", "predecessor_refs": []string{"c"}, "tags": []string{"nonfunc"}, "estimate_hours": 1},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bulk: %d %v", code, out)
	}
	created := out["created"].([]interface{})
	if len(created) != 3 {
		t.Fatalf("created = %v", created)
	}
	pid := created[0].(map[string]interface{})["id"].(string)
	cid := created[1].(map[string]interface{})["id"].(string)
	did := created[2].(map[string]interface{})["id"].(string)
	child, _ := ticket.ReadTicket(root, cid)
	if !ticket.Contains(child.Tags, "parent:"+pid) || len(child.TestCases) != 1 || child.Creator != "agent@x" {
		t.Errorf("child: tags %v, test cases %d, creator %q", child.Tags, len(child.TestCases), child.Creator)
	}
	dep, _ := ticket.ReadTicket(root, did)
	if len(dep.Predecessors) != 1 || dep.Predecessors[0] != cid {
		t.Errorf("predecessors = %v, want [%s]", dep.Predecessors, cid)
	}
	b, _ := strconv.Atoi(before)
	if n := git("rev-list", "--count", "HEAD"); n != strconv.Itoa(b+1) {
		t.Errorf("commits %s -> %s, want exactly one new commit", before, n)
	}
	if msg := git("log", "-1", "--format=%s"); !strings.Contains(msg, pid) || !strings.Contains(msg, did) {
		t.Errorf("commit message %q should name the batch", msg)
	}
	if st := git("status", "--porcelain", "tickets"); st != "" {
		t.Errorf("uncommitted ticket files: %s", st)
	}
}

// A bad ticket anywhere in the batch writes nothing.
func TestBulkCreateAllOrNothing(t *testing.T) {
	h, root := setupEstimateProject(t)
	cases := []map[string]interface{}{
		{"tickets": []map[string]interface{}{{"type": "task", "title": "ok"}, {"type": "task", "title": "bad", "estimate_hours": 0.3}}},
		{"tickets": []map[string]interface{}{{"type": "task", "title": "ok"}, {"type": "task", "title": "c", "parent_ref": "later"}, {"ref": "later", "type": "task", "title": "p"}}},
		{"tickets": []map[string]interface{}{{"type": "task", "title": "ok"}, {"type": "task", "title": ""}}},
		{"tickets": []map[string]interface{}{}},
	}
	for i, body := range cases {
		if code, out := do(t, h, "POST", "/api/projects/est/tickets/bulk", body); code != http.StatusUnprocessableEntity {
			t.Errorf("case %d: %d %v, want 422", i, code, out)
		}
	}
	if code, _ := do(t, h, "POST", "/api/projects/est/tickets/bulk", map[string]interface{}{
		"tickets": []map[string]interface{}{{"type": "task", "title": "x", "predecessors": []string{"EST-nope"}}},
	}); code != http.StatusNotFound {
		t.Errorf("unknown predecessor: %d, want 404", code)
	}
	all, _ := ticket.ReadAllTickets(root)
	if len(all) != 0 {
		t.Errorf("%d tickets written by failed batches, want 0", len(all))
	}
}

// tc4: GET /api/guide?section=3 returns only section 3 as markdown.
func TestGuideSection(t *testing.T) {
	md, err := os.ReadFile("../../docs/ticketing-and-cfp-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	service.SetGuide(string(md))
	t.Cleanup(func() { service.SetGuide("") })
	r := http.NewServeMux()
	r.HandleFunc("/api/guide", handleGetGuide)

	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/guide"+q, nil))
		return rec
	}
	rec := get("?section=3")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("section 3: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "## 3. Tag conventions") || strings.Contains(body, "## 4.") {
		t.Errorf("section 3 body starts %q", body[:60])
	}
	if rec := get(""); rec.Body.Len() != len(md) {
		t.Errorf("whole guide: %d bytes, want %d", rec.Body.Len(), len(md))
	}
	if rec := get("?section=99"); rec.Code != http.StatusNotFound {
		t.Errorf("section 99: %d, want 404", rec.Code)
	}
}
