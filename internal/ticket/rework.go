// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import "strings"

// Force-close categories (hate 3.0). "done" is finished work that skipped
// the workflow; the rest are out of scope.
var CloseCategories = []string{"done", "descoped", "duplicate", "wont_do"}

// CloseCategoryLabels name them for people.
var CloseCategoryLabels = map[string]string{
	"done": "Done", "descoped": "Descoped", "duplicate": "Duplicate", "wont_do": "Won't do",
}

// IsDescoped reports whether a ticket was force-closed as out of scope
// (anything but the "done" category): it leaves every number. A ticket
// force-closed as done counts as finished.
func IsDescoped(t *Ticket) bool {
	return t.Status == "closed" && t.CancellationReason != nil && *t.CancellationReason != "" && t.CloseCategory != "done"
}

// Rework (hate 3.0): closed tickets are never reopened; a rework ticket
// tagged rework-of:<id> redoes the work and is measured on its own.
const ReworkTagPrefix = "rework-of:"

// ReworkCategories are why work had to be redone. "changed_request" is
// deliberately absent: a client changing their mind is new scope, not rework.
var ReworkCategories = []string{"defect", "missed_requirement", "misunderstood_requirement", "incomplete", "regression", "environment"}

// ReworkCategoryLabels name them for people.
var ReworkCategoryLabels = map[string]string{
	"defect":                    "Defect (missed in testing)",
	"missed_requirement":        "Missed requirement",
	"misunderstood_requirement": "Misunderstood requirement",
	"incomplete":                "Incomplete",
	"regression":                "Regression",
	"environment":               "Environment / deployment",
}

// IntroducedByValues: who introduced the problem a rework fixes.
var IntroducedByValues = []string{"agent", "human"}

// ReworkOf returns the id of the ticket this one reworks ("" if none).
func ReworkOf(t *Ticket) string {
	for _, tag := range t.Tags {
		if strings.HasPrefix(tag, ReworkTagPrefix) {
			return strings.TrimPrefix(tag, ReworkTagPrefix)
		}
	}
	return ""
}

// IsRework reports whether a ticket is rework of another.
func IsRework(t *Ticket) bool { return ReworkOf(t) != "" }
