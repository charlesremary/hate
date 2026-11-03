// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// v1Project writes a format-1 project (as hate 1.x wrote it) with the given
// phases, as a git repo with one commit.
func v1Project(t *testing.T, blockWeeks int, phases map[string]string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "T")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@x")
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".tkt"), 0755)
	os.MkdirAll(TicketsDir(root), 0755)
	cfg := map[string]any{"schema_version": "1.0.0", "client": "c", "project_name": "P", "project_id": "p", "prefix": "P"}
	if blockWeeks > 0 {
		cfg["block_weeks"] = blockWeeks
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(ConfigPath(root), b, 0644)
	for id, phase := range phases {
		tk := map[string]any{"schema_version": "1.0.0", "id": id, "type": "task", "status": "not_started", "title": "T " + id,
			"priority": "medium", "tags": []string{}, "predecessors": []string{}, "creator": "a@x",
			"created_at": "2026-10-01T00:00:00Z", "updated_at": "2026-10-01T00:00:00Z",
			"activity": []any{map[string]any{"timestamp": "2026-10-01T00:00:00Z", "author": "a@x", "action": "created", "detail": ""}}}
		if phase != "" {
			tk["phase"] = phase
		} else {
			tk["phase"] = nil
		}
		b, _ := json.MarshalIndent(tk, "", "  ")
		os.WriteFile(TicketPath(root, id), b, 0644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-qm", "v1"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return root
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// HATE-wgxl tc1 + tc3, HATE-gswd: preview writes nothing; apply rewrites
// every ticket, bumps the format, commits once; git revert restores it.
func TestMigrateBlockProject(t *testing.T) {
	root := v1Project(t, 2, map[string]string{"P-1": "Block 03 (Nov 30-Dec 11)", "P-2": "02 - Build", "P-3": ""})
	head := gitOutput(t, root, "rev-parse", "HEAD")

	if err := CheckWritable(root); !IsNeedsMigration(err) {
		t.Fatalf("CheckWritable = %v, want NeedsMigrationError", err)
	}
	// Reads are translated in memory with the default rule.
	tk, err := ReadTicket(root, "P-1")
	if err != nil || tk.Block != 3 || tk.Lifecycle != nil {
		t.Fatalf("read P-1 = block %d lifecycle %v, %v", tk.Block, tk.Lifecycle, err)
	}
	if err := WriteTicket(root, tk); !IsNeedsMigration(err) {
		t.Fatalf("WriteTicket on unmigrated = %v", err)
	}

	plan, err := PlanMigration(root, MigrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Needed || !plan.BlocksFromPhase || plan.ToBlock != 1 || plan.ToLifecycle != 1 || len(plan.Changes) != 2 || plan.Applied {
		t.Fatalf("plan = %+v", plan)
	}
	if gitOutput(t, root, "status", "--porcelain") != "" || gitOutput(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("preview changed something")
	}

	res, err := ApplyMigration(root, MigrateOptions{}, "chuck@x")
	if err != nil || !res.Applied || res.CommitWarning != "" {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	if n := gitOutput(t, root, "rev-list", "--count", head+"..HEAD"); n != "1" {
		t.Errorf("%s commits, want 1", n)
	}
	if st := gitOutput(t, root, "status", "--porcelain"); st != "" {
		t.Errorf("dirty after apply: %s", st)
	}
	if err := CheckWritable(root); err != nil {
		t.Errorf("after apply CheckWritable = %v", err)
	}
	raw, _ := os.ReadFile(TicketPath(root, "P-1"))
	if strings.Contains(string(raw), `"phase"`) || !strings.Contains(string(raw), `"block": 3`) || !strings.Contains(string(raw), `"schema_version": "3.0.0"`) {
		t.Errorf("P-1 after:\n%s", raw)
	}
	p2, _ := ReadTicket(root, "P-2")
	if p2.Lifecycle == nil || *p2.Lifecycle != "02 - Build" || p2.Block != 0 {
		t.Errorf("P-2 = %v %d", p2.Lifecycle, p2.Block)
	}
	last := p2.Activity[len(p2.Activity)-1]
	if last.Action != ActionMigrated || last.Author != "chuck@x" || !strings.Contains(last.Detail, `lifecycle "02 - Build"`) {
		t.Errorf("activity = %+v", last)
	}
	// Applying again is a no-op.
	if again, err := ApplyMigration(root, MigrateOptions{}, "x"); err != nil || again.Needed || again.Applied {
		t.Errorf("second apply = %+v, %v", again, err)
	}
	// git revert undoes it.
	gitOutput(t, root, "revert", "--no-edit", "HEAD")
	if err := CheckWritable(root); !IsNeedsMigration(err) {
		t.Errorf("after revert = %v", err)
	}
}

// HATE-wgxl tc2 + override: a project without blocks keeps every phase as a
// lifecycle; the user can override the rule either way.
func TestMigrateLifecycleProjectAndOverride(t *testing.T) {
	root := v1Project(t, 0, map[string]string{"P-1": "Block 02", "P-2": "01 - Discovery"})
	plan, err := PlanMigration(root, MigrateOptions{})
	if err != nil || plan.BlocksFromPhase || plan.ToLifecycle != 2 || plan.ToBlock != 0 {
		t.Fatalf("default plan = %+v, %v", plan, err)
	}
	yes := true
	plan, _ = PlanMigration(root, MigrateOptions{BlocksFromPhase: &yes})
	if !plan.BlocksFromPhase || plan.ToBlock != 1 || plan.ToLifecycle != 1 {
		t.Errorf("override plan = %+v", plan)
	}
	if _, err := ApplyMigration(root, MigrateOptions{BlocksFromPhase: &yes}, "a"); err != nil {
		t.Fatal(err)
	}
	if tk, _ := ReadTicket(root, "P-1"); tk.Block != 2 {
		t.Errorf("P-1 block = %d", tk.Block)
	}
}

// A project made by a newer hate is never migrated "down".
func TestMigrateRefusesNewer(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".tkt"), 0755)
	os.WriteFile(ConfigPath(root), []byte(`{"schema_version": "9.0.0"}`), 0644)
	if _, err := PlanMigration(root, MigrateOptions{}); !IsNewerFormat(err) {
		t.Errorf("plan on newer = %v", err)
	}
}

// HATE-6wjb / HATE-1go0: 2 -> 3 turns a blocked status into the flag (back
// to the status it had) and gives force-closes a category; a 1.x project
// runs both steps in one update.
func TestMigrateTo3(t *testing.T) {
	root := v1Project(t, 0, map[string]string{"P-1": "", "P-2": "", "P-3": "", "P-4": ""})
	edit := func(id string, f func(m map[string]any)) {
		b, _ := os.ReadFile(TicketPath(root, id))
		var m map[string]any
		json.Unmarshal(b, &m)
		f(m)
		b, _ = json.MarshalIndent(m, "", "  ")
		os.WriteFile(TicketPath(root, id), b, 0644)
	}
	edit("P-1", func(m map[string]any) {
		m["status"] = "blocked"
		m["block_reason"] = "waiting on keys"
		m["activity"] = append(m["activity"].([]any),
			map[string]any{"timestamp": "2026-10-02T00:00:00Z", "action": "status_changed", "detail": "not_started -> in_progress"},
			map[string]any{"timestamp": "2026-10-03T00:00:00Z", "action": "status_changed", "detail": "in_progress -> blocked: waiting on keys"})
	})
	edit("P-2", func(m map[string]any) { m["status"] = "closed"; m["cancellation_reason"] = "Done: shipped in abc123" })
	edit("P-3", func(m map[string]any) { m["status"] = "closed"; m["cancellation_reason"] = "duplicate of P-4" })

	// Read before the update: translated in memory.
	if tk, _ := ReadTicket(root, "P-1"); tk.Status != "in_progress" || tk.Blocked == nil || tk.Blocked.Reason != "waiting on keys" {
		t.Fatalf("read P-1 = %s %+v", tk.Status, tk.Blocked)
	}
	if tk, _ := ReadTicket(root, "P-2"); tk.CloseCategory != "done" || IsDescoped(tk) {
		t.Errorf("read P-2 category %q", tk.CloseCategory)
	}
	plan, err := PlanMigration(root, MigrateOptions{})
	if err != nil || plan.Unblocked != 1 || plan.ClosedDone != 1 || plan.ClosedDescoped != 1 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := ApplyMigration(root, MigrateOptions{}, "a@x"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(TicketPath(root, "P-1"))
	if strings.Contains(string(raw), "block_reason") || strings.Contains(string(raw), `"status": "blocked"`) || !strings.Contains(string(raw), `"blocked": {`) {
		t.Errorf("P-1 after:\n%s", raw)
	}
	p3, _ := ReadTicket(root, "P-3")
	if p3.CloseCategory != "descoped" || !IsDescoped(p3) {
		t.Errorf("P-3 = %q", p3.CloseCategory)
	}
	if err := CheckWritable(root); err != nil {
		t.Errorf("after update: %v", err)
	}
	// Promote is refused while blocked; unblock, then it works.
	if _, err := Promote(root, "P-1", "a@x"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("promote blocked = %v", err)
	}
}
