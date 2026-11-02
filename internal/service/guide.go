// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"strings"
)

// guideText is the agent guide (docs/ticketing-and-cfp-guide.md), embedded in
// the binary by package main and handed over with SetGuide at startup.
var guideText string

// SetGuide installs the agent guide markdown.
func SetGuide(markdown string) { guideText = markdown }

// Guide returns the agent guide, or with section "N" only its "## N." section
// (heading through the line before the next "## " heading).
func Guide(section string) (string, error) {
	if guideText == "" {
		return "", &Error{Kind: ErrNotFound, Msg: "agent guide not available in this build"}
	}
	section = strings.TrimSpace(section)
	if section == "" {
		return guideText, nil
	}
	want := "## " + strings.TrimSuffix(section, ".") + "."
	lines := strings.SplitAfter(guideText, "\n")
	start := -1
	for i, l := range lines {
		if start < 0 {
			if strings.HasPrefix(l, want+" ") || strings.TrimRight(l, "\r\n") == want {
				start = i
			}
			continue
		}
		if strings.HasPrefix(l, "## ") {
			return strings.Join(lines[start:i], ""), nil
		}
	}
	if start < 0 {
		return "", &Error{Kind: ErrNotFound, Msg: "guide section not found: " + section}
	}
	return strings.Join(lines[start:], ""), nil
}
