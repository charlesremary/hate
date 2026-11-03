// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"testing"
)

// Status changes take the author from the JSON body or ?author= (body wins),
// so a caller using either convention is recorded (HATE-7jf1).
func TestStatusChangeAuthor(t *testing.T) {
	h, _ := setupEstimateProject(t)
	base := "/api/projects/est/tickets/"
	code, body := do(t, h, "POST", base, map[string]interface{}{"type": "task", "title": "author"})
	if code != http.StatusOK {
		t.Fatalf("create: %d %v", code, body)
	}
	id := body["id"].(string)
	lastAuthor := func(body map[string]interface{}) string {
		acts, _ := body["activity"].([]interface{})
		if len(acts) == 0 {
			return ""
		}
		a, _ := acts[len(acts)-1].(map[string]interface{})
		s, _ := a["author"].(string)
		return s
	}

	for _, c := range []struct {
		name, path string
		body       interface{}
		want       string
	}{
		{"promote body", base + id + "/promote", map[string]interface{}{"author": "body@x"}, "body@x"},
		{"demote query", base + id + "/demote?author=query@x", nil, "query@x"},
		{"promote body wins", base + id + "/promote?author=query@x", map[string]interface{}{"author": "body@x"}, "body@x"},
		{"force-close query", base + id + "/force-close?author=query@x", map[string]interface{}{"category": "descoped", "reason": "test cleanup"}, "query@x"},
	} {
		code, body := do(t, h, "POST", c.path, c.body)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", c.name, code, body)
		}
		if got := lastAuthor(body); got != c.want {
			t.Errorf("%s: last activity author = %q, want %q", c.name, got, c.want)
		}
	}

	// No body and no query still promotes.
	_, body = do(t, h, "POST", base, map[string]interface{}{"type": "task", "title": "anon"})
	if code, body := do(t, h, "POST", base+body["id"].(string)+"/promote", nil); code != http.StatusOK || body["status"] != "in_progress" {
		t.Errorf("bare promote: %d %v", code, body["status"])
	}
}
