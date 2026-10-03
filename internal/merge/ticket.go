// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package merge

import (
	"fmt"
	"sort"

	"hate/internal/ticket"
)

// SyncAuthor is the author of the activity notes a merge adds.
const SyncAuthor = "hate sync"

// MergeTicket three-way merges a ticket file (tickets/<id>.json).
//
//   - time_entries, test_cases, attachments: unioned by id. Two machines that
//     each added a different entry under the same new id (time entry ids are
//     "t<n>") keep both: this machine's gets the next free id.
//   - tags, predecessors: merged as sets (additions and removals from both).
//   - activity: the union of both logs, in time order.
//   - updated_at: the later of the two.
//   - any other field: changed on one side takes that side; changed on both,
//     the side with the newer updated_at wins (the remote on a tie) and an
//     activity note (action ticket.ActionSyncMerge) records both values.
//
// Returns the merged file (hate's format) and the notes added. err when mine
// or theirs isn't a ticket, or they are different tickets; ResolveFile then
// falls back.
func MergeTicket(base, mine, theirs []byte) ([]byte, []string, error) {
	m, err := decodeObject(mine, "this machine's ticket")
	if err != nil {
		return nil, nil, err
	}
	t, err := decodeObject(theirs, "the shared repo's ticket")
	if err != nil {
		return nil, nil, err
	}
	b := optionalBaseObject(base)
	mid, tid := asString(m["id"]), asString(t["id"])
	if mid == "" || mid != tid {
		return nil, nil, fmt.Errorf("the two versions are different tickets (%q, %q)", mid, tid)
	}

	mUpd, tUpd := asString(m["updated_at"]), asString(t["updated_at"])
	mineNewer := mUpd > tUpd
	newer := tUpd
	if mineNewer {
		newer = mUpd
	}
	side := func(fromMine bool) string {
		if fromMine {
			return "this machine"
		}
		return "the shared repo"
	}

	out := object{}
	var notes []string
	addNote := func(n string) { notes = append(notes, n) }
	for _, k := range sortedKeys(b, m, t) {
		bv, mv, tv := field(b, k), field(m, k), field(t, k)
		switch k {
		case "activity":
			continue // below, once the notes are known
		case "updated_at":
			out[k] = newer
		case "time_entries", "test_cases", "attachments":
			var newID func(map[string]bool) string
			switch k {
			case "time_entries":
				newID = newPrefixedID("t")
			case "test_cases":
				newID = newPrefixedID("tc")
			}
			merged, conflicts := mergeKeyed(asList(bv), asList(mv), asList(tv), "id", mineNewer, newID)
			out[k] = merged
			for _, c := range conflicts {
				addNote(fmt.Sprintf("%s %s was changed on both machines; kept %s's version (newer) %s, the other was %s",
					k, c.id, side(c.keptFromMine), show(c.kept, true), show(c.other, true)))
			}
		case "tags", "predecessors":
			out[k] = mergeSet(asList(bv), asList(mv), asList(tv))
		default:
			res, conflict := three(bv, mv, tv)
			if conflict {
				win, lose := tv, mv
				if mineNewer {
					win, lose = mv, tv
				}
				res = win
				addNote(fmt.Sprintf("%s was changed on both machines; kept %s from %s (newer edit), the other value was %s",
					k, show(win.v, win.ok), side(mineNewer), show(lose.v, lose.ok)))
			}
			if res.ok {
				out[k] = res.v
			}
		}
	}

	// Activity: union of both logs (the base entries are in both), in time
	// order, plus one note per field changed on both sides.
	act := []interface{}{}
	for _, l := range [][]interface{}{asList(field(t, "activity")), asList(field(m, "activity"))} {
		for _, a := range l {
			if !containsEqual(act, a) {
				act = append(act, a)
			}
		}
	}
	for _, n := range notes {
		act = append(act, object{"timestamp": newer, "author": SyncAuthor, "action": ticket.ActionSyncMerge, "detail": n})
	}
	sort.SliceStable(act, func(i, j int) bool {
		return asString(keyOfAny(act[i], "timestamp")) < asString(keyOfAny(act[j], "timestamp"))
	})
	out["activity"] = act

	var tk ticket.Ticket
	if err := remarshal(out, &tk); err != nil {
		return nil, nil, fmt.Errorf("merged ticket doesn't fit the ticket format: %v", err)
	}
	data, err := indent(&tk, true)
	if err != nil {
		return nil, nil, err
	}
	return data, notes, nil
}

func keyOfAny(item interface{}, key string) interface{} {
	if o, ok := item.(object); ok {
		return o[key]
	}
	return nil
}
