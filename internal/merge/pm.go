// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"hate/internal/pm"
)

// MergeSlipEvents merges .tkt/pm/slip_events.json as a union by
// slip_event_id (events are never deleted). For an id on both sides:
//
//   - a resolved event beats an unresolved one; between two of the same
//     status the three-way rule applies (a tie keeps the remote one);
//   - the earliest detected_date of the two is kept;
//   - superseded_by, once set on either side, is kept (a re-baseline is final).
//
// Deterministic ids (pm.SlipEventID) make "the same slip" the same id. Old
// sequential ids ("SE-X-001") could be reused by two machines for DIFFERENT
// slips; when an id's task / original / revised dates differ, both events are
// kept and this machine's gets the id "<id>-<6 hex>" (reported as a note).
func MergeSlipEvents(base, mine, theirs []byte) ([]byte, []string, error) {
	m, err := decodeArray(mine, "this machine's slip events")
	if err != nil {
		return nil, nil, err
	}
	t, err := decodeArray(theirs, "the shared repo's slip events")
	if err != nil {
		return nil, nil, err
	}
	bl := optionalBaseArray(base)
	const key = "slip_event_id"
	index := func(l []interface{}) map[string]object {
		out := map[string]object{}
		for _, it := range l {
			if o, ok := it.(object); ok && asString(o[key]) != "" {
				out[asString(o[key])] = o
			}
		}
		return out
	}
	bm, mm := index(bl), index(m)
	tIDs := index(t)
	sameSlip := func(a, b object) bool {
		for _, f := range []string{"task_id", "original_due_date", "revised_due_date"} {
			if !equal(a[f], b[f]) {
				return false
			}
		}
		return true
	}

	var out []interface{}
	var notes []string
	var extra []interface{}
	for _, it := range t {
		te, ok := it.(object)
		id := asString(te[key])
		me, inMine := mm[id]
		if !ok || id == "" || !inMine || equal(te, me) {
			out = append(out, it)
			continue
		}
		if !sameSlip(te, me) {
			out = append(out, te)
			c := copyObject(me)
			sum := sha256.Sum256([]byte(fmt.Sprint(me["task_id"], "|", me["original_due_date"], "|", me["revised_due_date"])))
			newID := id + "-" + hex.EncodeToString(sum[:])[:6]
			c[key] = newID
			extra = append(extra, c)
			notes = append(notes, fmt.Sprintf("two different slips shared the id %s; this machine's (%s) is now %s", id, asString(me["task_id"]), newID))
			continue
		}
		out = append(out, mergeSlip(bm[id], me, te))
	}
	for _, it := range m {
		me, ok := it.(object)
		if !ok {
			if !containsEqual(out, it) {
				out = append(out, it)
			}
			continue
		}
		if _, inTheirs := tIDs[asString(me[key])]; inTheirs && asString(me[key]) != "" {
			continue
		}
		out = append(out, me)
	}
	out = append(out, extra...)

	var evs []pm.SlipEvent
	if err := remarshal(out, &evs); err != nil {
		return nil, nil, fmt.Errorf("merged slip events don't fit the slip event format: %v", err)
	}
	if evs == nil {
		evs = []pm.SlipEvent{}
	}
	data, err := indent(evs, false) // WriteSlipEvents writes no trailing newline
	if err != nil {
		return nil, nil, err
	}
	return data, notes, nil
}

// mergeSlip merges two versions of the same slip event.
func mergeSlip(b, m, t object) object {
	var win, other object
	mRes, tRes := asString(m["status"]) == "resolved", asString(t["status"]) == "resolved"
	switch {
	case mRes && !tRes:
		win, other = m, t
	case tRes && !mRes:
		win, other = t, m
	case b != nil && equal(t, b):
		win, other = m, t
	default:
		win, other = t, m
	}
	out := copyObject(win)
	md, td := asString(m["detected_date"]), asString(t["detected_date"])
	if md != "" && (td == "" || md < td) {
		out["detected_date"] = md
	} else if td != "" {
		out["detected_date"] = td
	}
	if sb, ok := out["superseded_by"]; !ok || sb == nil {
		if osb, ok := other["superseded_by"]; ok && osb != nil {
			out["superseded_by"] = osb
		}
	}
	return out
}

