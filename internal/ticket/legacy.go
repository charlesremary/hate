// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import "fmt"

// Retired ticket types and statuses.
//
// Older projects still hold tickets typed "feature" or "defect" and the status
// "open". Those values no longer validate, so such a ticket could not be
// edited at all. They are mapped on read:
//
//	type feature -> dev_task
//	type defect  -> dev_task, plus a "defect" tag
//	status open  -> not_started
//
// The mapping is in memory only. The file keeps its legacy values until the
// next edit of that ticket, which writes the normalized form together with an
// activity note per change (e.g. "normalized legacy type feature -> dev_task").
// A repo is never bulk-rewritten.

// DefectTag marks a ticket that was typed "defect" before that type retired.
const DefectTag = "defect"

// ActionNormalized is the activity action recorded when a legacy value is
// written in its normalized form.
const ActionNormalized = "normalized"

// legacyTypes / legacyStatuses map retired values to their replacements.
var (
	legacyTypes    = map[string]string{"feature": "dev_task", "defect": "dev_task"}
	legacyStatuses = map[string]string{"open": "not_started"}
)

// NormalizeLegacy maps retired types and statuses on t to supported ones (see
// above) and remembers what it changed, for WriteTicket to record. It reports
// whether anything changed. ReadTicket and ReadAllTickets call it.
func NormalizeLegacy(t *Ticket) bool {
	changed := false
	if to, ok := legacyTypes[t.Type]; ok {
		t.legacyNotes = append(t.legacyNotes, fmt.Sprintf("normalized legacy type %s -> %s", t.Type, to))
		if t.Type == "defect" && !Contains(t.Tags, DefectTag) {
			t.Tags = append(t.Tags, DefectTag)
		}
		t.Type = to
		changed = true
	}
	if to, ok := legacyStatuses[t.Status]; ok {
		t.legacyNotes = append(t.legacyNotes, fmt.Sprintf("normalized legacy status %s -> %s", t.Status, to))
		t.Status = to
		changed = true
	}
	return changed
}

// recordNormalization appends the pending normalization notes to the activity
// log (once) before the ticket is written, credited to the author of the edit.
func recordNormalization(t *Ticket) {
	if len(t.legacyNotes) == 0 {
		return
	}
	// Credit the person whose edit triggered the write (the newest activity).
	author := "hate"
	if n := len(t.Activity); n > 0 && t.Activity[n-1].Author != "" {
		author = t.Activity[n-1].Author
	}
	ts := NowISO()
	for _, n := range t.legacyNotes {
		t.Activity = append(t.Activity, Activity{Timestamp: ts, Author: author, Action: ActionNormalized, Detail: n})
	}
	t.legacyNotes = nil
}
