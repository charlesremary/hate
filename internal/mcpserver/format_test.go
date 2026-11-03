// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"os"
	"strings"
	"testing"

	"hate/internal/ticket"
)

// HATE-189v tc2: MCP writes to a newer project return project_newer_format
// with a next hint; reads and the project list still work and flag it.
func TestNewerFormatMCP(t *testing.T) {
	root, _ := setupProject(t)
	id := newTicket(t, root, ticket.CreateTicketParams{Type: "task", Title: "One", Creator: "a@x"})
	p := ticket.ConfigPath(root)
	data, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(data), `"schema_version": "1.0.0"`, `"schema_version": "2.0.0"`, 1)), 0644)
	cs := connect(t)

	isErr, text, _ := call(t, cs, "hate_update_ticket", map[string]any{"ticket_id": id, "fields": map[string]any{"title": "changed"}})
	if !isErr || !strings.Contains(text, `"project_newer_format"`) || !strings.Contains(text, "next") {
		t.Errorf("update on newer: err=%v %s", isErr, text)
	}
	isErr, text, _ = call(t, cs, "hate_create_tickets", map[string]any{"project": "est", "tickets": []any{map[string]any{"type": "task", "title": "x"}}})
	if !isErr || !strings.Contains(text, `"project_newer_format"`) {
		t.Errorf("create on newer: err=%v %s", isErr, text)
	}
	if got := mustOK(t, cs, "hate_get_ticket", map[string]any{"ticket_id": id}); got["title"] != "One" {
		t.Errorf("read on newer: %v", got)
	}
	flagged := false
	for _, pr := range mustOK(t, cs, "hate_list_projects", nil)["projects"].([]any) {
		flagged = flagged || pr.(map[string]any)["newer_format"] == true
	}
	if !flagged {
		t.Error("hate_list_projects doesn't flag newer_format")
	}
}
