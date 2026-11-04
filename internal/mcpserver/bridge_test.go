// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hate/internal/mcpbridge"
	"hate/internal/ticket"
)

// bridged connects an SDK client to the app's /mcp through the stdio bridge
// (HATE-qypd): client <-stdio-> bridge <-HTTP-> app.
func bridged(t *testing.T, version string) *mcp.ClientSession {
	t.Helper()
	srv := fullServer(t)
	toBridge, clientOut := io.Pipe()
	clientIn, fromBridge := io.Pipe()
	b := &mcpbridge.Bridge{URL: srv.URL + Path}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Run(ctx, toBridge, fromBridge); fromBridge.Close() }()
	var opts *mcp.ClientSessionOptions
	if version != "" {
		opts = &mcp.ClientSessionOptions{ProtocolVersion: version}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-client", Version: "1"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, opts)
	if err != nil {
		t.Fatalf("connect through the bridge (%q): %v", version, err)
	}
	t.Cleanup(func() { cs.Close(); clientOut.Close(); cancel() })
	return cs
}

// HATE-qypd tc1/tc2 (in-process): every tool works through the bridge, on
// the current protocol and the older initialize handshake, and writes land in
// the project like any other.
func TestStdioBridge(t *testing.T) {
	root, _ := setupProject(t)
	for _, v := range []string{"", "2025-11-25"} {
		cs := bridged(t, v)
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil || len(res.Tools) != 23 {
			t.Fatalf("%q: tools = %d, %v", v, len(res.Tools), err)
		}
		out := mustOK(t, cs, "hate_create_tickets", map[string]any{"project": "est", "tickets": []any{map[string]any{"type": "task", "title": "via stdio " + v}}})
		id := out["created"].([]any)[0].(map[string]any)["id"].(string)
		if tk, err := ticket.ReadTicket(root, id); err != nil || !strings.HasPrefix(tk.Title, "via stdio") {
			t.Errorf("%q: ticket %s: %v", v, id, err)
		}
		if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "hate://guide?section=3"}); err != nil {
			t.Errorf("%q: resource through the bridge: %v", v, err)
		}
	}
}
