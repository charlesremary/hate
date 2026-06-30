// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hate/internal/catalog"
)

// Calibration profiles (HATE-h4ad).
//
// A profile is a cached snapshot of the wrap calibration for a set of source
// projects: the pooled per-(platform, type) measured rates plus the code constant
// in force when it was built. It's the stable input the estimator (HATE-y1wn)
// reads — like a baseline: it doesn't move on its own, but a "recompute" action
// refreshes it from the current tickets. Persisted git-native under
// <projects_root>/.pm-catalog/profiles/<name>.json.

const profileSchemaVersion = "1.0.0"

// Profile is the cached calibration for a named set of projects.
type Profile struct {
	SchemaVersion  string         `json:"schema_version"`
	Name           string         `json:"name"`
	SourceProjects []string       `json:"source_projects"`
	CodeConstant   float64        `json:"code_constant"`
	Rates          []WrapUnitRate `json:"rates"`
	TotalHours     float64        `json:"total_hours"`
	TotalUnits     float64        `json:"total_units"`
	ComputedAt     string         `json:"computed_at"`
}

// BuildProfile pools the given projects' wrap data into a profile snapshot.
// computedAt is supplied by the caller (so this stays deterministic/testable).
func BuildProfile(name string, sourceIDs []string, projects []WrapProjectData, codeConstant float64, cat *catalog.Catalog, computedAt string) Profile {
	agg := ComputeWrapAggregate(projects, cat)
	if sourceIDs == nil {
		sourceIDs = []string{}
	}
	return Profile{
		SchemaVersion:  profileSchemaVersion,
		Name:           name,
		SourceProjects: sourceIDs,
		CodeConstant:   codeConstant,
		Rates:          agg.Rates,
		TotalHours:     agg.TotalHours,
		TotalUnits:     agg.TotalUnits,
		ComputedAt:     computedAt,
	}
}

var profileSlugRE = regexp.MustCompile(`[^a-z0-9]+`)

// ProfileSlug normalises a profile name into a safe filename stem.
func ProfileSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = profileSlugRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func profilePath(name string) (string, error) {
	slug := ProfileSlug(name)
	if slug == "" {
		return "", fmt.Errorf("profile name must contain a letter or digit")
	}
	dir, err := catalog.ProfilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, slug+".json"), nil
}

// SaveProfile persists a profile (creating the profiles dir if needed).
func SaveProfile(p Profile) error {
	path, err := profilePath(p.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// LoadProfile reads one profile by name.
func LoadProfile(name string) (*Profile, error) {
	path, err := profilePath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("profile not found: %q", name)
	}
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("profile is corrupt (%s): %w", path, err)
	}
	return &p, nil
}

// ListProfiles returns every saved profile, sorted by name.
func ListProfiles() ([]Profile, error) {
	dir, err := catalog.ProfilesDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Profile{}, nil
	}
	if err != nil {
		return nil, err
	}
	profiles := []Profile{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p Profile
		if json.Unmarshal(data, &p) == nil {
			profiles = append(profiles, p)
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

// DeleteProfile removes a saved profile.
func DeleteProfile(name string) error {
	path, err := profilePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile not found: %q", name)
		}
		return err
	}
	return nil
}

// RateFor returns the measured rate for a (platform, type) in this profile, or nil.
func (p *Profile) RateFor(platform, typ string) *WrapUnitRate {
	for i := range p.Rates {
		if p.Rates[i].Platform == platform && p.Rates[i].Type == typ {
			return &p.Rates[i]
		}
	}
	return nil
}
