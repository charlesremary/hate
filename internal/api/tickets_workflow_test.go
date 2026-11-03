// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The ticket GET carries a computed "workflow" (promote_to, demote_to, path);
// meeting tickets have none, and every write response carries the updated one.
func TestTicketWorkflowField(t *testing.T) {
	h, _ := setupEstimateProject(t)
	base := "/api/projects/est/tickets/"
	create := func(typ string) string {
		code, body := do(t, h, "POST", base, map[string]interface{}{"type": typ, "title": typ})
		if code != http.StatusOK {
			t.Fatalf("create %s: %d %v", typ, code, body)
		}
		return body["id"].(string)
	}
	workflowOf := func(body map[string]interface{}) map[string]interface{} {
		wf, _ := body["workflow"].(map[string]interface{})
		return wf
	}
	asStrings := func(v interface{}) []string {
		var out []string
		for _, s := range v.([]interface{}) {
			out = append(out, s.(string))
		}
		return out
	}

	dev := create("dev_task")
	code, body := do(t, h, "GET", base+dev, nil)
	wf := workflowOf(body)
	if code != http.StatusOK || wf == nil {
		t.Fatalf("GET dev_task: %d, workflow = %v", code, body["workflow"])
	}
	if wf["promote_to"] != "in_progress" || wf["demote_to"] != nil {
		t.Errorf("dev_task not_started: promote_to=%v demote_to=%v, want in_progress / null", wf["promote_to"], wf["demote_to"])
	}
	if _, ok := wf["demote_to"]; !ok {
		t.Error("demote_to should be present as null, not omitted")
	}
	want := []string{"not_started", "in_progress", "dev_complete", "qa_testing", "complete", "closed"}
	if got := asStrings(wf["path"]); !reflect.DeepEqual(got, want) {
		t.Errorf("dev_task path = %v, want %v", got, want)
	}

	for typ, want := range map[string][]string{
		"task":        {"not_started", "in_progress", "complete", "closed"},
		"design_task": {"not_started", "in_progress", "submitted_for_review", "approved", "closed"},
	} {
		_, body := do(t, h, "GET", base+create(typ), nil)
		if got := asStrings(workflowOf(body)["path"]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s path = %v, want %v", typ, got, want)
		}
	}

	_, body = do(t, h, "GET", base+create("meeting"), nil)
	if _, ok := body["workflow"]; ok {
		t.Errorf("meeting should have no workflow key, got %v", body["workflow"])
	}

	// Promote, then block: the write responses carry the new targets, and a
	// blocked ticket's promote_to is the real previous status, not "_previous".
	code, body = do(t, h, "POST", base+dev+"/promote", nil)
	if code != http.StatusOK || workflowOf(body)["promote_to"] != "dev_complete" || workflowOf(body)["demote_to"] != "not_started" {
		t.Errorf("promote response: %d workflow=%v", code, body["workflow"])
	}
	// hate 3.0: blocked is a flag; the status stays and promote is refused
	// until it's unblocked.
	code, body = do(t, h, "POST", base+dev+"/block", map[string]interface{}{"reason": "waiting", "waits_on": []string{"client IT"}})
	if blk, _ := body["blocked"].(map[string]interface{}); code != http.StatusOK || body["status"] != "in_progress" || blk["reason"] != "waiting" {
		t.Fatalf("block: %d %v", code, body)
	}
	if code, body = do(t, h, "POST", base+dev+"/promote", nil); code < 400 || !strings.Contains(fmt.Sprint(body["detail"]), "blocked") {
		t.Errorf("promote while blocked = %d %v", code, body)
	}
	code, body = do(t, h, "POST", base+dev+"/unblock", map[string]interface{}{"note": "keys arrived"})
	if code != http.StatusOK || body["blocked"] != nil || body["status"] != "in_progress" {
		t.Errorf("unblock: %d %v", code, body)
	}
}
