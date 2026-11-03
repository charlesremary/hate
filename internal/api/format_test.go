// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/ticket"
)

// One version number: the project format major is the app's major.
func TestFormatMajorMatchesAppVersion(t *testing.T) {
	major, _, _ := strings.Cut(config.AppVersion, ".")
	if n, _ := strconv.Atoi(major); n != ticket.FormatMajor {
		t.Fatalf("AppVersion %s but ticket.FormatMajor %d: bump both together (and add a migration)", config.AppVersion, ticket.FormatMajor)
	}
}

func makeNewer(t *testing.T, root string) {
	t.Helper()
	p := ticket.ConfigPath(root)
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), `"schema_version": "2.0.0"`, `"schema_version": "3.0.0"`, 1)), 0644); err != nil {
		t.Fatal(err)
	}
}

// HATE-189v tc1 + tc4: REST writes to a newer project get 409 newer_format;
// reads work; the project list flags it.
func TestNewerFormatREST(t *testing.T) {
	h, root := setupEstimateProject(t)
	r := chi.NewRouter()
	RegisterProjectRoutes(r)
	RegisterTicketRoutes(r)
	_ = h

	if code, out := do(t, r, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "before"}); code != 200 && code != 201 {
		t.Fatalf("create before = %d %v", code, out)
	}
	makeNewer(t, root)

	code, out := do(t, r, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "after"})
	if code != 409 || out["newer_format"] != true || !strings.Contains(out["detail"].(string), "brew upgrade") {
		t.Errorf("create on newer = %d %v", code, out)
	}
	if code, out := do(t, r, "PUT", "/api/projects/est/block-weeks", map[string]interface{}{"block_weeks": 2}); code != 409 {
		t.Errorf("settings on newer = %d %v", code, out)
	}
	if code, _ := do(t, r, "GET", "/api/projects/est/tickets/", nil); code != 200 {
		t.Errorf("list on newer = %d", code)
	}
	code, _ = do(t, r, "GET", "/api/projects/", nil)
	if code != 200 {
		t.Fatalf("projects = %d", code)
	}
}

// HATE-ui7x: REST preview and apply of the one-time update; writes before it
// answer 409 needs_migration.
func TestMigrationREST(t *testing.T) {
	_, root := setupEstimateProject(t)
	r := chi.NewRouter()
	RegisterProjectRoutes(r)
	RegisterTicketRoutes(r)
	code, out := do(t, r, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "t"})
	if code != 200 && code != 201 {
		t.Fatalf("create = %d %v", code, out)
	}
	id := out["id"].(string)
	p := ticket.ConfigPath(root)
	data, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(data), `"schema_version": "2.0.0"`, `"schema_version": "1.0.0"`, 1)), 0644)
	tp := ticket.TicketPath(root, id)
	data, _ = os.ReadFile(tp)
	os.WriteFile(tp, []byte(strings.Replace(string(data), `"lifecycle": null,`, `"phase": "01 - Design",`, 1)), 0644)

	code, out = do(t, r, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "t2"})
	if code != 409 || out["needs_migration"] != true {
		t.Errorf("write before update = %d %v", code, out)
	}
	code, out = do(t, r, "GET", "/api/projects/est/migration", nil)
	if code != 200 || out["needed"] != true || out["to_lifecycle"] != float64(1) {
		t.Fatalf("preview = %d %v", code, out)
	}
	code, out = do(t, r, "POST", "/api/projects/est/migration", map[string]interface{}{})
	if code != 200 || out["applied"] != true {
		t.Fatalf("apply = %d %v", code, out)
	}
	tk, _ := ticket.ReadTicket(root, id)
	if tk.Lifecycle == nil || *tk.Lifecycle != "01 - Design" {
		t.Errorf("lifecycle = %v", tk.Lifecycle)
	}
	if code, out := do(t, r, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "t3", "phase": "x"}); code != 422 || !strings.Contains(out["detail"].(string), "lifecycle") {
		t.Errorf("phase on create = %d %v", code, out)
	}
	if code, out := do(t, r, "POST", "/api/projects/est/tickets/", map[string]interface{}{"type": "task", "title": "t4", "lifecycle": "Build", "block": 2}); code != 200 && code != 201 || out["block"] != float64(2) {
		t.Errorf("create with lifecycle/block = %d %v", code, out)
	}
}
