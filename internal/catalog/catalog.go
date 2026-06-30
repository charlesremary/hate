// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package catalog persists an org-level, OPTIONAL suggested-tag vocabulary of
// domain-neutral wrap deliverables. It drives the wrap-type picklist on tickets;
// projects may use any tag, in or out of this list. The per-project stats report
// groups by whatever tags actually appear, so the catalog is a convenience, not a
// requirement. See docs/wrap-catalog-data-model.md (HATE-b376) for background.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"hate/internal/config"
)

// SchemaVersion is the catalog file schema version.
const SchemaVersion = "1.0.0"

// Activities. "author" (the code, priced by the CFP constant) is intentionally
// NOT a catalog activity — only the variable wrap activities are catalogued.
const (
	ActivityOperate   = "operate"
	ActivityConfigure = "configure"
)

// CatalogEntry is one controlled deliverable archetype.
type CatalogEntry struct {
	Type              string             `json:"type"`     // archetype id — kebab-case, unique
	Label             string             `json:"label"`    // human label
	Activity          string             `json:"activity"` // "operate" | "configure"
	Unit              string             `json:"unit"`     // countable unit noun
	Description       string             `json:"description"`
	SeedHours         float64            `json:"seed_hours"`                     // estimate used while measured n=0
	PlatformSeedHours map[string]float64 `json:"platform_seed_hours,omitempty"` // optional per-platform overrides
}

// Catalog is the full org-level vocabulary.
type Catalog struct {
	SchemaVersion string         `json:"schema_version"`
	UpdatedAt     string         `json:"updated_at"`
	Entries       []CatalogEntry `json:"entries"`
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// Path resolves the catalog file location. Prefers the org-level, git-native
// location beside the project folders (<projects_root>/.pm-catalog/catalog.json);
// falls back to ~/.pm-agent/catalog.json if the projects root isn't writable.
// An already-existing file at either location wins so we never silently fork.
func Path() (string, error) {
	primary := filepath.Join(config.GetProjectsRoot(), ".pm-catalog", "catalog.json")
	fallback := filepath.Join(homeDir(), ".pm-agent", "catalog.json")
	if fileExists(primary) {
		return primary, nil
	}
	if fileExists(fallback) {
		return fallback, nil
	}
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err == nil {
		return primary, nil
	}
	if err := os.MkdirAll(filepath.Dir(fallback), 0o755); err != nil {
		return "", fmt.Errorf("no writable catalog location: %w", err)
	}
	return fallback, nil
}

// ProfilesDir returns the directory cached calibration profiles live in (HATE-h4ad),
// alongside the catalog file.
func ProfilesDir() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "profiles"), nil
}

// Load reads the catalog, seeding (and persisting) the default vocabulary on first
// access if no file exists yet.
func Load() (*Catalog, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		c := SeedCatalog()
		if err := write(p, c); err != nil {
			return nil, err
		}
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("catalog is corrupt (%s): %w", p, err)
	}
	if c.Entries == nil {
		c.Entries = []CatalogEntry{}
	}
	return &c, nil
}

// write persists the catalog with a stable entry order and a fresh timestamp.
func write(path string, c *Catalog) error {
	c.SchemaVersion = SchemaVersion
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	sortEntries(c.Entries)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// Save persists a caller-built catalog to the resolved path.
func Save(c *Catalog) error {
	p, err := Path()
	if err != nil {
		return err
	}
	return write(p, c)
}

// sortEntries orders entries by activity then type for diff-friendly output.
func sortEntries(entries []CatalogEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Activity != entries[j].Activity {
			return entries[i].Activity < entries[j].Activity
		}
		return entries[i].Type < entries[j].Type
	})
}

var typeRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate checks a single entry against the data-model rules.
func Validate(e CatalogEntry) error {
	if !typeRE.MatchString(e.Type) {
		return fmt.Errorf("type must be kebab-case (lowercase letters, digits, hyphens): %q", e.Type)
	}
	if e.Activity != ActivityOperate && e.Activity != ActivityConfigure {
		return fmt.Errorf("activity must be %q or %q, got %q", ActivityOperate, ActivityConfigure, e.Activity)
	}
	if strings.TrimSpace(e.Label) == "" {
		return fmt.Errorf("label is required")
	}
	if strings.TrimSpace(e.Unit) == "" {
		return fmt.Errorf("unit is required")
	}
	if e.SeedHours < 0 {
		return fmt.Errorf("seed_hours must be >= 0")
	}
	for plat, h := range e.PlatformSeedHours {
		if h < 0 {
			return fmt.Errorf("platform_seed_hours[%s] must be >= 0", plat)
		}
	}
	return nil
}

func indexOf(entries []CatalogEntry, typ string) int {
	for i, e := range entries {
		if e.Type == typ {
			return i
		}
	}
	return -1
}

// AddEntry validates and appends a new entry, rejecting duplicate types.
func AddEntry(e CatalogEntry) (*Catalog, error) {
	if err := Validate(e); err != nil {
		return nil, err
	}
	c, err := Load()
	if err != nil {
		return nil, err
	}
	if indexOf(c.Entries, e.Type) != -1 {
		return nil, fmt.Errorf("catalog entry already exists: %q", e.Type)
	}
	c.Entries = append(c.Entries, e)
	if err := Save(c); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateEntry replaces the entry identified by typ. The body may rename the type
// (e.Type != typ), in which case the new type must not collide with another entry.
func UpdateEntry(typ string, e CatalogEntry) (*Catalog, error) {
	if err := Validate(e); err != nil {
		return nil, err
	}
	c, err := Load()
	if err != nil {
		return nil, err
	}
	idx := indexOf(c.Entries, typ)
	if idx == -1 {
		return nil, fmt.Errorf("catalog entry not found: %q", typ)
	}
	if e.Type != typ {
		if indexOf(c.Entries, e.Type) != -1 {
			return nil, fmt.Errorf("catalog entry already exists: %q", e.Type)
		}
	}
	c.Entries[idx] = e
	if err := Save(c); err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteEntry removes the entry identified by typ.
func DeleteEntry(typ string) (*Catalog, error) {
	c, err := Load()
	if err != nil {
		return nil, err
	}
	idx := indexOf(c.Entries, typ)
	if idx == -1 {
		return nil, fmt.Errorf("catalog entry not found: %q", typ)
	}
	c.Entries = append(c.Entries[:idx], c.Entries[idx+1:]...)
	if err := Save(c); err != nil {
		return nil, err
	}
	return c, nil
}

// SeedCatalog returns the default suggested-tag vocabulary. These are
// domain-neutral wrap deliverables that apply across project types — IVR, audit,
// web app, data pipeline, etc. Domain-specific deliverables (an IVR's flow/bot/
// prompt, say) are added per-org by editing the catalog; they're not seeded here.
func SeedCatalog() *Catalog {
	return &Catalog{
		SchemaVersion: SchemaVersion,
		Entries: []CatalogEntry{
			{Type: "deploy", Label: "Deploy", Activity: ActivityOperate, Unit: "stack", SeedHours: 0.5, Description: "Run IaC, watch rollout, confirm resources."},
			{Type: "deploy-troubleshoot", Label: "Deploy troubleshoot", Activity: ActivityOperate, Unit: "incident", SeedHours: 0.75, Description: "Failed deploys, perms, limits — the variance bucket."},
			{Type: "smoke-validate", Label: "Smoke validate", Activity: ActivityOperate, Unit: "scenario", SeedHours: 0.5, Description: "Exercise the happy path and confirm it works."},
			{Type: "managed-service-setup", Label: "Managed-service setup", Activity: ActivityConfigure, Unit: "service", SeedHours: 0.5, Description: "Stand up + configure a managed/cloud service in the console."},
			{Type: "integration-wiring", Label: "Integration wiring", Activity: ActivityConfigure, Unit: "hookup", SeedHours: 0.25, Description: "Console wiring between services."},
			{Type: "manual-step", Label: "Manual step", Activity: ActivityConfigure, Unit: "step", SeedHours: 0.25, Description: "An irreducible manual/console action with no IaC equivalent."},
		},
	}
}
