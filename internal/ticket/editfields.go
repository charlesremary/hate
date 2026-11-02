// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"fmt"
	"sort"
)

// MultiEditFields are the fields EditFields accepts (the grooming fields an
// agent sets while classing, estimating and planning tickets).
var MultiEditFields = map[string]bool{
	"title": true, "description": true, "priority": true, "estimate_hours": true,
	"assignee": true, "phase": true, "planned_start_date": true, "due_date": true,
}

// EditFields applies several field changes and tag additions/removals to one
// ticket in a single write. It is all-or-nothing: every change is validated
// on an in-memory copy and nothing is written if any fails. Each changed
// field gets its own "edited" activity entry (fields in name order, then
// tags), in the same format as EditField. The caller holds the project lock.
func EditFields(repoRoot, ticketID string, fields map[string]interface{}, addTags, removeTags []string, author string) (*Ticket, error) {
	if len(fields) == 0 && len(addTags) == 0 && len(removeTags) == 0 {
		return nil, fmt.Errorf("nothing to change: give fields, add_tags or remove_tags")
	}
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(fields))
	for f := range fields {
		if !MultiEditFields[f] {
			return nil, fmt.Errorf("field %q can't be set here (allowed: title, description, priority, estimate_hours, assignee, phase, planned_start_date, due_date)", f)
		}
		names = append(names, f)
	}
	sort.Strings(names)

	type change struct{ field, old, new string }
	var changes []change
	for _, f := range names {
		v := fields[f]
		if (f == "planned_start_date" || f == "due_date") && v != nil {
			if s, ok := v.(string); ok && s != "" {
				if err := ValidateDate(s); err != nil {
					return nil, err
				}
			}
		}
		old := getFieldValue(t, f)
		if err := setFieldValue(t, f, v); err != nil {
			return nil, err
		}
		changes = append(changes, change{f, formatFieldValue(old), formatFieldValue(v)})
	}

	if len(addTags) > 0 || len(removeTags) > 0 {
		old := append([]string{}, t.Tags...)
		drop := map[string]bool{}
		for _, tg := range removeTags {
			drop[tg] = true
		}
		next := []string{}
		for _, tg := range t.Tags {
			if !drop[tg] && !Contains(next, tg) {
				next = append(next, tg)
			}
		}
		for _, tg := range addTags {
			if tg != "" && !Contains(next, tg) {
				next = append(next, tg)
			}
		}
		t.Tags = next
		changes = append(changes, change{"tags", formatFieldValue(old), formatFieldValue(next)})
	}

	for _, c := range changes {
		addActivity(t, author, "edited", fmt.Sprintf("%s: %v -> %v", c.field, c.old, c.new))
	}
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}
