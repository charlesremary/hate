// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/catalog"
	"hate/internal/ticket"
)

func TestComputeWrapAggregate(t *testing.T) {
	cat := &catalog.Catalog{Entries: []catalog.CatalogEntry{
		{Type: "flow", Activity: "configure", Unit: "flow", SeedHours: 0.75},
		{Type: "deploy", Activity: "operate", Unit: "stack", SeedHours: 0.5},
	}}

	projects := []WrapProjectData{
		{Platform: "connect", Tickets: []*ticket.Ticket{
			tkt("a", []string{"wt:flow"}, 1.0),                 // 1 flow, 1h
			tkt("b", []string{"wt:flow", "wtn:3"}, 3.0),        // 3 flows, 3h
			tkt("c", []string{"wt:deploy"}, 0.5),               // 1 deploy, 0.5h
			tkt("d", []string{"wt:mystery"}, 2.0),              // orphan type
			tkt("e", []string{"config"}, 9.0),                  // no wt: — ignored
		}},
		{Platform: "", Tickets: []*ticket.Ticket{
			tkt("f", []string{"wt:flow"}, 2.0), // platform unset → "(unset)"
		}},
	}

	agg := ComputeWrapAggregate(projects, cat)

	// connect/flow: 4 units, 4h → 1.0/unit, 2 tickets
	var flow *WrapUnitRate
	for i := range agg.Rates {
		if agg.Rates[i].Platform == "connect" && agg.Rates[i].Type == "flow" {
			flow = &agg.Rates[i]
		}
	}
	if flow == nil {
		t.Fatal("connect/flow rate missing")
	}
	if flow.Tickets != 2 || flow.Units != 4 || flow.Hours != 4 {
		t.Errorf("flow: tickets=%d units=%v hours=%v (want 2/4/4)", flow.Tickets, flow.Units, flow.Hours)
	}
	if flow.AvgHoursPerUnit == nil || *flow.AvgHoursPerUnit != 1.0 {
		t.Errorf("flow avg = %v, want 1.0", flow.AvgHoursPerUnit)
	}
	if !flow.InCatalog || flow.Activity != "configure" || flow.SeedHours == nil || *flow.SeedHours != 0.75 {
		t.Errorf("flow catalog enrichment wrong: %+v", flow)
	}

	// orphan type flagged
	if len(agg.OrphanTypes) != 1 || agg.OrphanTypes[0] != "mystery" {
		t.Errorf("orphan types = %v, want [mystery]", agg.OrphanTypes)
	}

	// platforms include connect + (unset)
	if len(agg.Platforms) != 2 {
		t.Errorf("platforms = %v, want 2", agg.Platforms)
	}

	// totals: 4 (connect flow) + 0.5 (deploy) + 2 (orphan) + 2 (unset flow) = 8.5 hours
	if agg.TotalHours != 8.5 {
		t.Errorf("total hours = %v, want 8.5", agg.TotalHours)
	}
}
