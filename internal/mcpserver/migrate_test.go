// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"os"
	"strings"
	"testing"

	"hate/internal/ticket"
)

// downgrade makes the test project look like hate 1.x wrote it: format 1 and
// a phase on the given ticket.
func downgrade(t *testing.T, root, id, phase string) {
	t.Helper()
	cp := ticket.ConfigPath(root)
	data, _ := os.ReadFile(cp)
	s := strings.Replace(string(data), `"schema_version": "2.0.0"`, `"schema_version": "1.0.0"`, 1)
	s = strings.Replace(s, `"prefix": "EST",`, `"prefix": "EST", "block_weeks": 2,`, 1)
	os.WriteFile(cp, []byte(s), 0644)
	tp := ticket.TicketPath(root, id)
	data, _ = os.ReadFile(tp)
	os.WriteFile(tp, []byte(strings.Replace(string(data), `"lifecycle": null,`, `"phase": "`+phase+`",`, 1)), 0644)
}

// HATE-ui7x tc2 + tc3: writes say needs_migration; hate_migrate_project
// previews without writing, then applies on request.
func TestMigrateProjectTool(t *testing.T) {
	root, git := setupProject(t)
	id := newTicket(t, root, ticket.CreateTicketParams{Type: "task", Title: "One", Creator: "a@x"})
	git("add", "-A")
	git("commit", "-qm", "ticket")
	downgrade(t, root, id, "Block 04 (Dec 14-25)")
	git("commit", "-qam", "as 1.x")
	cs := connect(t)

	isErr, text, _ := call(t, cs, "hate_update_ticket", map[string]any{"ticket_id": id, "fields": map[string]any{"title": "x"}})
	if !isErr || !strings.Contains(text, `"needs_migration"`) || !strings.Contains(text, "hate_migrate_project") {
		t.Fatalf("write before update: %v %s", isErr, text)
	}
	if got := mustOK(t, cs, "hate_get_ticket", map[string]any{"ticket_id": id}); got["block"] != float64(4) {
		t.Errorf("read translates: block = %v", got["block"])
	}
	flagged := false
	for _, p := range mustOK(t, cs, "hate_list_projects", nil)["projects"].([]any) {
		flagged = flagged || p.(map[string]any)["needs_migration"] == true
	}
	if !flagged {
		t.Error("hate_list_projects doesn't flag needs_migration")
	}

	head := git("rev-parse", "HEAD")
	plan := mustOK(t, cs, "hate_migrate_project", map[string]any{"project": "est"})
	if plan["needed"] != true || plan["applied"] != false || plan["to_block"] != float64(1) {
		t.Fatalf("preview = %v", plan)
	}
	if git("rev-parse", "HEAD") != head || git("status", "--porcelain") != "" {
		t.Fatal("preview wrote something")
	}
	res := mustOK(t, cs, "hate_migrate_project", map[string]any{"project": "est", "apply": true})
	if res["applied"] != true {
		t.Fatalf("apply = %v", res)
	}
	mustOK(t, cs, "hate_update_ticket", map[string]any{"ticket_id": id, "fields": map[string]any{"title": "after"}})
}
