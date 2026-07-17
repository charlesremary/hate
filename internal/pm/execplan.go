// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"hate/internal/ticket"
)

// RenderExecPlanHTML renders a dependency "execution plan" for the PM dashboard:
// a tree (nested by each ticket's gating predecessor) that shows order and
// parallelism, with the effort-weighted critical path highlighted and a per-wave
// parallel-width summary. Derived purely from `predecessors`, so it works
// pre-baseline. Backlog tickets are excluded.
func RenderExecPlanHTML(tickets []*ticket.Ticket, effortToDays map[string]float64) string {
	byID := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		if ticket.IsBacklog(t) {
			continue
		}
		byID[t.ID] = t
	}

	// Edges limited to existing, non-backlog tickets.
	preds := map[string][]string{}
	succ := map[string][]string{}
	for id, t := range byID {
		for _, p := range t.Predecessors {
			if _, ok := byID[p]; ok {
				preds[id] = append(preds[id], p)
				succ[p] = append(succ[p], id)
			}
		}
	}

	if len(byID) == 0 {
		return execPlanCard(`<p style="padding:4px 0;color:#999;font-size:13px">No tickets to plan.</p>`)
	}

	// Wave = longest dependency chain to this node (topological level).
	wave := map[string]int{}
	var waveOf func(id string, stk map[string]bool) int
	waveOf = func(id string, stk map[string]bool) int {
		if w, ok := wave[id]; ok {
			return w
		}
		if stk[id] {
			return 0 // defensive: cycles shouldn't exist
		}
		stk[id] = true
		best := 0
		for _, p := range preds[id] {
			if l := waveOf(p, stk); l+1 > best {
				best = l + 1
			}
		}
		stk[id] = false
		wave[id] = best
		return best
	}

	// Effort-weighted earliest finish, for the critical path.
	dur := func(id string) float64 {
		t := byID[id]
		if t.Effort == nil {
			return 0
		}
		return effortDaysFor(*t.Effort, effortToDays)
	}
	ef := map[string]float64{}
	var efOf func(id string, stk map[string]bool) float64
	efOf = func(id string, stk map[string]bool) float64 {
		if v, ok := ef[id]; ok {
			return v
		}
		if stk[id] {
			return 0
		}
		stk[id] = true
		best := 0.0
		for _, p := range preds[id] {
			if v := efOf(p, stk); v > best {
				best = v
			}
		}
		stk[id] = false
		ef[id] = best + dur(id)
		return ef[id]
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
		waveOf(id, map[string]bool{})
		efOf(id, map[string]bool{})
	}
	sort.Strings(ids)

	// Critical path: backtrack the single longest effort-weighted chain.
	var endNode string
	maxEF := -1.0
	for _, id := range ids {
		if ef[id] > maxEF {
			maxEF = ef[id]
			endNode = id
		}
	}
	critical := map[string]bool{}
	for cur := endNode; cur != ""; {
		critical[cur] = true
		want := ef[cur] - dur(cur)
		next := ""
		for _, p := range preds[cur] {
			if ef[p] >= want-1e-9 && ef[p] <= want+1e-9 {
				next = p
				break
			}
		}
		cur = next
	}

	// Tree parent = the predecessor with the highest wave (the gating dep).
	primary := map[string]string{}
	for _, id := range ids {
		pl := preds[id]
		if len(pl) == 0 {
			continue
		}
		best := pl[0]
		for _, p := range pl[1:] {
			if wave[p] > wave[best] {
				best = p
			}
		}
		primary[id] = best
	}
	kids := map[string][]string{}
	var roots, isolated []string
	for _, id := range ids {
		if p, ok := primary[id]; ok {
			kids[p] = append(kids[p], id)
			continue
		}
		if len(succ[id]) == 0 { // no predecessors and nothing depends on it
			isolated = append(isolated, id)
		} else {
			roots = append(roots, id)
		}
	}

	sortIDs := func(s []string) {
		sort.SliceStable(s, func(i, j int) bool {
			a, b := s[i], s[j]
			if critical[a] != critical[b] {
				return critical[a] // critical branches first
			}
			if wave[a] != wave[b] {
				return wave[a] < wave[b]
			}
			return a < b
		})
	}
	for k := range kids {
		sortIDs(kids[k])
	}
	sortIDs(roots)

	esc := html.EscapeString
	trunc := func(s string) string {
		if len(s) > 64 {
			return s[:63] + "…"
		}
		return s
	}
	line := func(id string) string {
		t := byID[id]
		star, style := "", "color:#333"
		if critical[id] {
			star, style = "★ ", "color:#dc2626;font-weight:600"
		}
		var sec []string
		for _, p := range preds[id] {
			if p != primary[id] {
				sec = append(sec, p)
			}
		}
		extra := ""
		if len(sec) > 0 {
			extra = fmt.Sprintf(`  <span style="color:#b45309">+needs %s</span>`, esc(strings.Join(sec, ", ")))
		}
		return fmt.Sprintf(`<span style="%s">%s%s</span> %s <span style="color:#bbb">W%d</span>%s`,
			style, star, esc(id), esc(trunc(t.Title)), wave[id], extra)
	}

	// Subtree effort rollup: person-days summed over a branch and its descendants.
	subDays := map[string]float64{}
	var sub func(id string) float64
	sub = func(id string) float64 {
		if v, ok := subDays[id]; ok {
			return v
		}
		total := dur(id)
		for _, c := range kids[id] {
			total += sub(c)
		}
		subDays[id] = total
		return total
	}
	metric := func(days float64, roll bool) string {
		if days <= 0 {
			return ""
		}
		if roll {
			return fmt.Sprintf(` <span style="color:#0d9488;font-weight:600;white-space:nowrap">&Sigma; %.0fh &asymp; %.0fd</span>`, days*HoursPerDay, days)
		}
		return fmt.Sprintf(` <span style="color:#aaa;white-space:nowrap">%.0fh</span>`, days*HoursPerDay)
	}

	// Render as nested <details> so every branch collapses independently.
	var sb strings.Builder
	var render func(id string)
	render = func(id string) {
		ch := kids[id]
		if len(ch) == 0 {
			sb.WriteString(fmt.Sprintf(`<div style="padding:2px 4px 2px 22px">%s%s</div>`, line(id), metric(dur(id), false)))
			return
		}
		sb.WriteString(fmt.Sprintf(`<details style="margin:0"><summary style="cursor:pointer;padding:2px 4px">%s%s</summary><div style="margin-left:16px;border-left:1px solid #eee;padding-left:10px">`, line(id), metric(sub(id), true)))
		for _, c := range ch {
			render(c)
		}
		sb.WriteString(`</div></details>`)
	}
	for _, r := range roots {
		render(r)
	}

	// Per-wave parallel width summary.
	widths := map[int]int{}
	maxWave := 0
	for _, id := range ids {
		widths[wave[id]]++
		if wave[id] > maxWave {
			maxWave = wave[id]
		}
	}
	var wparts []string
	for w := 0; w <= maxWave; w++ {
		wparts = append(wparts, fmt.Sprintf("W%d %d", w, widths[w]))
	}
	critCount := len(critical)
	summary := fmt.Sprintf(
		`<p style="font-size:13px;color:#555;margin:0 0 4px"><strong>Critical path:</strong> `+
			`<span style="color:#dc2626;font-weight:600">%d tickets ★</span> &middot; ~%.0f effort-days (the long pole; everything off it has slack).</p>`+
			`<p style="font-size:12px;color:#888;margin:0 0 10px"><strong>Parallel width by wave</strong> (tickets that can run at once): %s</p>`+
			`<p style="font-size:12px;color:#999;margin:0 0 10px">Siblings in the tree run in parallel; nesting = order. <span style="color:#b45309">+needs</span> flags an extra blocker beyond the tree parent. <strong style="color:#0d9488">&Sigma;</strong> = total effort in that branch (person-days @ 8h/day) &mdash; work, not elapsed time.</p>`,
		critCount, maxEF, esc(strings.Join(wparts, " · ")))

	tree := fmt.Sprintf(`<div style="font-size:13px;line-height:1.5">%s</div>`, sb.String())

	iso := ""
	if len(isolated) > 0 {
		sortIDs(isolated)
		iso = fmt.Sprintf(`<details style="margin-top:10px"><summary style="cursor:pointer;font-size:12px;color:#888">Independent — %d tickets with no dependencies (can run any time)</summary><p style="font-size:12px;color:#777;margin:6px 0 0">%s</p></details>`,
			len(isolated), esc(strings.Join(isolated, ", ")))
	}

	return execPlanCard(summary + tree + iso)
}

func execPlanCard(inner string) string {
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">&#127795; Execution plan &mdash; dependency order &amp; parallelism</h3>
  %s
</div>`, inner)
}
