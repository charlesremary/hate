// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"regexp"
	"strings"
	"testing"
)

// The baselined dashboard used to be dark while the shared panels are white
// cards, so panel text inherited light gray on white (HATE-dtda). Both
// dashboard flavors now share one light theme.
func TestBaselinedDashboardIsLight(t *testing.T) {
	page := GenerateDashboard(ganttFixture(), nil, "", "")
	for _, dark := range []string{"#0d0d0f", "#13131a", "#0a0a0c", "--text:     #e5e7eb"} {
		if strings.Contains(page, dark) {
			t.Errorf("baselined dashboard still contains dark palette value %q", dark)
		}
	}
	for _, want := range []string{"--bg:       #f4f5f7", "--surface:  #fff", "--text:     #1a1a1a"} {
		if !strings.Contains(page, want) {
			t.Errorf("baselined dashboard missing light palette value %q", want)
		}
	}
}

// panelCard matches a shared panel's white card container and captures the
// rest of its style attribute.
var panelCard = regexp.MustCompile(`background:#fff;([^"]*)"`)

// lowContrastText are text colors below 4.5:1 on white.
var lowContrastText = []string{"color:#999", "color:#888", "color:#9ca3af", "color:#eab308", "color:#16a34a", `fill="#888"`}

// Every shared panel's card sets its own dark text color, so it reads the
// same whatever page theme it is dropped into.
func TestSharedPanelsSetExplicitTextColor(t *testing.T) {
	panels := map[string]string{
		"plan strip":        RenderPlanStripHTML("TEST", PlanStatus{HasBaseline: true}),
		"forecast":          RenderForecastHTML(ForecastReport{}, nil),
		"load":              RenderLoadHTML(LoadReport{}),
		"hours budget":      RenderHoursBudgetHTML(HoursBudget{}),
		"estimate variance": RenderEstimateVarianceHTML(EstimateVariance{}),
		"hours at risk":     RenderHoursAtRiskHTML(nil),
		"overrides":         RenderOverridesHTML([]OverrideRow{{}}),
		"project cost":      RenderProjectCostHTML(ProjectCost{}),
		"test summary":      RenderTestSummaryLineHTML(nil),
		"blocked":           RenderBlockedHTML(nil),
	}
	for name, html := range panels {
		cards := 0
		for _, m := range panelCard.FindAllStringSubmatch(html, -1) {
			if strings.Contains(m[0], "border-radius:8px") && strings.Contains(m[0], "box-shadow") {
				cards++
				if !strings.Contains(m[1], "color:#1a1a1a") {
					t.Errorf("%s: card has no explicit dark text color: %s", name, m[0])
				}
			}
		}
		if html != "" && cards == 0 {
			t.Errorf("%s: no white card container found", name)
		}
		for _, bad := range lowContrastText {
			if strings.Contains(html, bad) {
				t.Errorf("%s: uses low-contrast text color %q", name, bad)
			}
		}
	}

	gantt := renderGanttPanel(ganttFixture(), nil, "note", "/x.drawio")
	if !strings.Contains(gantt, `<div style="padding:16px 24px 8px;color:#1a1a1a">`) {
		t.Error("gantt panel wrapper has no explicit dark text color")
	}
}
