// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"reflect"
	"testing"
)

func TestWorkflowPath(t *testing.T) {
	want := map[string][]string{
		"dev_task":    {"not_started", "in_progress", "dev_complete", "qa_testing", "complete", "closed"},
		"task":        {"not_started", "in_progress", "complete", "closed"},
		"design_task": {"not_started", "in_progress", "submitted_for_review", "approved", "closed"},
	}
	for typ, w := range want {
		wf, ok := GetWorkflowForType(typ)
		if !ok {
			t.Fatalf("%s: no workflow", typ)
		}
		if got := WorkflowPath(wf); !reflect.DeepEqual(got, w) {
			t.Errorf("%s path = %v, want %v", typ, got, w)
		}
	}
	// Every workflow type has a path test above.
	if len(TypeWorkflows) != len(want) {
		t.Errorf("TypeWorkflows has %d types, the test covers %d", len(TypeWorkflows), len(want))
	}
}

func strOrNil(p *string) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

func TestTicketWorkflow(t *testing.T) {
	cases := []struct {
		name, typ, status string
		activity          []Activity
		promote, demote   interface{}
	}{
		{"dev not_started", "dev_task", "not_started", nil, "in_progress", nil},
		{"dev qa_testing", "dev_task", "qa_testing", nil, "complete", "rework"},
		{"dev rework", "dev_task", "rework", nil, "qa_testing", nil},
		{"task closed", "task", "closed", nil, nil, nil},
		{"design approved", "design_task", "approved", nil, "closed", "submitted_for_review"},
		{"dev blocked resolves _previous", "dev_task", "blocked", []Activity{
			{Action: "status_changed", Detail: "not_started -> in_progress"},
			{Action: "status_changed", Detail: "in_progress -> blocked"},
		}, "in_progress", "in_progress"},
	}
	for _, c := range cases {
		v := TicketWorkflow(&Ticket{Type: c.typ, Status: c.status, Activity: c.activity})
		if v == nil {
			t.Fatalf("%s: nil workflow", c.name)
		}
		if got := strOrNil(v.PromoteTo); got != c.promote {
			t.Errorf("%s: promote_to = %v, want %v", c.name, got, c.promote)
		}
		if got := strOrNil(v.DemoteTo); got != c.demote {
			t.Errorf("%s: demote_to = %v, want %v", c.name, got, c.demote)
		}
		if len(v.Path) == 0 || v.Path[0] != "not_started" {
			t.Errorf("%s: path = %v", c.name, v.Path)
		}
	}
	// Types with no workflow get none.
	for _, typ := range []string{"meeting", "administration"} {
		if v := TicketWorkflow(&Ticket{Type: typ, Status: "complete"}); v != nil {
			t.Errorf("%s: workflow = %+v, want nil", typ, v)
		}
	}
}
