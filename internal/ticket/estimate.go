// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Estimation model (see docs/plan-estimation-rework.md):
//   - A feature carries its COSMIC size as a `cfp:N` tag. Its children are tagged
//     `parent:<feature-id>` and each carries exactly one class tag.
//   - functional ("Code"): the human test/debug loop on generated code. Sized
//     at the feature level from the parent's CFP, never per ticket.
//   - config / nonfunc ("wrap"): platform work. Sized per ticket in hours via
//     estimate_hours.
// T-shirt effort is retired; legacy effort values stay readable on old tickets.

// Class tag values. Labels in the UI: Code / Config / Non-functional.
const (
	ClassFunctional = "functional"
	ClassConfig     = "config"
	ClassNonfunc    = "nonfunc"

	// ParentTagPrefix links a child ticket to its feature: `parent:<id>`.
	ParentTagPrefix = "parent:"
	// CFPTagPrefix carries a feature's COSMIC size: `cfp:<N>`.
	CFPTagPrefix = "cfp:"
)

// ErrEffortRetired is returned when a create or edit tries to set a non-empty
// t-shirt effort. The API maps it to 400.
var ErrEffortRetired = errors.New("effort sizing is retired; use estimate_hours on config/nonfunc tickets (code tickets are sized by the parent's cfp:)")

// EstimateValidationError is a promote-time estimation rule failure (missing or
// conflicting class, estimate_hours, or cfp: on a child). The API maps it to 422.
type EstimateValidationError struct {
	Msg string
}

func (e *EstimateValidationError) Error() string { return e.Msg }

// isClassTag reports whether tag is one of the three class values.
func isClassTag(tag string) bool {
	return tag == ClassFunctional || tag == ClassConfig || tag == ClassNonfunc
}

// ClassOf returns the ticket's class: "functional", "config", "nonfunc", or ""
// when it carries none. With several class tags the first one wins.
func ClassOf(t *Ticket) string {
	for _, tag := range t.Tags {
		if isClassTag(tag) {
			return tag
		}
	}
	return ""
}

// classTagCount counts the class tags on a ticket (duplicates included).
func classTagCount(t *Ticket) int {
	n := 0
	for _, tag := range t.Tags {
		if isClassTag(tag) {
			n++
		}
	}
	return n
}

// IsWrapClass reports whether a class is platform wrap (config or nonfunc).
func IsWrapClass(class string) bool {
	return class == ClassConfig || class == ClassNonfunc
}

// HasTagPrefix reports whether any tag starts with prefix.
func HasTagPrefix(t *Ticket, prefix string) bool {
	for _, tag := range t.Tags {
		if strings.HasPrefix(tag, prefix) {
			return true
		}
	}
	return false
}

// ParentID returns the feature id from the ticket's `parent:<id>` tag, or "".
func ParentID(t *Ticket) string {
	for _, tag := range t.Tags {
		if strings.HasPrefix(tag, ParentTagPrefix) {
			return strings.TrimSpace(tag[len(ParentTagPrefix):])
		}
	}
	return ""
}

// ValidateEstimateHours checks an estimate_hours value: nil, or >= 0.25 in
// quarter-hour steps.
func ValidateEstimateHours(h *float64) error {
	if h == nil {
		return nil
	}
	v := *h
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0.25 {
		return fmt.Errorf("estimate_hours must be at least 0.25")
	}
	if q := v / 0.25; math.Abs(q-math.Round(q)) > 1e-9 {
		return fmt.Errorf("estimate_hours must be in 0.25-hour steps (got %g)", v)
	}
	return nil
}

// ValidatePromoteEstimate applies the estimation rules checked at the first
// promote out of not_started. Only child tickets (with a parent: tag) that are
// not meeting/administration are checked; parents and self-contained features
// are exempt. Returns nil when the ticket passes.
func ValidatePromoteEstimate(t *Ticket) error {
	if t.Status != "not_started" || ParentID(t) == "" {
		return nil
	}
	if t.Type == "meeting" || t.Type == "administration" {
		return nil
	}
	fail := func(format string, a ...interface{}) error {
		return &EstimateValidationError{Msg: fmt.Sprintf(format, a...)}
	}
	switch n := classTagCount(t); {
	case n == 0:
		return fail("Cannot start %s: a child ticket needs a class tag (functional, config, or nonfunc).", t.ID)
	case n > 1:
		return fail("Cannot start %s: it has %d class tags; keep exactly one of functional, config, nonfunc.", t.ID, n)
	}
	class := ClassOf(t)
	if IsWrapClass(class) && t.EstimateHours == nil {
		return fail("Cannot start %s: %s tickets need estimate_hours (pick an hours estimate).", t.ID, class)
	}
	if class == ClassFunctional && t.EstimateHours != nil {
		return fail("Cannot start %s: functional (code) tickets must not carry estimate_hours; they are sized by the parent's cfp:. Clear estimate_hours.", t.ID)
	}
	if HasTagPrefix(t, CFPTagPrefix) {
		return fail("Cannot start %s: cfp: belongs on the parent feature, not on a child ticket. Remove the cfp: tag.", t.ID)
	}
	return nil
}
