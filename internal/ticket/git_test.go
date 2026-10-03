// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes root a git repo (isolated from the user's git config) and
// returns a git runner.
func gitRepo(t *testing.T, root string) func(args ...string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	return git
}

// HATE-lp54 tc1/tc2: in a repo that tracks index.json, the first commit adds
// one extra commit that untracks it and ignores it; later commits never
// include it and git status never lists it. Something else the user staged
// is not swept into either commit.
func TestCommitFilesUntracksIndex(t *testing.T) {
	root := t.TempDir()
	if err := WriteConfig(root, DefaultConfig("c", "P", "p", "P")); err != nil {
		t.Fatal(err)
	}
	tk := BlankTicket("P-1", "task", "one", "a@x")
	if err := WriteTicket(root, tk); err != nil {
		t.Fatal(err)
	}
	if err := RegenerateIndex(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".DS_Store"), 0644); err != nil {
		t.Fatal(err)
	}
	git := gitRepo(t, root)
	git("add", "-A")
	git("commit", "-q", "-m", "init (old hate tracked index.json)")
	if git("ls-files", "index.json") != "index.json" {
		t.Fatal("setup: index.json should be tracked")
	}
	// The user has something unrelated staged.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("wip"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "notes.txt")

	if _, err := EditField(root, "P-1", "title", "one, edited", "a@x"); err != nil {
		t.Fatal(err)
	}
	if err := RegenerateIndex(root); err != nil {
		t.Fatal(err)
	}
	if err := CommitFiles(root, []string{TicketPath(root, "P-1"), IndexPath(root)}, "P-1: title updated"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	log := strings.Split(git("log", "--format=%s"), "\n")
	if len(log) != 3 || log[0] != "P-1: title updated" || log[1] != IndexUntrackMessage {
		t.Fatalf("log = %q", log)
	}
	if files := git("show", "--name-status", "--format=", "HEAD~1"); files != "M\t.gitignore\nD\tindex.json" {
		t.Errorf("untrack commit = %q", files)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "tickets/P-1.json" {
		t.Errorf("edit commit = %q", files)
	}
	if gi, _ := os.ReadFile(filepath.Join(root, ".gitignore")); string(gi) != ".DS_Store\nindex.json\n" {
		t.Errorf(".gitignore = %q", gi)
	}
	if _, err := os.Stat(IndexPath(root)); err != nil {
		t.Error("index.json should stay on disk")
	}
	if st := git("status", "--porcelain"); st != "A  notes.txt" {
		t.Errorf("status = %q, want only the user's staged file", st)
	}

	// Later commits: no further untrack commit, index.json never included.
	if _, err := AddComment(root, "P-1", "hi", "a@x"); err != nil {
		t.Fatal(err)
	}
	_ = RegenerateIndex(root)
	if err := CommitFiles(root, []string{TicketPath(root, "P-1"), IndexPath(root)}, "P-1: comment added"); err != nil {
		t.Fatal(err)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "tickets/P-1.json" {
		t.Errorf("later commit = %q", files)
	}
	if n := strings.Count(git("log", "--format=%s"), IndexUntrackMessage); n != 1 {
		t.Errorf("untrack commits = %d, want 1", n)
	}
	if strings.Contains(git("status", "--porcelain", "--untracked-files=all"), "index.json") {
		t.Error("git status lists index.json")
	}
	if strings.Contains(git("log", "--name-only", "--format=", "-n", "2"), "index.json") {
		t.Error("a later commit includes index.json")
	}
}

// HATE-lp54: a repo that never tracked index.json gets no extra commit and
// no .gitignore change.
func TestCommitFilesUntrackedRepoUnaffected(t *testing.T) {
	root := t.TempDir()
	if err := WriteConfig(root, DefaultConfig("c", "P", "p", "P")); err != nil {
		t.Fatal(err)
	}
	git := gitRepo(t, root)
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	if err := WriteTicket(root, BlankTicket("P-1", "task", "one", "a@x")); err != nil {
		t.Fatal(err)
	}
	_ = RegenerateIndex(root)
	if err := CommitFiles(root, []string{TicketPath(root, "P-1"), IndexPath(root)}, "P-1: created"); err != nil {
		t.Fatal(err)
	}
	if n := git("rev-list", "--count", "HEAD"); n != "2" {
		t.Errorf("commits = %s, want 2", n)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Error(".gitignore was created in a repo that never tracked index.json")
	}
}

// HATE-2yqw tc3 (ticket level): a commit in a folder that is not a git repo
// returns an error and logs it.
func TestCommitFilesReportsFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(root, "no-git"))
	if err := WriteTicket(root, BlankTicket("P-1", "task", "one", "a@x")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	err := CommitFiles(root, []string{TicketPath(root, "P-1")}, "P-1: created")
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(buf.String(), "commit warning") || !strings.Contains(buf.String(), "P-1: created") {
		t.Errorf("log = %q", buf.String())
	}
	if CommitWarning(err) == "" || CommitWarning(nil) != "" {
		t.Error("CommitWarning")
	}
}

// TryLockProject fails while the lock is held and succeeds after.
func TestTryLockProject(t *testing.T) {
	root := t.TempDir()
	unlock := LockProject(root)
	if _, ok := TryLockProject(root + "/."); ok {
		t.Error("TryLock succeeded while held (same project, different spelling)")
	}
	unlock()
	u, ok := TryLockProject(root)
	if !ok {
		t.Fatal("TryLock failed on a free lock")
	}
	u()
}