// MergeForecastHistory merges .tkt/pm/forecast_history.json as a union by
// date. Two entries for the same date: the later computed_at wins; without
// computed_at (older entries) the three-way rule, then the remote one.
// The result is sorted by date.
func MergeForecastHistory(base, mine, theirs []byte) ([]byte, error) {
	m, err := decodeArray(mine, "this machine's forecast history")
	if err != nil {
		return nil, err
	}
	t, err := decodeArray(theirs, "the shared repo's forecast history")
	if err != nil {
		return nil, err
	}
	bl := optionalBaseArray(base)
	byDate := func(l []interface{}) map[string]object {
		out := map[string]object{}
		for _, it := range l {
			if o, ok := it.(object); ok {
				out[asString(o["date"])] = o
			}
		}
		return out
	}
	bm, mm, tm := byDate(bl), byDate(m), byDate(t)
	merged := map[string]object{}
	for d, te := range tm {
		merged[d] = te
	}
	for d, me := range mm {
		te, ok := tm[d]
		if !ok || equal(me, te) {
			merged[d] = me
			continue
		}
		mc, tc := asString(me["computed_at"]), asString(te["computed_at"])
		switch {
		case mc != "" && tc != "" && mc != tc:
			if mc > tc {
				merged[d] = me
			}
		case mc != "" && tc == "":
			merged[d] = me
		case mc == "" && tc != "":
			// keep theirs
		default:
			if b, inBase := bm[d]; inBase && equal(te, b) {
				merged[d] = me
			}
		}
	}
	dates := make([]string, 0, len(merged))
	for d := range merged {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	out := make([]interface{}, 0, len(dates))
	for _, d := range dates {
		out = append(out, merged[d])
	}
	var hist []pm.ForecastHistoryEntry
	if err := remarshal(out, &hist); err != nil {
		return nil, fmt.Errorf("merged forecast history doesn't fit its format: %v", err)
	}
	if hist == nil {
		hist = []pm.ForecastHistoryEntry{}
	}
	return indent(hist, true)
}

// mergeBaseline resolves .tkt/pm/baseline.json: a baseline changed on one side
// takes that side; re-baselined on both, the remote baseline is kept and this
// machine's is archived as an extra archived baseline (needs attention).
func mergeBaseline(rel string, base, mine, theirs []byte) Resolution {
	var mb, tb pm.Baseline
	if json.Unmarshal(mine, &mb) != nil || json.Unmarshal(theirs, &tb) != nil {
		return fallback(KindBaseline, rel, theirs, mine, "a baseline is not valid JSON")
	}
	if base != nil {
		if sameContent(mine, base) {
			return Resolution{Kind: KindBaseline, Merged: theirs}
		}
		if sameContent(theirs, base) {
			return Resolution{Kind: KindBaseline, Merged: mine}
		}
	}
	arc := pm.BaselineArchive{
		ArchivedAt: mb.CreatedDate,
		ArchivedBy: mb.CreatedBy,
		Reason: "sync: this baseline was taken on another machine at the same time as the shared one " +
			"(by " + tb.CreatedBy + ", " + tb.CreatedDate + "), which was kept",
		Baseline: json.RawMessage(compactJSON(mine)),
	}
	data, err := indent(arc, true)
	if err != nil {
		return fallback(KindBaseline, rel, theirs, mine, err.Error())
	}
	return Resolution{
		Kind:      KindBaseline,
		Merged:    theirs,
		SideFiles: []SideFile{{Kind: SideBaselineArchive, Data: data}},
		Note: fmt.Sprintf("The baseline was replaced on two machines at once. Kept the shared one (by %s, %s); "+
			"this machine's (by %s, %s) is kept as an archived baseline.", tb.CreatedBy, tb.CreatedDate, mb.CreatedBy, mb.CreatedDate),
		NeedsAttention: true,
	}
}

// mergeBaselineArchive resolves two different archive files under the same
// name (two machines re-baselined on the same day): the remote file keeps the
// name, this machine's is written as the next free archive entry.
func mergeBaselineArchive(rel string, mine, theirs []byte) Resolution {
	var a pm.BaselineArchive
	if json.Unmarshal(mine, &a) != nil || json.Unmarshal(theirs, &a) != nil {
		return fallback(KindBaselineArchive, rel, theirs, mine, "an archived baseline is not valid JSON")
	}
	return Resolution{
		Kind:      KindBaselineArchive,
		Merged:    theirs,
		SideFiles: []SideFile{{Kind: SideBaselineArchive, Data: mine}},
		Note:      fmt.Sprintf("%s was archived on two machines; this machine's copy is kept as another archived baseline.", rel),
	}
}

func compactJSON(data []byte) []byte {
	v, err := decode(data)
	if err != nil {
		return data
	}
	out, err := json.Marshal(v)
	if err != nil {
		return data
	}
	return out
}
