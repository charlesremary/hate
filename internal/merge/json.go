// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package merge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// Generic JSON building blocks for the three-way merges. Values are decoded
// with UseNumber, so numbers compare and re-encode exactly.

type object = map[string]interface{}

// decode parses one JSON value (and nothing after it).
func decode(data []byte) (interface{}, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v interface{}
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after the JSON value")
	}
	return v, nil
}

func decodeObject(data []byte, what string) (object, error) {
	v, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %v", what, err)
	}
	o, ok := v.(object)
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", what)
	}
	return o, nil
}

func decodeArray(data []byte, what string) ([]interface{}, error) {
	v, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %v", what, err)
	}
	if v == nil {
		return []interface{}{}, nil
	}
	a, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON list", what)
	}
	return a, nil
}

// optionalBase decodes the base version; a missing or unreadable base merges
// as "added on both sides" (empty).
func optionalBaseObject(data []byte) object {
	if data == nil {
		return object{}
	}
	o, err := decodeObject(data, "base")
	if err != nil {
		return object{}
	}
	return o
}

func optionalBaseArray(data []byte) []interface{} {
	if data == nil {
		return nil
	}
	a, err := decodeArray(data, "base")
	if err != nil {
		return nil
	}
	return a
}

func equal(a, b interface{}) bool { return reflect.DeepEqual(a, b) }

// remarshal round-trips a generic value into a typed struct (dst), which
// fixes the field order and drops unknown fields, as hate's own writes do.
func remarshal(v interface{}, dst interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

// indent is hate's on-disk JSON format (two-space indent), with an optional
// trailing newline.
func indent(v interface{}, newline bool) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	if newline {
		data = append(data, '\n')
	}
	return data, nil
}

// show renders a value for a human note: compact JSON, cut at 120 runes.
func show(v interface{}, ok bool) string {
	if !ok {
		return "(none)"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(data)
	if r := []rune(s); len(r) > 120 {
		s = string(r[:117]) + "..."
	}
	return s
}

// opt is a field value that may be absent.
type opt struct {
	v  interface{}
	ok bool
}

func field(o object, k string) opt {
	v, ok := o[k]
	return opt{v, ok}
}

func (a opt) eq(b opt) bool { return a.ok == b.ok && (!a.ok || equal(a.v, b.v)) }

// three is the three-way rule for one value: equal sides, or a side that
// didn't change, decide it; otherwise conflict is true.
func three(base, mine, theirs opt) (res opt, conflict bool) {
	switch {
	case mine.eq(theirs):
		return theirs, false
	case mine.eq(base):
		return theirs, false
	case theirs.eq(base):
		return mine, false
	}
	return opt{}, true
}

// sortedKeys returns the union of the objects' keys, sorted.
func sortedKeys(objs ...object) []string {
	seen := map[string]bool{}
	var keys []string
	for _, o := range objs {
		for k := range o {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func asList(o opt) []interface{} {
	if l, ok := o.v.([]interface{}); ok && o.ok {
		return l
	}
	return nil
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// keyOf returns an item's key (the string value of field key), or "".
func keyOf(item interface{}, key string) string {
	if o, ok := item.(object); ok {
		return asString(o[key])
	}
	return ""
}

// listConflict describes one item changed differently on both sides.
type listConflict struct {
	id           string
	kept, other  interface{}
	keptFromMine bool
}

// mergeKeyed merges lists of objects identified by field key.
//
//   - An item on one side only: kept, unless the other side deleted it
//     unchanged since base (then it stays deleted).
//   - An item on both sides: the three-way rule; changed differently on both,
//     mine wins when mineWins, else theirs (reported as a conflict).
//   - newID, when set, handles an id that is new on BOTH sides with different
//     content (two machines each appended "t3"): those are different items,
//     so mine's is kept under a fresh id from newID(used).
//
// Items without a key are unioned by content. Order: theirs, then mine's
// additions.
func mergeKeyed(base, mine, theirs []interface{}, key string, mineWins bool, newID func(used map[string]bool) string) ([]interface{}, []listConflict) {
	index := func(l []interface{}) map[string]interface{} {
		m := map[string]interface{}{}
		for _, it := range l {
			if k := keyOf(it, key); k != "" {
				m[k] = it
			}
		}
		return m
	}
	bm, mm, tm := index(base), index(mine), index(theirs)
	used := map[string]bool{}
	for _, l := range []map[string]interface{}{bm, mm, tm} {
		for k := range l {
			used[k] = true
		}
	}

	var out []interface{}
	var conflicts []listConflict
	var renumber []interface{}
	for _, t := range theirs {
		k := keyOf(t, key)
		if k == "" {
			out = append(out, t)
			continue
		}
		b, inBase := bm[k]
		m, inMine := mm[k]
		switch {
		case !inMine:
			if inBase && equal(t, b) {
				continue // deleted on this machine, unchanged remotely
			}
			out = append(out, t)
		case equal(m, t):
			out = append(out, t)
		case !inBase && newID != nil:
			out = append(out, t) // both sides added a different item under the same id
			renumber = append(renumber, m)
		default:
			res, conflict := three(opt{b, inBase}, opt{m, true}, opt{t, true})
			if !conflict {
				out = append(out, res.v)
				continue
			}
			if mineWins {
				out = append(out, m)
				conflicts = append(conflicts, listConflict{k, m, t, true})
			} else {
				out = append(out, t)
				conflicts = append(conflicts, listConflict{k, t, m, false})
			}
		}
	}
	for _, m := range mine {
		k := keyOf(m, key)
		if k == "" {
			if !containsEqual(out, m) {
				out = append(out, m)
			}
			continue
		}
		if _, inTheirs := tm[k]; inTheirs {
			continue
		}
		if b, inBase := bm[k]; inBase && equal(m, b) {
			continue // deleted remotely, unchanged here
		}
		out = append(out, m)
	}
	for _, m := range renumber {
		id := newID(used)
		used[id] = true
		c := copyObject(m.(object))
		c[key] = id
		out = append(out, c)
	}
	if out == nil {
		out = []interface{}{}
	}
	return out, conflicts
}

// mergeSet merges lists of scalars as sets: an element removed on either side
// (present in base) is removed; an element added on either side is added.
// Order: theirs, then mine's additions.
func mergeSet(base, mine, theirs []interface{}) []interface{} {
	out := []interface{}{}
	for _, t := range theirs {
		if containsEqual(base, t) && !containsEqual(mine, t) {
			continue // removed on this machine
		}
		if !containsEqual(out, t) {
			out = append(out, t)
		}
	}
	for _, m := range mine {
		if containsEqual(theirs, m) || containsEqual(base, m) {
			continue // already in, or removed remotely
		}
		if !containsEqual(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// newPrefixedID returns a newID function giving "<prefix><max+1>" over the
// used ids with that prefix (t1, t2, ... / tc1, tc2, ...).
func newPrefixedID(prefix string) func(map[string]bool) string {
	return func(used map[string]bool) string {
		max := 0
		for id := range used {
			if strings.HasPrefix(id, prefix) {
				var n int
				if _, err := fmt.Sscanf(id[len(prefix):], "%d", &n); err == nil && fmt.Sprintf("%s%d", prefix, n) == id && n > max {
					max = n
				}
			}
		}
		return fmt.Sprintf("%s%d", prefix, max+1)
	}
}

func containsEqual(list []interface{}, v interface{}) bool {
	for _, x := range list {
		if equal(x, v) {
			return true
		}
	}
	return false
}

func copyObject(o object) object {
	c := make(object, len(o))
	for k, v := range o {
		c[k] = v
	}
	return c
}
