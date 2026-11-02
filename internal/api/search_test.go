// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/ticket"
)

// setupSearchProjects makes three projects under one root: "one" (prefix KC),
// "two" (also KC, closed), and "hid" (prefix HID, hidden).
func setupSearchProjects(t *testing.T) http.Handler {
	t.Helper()
	base := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(base, "no-git"))
	oldPath := config.AppConfigPath
	config.AppConfigPath = filepath.Join(base, "app.json")
	t.Cleanup(func() { config.AppConfigPath = oldPath })

	projects := filepath.Join(base, "projects")
	mk := func(dir, id, name, prefix string, closed bool, tickets map[string][2]string) {
		root := filepath.Join(projects, dir)
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
		cfg := ticket.DefaultConfig("c", name, id, prefix)
		if closed {
			cfg.ClosedAt = "2026-10-01"
		}
		if err := ticket.WriteConfig(root, cfg); err != nil {
			t.Fatal(err)
		}
		for tid, v := range tickets {
			tk := newSearchTicket(tid, v[0], v[1])
			if err := ticket.WriteTicket(root, tk); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("one", "one", "Project One", "KC", false, map[string][2]string{
		"KC-8414": {"Gate promotion on logged time", "in_progress"},
		"KC-84ab": {"Done thing", "complete"},
		"KC-84cd": {"Open thing", "not_started"},
		"KC-zzzz": {"Mentions kc-84 in the title", "not_started"},
	})
	mk("two", "two", "Project Two", "KC", true, map[string][2]string{
		"KC-e01e": {"Other project ticket", "not_started"},
	})
	mk("hid", "hid", "Hidden", "HID", false, map[string][2]string{
		"HID-0001": {"Hidden ticket", "not_started"},
	})
	app, _ := json.Marshal(map[string]interface{}{
		"projects_root":   projects,
		"hidden_projects": []string{filepath.Join(projects, "hid")},
	})
	if err := os.WriteFile(config.AppConfigPath, app, 0644); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterSearchRoutes(r)
	return r
}

func search(t *testing.T, h http.Handler, query string) []SearchResult {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/search?"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search %q: %d %s", query, rec.Code, rec.Body.String())
	}
	var out []SearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("search %q: %v %s", query, err, rec.Body.String())
	}
	return out
}

func ids(rs []SearchResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.TicketID
	}
	return out
}

func TestSearch(t *testing.T) {
	h := setupSearchProjects(t)

	if got := search(t, h, "q=KC-8414"); len(got) == 0 || got[0].TicketID != "KC-8414" || got[0].ProjectID != "one" {
		t.Errorf("exact ID: got %v", got)
	}

	// ID prefix beats title match; open beats done within a rank.
	got := ids(search(t, h, "q=kc-84"))
	want := []string{"KC-8414", "KC-84cd", "KC-84ab", "KC-zzzz"}
	if len(got) != len(want) {
		t.Fatalf("kc-84: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kc-84: got %v, want %v", got, want)
		}
	}

	if got := ids(search(t, h, "q=8414")); len(got) != 1 || got[0] != "KC-8414" {
		t.Errorf("suffix only: got %v", got)
	}
	if got := ids(search(t, h, "q=promotion")); len(got) != 1 || got[0] != "KC-8414" {
		t.Errorf("title word: got %v", got)
	}

	// Shared prefix: both projects, told apart by name; closed project included.
	projects := map[string]bool{}
	for _, r := range search(t, h, "q=KC-") {
		projects[r.ProjectName] = true
		if r.TicketID == "KC-e01e" && !r.ClosedProject {
			t.Errorf("KC-e01e should be flagged closed_project")
		}
	}
	if !projects["Project One"] || !projects["Project Two"] {
		t.Errorf("KC- should span both projects, got %v", projects)
	}

	for _, q := range []string{"q=", "q=%20%20", ""} {
		if got := search(t, h, q); len(got) != 0 {
			t.Errorf("empty query %q: got %v", q, got)
		}
	}

	if got := search(t, h, "q=HID-0001"); len(got) != 0 {
		t.Errorf("hidden project leaked: %v", got)
	}

	if got := search(t, h, "q=kc&limit=2"); len(got) != 2 {
		t.Errorf("limit=2: got %d", len(got))
	}
}

func TestSearchLimitCap(t *testing.T) {
	h := setupSearchProjects(t)
	root := filepath.Join(filepath.Dir(config.AppConfigPath), "projects", "one")
	for i := 0; i < 60; i++ {
		id := "KC-" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + "xx"
		if err := ticket.WriteTicket(root, newSearchTicket(id, "bulk", "not_started")); err != nil {
			t.Fatal(err)
		}
	}
	if got := search(t, h, "q=bulk&limit=500"); len(got) != searchMaxLimit {
		t.Errorf("limit=500: got %d, want %d", len(got), searchMaxLimit)
	}
	if got := search(t, h, "q=bulk"); len(got) != searchDefaultLimit {
		t.Errorf("default limit: got %d, want %d", len(got), searchDefaultLimit)
	}
}

func newSearchTicket(id, title, status string) *ticket.Ticket {
	return &ticket.Ticket{SchemaVersion: ticket.SchemaVersion, ID: id, Type: "task", Title: title, Status: status,
		CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"}
}
