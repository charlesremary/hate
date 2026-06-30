// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"sort"
	"strconv"
	"strings"

	"hate/internal/ticket"
)

// structuralTagPrefixes are plumbing tags that link/size tickets rather than
// describe the work — excluded from the hours-by-tag breakdown (they're pure
// noise there: one parent: per feature, cfp: on zero-hour parents).
var structuralTagPrefixes = []string{parentTagPrefix, cfpTagPrefix, "wtn:"}

func isStructuralTag(tag string) bool {
	for _, p := range structuralTagPrefixes {
		if strings.HasPrefix(tag, p) {
			return true
		}
	}
	return false
}

// Per-project stats report ("Generate stats").
//
// Informational, not a calibration engine: it reports what THIS project's tickets
// actually cost, so a human can eyeball it and carry numbers forward to a similar
// new project by hand. Two cuts:
//
//   - Class breakdown (clean, non-overlapping): functional / config / nonfunc /
//     author / unclassed hours, the code rate (h/CFP), and wrap %. These reconcile
//     to the total logged hours.
//   - Tag breakdown: for every DESCRIPTIVE tag on the project's tickets — count,
//     total hours, avg hours/ticket. Structural plumbing tags (parent:/cfp:/wtn:)
//     are excluded — they're per-ticket links/sizes, not work categories, and just
//     drown out the signal. A ticket has many tags, so these OVERLAP and do NOT sum
//     to the project total (that's the class cut) — it answers "tickets tagged X
//     typically cost this much."

// TagStat is the rollup for one tag across the project.
type TagStat struct {
	Tag               string  `json:"tag"`
	Tickets           int     `json:"tickets"`
	Hours             float64 `json:"hours"`
	AvgHoursPerTicket float64 `json:"avg_hours_per_ticket"`
}

// ProjectStats is the full per-project report.
type ProjectStats struct {
	// Code / class cut (project-wide, non-overlapping).
	TotalCFP        int      `json:"total_cfp"`
	FunctionalHours float64  `json:"functional_hours"`
	ConfigHours     float64  `json:"config_hours"`
	NonfuncHours    float64  `json:"nonfunc_hours"`
	UnclassedHours  float64  `json:"unclassed_hours"`
	HPerCFP         *float64 `json:"h_per_cfp"` // functional ÷ CFP, nil if either is 0
	WrapPct         *float64 `json:"wrap_pct"`  // (config+nonfunc) ÷ functional, nil if no functional

	// Totals.
	TotalLoggedHours float64 `json:"total_logged_hours"`
	TicketsWithHours int     `json:"tickets_with_hours"`
	TicketCount      int     `json:"ticket_count"`

	// Tag cut (every tag, overlapping), sorted by hours desc then tag.
	TagStats []TagStat `json:"tag_stats"`
}

// ComputeProjectStats builds the per-project report from all its tickets.
func ComputeProjectStats(tickets []*ticket.Ticket) ProjectStats {
	stats := ProjectStats{TagStats: []TagStat{}, TicketCount: len(tickets)}
	tagMap := map[string]*TagStat{}

	for _, t := range tickets {
		h := cosmicLoggedHours(t)
		stats.TotalLoggedHours += h
		if h > 0 {
			stats.TicketsWithHours++
		}

		// Class cut — project-wide, one class per ticket.
		switch cosmicClassOf(t.Tags) {
		case classFunctional:
			stats.FunctionalHours += h
		case classConfig:
			stats.ConfigHours += h
		case classNonfunc:
			stats.NonfuncHours += h
		default:
			stats.UnclassedHours += h
		}

		// CFP from cfp: tags (parents).
		if cfpStr, ok := cosmicTagValue(t.Tags, cfpTagPrefix); ok {
			if cfp, err := strconv.Atoi(cfpStr); err == nil && cfp > 0 {
				stats.TotalCFP += cfp
			}
		}

		// Tag cut — descriptive tags only (structural plumbing excluded), overlapping.
		for _, tag := range t.Tags {
			if isStructuralTag(tag) {
				continue
			}
			ts := tagMap[tag]
			if ts == nil {
				ts = &TagStat{Tag: tag}
				tagMap[tag] = ts
			}
			ts.Tickets++
			ts.Hours += h
		}
	}

	if stats.TotalCFP > 0 && stats.FunctionalHours > 0 {
		v := stats.FunctionalHours / float64(stats.TotalCFP)
		stats.HPerCFP = &v
	}
	if stats.FunctionalHours > 0 {
		w := (stats.ConfigHours + stats.NonfuncHours) / stats.FunctionalHours * 100
		stats.WrapPct = &w
	}

	for _, ts := range tagMap {
		if ts.Tickets > 0 {
			ts.AvgHoursPerTicket = ts.Hours / float64(ts.Tickets)
		}
		stats.TagStats = append(stats.TagStats, *ts)
	}
	sort.SliceStable(stats.TagStats, func(i, j int) bool {
		if stats.TagStats[i].Hours != stats.TagStats[j].Hours {
			return stats.TagStats[i].Hours > stats.TagStats[j].Hours
		}
		return stats.TagStats[i].Tag < stats.TagStats[j].Tag
	})

	return stats
}
