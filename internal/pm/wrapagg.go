// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"sort"
	"strconv"
	"strings"

	"hate/internal/catalog"
	"hate/internal/ticket"
)

// Wrap hours-per-unit aggregation (HATE-2b1x).
//
// Pools wrap-tagged tickets across projects and reports measured hours-per-unit
// keyed by (platform, deliverable-type) with a sample size. Per the data model
// (docs/wrap-catalog-data-model.md §4) these numbers are computed live from the
// tickets — never stored in the catalog. Pooling is over RAW units
// (Σ hours ÷ Σ units), not an average of per-ticket rates.
//
// Tag convention (HATE-qjz4): `wt:<type>` names the catalog archetype; `wtn:<N>`
// is the unit count on the ticket (default 1).

const (
	wrapTypeTagPrefix  = "wt:"
	wrapCountTagPrefix = "wtn:"
)

// WrapProjectData is one project's contribution: its platform plus its tickets.
type WrapProjectData struct {
	Platform string
	Tickets  []*ticket.Ticket
}

// WrapUnitRate is the measured rate for one (platform, type) cell.
type WrapUnitRate struct {
	Platform        string   `json:"platform"`
	Type            string   `json:"type"`
	Activity        string   `json:"activity"`  // from catalog ("" if type not in catalog)
	Unit            string   `json:"unit"`      // from catalog
	InCatalog       bool     `json:"in_catalog"` // false flags an orphan tag (typo / retired type)
	Tickets         int      `json:"tickets"`   // # tickets contributing — the sample size
	Units           float64  `json:"units"`     // Σ wtn counts
	Hours           float64  `json:"hours"`     // Σ logged hours
	AvgHoursPerUnit *float64 `json:"avg_hours_per_unit"`
	SeedHours       *float64 `json:"seed_hours"` // catalog seed, for comparison (nil if not catalogued)
}

// WrapAggregate is the full org-level rollup.
type WrapAggregate struct {
	Rates       []WrapUnitRate `json:"rates"`
	TotalHours  float64        `json:"total_hours"`
	TotalUnits  float64        `json:"total_units"`
	Platforms   []string       `json:"platforms"`
	OrphanTypes []string       `json:"orphan_types"` // wt: tags with no catalog entry
}

// wrapTagsOf extracts the wrap type and unit count from a ticket's tags.
// Returns ok=false when the ticket carries no wt: tag.
func wrapTagsOf(tags []string) (typ string, count float64, ok bool) {
	count = 1
	for _, t := range tags {
		switch {
		case strings.HasPrefix(t, wrapCountTagPrefix):
			if n, err := strconv.Atoi(strings.TrimSpace(t[len(wrapCountTagPrefix):])); err == nil && n > 0 {
				count = float64(n)
			}
		case strings.HasPrefix(t, wrapTypeTagPrefix):
			typ = strings.TrimSpace(t[len(wrapTypeTagPrefix):])
			ok = true
		}
	}
	if typ == "" {
		ok = false
	}
	return typ, count, ok
}

// ComputeWrapAggregate pools wrap-tagged tickets across projects into per-(platform,
// type) measured rates, enriched from the catalog.
func ComputeWrapAggregate(projects []WrapProjectData, cat *catalog.Catalog) WrapAggregate {
	type cell struct {
		tickets int
		units   float64
		hours   float64
	}
	cells := map[string]*cell{} // key: platform\x00type

	catByType := map[string]catalog.CatalogEntry{}
	if cat != nil {
		for _, e := range cat.Entries {
			catByType[e.Type] = e
		}
	}

	platformSet := map[string]bool{}
	for _, p := range projects {
		plat := p.Platform
		if plat == "" {
			plat = "(unset)"
		}
		for _, t := range p.Tickets {
			typ, count, ok := wrapTagsOf(t.Tags)
			if !ok {
				continue
			}
			hours := cosmicLoggedHours(t)
			key := plat + "\x00" + typ
			c := cells[key]
			if c == nil {
				c = &cell{}
				cells[key] = c
			}
			c.tickets++
			c.units += count
			c.hours += hours
			platformSet[plat] = true
		}
	}

	agg := WrapAggregate{Rates: []WrapUnitRate{}, Platforms: []string{}, OrphanTypes: []string{}}
	orphanSet := map[string]bool{}
	for key, c := range cells {
		parts := strings.SplitN(key, "\x00", 2)
		plat, typ := parts[0], parts[1]
		r := WrapUnitRate{
			Platform: plat,
			Type:     typ,
			Tickets:  c.tickets,
			Units:    c.units,
			Hours:    c.hours,
		}
		if e, found := catByType[typ]; found {
			r.InCatalog = true
			r.Activity = e.Activity
			r.Unit = e.Unit
			seed := e.SeedHours
			// Per-platform seed override if present.
			if ps, ok := e.PlatformSeedHours[plat]; ok {
				seed = ps
			}
			r.SeedHours = &seed
		} else {
			orphanSet[typ] = true
		}
		if c.units > 0 {
			avg := c.hours / c.units
			r.AvgHoursPerUnit = &avg
		}
		agg.Rates = append(agg.Rates, r)
		agg.TotalHours += c.hours
		agg.TotalUnits += c.units
	}

	// Stable order: platform, then activity, then type.
	sort.SliceStable(agg.Rates, func(i, j int) bool {
		if agg.Rates[i].Platform != agg.Rates[j].Platform {
			return agg.Rates[i].Platform < agg.Rates[j].Platform
		}
		if agg.Rates[i].Activity != agg.Rates[j].Activity {
			return agg.Rates[i].Activity < agg.Rates[j].Activity
		}
		return agg.Rates[i].Type < agg.Rates[j].Type
	})
	for p := range platformSet {
		agg.Platforms = append(agg.Platforms, p)
	}
	sort.Strings(agg.Platforms)
	for t := range orphanSet {
		agg.OrphanTypes = append(agg.OrphanTypes, t)
	}
	sort.Strings(agg.OrphanTypes)
	return agg
}
