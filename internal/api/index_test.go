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

	"hate/internal/ticket"
)

// An index.json that can't be written (on Windows: a rename refused while
// another process holds the file) must not hide tickets or fail writes
// (HATE-l083). Here index.json is a directory, so every write fails.
func TestIndexUnwritable(t *testing.T) {
	h, root := setupEstimateProject(t)
	if err := os.MkdirAll(ticket.IndexPath(root), 0755); err != nil {
		t.Fatal(err)
	}
	base := "/api/projects/est/tickets/"

	var ids []string
	for _, title := range []string{"one", "two"} {
		code, body := do(t, h, "POST", base, map[string]interface{}{"type": "task", "title": title})
		if code != http.StatusOK {
			t.Fatalf("create %s with an unwritable index: %d %v", title, code, body)
		}
		ids = append(ids, body["id"].(string))
	}
	if _, err := os.Stat(filepath.Join(ticket.TicketsDir(root), ids[0]+".json")); err != nil {
		t.Errorf("ticket file not written: %v", err)
	}
	if code, body := do(t, h, "POST", base+ids[0]+"/time",
		map[string]interface{}{"hours": 1.5, "description": "work", "date": "2026-10-21"}); code != http.StatusOK {
		t.Fatalf("log time: %d %v", code, body)
	}

	// A ticket file that isn't valid JSON is skipped (and logged), not fatal.
	if err := os.WriteFile(filepath.Join(ticket.TicketsDir(root), "BROKEN.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", base, nil))
	var list []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("list: %d %v %s", rec.Code, err, rec.Body.String())
	}
	if len(list) != 2 {
		t.Errorf("list has %d tickets, want 2", len(list))
	}

	if code, body := do(t, h, "GET", base+"billing", nil); code != http.StatusOK || body["total_hours"] != 1.5 {
		t.Errorf("billing: %d total_hours=%v, want 1.5", code, body["total_hours"])
	}

	pr := chi.NewRouter()
	RegisterProjectRoutes(pr)
	rec = httptest.NewRecorder()
	pr.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects/", nil))
	var projects []map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &projects)
	if len(projects) != 1 || projects[0]["ticket_count"] != float64(2) {
		t.Errorf("project list = %v, want one project with ticket_count 2", projects)
	}
}
