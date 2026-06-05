// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ValidSlipCategories is the list of valid slip reason categories.
var ValidSlipCategories = []string{
	"estimation_error", "external_dependency", "scope_change",
	"resource_diversion", "technical_blocker", "environment_tooling",
	"client_delay", "requirements_change",
}

// BaselineExists checks whether a baseline.json file exists for the project.
func BaselineExists(projectRoot string) bool {
	_, err := os.Stat(BaselinePath(projectRoot))
	return err == nil
}

// IsValidSlipCategory checks if a category string is in the valid list.
func IsValidSlipCategory(cat string) bool {
	for _, c := range ValidSlipCategories {
		if c == cat {
			return true
		}
	}
	return false
}

// ReadSlipEvents reads and parses the slip_events.json file.
// Returns an empty slice if the file does not exist.
func ReadSlipEvents(projectRoot string) ([]SlipEvent, error) {
	path := SlipEventsPath(projectRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []SlipEvent{}, nil
		}
		return nil, err
	}
	var events []SlipEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// WriteSlipEvents marshals and writes the slip events to disk.
func WriteSlipEvents(projectRoot string, events []SlipEvent) error {
	path := SlipEventsPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ValidSlipCategoriesString returns the categories joined for error messages.
func ValidSlipCategoriesString() string {
	return strings.Join(ValidSlipCategories, ", ")
}
