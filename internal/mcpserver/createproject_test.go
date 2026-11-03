// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// HATE-5yth: an agent creates a project, then works it like any other.
func TestCreateProjectTool(t *testing.T) {
	est, _ := setupProject(t)
	projects := filepath.Dir(est)
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Agent")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "agent@example.com")
	}
	cs := connect(t)

	out := mustOK(t, cs, "hate_create_project", map[string]any{
		"project_name": "Billing Portal", "project_id": "billing", "client": "Acme",
	})
	dir := filepath.Join(projects, "tkt-billing")
	if out["path"] != dir || out["prefix"] != "BILLING" || out["git"] != true || out["commit_warning"] != nil {
		t.Fatalf("created = %v", out)
	}
	log, err := exec.Command("git", "-C", dir, "log", "--format=%s").CombinedOutput()
	if err != nil || strings.TrimSpace(string(log)) != "Create project Billing Portal" {
		t.Fatalf("git log = %q, %v", log, err)
	}

	if st, _ := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput(); len(st) != 0 {
		t.Errorf("new repo not clean: %s", st)
	}

	listed := false
	for _, p := range mustOK(t, cs, "hate_list_projects", nil)["projects"].([]any) {
		listed = listed || p.(map[string]any)["id"] == "billing"
	}
	if !listed {
		t.Fatal("new project missing from hate_list_projects")
	}
	created := mustOK(t, cs, "hate_create_tickets", map[string]any{
		"project": "billing", "tickets": []any{map[string]any{"type": "task", "title": "First"}},
	})["created"].([]any)
	if id := created[0].(map[string]any)["id"].(string); !strings.HasPrefix(id, "BILLING-") {
		t.Fatalf("ticket id %s", id)
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"project_name": "Again", "project_id": "billing2", "prefix": "billing"}, `"conflict"`},
		{map[string]any{"project_name": "Again", "project_id": "BILLING", "prefix": "BIL2"}, `"conflict"`},
		{map[string]any{"project_name": "Est", "project_id": "x1", "prefix": "X1", "folder_name": "est"}, `"conflict"`},
		{map[string]any{"project_name": "Evil", "project_id": "evil", "folder_name": "../evil"}, `"invalid_input"`},
		{map[string]any{"project_name": "Evil", "project_id": "evil", "folder_name": "a/b"}, `"invalid_input"`},
		{map[string]any{"project_name": "", "project_id": "noname"}, `"invalid_input"`},
		{map[string]any{"project_name": "Bad", "project_id": "bad", "prefix": "9X"}, `"invalid_input"`},
		{map[string]any{"project_name": "Bad", "project_id": "bad id"}, `"invalid_input"`},
	} {
		isErr, text, _ := call(t, cs, "hate_create_project", tc.args)
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%v: err=%v %s, want %s", tc.args, isErr, text, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(projects), "evil")); !os.IsNotExist(err) {
		t.Error("../evil was created outside the projects root")
	}
	if _, err := os.Stat(filepath.Join(est, ".tkt", "config.json")); err != nil {
		t.Error("existing project folder was disturbed")
	}
}
