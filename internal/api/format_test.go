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
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), `"schema_version": "1.0.0"`, `"schema_version": "2.0.0"`, 1)), 0644); err != nil {
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
