// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"strings"
	"testing"

	"hate/internal/ticket"
)

// HATE-1cco: comment editing with an edit trail.
func TestEditComment(t *testing.T) {
	h, root := setupEstimateProject(t)
	id := createT(t, h, map[string]interface{}{"type": "task", "title": "A"})
	base := "/api/projects/est/tickets/" + id

	// tc1: a new comment gets id c1.
	if code, out := do(t, h, "POST", base+"/comment", map[string]interface{}{"message": "teh fix"}); code != http.StatusOK {
		t.Fatalf("comment: %d %v", code, out)
	}
	tk, _ := ticket.ReadTicket(root, id)
	last := tk.Activity[len(tk.Activity)-1]
	if last.ID != "c1" || last.Action != "comment" {
		t.Fatalf("new comment = %+v, want id c1", last)
	}

	// tc2: edit replaces the text, stamps edited_at, and records the old text.
	if code, out := do(t, h, "PATCH", base+"/comments/c1", map[string]interface{}{"message": "the fix", "author": "a@x"}); code != http.StatusOK {
		t.Fatalf("edit: %d %v", code, out)
	}
	tk, _ = ticket.ReadTicket(root, id)
	var c1, trail *ticket.Activity
	for i := range tk.Activity {
		a := &tk.Activity[i]
		if a.ID == "c1" {
			c1 = a
		}
		if a.Action == "comment_edited" {
			trail = a
		}
	}
	if c1 == nil || c1.Detail != "the fix" || c1.EditedAt == "" {
		t.Errorf("c1 after edit = %+v", c1)
	}
	if trail == nil || !strings.Contains(trail.Detail, "c1") || !strings.Contains(trail.Detail, "teh fix") || trail.Author != "a@x" {
		t.Errorf("edit trail = %+v", trail)
	}

	// tc3: a non-comment entry with an id can't be edited.
	tk.Activity[0].ID = "c50" // the "created" entry, id added by hand
	if err := ticket.WriteTicket(root, tk); err != nil {
		t.Fatal(err)
	}
	if code, out := do(t, h, "PATCH", base+"/comments/c50", map[string]interface{}{"message": "x"}); code != http.StatusUnprocessableEntity || !strings.Contains(out["detail"].(string), "only comments can be edited") {
		t.Errorf("edit non-comment: %d %v, want 422", code, out)
	}

	// tc4: unknown comment id -> 404.
	if code, _ := do(t, h, "PATCH", base+"/comments/c99", map[string]interface{}{"message": "x"}); code != http.StatusNotFound {
		t.Errorf("unknown comment: %d, want 404", code)
	}

	// tc5: blank message -> 422, text unchanged.
	if code, _ := do(t, h, "PATCH", base+"/comments/c1", map[string]interface{}{"message": "  "}); code != http.StatusUnprocessableEntity {
		t.Errorf("blank message: %d, want 422", code)
	}
	tk, _ = ticket.ReadTicket(root, id)
	for _, a := range tk.Activity {
		if a.ID == "c1" && a.Detail != "the fix" {
			t.Errorf("blank edit changed the text to %q", a.Detail)
		}
	}

	// Ids are never reused: the next comment is past the highest id (c50).
	do(t, h, "POST", base+"/comment", map[string]interface{}{"message": "second"})
	tk, _ = ticket.ReadTicket(root, id)
	if got := tk.Activity[len(tk.Activity)-1].ID; got != "c51" {
		t.Errorf("next comment id = %q, want c51", got)
	}
}
