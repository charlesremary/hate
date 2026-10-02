// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/ticket"
)

// setupEstimateProject points the app config at a temp projects root holding
// one project ("est") and returns a router with the ticket routes. Git is
// pointed at a nonexistent dir so the handlers' auto-commits are no-ops.
func setupEstimateProject(t *testing.T) (http.Handler, string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(base, "no-git"))
	oldPath := config.AppConfigPath
	config.AppConfigPath = filepath.Join(base, "app.json")
	t.Cleanup(func() { config.AppConfigPath = oldPath })

	projects := filepath.Join(base, "projects")
	root := filepath.Join(projects, "est")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	app, _ := json.Marshal(map[string]interface{}{"projects_root": projects})
	if err := os.WriteFile(config.AppConfigPath, app, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ticket.WriteConfig(root, ticket.DefaultConfig("c", "Est", "est", "EST")); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterTicketRoutes(r)
	return r, root
}

// do sends a JSON request and returns the status and decoded body.
func do(t *testing.T, h http.Handler, method, path string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestCreateAndEditEstimateHours(t *testing.T) {
	h, _ := setupEstimateProject(t)
	base := "/api/projects/est/tickets/"

	// Non-empty effort is retired → 400.
	code, body := do(t, h, "POST", base, map[string]interface{}{"type": "task", "title": "x", "effort": "m"})
	if code != http.StatusBadRequest || !strings.Contains(body["detail"].(string), "effort sizing is retired") {
		t.Errorf("create with effort: %d %v, want 400 retired", code, body)
	}
	// Empty effort is tolerated (no value to set).
	if code, body = do(t, h, "POST", base, map[string]interface{}{"type": "task", "title": "x", "effort": ""}); code != http.StatusOK {
		t.Errorf("create with empty effort: %d %v, want 200", code, body)
	}
	// Bad estimate_hours → 422.
	if code, _ = do(t, h, "POST", base, map[string]interface{}{"type": "task", "title": "x", "estimate_hours": 0.3}); code != http.StatusUnprocessableEntity {
		t.Errorf("create with estimate_hours 0.3: %d, want 422", code)
	}
	// Valid wrap ticket.
	code, body = do(t, h, "POST", base, map[string]interface{}{
		"type": "task", "title": "deploy", "tags": []string{"parent:F", "config"}, "estimate_hours": 1.5,
	})
	if code != http.StatusOK || body["estimate_hours"] != 1.5 {
		t.Fatalf("create wrap: %d %v, want 200 with estimate_hours 1.5", code, body)
	}
	id := body["id"].(string)

	// PATCH estimate_hours: valid, invalid, clear.
	if code, body = do(t, h, "PATCH", base+id, map[string]interface{}{"field": "estimate_hours", "value": 4}); code != http.StatusOK || body["estimate_hours"] != 4.0 {
		t.Errorf("patch estimate_hours 4: %d %v", code, body)
	}
	if code, _ = do(t, h, "PATCH", base+id, map[string]interface{}{"field": "estimate_hours", "value": 0.1}); code != http.StatusUnprocessableEntity {
		t.Errorf("patch estimate_hours 0.1: %d, want 422", code)
	}
	// PATCH effort: setting → 400, clearing → 200.
	if code, _ = do(t, h, "PATCH", base+id, map[string]interface{}{"field": "effort", "value": "l"}); code != http.StatusBadRequest {
		t.Errorf("patch effort l: %d, want 400", code)
	}
	if code, _ = do(t, h, "PATCH", base+id, map[string]interface{}{"field": "effort", "value": nil}); code != http.StatusOK {
		t.Errorf("patch effort null: %d, want 200", code)
	}

	// The list carries estimate_hours and class.
	req := httptest.NewRequest("GET", base, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var list []map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	found := false
	for _, it := range list {
		if it["id"] == id {
			found = true
			if it["class"] != "config" || it["estimate_hours"] != 4.0 {
				t.Errorf("list item = %v, want class config, estimate_hours 4", it)
			}
		}
	}
	if !found {
		t.Errorf("ticket %s missing from list", id)
	}
}

func TestPromoteEstimateValidation422(t *testing.T) {
	h, _ := setupEstimateProject(t)
	base := "/api/projects/est/tickets/"
	_, body := do(t, h, "POST", base, map[string]interface{}{
		"type": "dev_task", "title": "code", "tags": []string{"parent:F", "functional"}, "estimate_hours": 2,
	})
	id := body["id"].(string)
	code, body := do(t, h, "POST", base+id+"/promote", nil)
	if code != http.StatusUnprocessableEntity || body["estimate_invalid"] != true ||
		!strings.Contains(body["detail"].(string), "must not carry estimate_hours") {
		t.Errorf("promote functional with hours: %d %v, want 422 estimate_invalid", code, body)
	}
}

func TestStrictTimeWrapOnly(t *testing.T) {
	h, root := setupEstimateProject(t)
	cfg, _ := ticket.ReadConfig(root)
	cfg.StrictTimeEnforcement = true
	_ = ticket.WriteConfig(root, cfg)
	base := "/api/projects/est/tickets/"

	_, wrap := do(t, h, "POST", base, map[string]interface{}{"type": "task", "title": "w", "tags": []string{"config"}, "estimate_hours": 1})
	_, code := do(t, h, "POST", base, map[string]interface{}{"type": "dev_task", "title": "c", "tags": []string{"functional", "cfp:2"}})
	logBody := map[string]interface{}{"date": "2026-10-01", "hours": 3, "description": "work"}

	st, body := do(t, h, "POST", base+wrap["id"].(string)+"/time", logBody)
	if st != http.StatusConflict || body["needs_time_extension"] != true || body["allotted_hours"] != 1.0 {
		t.Errorf("wrap over allotment: %d %v, want 409 needs_time_extension, allotted 1", st, body)
	}
	if st, body = do(t, h, "POST", base+code["id"].(string)+"/time", logBody); st != http.StatusOK {
		t.Errorf("functional ticket must not be gated: %d %v", st, body)
	}
}
