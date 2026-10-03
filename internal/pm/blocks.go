// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"hate/internal/ticket"
)

// Planning blocks are fixed 2- or 3-week timeboxes a project's tickets are
// planned into (the project's block_weeks setting; unset = no blocks). Block 1
// starts on the Monday on or before the requested start (else the earliest
// planned start of an in-scope ticket, else today); each block runs Monday to
// the Friday of its last week, and the next one starts the Monday after. A
// ticket is planned into a block by setting its phase to the block label
// ("Block 01 (Nov 2-13)") and its planned start / due date to the block's
// bounds. Blocks are derived, never stored: nothing here writes.

// Block is one planning block.
type Block struct {
	N     int    `json:"n"`     // 1-based
	Label string `json:"label"` // "Block 01 (Nov 2-13)"
	Start string `json:"start"` // YYYY-MM-DD, a Monday
	End   string `json:"end"`   // YYYY-MM-DD, the Friday of the block's last week
}

// maxBlocks caps a block list (a 3-week block x 200 is over 11 years).
const maxBlocks = 200

// ValidBlockWeeks reports whether n is an allowed block length: 2 or 3 weeks,
// or 0 (no blocks).
func ValidBlockWeeks(n int) bool {
	return n == 0 || n == 2 || n == 3
}

// mondayOnOrBefore returns the Monday of d's week (d itself on a Monday;
// Saturday and Sunday go back to the Monday before them).
func mondayOnOrBefore(d time.Time) time.Time {
	d = dateOnly(d)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// BlockLabel names block n by its dates: "Block 01 (Nov 2-13)", or
// "Block 03 (Nov 30-Dec 11)" across a month boundary.
func BlockLabel(n int, start, end time.Time) string {
	if start.Month() == end.Month() && start.Year() == end.Year() {
		return fmt.Sprintf("Block %02d (%s %d-%d)", n, start.Format("Jan"), start.Day(), end.Day())
	}
	return fmt.Sprintf("Block %02d (%s-%s)", n, start.Format("Jan 2"), end.Format("Jan 2"))
}

// blockPhaseRe matches a block phase: "Block 01", "Block 7 (Nov 2-13)", ...
var blockPhaseRe = regexp.MustCompile(`(?i)^\s*block\s+(\d+)\b`)

// ParseBlockPhase returns the block number of a "Block NN ..." phase.
func ParseBlockPhase(phase string) (int, bool) {
	m := blockPhaseRe.FindStringSubmatch(phase)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// BlockAnchor is the start of block 1: the Monday on or before the requested
// start, else on or before the earliest planned_start_date of an in-scope
// (not backlog, not cancelled) ticket, else on or before today.
func BlockAnchor(requestedStart string, tickets []*ticket.Ticket, today time.Time) time.Time {
	if d := parseDate(requestedStart); !d.IsZero() {
		return mondayOnOrBefore(d)
	}
	var earliest time.Time
	for _, t := range tickets {
		if ticket.IsBacklog(t) || isCancelled(t) || t.PlannedStartDate == nil {
			continue
		}
		if d := parseDate(*t.PlannedStartDate); !d.IsZero() && (earliest.IsZero() || d.Before(earliest)) {
			earliest = d
		}
	}
	if !earliest.IsZero() {
		return mondayOnOrBefore(earliest)
	}
	return mondayOnOrBefore(today)
}

// BuildBlocks lists consecutive blocks of `weeks` weeks from the Monday of
// anchor's week through the block containing `until`, plus one more. A
// non-block length returns an empty (non-nil) list.
func BuildBlocks(weeks int, anchor, until time.Time) []Block {
	out := []Block{}
	if weeks != 2 && weeks != 3 {
		return out
	}
	start := mondayOnOrBefore(anchor)
	until = dateOnly(until)
	reached := false
	for n := 1; n <= maxBlocks; n++ {
		end := start.AddDate(0, 0, weeks*7-3) // Friday of the last week
		out = append(out, Block{N: n, Label: BlockLabel(n, start, end), Start: fmtDate(start), End: fmtDate(end)})
		if reached {
			break
		}
		// The block "contains" until through its weekend (until the next Monday).
		next := start.AddDate(0, 0, weeks*7)
		if until.Before(next) {
			reached = true
		}
		start = next
	}
	return out
}

// BlocksForProject returns the project's blocks, or an empty list when
// block_weeks is unset. The list runs to the later of the requested end and
// the projected finish (the capacity-aware schedule from the later of today
// and the requested start), plus one block. cfg may be nil.
func BlocksForProject(cfg *ticket.ProjectConfig, tickets []*ticket.Ticket, ctx EstimateContext, today time.Time) []Block {
	if cfg == nil || (cfg.BlockWeeks != 2 && cfg.BlockWeeks != 3) {
		return []Block{}
	}
	anchor := BlockAnchor(cfg.RequestedStart, tickets, today)
	until := parseDate(cfg.EffectiveRequestedEnd())
	start := dateOnly(today)
	if rs := parseDate(cfg.RequestedStart); rs.After(start) {
		start = rs
	}
	if f := planFinish(ScheduleCapacity(tickets, cfg.Resources, ctx, start)); f.After(until) {
		until = f
	}
	if until.Before(anchor) {
		until = anchor
	}
	return BuildBlocks(cfg.BlockWeeks, anchor, until)
}

// blockIndexAt returns the index of the block whose span (Monday through the
// Sunday after its last Friday) contains d: -1 before the first block,
// len(blocks) after the last.
func blockIndexAt(blocks []Block, d time.Time) int {
	if len(blocks) == 0 {
		return -1
	}
	if d.Before(parseDate(blocks[0].Start)) {
		return -1
	}
	for i, b := range blocks {
		if !d.After(parseDate(b.End).AddDate(0, 0, 2)) {
			return i
		}
	}
	return len(blocks)
}
