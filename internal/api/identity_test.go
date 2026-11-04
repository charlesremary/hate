// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"hate/internal/pm"
	"hate/internal/ticket"
)

// HATE-5t80: Settings > Git identity is per machine. Setting it writes the
// repo-local git config and leaves the shared .tkt/config.json alone; the
// Working-as endpoints report a mismatch with this computer's own identity,
// and the one-click fix clears it.
func TestGitIdentityPerMachine(t *testing.T) {
	_, root, git := setupPlanProject(t)
	r := chi.NewRouter()
	RegisterProjectRoutes(r)
	h := http.Handler(r)
	t.Setenv("HATE_CONFIG_DIR", t.TempDir()) // no signed-in Git account
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Chuck\n\temail = chuck@c.test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_DIR", "")
	os.Unsetenv("GIT_DIR")

	// An older hate copied a teammate's identity into the repo-local config
	// and into the shared file.
	git("config", "--local", "user.name", "Matt")
	git("config", "--local", "user.email", "matt@m.test")
	before, _ := os.ReadFile(ticket.ConfigPath(root))
	shared := strings.TrimSpace(string(before))
	shared = shared[:len(shared)-1] + `,"git_identity":{"name":"Matt","email":"matt@m.test"}}`
	if err := os.WriteFile(ticket.ConfigPath(root), []byte(shared), 0644); err != nil {
		t.Fatal(err)
	}

	code, body := do(t, h, "GET", "/api/projects/est/whoami", nil)
	if code != http.StatusOK {
		t.Fatalf("whoami: %d %v", code, body)
	}
	if body["mismatch"] != true {
		t.Fatalf("whoami mismatch = %v, want true (%v)", body["mismatch"], body)
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "Matt <matt@m.test>") || !strings.Contains(msg, "Chuck <chuck@c.test>") {
		t.Errorf("message = %q", msg)
	}
	if m, _ := body["machine"].(map[string]interface{}); m["email"] != "chuck@c.test" {
		t.Errorf("machine = %v", body["machine"])
	}
	if code, body := do(t, h, "GET", "/api/projects/est/git-identity", nil); code != http.StatusOK || body["mismatch"] != true {
		t.Errorf("git-identity: %d %v", code, body)
	}

	// The one-click fix: set this machine's identity.
	sharedBefore, _ := os.ReadFile(ticket.ConfigPath(root))
	code, body = do(t, h, "POST", "/api/projects/est/git-identity", map[string]string{"name": "Chuck", "email": "chuck@c.test"})
	if code != http.StatusOK || body["mismatch"] != false {
		t.Fatalf("set: %d %v", code, body)
	}
	sharedAfter, _ := os.ReadFile(ticket.ConfigPath(root))
	if !bytes.Equal(sharedBefore, sharedAfter) {
		t.Errorf("setting the identity changed .tkt/config.json:\n%s", sharedAfter)
	}
	if got := git("config", "--local", "user.email"); got != "chuck@c.test" {
		t.Errorf("local email = %q", got)
	}
	if got := pm.ResolveAuthor(root, ""); got != "chuck@c.test" {
		t.Errorf("ResolveAuthor = %q, want chuck", got)
	}
	if got := pm.ResolveAuthor(root, "explicit@x"); got != "explicit@x" {
		t.Errorf("explicit author = %q", got)
	}

	// Keeping a per-repo identity on purpose.
	git("config", "--local", "user.email", "chuck@work.test")
	if _, body := do(t, h, "GET", "/api/projects/est/whoami", nil); body["mismatch"] != true {
		t.Errorf("work email not flagged before keep: %v", body)
	}
	if code, body := do(t, h, "POST", "/api/projects/est/git-identity/confirm", nil); code != http.StatusOK || body["mismatch"] != false {
		t.Errorf("confirm: %d %v", code, body)
	}
}
