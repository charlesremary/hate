// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hate/internal/config"
)

const discoverBody = `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`

// post sends a server/discover POST to h as if from peer, with optional
// extra headers, and returns the recorder.
func post(h http.Handler, peer string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8000/mcp", strings.NewReader(discoverBody))
	req.RemoteAddr = peer
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "server/discover")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDiscover(t *testing.T) {
	rec := post(Handler(8000), "127.0.0.1:50000", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("discover: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"2026-07-28"`) {
		t.Errorf("supported versions missing 2026-07-28: %s", body)
	}
	if !strings.Contains(body, `"name":"hate"`) || !strings.Contains(body, `"version":"`+config.AppVersion+`"`) {
		t.Errorf("serverInfo should be hate v%s: %s", config.AppVersion, body)
	}
}

func TestOriginRejected(t *testing.T) {
	h := Handler(8000)
	rec := post(h, "127.0.0.1:50000", map[string]string{"Origin": "http://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("evil origin: %d, want 403", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, hasID := body["id"]; hasID || body["error"] == nil {
		t.Errorf("want a JSON-RPC error without id, got %s", rec.Body.String())
	}
	// The hate UI's own origin passes the Origin check.
	if rec := post(h, "127.0.0.1:50000", map[string]string{"Origin": "http://127.0.0.1:8000", "Sec-Fetch-Site": "same-origin"}); rec.Code != http.StatusOK {
		t.Errorf("own origin: %d %s, want 200", rec.Code, rec.Body.String())
	}
	// Another port on localhost is a different origin.
	if rec := post(h, "127.0.0.1:50000", map[string]string{"Origin": "http://localhost:3000"}); rec.Code != http.StatusForbidden {
		t.Errorf("other localhost port: %d, want 403", rec.Code)
	}
}

func TestNonLoopbackPeerRejected(t *testing.T) {
	if rec := post(Handler(8000), "192.168.1.20:50000", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("LAN peer: %d, want 403", rec.Code)
	}
	if rec := post(Handler(8000), "[::1]:50000", nil); rec.Code != http.StatusOK {
		t.Errorf("IPv6 loopback peer: %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestHostMismatchRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://evil.example/mcp", strings.NewReader(discoverBody))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	Handler(8000).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DNS-rebinding Host: %d, want 403", rec.Code)
	}
}

func TestGetNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8000/mcp", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	Handler(8000).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d, want 405", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Unix(0, 0)
	rl := newRateLimiter(20, 40)
	rl.now = func() time.Time { return now }
	for i := 0; i < 40; i++ {
		if !rl.allow() {
			t.Fatalf("call %d refused inside the burst", i+1)
		}
	}
	if rl.allow() {
		t.Fatal("call 41 allowed, want refused")
	}
	now = now.Add(100 * time.Millisecond) // refills 2 tokens
	if !rl.allow() || !rl.allow() || rl.allow() {
		t.Error("after 100ms want exactly 2 more calls")
	}

	h := guard(8000, newRateLimiter(0, 1), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	post(h, "127.0.0.1:1", nil)
	if rec := post(h, "127.0.0.1:1", nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("over limit: %d, want 429", rec.Code)
	}
}
