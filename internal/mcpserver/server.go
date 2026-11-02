// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package mcpserver is hate's MCP server: Streamable HTTP at /mcp inside the
// running app (MCP 2026-07-28, stateless), so every tool call goes through the
// same process-local project lock, auto-commit and auto-push as the UI. See
// docs/plan-mcp-server.md.
package mcpserver

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hate/internal/config"
)

// Path is where the MCP endpoint is mounted.
const Path = "/mcp"

// NewServer builds the MCP server with hate's tools and resources registered
// (in a fixed order, so tools/list is deterministic).
func NewServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "hate",
		Title:   "hate ticketing",
		Version: config.AppVersion,
	}, &mcp.ServerOptions{
		// The tool and resource sets are static: no list_changed notifications,
		// and no logging capability (deprecated in 2026-07-28).
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{},
			Resources: &mcp.ResourceCapabilities{},
		},
	})
	return s
}

// Handler returns the /mcp HTTP handler for an app listening on port: the
// stateless Streamable HTTP transport behind the localhost guards (loopback
// peer, Origin allowlist, rate limit, cross-origin protection). The SDK's own
// DNS-rebinding check (non-localhost Host on a loopback connection -> 403)
// stays on.
func Handler(port int) http.Handler {
	srv := NewServer()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		PropagateRequestCancellation: true,
	})
	return guard(port, newRateLimiter(20, 40), http.NewCrossOriginProtection().Handler(h))
}
