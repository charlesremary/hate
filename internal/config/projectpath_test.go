// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeProject(t *testing.T, dir, id, prefix string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".tkt"), 0755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"project_id": id, "prefix": prefix, "project_name": id})
	if err := os.WriteFile(filepath.Join(dir, ".tkt", "config.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

// Every way of naming a project resolves to the one canonical path, so all
// callers share one project lock (ticket.LockProject keys on the path).
func TestGetProjectPathCanonical(t *testing.T) {
	base := t.TempDir()
	old := AppConfigPath
	AppConfigPath = filepath.Join(base, "config.json")
	t.Cleanup(func() { AppConfigPath = old })
	projects := filepath.Join(base, "projects")
	b, _ := json.Marshal(map[string]string{"projects_root": projects})
	if err := os.WriteFile(AppConfigPath, b, 0644); err != nil {
		t.Fatal(err)
	}
	est := filepath.Join(projects, "est")
	writeProject(t, est, "est", "EST")
	writeProject(t, filepath.Join(projects, "tkt-a"), "A1", "AAA")
	writeProject(t, filepath.Join(projects, "tkt-b"), "B1", "AAA")

	want, _ := GetProjectPath("est")
	for _, ref := range []string{"est", "EST", "Est"} {
		got, err := GetProjectPath(ref)
		if err != nil || got != want {
			t.Errorf("GetProjectPath(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	if got, err := GetProjectPath("tkt-a"); err != nil || filepath.Base(got) != "tkt-a" {
		t.Errorf("by folder: %q %v", got, err)
	}
	if _, err := GetProjectPath("TKT-A"); err == nil {
		t.Error("folder names match exactly; TKT-A should not resolve")
	}
	var amb *AmbiguousProjectError
	if _, err := GetProjectPath("aaa"); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Errorf("shared prefix: %v", err)
	}
	if _, err := GetProjectPath("../projects/est"); err == nil {
		t.Error("a relative path should not resolve")
	}
}
