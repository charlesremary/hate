// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// AppConfigPath is the path to the application-level config file.
var AppConfigPath = filepath.Join(homeDir(), ".pm-agent", "config.json")

// SchedulerConfig holds scheduler settings.
type SchedulerConfig struct {
	Enabled       bool        `json:"enabled"`
	IntervalHours int         `json:"interval_hours"`
	Projects      interface{} `json:"projects"` // "all" or []string
}

// AppConfig holds the application-level configuration.
type AppConfig struct {
	ProjectsRoot string          `json:"projects_root"`
	Scheduler    SchedulerConfig `json:"scheduler"`
	// ExtraProjects holds absolute paths to project folders that live outside
	// ProjectsRoot — registered via "Open Existing Project".
	ExtraProjects []string `json:"extra_projects"`
	// HiddenProjects holds absolute paths of projects removed from tracking —
	// they exist on disk but are filtered out of the UI.
	HiddenProjects []string `json:"hidden_projects"`
	// ShowBilling controls whether the Billing tab is visible. Hidden by default.
	ShowBilling bool `json:"show_billing"`
	// ShowCosmic controls whether the experimental COSMIC tab is visible. Hidden by default.
	ShowCosmic bool `json:"show_cosmic"`
}

// ProjectInfo describes a discovered project.
type ProjectInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Client string `json:"client"`
	Prefix string `json:"prefix"`
	Path   string `json:"path"`
}

// homeDir returns the user's home directory.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// defaultConfig returns the default application config.
func defaultConfig() *AppConfig {
	return &AppConfig{
		ProjectsRoot: filepath.Join(homeDir(), "projects"),
		Scheduler: SchedulerConfig{
			Enabled:       false,
			IntervalHours: 24,
			Projects:      "all",
		},
		ExtraProjects:  []string{},
		HiddenProjects: []string{},
		ShowBilling:    false,
		ShowCosmic:     false,
	}
}

// LoadConfig loads the app config. Creates default if missing.
func LoadConfig() *AppConfig {
	if _, err := os.Stat(AppConfigPath); os.IsNotExist(err) {
		cfg := defaultConfig()
		_ = SaveConfig(cfg)
		return cfg
	}

	data, err := os.ReadFile(AppConfigPath)
	if err != nil {
		return defaultConfig()
	}

	// Parse into a raw map first to merge with defaults
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return defaultConfig()
	}

	cfg := defaultConfig()

	if pr, ok := raw["projects_root"].(string); ok {
		cfg.ProjectsRoot = pr
	}

	if sched, ok := raw["scheduler"].(map[string]interface{}); ok {
		if enabled, ok := sched["enabled"].(bool); ok {
			cfg.Scheduler.Enabled = enabled
		}
		if interval, ok := sched["interval_hours"].(float64); ok {
			cfg.Scheduler.IntervalHours = int(interval)
		}
		if projects, exists := sched["projects"]; exists {
			cfg.Scheduler.Projects = projects
		}
	}

	if extras, ok := raw["extra_projects"].([]interface{}); ok {
		for _, e := range extras {
			if s, ok := e.(string); ok && s != "" {
				cfg.ExtraProjects = append(cfg.ExtraProjects, s)
			}
		}
	}

	if hidden, ok := raw["hidden_projects"].([]interface{}); ok {
		for _, e := range hidden {
			if s, ok := e.(string); ok && s != "" {
				cfg.HiddenProjects = append(cfg.HiddenProjects, s)
			}
		}
	}

	if sb, ok := raw["show_billing"].(bool); ok {
		cfg.ShowBilling = sb
	}

	if sc, ok := raw["show_cosmic"].(bool); ok {
		cfg.ShowCosmic = sc
	}

	return cfg
}

