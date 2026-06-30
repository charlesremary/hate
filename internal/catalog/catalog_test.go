// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"hate/internal/config"
)

// withTempRoot points the catalog at a throwaway projects root for the test, so
// Load/Save touch a temp dir instead of the real ~/.pm-catalog.
func withTempRoot(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"projects_root":"`+dir+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// config.AppConfigPath drives GetProjectsRoot via LoadConfig.
	old := config.AppConfigPath
	config.AppConfigPath = cfgPath
	t.Cleanup(func() { config.AppConfigPath = old })
}

func TestSeedAndLoad(t *testing.T) {
	withTempRoot(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.Entries) != len(SeedCatalog().Entries) {
		t.Fatalf("seed entries = %d, want %d", len(c.Entries), len(SeedCatalog().Entries))
	}
	// Loading again must read the persisted file, not reseed.
	c2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(c2.Entries) != len(c.Entries) {
		t.Fatalf("reload changed entry count: %d vs %d", len(c2.Entries), len(c.Entries))
	}
}

func TestAddUpdateDelete(t *testing.T) {
	withTempRoot(t)
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}

	// Add
	c, err := AddEntry(CatalogEntry{Type: "webhook", Label: "Webhook", Activity: ActivityConfigure, Unit: "hook", SeedHours: 0.3})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if indexOf(c.Entries, "webhook") == -1 {
		t.Fatal("webhook not added")
	}

	// Duplicate rejected
	if _, err := AddEntry(CatalogEntry{Type: "webhook", Label: "x", Activity: ActivityConfigure, Unit: "hook"}); err == nil {
		t.Fatal("expected duplicate error")
	}

	// Update (incl. rename)
	c, err = UpdateEntry("webhook", CatalogEntry{Type: "webhook-v2", Label: "Webhook v2", Activity: ActivityOperate, Unit: "hook", SeedHours: 0.4})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if indexOf(c.Entries, "webhook") != -1 || indexOf(c.Entries, "webhook-v2") == -1 {
		t.Fatal("rename did not take effect")
	}

	// Delete
	c, err = DeleteEntry("webhook-v2")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if indexOf(c.Entries, "webhook-v2") != -1 {
		t.Fatal("entry not deleted")
	}
	if _, err := DeleteEntry("nope"); err == nil {
		t.Fatal("expected not-found on delete")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		e    CatalogEntry
		ok   bool
	}{
		{"good", CatalogEntry{Type: "flow", Label: "Flow", Activity: ActivityConfigure, Unit: "flow", SeedHours: 1}, true},
		{"bad-type-upper", CatalogEntry{Type: "Flow", Label: "Flow", Activity: ActivityConfigure, Unit: "flow"}, false},
		{"bad-type-space", CatalogEntry{Type: "my flow", Label: "Flow", Activity: ActivityConfigure, Unit: "flow"}, false},
		{"bad-activity", CatalogEntry{Type: "flow", Label: "Flow", Activity: "author", Unit: "flow"}, false},
		{"no-label", CatalogEntry{Type: "flow", Label: " ", Activity: ActivityConfigure, Unit: "flow"}, false},
		{"no-unit", CatalogEntry{Type: "flow", Label: "Flow", Activity: ActivityConfigure, Unit: ""}, false},
		{"neg-hours", CatalogEntry{Type: "flow", Label: "Flow", Activity: ActivityConfigure, Unit: "flow", SeedHours: -1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.e)
			if tc.ok && err != nil {
				t.Errorf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("expected invalid")
			}
		})
	}
}
