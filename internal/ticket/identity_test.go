// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// identityRepo is a git repo with no git user anywhere (no local, global or
// system config, no Git account) and a project config. It returns a git
// runner and a function that sets this computer's global git user.
func identityRepo(t *testing.T) (root string, git func(args ...string) string, setGlobal func(name, email string)) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HATE_CONFIG_DIR", t.TempDir()) // no signed-in Git account
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git = func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	setGlobal = func(name, email string) {
		git("config", "--global", "user.name", name)
		git("config", "--global", "user.email", email)
	}
	if err := WriteConfig(root, DefaultConfig("c", "P", "p", "P")); err != nil {
		t.Fatal(err)
	}
	return root, git, setGlobal
}

// writeSharedIdentity puts git_identity into .tkt/config.json the way an older
// hate on machine B did (and sync brought it here), plus a member this hate
// doesn't know.
func writeSharedIdentity(t *testing.T, root, name, email string) {
	t.Helper()
	data, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(data))
	s = s[:len(s)-1] + `,"git_identity":{"name":"` + name + `","email":"` + email + `"},"future_field":{"x":1}}`
	if err := os.WriteFile(ConfigPath(root), []byte(s), 0644); err != nil {
		t.Fatal(err)
	}
}

func lastAuthor(git func(args ...string) string) string {
	return git("log", "-1", "--format=%an <%ae>")
}

// HATE-5t80: a config synced from machine B carrying B's git_identity, opened
// where the repo-local identity is A, commits and authors as A, never B. The
// next config write drops git_identity and keeps unknown members.
func TestSharedIdentityNeverOverridesLocal(t *testing.T) {
	root, git, _ := identityRepo(t)
	git("config", "--local", "user.name", "Alice")
	git("config", "--local", "user.email", "alice@a.test")
	writeSharedIdentity(t, root, "Bob", "bob@b.test")

	cfg, err := ReadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if id := EffectiveIdentity(root, cfg); id.Email != "alice@a.test" || id.Source != "local" {
		t.Errorf("effective identity = %+v, want alice (local)", id)
	}
	if c := CheckIdentity(root, cfg); c.Mismatch || c.FromShared {
		t.Errorf("check = %+v, want no warning", c)
	}

	f := filepath.Join(root, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CommitFiles(root, []string{f}, "a"); err != nil {
		t.Fatal(err)
	}
	if got := lastAuthor(git); got != "Alice <alice@a.test>" {
		t.Errorf("commit author = %q, want Alice", got)
	}
	if got := git("config", "--local", "user.email"); got != "alice@a.test" {
		t.Errorf("local email = %q, want alice (not overwritten)", got)
	}

	// Rewriting the config (any write, or the format migration's re-encode)
	// drops the personal identity and keeps what it doesn't know.
	if err := WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(ConfigPath(root))
	if strings.Contains(string(data), "git_identity") || strings.Contains(string(data), "bob@b.test") {
		t.Errorf("config still carries the identity:\n%s", data)
	}
	if !strings.Contains(string(data), "future_field") {
		t.Errorf("unknown member lost:\n%s", data)
	}
}

// The global git user wins over the shared value when there's no local one.
func TestSharedIdentityNeverOverridesGlobal(t *testing.T) {
	root, git, setGlobal := identityRepo(t)
	setGlobal("Alice", "alice@a.test")
	writeSharedIdentity(t, root, "Bob", "bob@b.test")
	f := filepath.Join(root, "a.txt")
	_ = os.WriteFile(f, []byte("x"), 0644)
	if err := CommitFiles(root, []string{f}, "a"); err != nil {
		t.Fatal(err)
	}
	if got := lastAuthor(git); got != "Alice <alice@a.test>" {
		t.Errorf("commit author = %q, want Alice", got)
	}
	if got := LocalIdentity(root); !got.IsZero() {
		t.Errorf("local identity set to %+v, want untouched", got)
	}
}

// The legacy shared identity fills in only when the machine has none at all.
func TestSharedIdentityFillsInWhenNoIdentity(t *testing.T) {
	root, git, _ := identityRepo(t)
	writeSharedIdentity(t, root, "Bob", "bob@b.test")
	cfg, _ := ReadConfig(root)
	if id := EffectiveIdentity(root, cfg); id.Email != "bob@b.test" || id.Source != "shared" {
		t.Errorf("effective identity = %+v, want bob (shared fill-in)", id)
	}
	f := filepath.Join(root, "a.txt")
	_ = os.WriteFile(f, []byte("x"), 0644)
	if err := CommitFiles(root, []string{f}, "a"); err != nil {
		t.Fatal(err)
	}
	if got := lastAuthor(git); got != "Bob <bob@b.test>" {
		t.Errorf("commit author = %q, want the fill-in", got)
	}
	// It was copied, not chosen here: the banner says so until confirmed.
	if c := CheckIdentity(root, cfg); !c.FromShared || c.Mismatch {
		t.Errorf("check = %+v, want from_shared", c)
	}
	if err := ConfirmLocalIdentity(root, "bob@b.test"); err != nil {
		t.Fatal(err)
	}
	if c := CheckIdentity(root, cfg); c.FromShared || c.Mismatch {
		t.Errorf("after confirm: %+v, want no warning", c)
	}
}

// An older hate set the repo-local identity to a teammate; this computer's
// global git user is someone else: a mismatch, with both identities and a
// plain message. Setting the identity here (or keeping it) clears it.
func TestIdentityMismatch(t *testing.T) {
	root, git, setGlobal := identityRepo(t)
	setGlobal("Chuck", "chuck@c.test")
	git("config", "--local", "user.name", "Matt")
	git("config", "--local", "user.email", "matt@m.test")

	c := CheckIdentity(root, nil)
	if !c.Mismatch {
		t.Fatalf("check = %+v, want a mismatch", c)
	}
	want := "hate is set to work as Matt <matt@m.test>, but you are Chuck <chuck@c.test> on this computer."
	if c.Message != want {
		t.Errorf("message = %q, want %q", c.Message, want)
	}
	if c.Working.Email != "matt@m.test" || c.Machine.Email != "chuck@c.test" {
		t.Errorf("identities = %+v / %+v", c.Working, c.Machine)
	}

	// Keep it on purpose (a per-repo identity): no longer a mismatch.
	if err := ConfirmLocalIdentity(root, "matt@m.test"); err != nil {
		t.Fatal(err)
	}
	if c := CheckIdentity(root, nil); c.Mismatch {
		t.Errorf("after keep: %+v", c)
	}

	// A different local identity later (another older-hate copy) is flagged
	// again; the one-click fix sets the machine's own.
	git("config", "--local", "user.email", "other@o.test")
	if c := CheckIdentity(root, nil); !c.Mismatch {
		t.Errorf("new local identity not flagged: %+v", c)
	}
	if err := SetMachineIdentity(root, "Chuck", "chuck@c.test"); err != nil {
		t.Fatal(err)
	}
	if c := CheckIdentity(root, nil); c.Mismatch || c.Working.Email != "chuck@c.test" {
		t.Errorf("after fix: %+v", c)
	}
}

// Single-user setups: a local identity with no global git user and nothing
// shared is not a warning.
func TestIdentityNoGlobalNoWarning(t *testing.T) {
	root, git, _ := identityRepo(t)
	git("config", "--local", "user.name", "Solo")
	git("config", "--local", "user.email", "solo@s.test")
	if c := CheckIdentity(root, nil); c.Mismatch || c.FromShared || c.Working.Email != "solo@s.test" {
		t.Errorf("check = %+v", c)
	}
}
