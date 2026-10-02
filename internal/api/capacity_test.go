// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"hate/internal/ticket"
)

// HATE-1rne tc2/tc4: the target date is validated, persisted in
// .tkt/config.json, committed, and cleared by null. Since v1.0.8 the
// target-date routes are an alias for the requested end (HATE-5ux7).
func TestTargetDateAPI(t *testing.T) {
	h, root := setupCosmicProjects(t)

	// Real git for this project so the commit can be checked (the setup points
	// GIT_DIR at a dead dir; t.Setenv restores it afterwards).
	t.Setenv("GIT_DIR", "")
	os.Unsetenv("GIT_DIR")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	git("add", "-A")
	git("commit", "-q", "-m", "init")

	if code, body := do(t, h, "GET", "/api/projects/est/target-date", nil); code != http.StatusOK || body["target_date"] != nil {
		t.Fatalf("GET unset: %d %v", code, body)
	}
	for _, bad := range []interface{}{"2026-13-01", "next friday", 20261030} {
		// A bad date is a 400; a non-string fails JSON decoding (422).
		if code, _ := do(t, h, "PUT", "/api/projects/est/target-date", map[string]interface{}{"target_date": bad}); code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
			t.Errorf("PUT %v: %d, want 400/422", bad, code)
		}
	}

	code, body := do(t, h, "PUT", "/api/projects/est/target-date", map[string]interface{}{"target_date": "2026-10-30"})
	if code != http.StatusOK || body["target_date"] != "2026-10-30" {
		t.Fatalf("PUT: %d %v", code, body)
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil || cfg.RequestedEnd != "2026-10-30" || cfg.TargetDate != "" {
		t.Fatalf("persisted requested end = %q, target = %q (%v)", cfg.RequestedEnd, cfg.TargetDate, err)
	}
	raw, _ := os.ReadFile(ticket.ConfigPath(root))
	if !strings.Contains(string(raw), `"requested_end": "2026-10-30"`) {
		t.Errorf("config.json lacks requested_end: %s", raw)
	}
	if log := git("log", "-1", "--format=%s"); !strings.Contains(log, "target date 2026-10-30") {
		t.Errorf("last commit = %q, want the target date commit", log)
	}
	if st := git("status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Errorf("config not committed: %s", st)
	}

	// The dashboard's Load table picks it up.
	if html := getHTML(t, h, "/api/projects/est/dashboard"); !strings.Contains(html, "Working days to requested end") {
		t.Error("dashboard Load table missing target columns with a target set")
	}

	// null clears it (and the field is omitted from the file).
	code, body = do(t, h, "PUT", "/api/projects/est/target-date", map[string]interface{}{"target_date": nil})
	if code != http.StatusOK || body["target_date"] != nil {
		t.Fatalf("clear: %d %v", code, body)
	}
	raw, _ = os.ReadFile(ticket.ConfigPath(root))
	if strings.Contains(string(raw), "target_date") || strings.Contains(string(raw), "requested_end") {
		t.Errorf("cleared config still has target_date: %s", raw)
	}
	if log := git("log", "-1", "--format=%s"); !strings.Contains(log, "clear target date") {
		t.Errorf("last commit = %q, want the clear commit", log)
	}
	if html := getHTML(t, h, "/api/projects/est/dashboard"); strings.Contains(html, "Working days to requested end") || !strings.Contains(html, "Load &mdash;") {
		t.Error("dashboard should show Load without target columns once cleared")
	}
}

// HATE-prb6 tc2: the Balance and Check schedule endpoints are gone.
func TestBalanceAndCheckConflictsRemoved(t *testing.T) {
	h, _ := setupCosmicProjects(t)
	for _, p := range []string{"/api/projects/est/balance", "/api/projects/est/check-conflicts"} {
		if code, _ := do(t, h, "POST", p, map[string]interface{}{"apply": true}); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d, want 404/405", p, code)
		}
	}
}

func getHTML(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	return rec.Body.String()
}

// The baselined dashboard carries the Load table too.
func TestBaselinedDashboardHasLoad(t *testing.T) {
	h, _ := setupCosmicProjects(t)
	if code, body := do(t, h, "POST", "/api/projects/est/baseline-now", nil); code != http.StatusOK {
		t.Fatalf("baseline-now: %d %v", code, body)
	}
	if code, body := do(t, h, "POST", "/api/projects/est/snapshot", nil); code != http.StatusOK {
		t.Fatalf("snapshot: %d %v", code, body)
	}
	if html := getHTML(t, h, "/api/projects/est/dashboard"); !strings.Contains(html, "Load &mdash;") {
		t.Error("baselined dashboard missing the Load table")
	}
}
