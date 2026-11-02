// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import "testing"

func TestNormalizeCommentIDs(t *testing.T) {
	tk := &Ticket{Activity: []Activity{
		{Timestamp: "1", Action: "created"},
		{Timestamp: "2", Author: "a", Action: "comment", Detail: "old one"},     // legacy, no id
		{ID: "c1", Timestamp: "3", Author: "a", Action: "comment", Detail: "x"}, // collides with nothing yet
		{Timestamp: "4", Author: "b", Action: "comment", Detail: "legacy two"},
		// The same comment kept twice by a merge: original and edited copy.
		{ID: "c1", Timestamp: "3", Author: "a", Action: "comment", Detail: "x edited", EditedAt: "9"},
		// A different comment that got the same id on another machine.
		{ID: "c1", Timestamp: "5", Author: "c", Action: "comment", Detail: "other"},
	}}
	normalizeCommentIDs(tk)

	got := map[string]string{}
	for _, a := range tk.Activity {
		if a.Action == "comment" {
			if _, dup := got[a.ID]; dup {
				t.Fatalf("duplicate id %s after normalize: %+v", a.ID, tk.Activity)
			}
			got[a.ID] = a.Detail
		}
	}
	if len(got) != 4 {
		t.Fatalf("comments = %v, want 4 (merge duplicate dropped)", got)
	}
	if got["c1"] != "x edited" {
		t.Errorf("c1 = %q, want the edited copy", got["c1"])
	}
	for _, d := range []string{"old one", "legacy two", "other"} {
		found := false
		for id, v := range got {
			if v == d && id != "" && id != "c1" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q lost or without a fresh id: %v", d, got)
		}
	}
	if tk.Activity[0].ID != "" {
		t.Errorf("system entry got an id: %+v", tk.Activity[0])
	}
}
