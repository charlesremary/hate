// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
)

// HATE-5yth: POST /api/projects validates its input and refuses conflicts.
func TestCreateProjectREST(t *testing.T) {
	_, est := setupEstimateProject(t)
	r := chi.NewRouter()
	RegisterProjectRoutes(r)
	projects := filepath.Dir(est)

	code, out := do(t, r, "POST", "/api/projects/", map[string]interface{}{
		"client": "Acme", "folder_name": "tkt-web", "project_name": "Web", "project_id": "WEB", "prefix": "WEB",
	})
	if code != 200 || out["path"] != filepath.Join(projects, "tkt-web") || out["prefix"] != "WEB" {
		t.Fatalf("create = %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(projects, "tkt-web", ".tkt", "config.json")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		body map[string]interface{}
		want int
	}{
		{map[string]interface{}{"folder_name": "tkt-web", "project_name": "W2", "project_id": "W2"}, 409},
		{map[string]interface{}{"folder_name": "other", "project_name": "W3", "project_id": "web"}, 409},
		{map[string]interface{}{"folder_name": "../escape", "project_name": "E", "project_id": "E"}, 422},
		{map[string]interface{}{"folder_name": "ok", "project_name": "", "project_id": "OK"}, 422},
	} {
		if code, out := do(t, r, "POST", "/api/projects/", tc.body); code != tc.want {
			t.Errorf("%v: %d %v, want %d", tc.body, code, out, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(projects), "escape")); !os.IsNotExist(err) {
		t.Error("../escape was created outside the projects root")
	}
}
