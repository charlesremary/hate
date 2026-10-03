// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"strings"
	"testing"
)

func staticFile(t *testing.T, name string) string {
	t.Helper()
	b, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// HATE-cwuo tc1: Help has no "Working in Agile" section (or TOC link) and has a
// "Planning in blocks" section pointing to the agent guide.
func TestHelpPlanningInBlocks(t *testing.T) {
	html := staticFile(t, "index.html")
	for _, bad := range []string{"Working in Agile", "help-agile"} {
		if strings.Contains(html, bad) {
			t.Errorf("index.html still has %q", bad)
		}
	}
	for _, want := range []string{`<a href="#help-blocks">9. Planning in blocks</a>`, `<section id="help-blocks">`, "<h2>9. Planning in blocks</h2>", "ticketing-and-cfp-guide.md"} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
}

// HATE-evqw tc1 (UI) and HATE-43sr tc4 (UI): Settings has the Planning blocks
// select; the Tickets tab's second view is "Work order", fed by GET /ready
// (no client-side stage computation left).
func TestUIBlocksAndWorkOrder(t *testing.T) {
	html, js := staticFile(t, "index.html"), staticFile(t, "app.js")
	for _, want := range []string{`id="block-weeks"`, `<option value="2">2 weeks</option>`, `<option value="3">3 weeks</option>`, `>Work order</button>`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
	if strings.Contains(html, `>Plan</button>`) {
		t.Error("the Plan view button wasn't renamed")
	}
	for _, want := range []string{"/block-weeks", "/ready`", "Ready now", "stage ${"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
	for _, bad := range []string{"computeExecPlan", "computeWorkOrder", "waveOf"} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js still has client-side stage logic %q", bad)
		}
	}
	// The view's markup puts the Ready now group first, then the rest.
	if r, d := strings.Index(js, "${readyHtml}"), strings.Index(js, "${restHtml}"); r < 0 || d < 0 || r > d {
		t.Error("Ready now should come before the dependency order")
	}
}
