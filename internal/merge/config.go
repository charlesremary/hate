// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package merge

import (
	"fmt"

	"hate/internal/ticket"
)

// configKeyedLists are the config lists merged item by item, by key.
var configKeyedLists = map[string]string{
	"resources":    "email",
	"contacts":     "id",
	"links":        "id",
	"instructions": "id",
}

// configSets are the config lists merged as sets.
var configSets = map[string]bool{
	"repos":                 true,
	"estimate_ref_projects": true,
}

// configMaps are the config objects merged key by key.
var configMaps = map[string]bool{
	"effort_to_days": true,
}

// MergeConfig three-way merges .tkt/config.json: resources by email,
// contacts / links / instructions by id, repos and estimate_ref_projects as
// sets, effort_to_days key by key, every other field by the three-way rule.
// A field (or list item) changed on both sides keeps the remote value: config
// has no updated_at to tell which edit is newer. Each such case is returned as
// a note. err when either side isn't a config object.
func MergeConfig(base, mine, theirs []byte) ([]byte, []string, error) {
	m, err := decodeObject(mine, "this machine's config")
	if err != nil {
		return nil, nil, err
	}
	t, err := decodeObject(theirs, "the shared repo's config")
	if err != nil {
		return nil, nil, err
	}
	b := optionalBaseObject(base)

	out := object{}
	var notes []string
	for _, k := range sortedKeys(b, m, t) {
		bv, mv, tv := field(b, k), field(m, k), field(t, k)
		switch {
		case configKeyedLists[k] != "":
			merged, conflicts := mergeKeyed(asList(bv), asList(mv), asList(tv), configKeyedLists[k], false, nil)
			if len(merged) > 0 || mv.ok || tv.ok {
				out[k] = merged
			}
			for _, c := range conflicts {
				notes = append(notes, fmt.Sprintf("%s %s was changed on both machines; kept the shared repo's version %s, this machine had %s",
					k, c.id, show(c.kept, true), show(c.other, true)))
			}
		case configSets[k]:
			merged := mergeSet(asList(bv), asList(mv), asList(tv))
			if len(merged) > 0 || mv.ok || tv.ok {
				out[k] = merged
			}
		case configMaps[k]:
			bo, _ := bv.v.(object)
			mo, _ := mv.v.(object)
			to, _ := tv.v.(object)
			merged := object{}
			for _, kk := range sortedKeys(bo, mo, to) {
				res, conflict := three(field(bo, kk), field(mo, kk), field(to, kk))
				if conflict {
					res = field(to, kk)
					notes = append(notes, fmt.Sprintf("%s.%s was changed on both machines; kept the shared repo's %s, this machine had %s",
						k, kk, show(res.v, res.ok), show(field(mo, kk).v, field(mo, kk).ok)))
				}
				if res.ok {
					merged[kk] = res.v
				}
			}
			if len(merged) > 0 || mv.ok || tv.ok {
				out[k] = merged
			}
		default:
			res, conflict := three(bv, mv, tv)
			if conflict {
				res = tv
				notes = append(notes, fmt.Sprintf("%s was changed on both machines; kept the shared repo's %s, this machine had %s",
					k, show(tv.v, tv.ok), show(mv.v, mv.ok)))
			}
			if res.ok {
				out[k] = res.v
			}
		}
	}

	var cfg ticket.ProjectConfig
	if err := remarshal(out, &cfg); err != nil {
		return nil, nil, fmt.Errorf("merged config doesn't fit the config format: %v", err)
	}
	data, err := indent(&cfg, true)
	if err != nil {
		return nil, nil, err
	}
	return data, notes, nil
}
