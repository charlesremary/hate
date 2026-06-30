// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package catalog persists the org-level wrap-deliverable catalog — the
// controlled vocabulary of platform-agnostic archetypes the wrap-based estimator
// is built on. See docs/wrap-catalog-data-model.md (HATE-b376) for the model.
//
// The catalog holds DEFINITIONS ONLY (type, label, activity, unit, description,
// seed_hours). Measured hours-per-unit are computed live from tagged tickets
// (HATE-2b1x) and cached only in profiles (HATE-h4ad) — never stored here — so the
// tickets stay the single source of truth and the catalog can't drift.
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

// SeedCatalog returns the default controlled vocabulary (docs/wrap-catalog-data-model.md §2).
func SeedCatalog() *Catalog {
	return &Catalog{
		SchemaVersion: SchemaVersion,
		Entries: []CatalogEntry{
			{Type: "deploy", Label: "Deploy", Activity: ActivityOperate, Unit: "stack", SeedHours: 0.5, Description: "Run IaC, watch rollout, confirm resources."},
			{Type: "deploy-troubleshoot", Label: "Deploy troubleshoot", Activity: ActivityOperate, Unit: "incident", SeedHours: 0.75, Description: "Failed deploys, perms, limits — the variance bucket."},
			{Type: "smoke-validate", Label: "Smoke validate", Activity: ActivityOperate, Unit: "scenario", SeedHours: 0.5, Description: "Connect: place test calls; web: click the happy path."},
			{Type: "flow", Label: "Contact / Architect flow", Activity: ActivityConfigure, Unit: "flow", SeedHours: 0.75, Description: "Author a routing/contact flow in the console."},
			{Type: "bot", Label: "Bot / NLU", Activity: ActivityConfigure, Unit: "bot", SeedHours: 1.5, Description: "Lex / Dialogflow / Genesys NLU."},
			{Type: "prompt", Label: "Voice prompt", Activity: ActivityConfigure, Unit: "prompt", SeedHours: 0.25, Description: "Voice prompt authoring/upload."},
			{Type: "queue", Label: "Queue", Activity: ActivityConfigure, Unit: "queue", SeedHours: 0.25, Description: "Routing/queue config."},
			{Type: "integration-wiring", Label: "Integration wiring", Activity: ActivityConfigure, Unit: "hookup", SeedHours: 0.25, Description: "Console wiring of a fn/service."},
			{Type: "knowledge-base", Label: "Knowledge base", Activity: ActivityConfigure, Unit: "KB", SeedHours: 0.5, Description: "Stand up + sync + verify."},
			{Type: "knowledge-article", Label: "Knowledge article", Activity: ActivityConfigure, Unit: "article", SeedHours: 0.25, Description: "Author + ingest."},
			{Type: "instance", Label: "Instance", Activity: ActivityConfigure, Unit: "instance", SeedHours: 0.5, Description: "Connect instance / Genesys org."},
			{Type: "number", Label: "Telephony number", Activity: ActivityConfigure, Unit: "number", SeedHours: 0.25, Description: "Claim/port telephony."},
		},
	}
}