// SaveConfig saves the app config to disk.
func SaveConfig(cfg *AppConfig) error {
	dir := filepath.Dir(AppConfigPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(AppConfigPath, data, 0644)
}

// GetProjectsRoot returns the projects root directory path.
func GetProjectsRoot() string {
	cfg := LoadConfig()
	root := cfg.ProjectsRoot
	if len(root) > 0 && root[0] == '~' {
		root = filepath.Join(homeDir(), root[1:])
	}
	return root
}

// projectInfoFromDir reads .tkt/config.json from dir and builds a ProjectInfo.
// Returns false if dir is not a valid tkt project.
func projectInfoFromDir(dir string) (ProjectInfo, bool) {
	tktConfigPath := filepath.Join(dir, ".tkt", "config.json")
	data, err := os.ReadFile(tktConfigPath)
	if err != nil {
		return ProjectInfo{}, false
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProjectInfo{}, false
	}

	base := filepath.Base(dir)
	id := base
	if pid, ok := cfg["project_id"].(string); ok && pid != "" {
		id = pid
	}
	name := base
	if pname, ok := cfg["project_name"].(string); ok && pname != "" {
		name = pname
	}
	prefix := "TKT"
	if p, ok := cfg["prefix"].(string); ok && p != "" {
		prefix = p
	}
	client := ""
	if c, ok := cfg["client"].(string); ok {
		client = c
	}

	return ProjectInfo{
		ID:     id,
		Name:   name,
		Client: client,
		Prefix: prefix,
		Path:   dir,
	}, true
}

// ListProjects returns all tracked projects: directories under the projects root
// containing .tkt/config.json, plus any explicitly-opened ExtraProjects — minus
// any projects the user has removed from tracking (HiddenProjects).
func ListProjects() []ProjectInfo {
	var projects []ProjectInfo
	seen := map[string]bool{}
	cfg := LoadConfig()

	hidden := map[string]bool{}
	for _, p := range cfg.HiddenProjects {
		hidden[filepath.Clean(p)] = true
	}

	// Scan the configured projects root.
	root := GetProjectsRoot()
	if entries, err := os.ReadDir(root); err == nil {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if hidden[dir] {
				continue
			}
			if info, ok := projectInfoFromDir(dir); ok {
				projects = append(projects, info)
				seen[dir] = true
			}
		}
	}

	// Include projects registered via "Open Existing Project".
	for _, dir := range cfg.ExtraProjects {
		dir = filepath.Clean(dir)
		if seen[dir] || hidden[dir] {
			continue
		}
		if info, ok := projectInfoFromDir(dir); ok {
			projects = append(projects, info)
			seen[dir] = true
		}
	}

	if projects == nil {
		projects = []ProjectInfo{}
	}
	return projects
}

// HideProject removes a project from the UI by path. The folder is left untouched
// on disk — UnhideProject brings it back. No-op if already hidden.
func HideProject(path string) error {
	path = filepath.Clean(path)
	cfg := LoadConfig()
	for _, p := range cfg.HiddenProjects {
		if filepath.Clean(p) == path {
			return nil
		}
	}
	cfg.HiddenProjects = append(cfg.HiddenProjects, path)
	return SaveConfig(cfg)
}

// UnhideProject restores a previously hidden project to the UI.
func UnhideProject(path string) error {
	path = filepath.Clean(path)
	cfg := LoadConfig()
	kept := []string{}
	for _, p := range cfg.HiddenProjects {
		if filepath.Clean(p) != path {
			kept = append(kept, p)
		}
	}
	cfg.HiddenProjects = kept
	return SaveConfig(cfg)
}

// ListHiddenProjects returns ProjectInfo for each hidden project still on disk.
func ListHiddenProjects() []ProjectInfo {
	cfg := LoadConfig()
	projects := []ProjectInfo{}
	for _, dir := range cfg.HiddenProjects {
		if info, ok := projectInfoFromDir(filepath.Clean(dir)); ok {
			projects = append(projects, info)
		}
	}
	return projects
}

// AddExtraProject registers an existing project directory so it appears in the
// project list. The path should be absolute. No-op (returns nil) if the folder
// is already discoverable under the projects root or already registered.
func AddExtraProject(path string) error {
	path = filepath.Clean(path)

	// Already discoverable by scanning the root — nothing to persist.
	if filepath.Dir(path) == filepath.Clean(GetProjectsRoot()) {
		return nil
	}

	cfg := LoadConfig()
	for _, p := range cfg.ExtraProjects {
		if filepath.Clean(p) == path {
			return nil
		}
	}
	cfg.ExtraProjects = append(cfg.ExtraProjects, path)
	return SaveConfig(cfg)
}

// GetProjectPath finds a project directory by project_id or folder name.
func GetProjectPath(projectID string) (string, error) {
	for _, project := range ListProjects() {
		if project.ID == projectID {
			return project.Path, nil
		}
	}

	// Fallback: try folder name directly
	root := GetProjectsRoot()
	direct := filepath.Join(root, projectID)
	tktConfig := filepath.Join(direct, ".tkt", "config.json")
	if _, err := os.Stat(tktConfig); err == nil {
		return direct, nil
	}

	return "", fmt.Errorf("Project not found: %s", projectID)
}
