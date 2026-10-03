// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// writeRaw stores a ticket file exactly as given (legacy values included),
// bypassing WriteTicket's validation.
func writeRaw(t *testing.T, root string, tk *Ticket) {
	t.Helper()
	data, _ := json.MarshalIndent(tk, "", "  ")
	if err := os.MkdirAll(TicketsDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TicketPath(root, tk.ID), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}

func rawField(t *testing.T, root, id, field string) interface{} {
	t.Helper()
	data, err := os.ReadFile(TicketPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m[field]
}

func legacyRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := WriteConfig(root, DefaultConfig("c", "Legacy", "leg", "L")); err != nil {
		t.Fatal(err)
	}
	f := BlankTicket("L-feat", "feature", "Old feature", "a@x")
	d := BlankTicket("L-def", "defect", "Old defect", "a@x")
	sev := "major"
	d.DefectSeverity = &sev
	o := BlankTicket("L-open", "task", "Open task", "a@x")
	o.Status = "open"
	for _, tk := range []*Ticket{f, d, o} {
		writeRaw(t, root, tk)
	}
	return root
}

// HATE-w0in tc1: promoting a ticket stored as "feature" succeeds; the file
// then says dev_task with a "normalized legacy type" activity note.
func TestNormalizeFeatureOnPromote(t *testing.T) {
	root := legacyRoot(t)
	tk, err := Promote(root, "L-feat", "pm@x")
	if err != nil {
		t.Fatalf("promote feature: %v", err)
	}
	if tk.Type != "dev_task" || tk.Status != "in_progress" {
		t.Errorf("after promote: %s / %s", tk.Type, tk.Status)
	}
	if got := rawField(t, root, "L-feat", "type"); got != "dev_task" {
		t.Errorf("file type = %v, want dev_task", got)
	}
	re, _ := ReadTicket(root, "L-feat")
	notes := 0
	for _, a := range re.Activity {
		if a.Action == ActionNormalized {
			notes++
			if a.Detail != "normalized legacy type feature -> dev_task" || a.Author != "pm@x" {
				t.Errorf("note = %+v", a)
			}
		}
	}
	if notes != 1 {
		t.Errorf("normalization notes = %d, want 1", notes)
	}
	// A second edit doesn't repeat the note.
	if _, err := AddComment(root, "L-feat", "again", "pm@x"); err != nil {
		t.Fatal(err)
	}
	re, _ = ReadTicket(root, "L-feat")
	if n := strings.Count(activityDetails(re), "normalized legacy"); n != 1 {
		t.Errorf("notes after a second edit = %d, want 1", n)
	}
}

func activityDetails(t *Ticket) string {
	var b strings.Builder
	for _, a := range t.Activity {
		b.WriteString(a.Detail + "\n")
	}
	return b.String()
}

// HATE-w0in tc2: editing a ticket stored as "defect" succeeds; it becomes a
// dev_task tagged "defect" (its defect fields are kept).
func TestNormalizeDefectOnEdit(t *testing.T) {
	root := legacyRoot(t)
	if _, err := EditField(root, "L-def", "title", "Renamed defect", "pm@x"); err != nil {
		t.Fatalf("edit defect: %v", err)
	}
	if got := rawField(t, root, "L-def", "type"); got != "dev_task" {
		t.Errorf("file type = %v", got)
	}
	tags, _ := rawField(t, root, "L-def", "tags").([]interface{})
	if len(tags) != 1 || tags[0] != DefectTag {
		t.Errorf("tags = %v, want [defect]", tags)
	}
	if got := rawField(t, root, "L-def", "defect_severity"); got != "major" {
		t.Errorf("defect_severity = %v, want kept", got)
	}
	re, _ := ReadTicket(root, "L-def")
	if !strings.Contains(activityDetails(re), "normalized legacy type defect -> dev_task") {
		t.Error("missing the normalization note")
	}
}

// HATE-w0in tc3: status "open" reads as not_started (and is written so on the
// next edit). Reading alone never rewrites a file (no bulk rewrite).
func TestNormalizeOpenStatusOnRead(t *testing.T) {
	root := legacyRoot(t)
	before := map[string][]byte{}
	for _, id := range []string{"L-feat", "L-def", "L-open"} {
		before[id], _ = os.ReadFile(TicketPath(root, id))
	}
	tk, err := ReadTicket(root, "L-open")
	if err != nil || tk.Status != "not_started" {
		t.Fatalf("read open: %v %v", tk, err)
	}
	all, _ := ReadAllTickets(root)
	for _, x := range all {
		if !Contains(TicketTypes, x.Type) || !Contains(Statuses, x.Status) {
			t.Errorf("%s reads as %s/%s", x.ID, x.Type, x.Status)
		}
	}
	if err := RegenerateIndex(root); err != nil {
		t.Fatal(err)
	}
	idx, _ := ReadIndex(root)
	for _, s := range idx.Tickets {
		if s.Type == "feature" || s.Type == "defect" || s.Status == "open" {
			t.Errorf("index lists %s as %s/%s", s.ID, s.Type, s.Status)
		}
	}
	for id, b := range before {
		after, _ := os.ReadFile(TicketPath(root, id))
		if string(after) != string(b) {
			t.Errorf("%s was rewritten by a read", id)
		}
	}
	if _, err := AssignTicket(root, "L-open", "dev@x", "pm@x"); err != nil {
		t.Fatal(err)
	}
	if got := rawField(t, root, "L-open", "status"); got != "not_started" {
		t.Errorf("status after edit = %v", got)
	}
	re, _ := ReadTicket(root, "L-open")
	if !strings.Contains(activityDetails(re), "normalized legacy status open -> not_started") {
		t.Error("missing the status note")
	}
}
