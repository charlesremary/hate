// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hate/internal/service"
)

func registerResources(s *mcp.Server) {
	guide := func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		u, err := url.Parse(req.Params.URI)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		md, err := service.Guide(u.Query().Get("section"))
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		res := &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: md}}}
		res.TTLMs = 3600000 // changes only with the binary
		res.CacheScope = "private"
		return res, nil
	}
	const guideDesc = "The hate agent guide: how to create tickets, the tag contract (class, cfp:, parent:), estimating, lifecycle and blocks, planning, the API. " +
		"Read section 3 before creating or promoting tickets. ?section=N returns only '## N.'."
	s.AddResource(&mcp.Resource{URI: "hate://guide", Name: "guide", Title: "hate agent guide", MIMEType: "text/markdown", Description: guideDesc}, guide)
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "hate://guide{?section}", Name: "guide-section", Title: "hate agent guide (one section)", MIMEType: "text/markdown", Description: guideDesc}, guide)

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "hate://projects/{project}/overview",
		Name:        "project-overview",
		Title:       "Project overview",
		MIMEType:    "application/json",
		Description: "A project's contacts, links and standing instructions (hand-maintained in the UI). project is an id, folder or prefix.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		rest, ok := strings.CutPrefix(req.Params.URI, "hate://projects/")
		project, ok2 := strings.CutSuffix(rest, "/overview")
		if !ok || !ok2 || project == "" {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		if p, err := url.PathUnescape(project); err == nil {
			project = p
		}
		root, err := service.ResolveProject(project)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		o, err := service.GetOverview(root)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(o)
		res := &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(b)}}}
		res.TTLMs = 60000
		res.CacheScope = "private"
		return res, nil
	})
}
