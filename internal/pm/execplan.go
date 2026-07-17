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

// RenderExecPlanHTML renders a dependency "execution plan" for the PM dashboard,
// grouped into parallel stages. A stage (topological wave) is a batch of tickets
// whose blockers are all satisfied, so they can run at once; a stage opens once
// the previous one's work is done. Each stage is a collapsible row whose header
// carries the stage's ticket count, total effort (Σ), and an effort bar; the
// effort-weighted critical path is flagged. Derived purely from `predecessors`,
// so it works pre-baseline. Backlog tickets and feature parents are excluded.
func RenderExecPlanHTML(tickets []*ticket.Ticket, effortToDays map[string]float64) string {
	byID := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		if ticket.IsBacklog(t) {
			continue
		}
		byID[t.ID] = t
	}

	preds := map[string][]string{}
	for id, t := range byID {
		for _, p := range t.Predecessors {
			if _, ok := byID[p]; ok {
				preds[id] = append(preds[id], p)
			}
		}
	}

	if len(byID) == 0 {
		return execPlanCard(`<p style="padding:4px 0;color:#999;font-size:13px">No tickets to plan.</p>`)
	}

	// Wave = longest dependency chain to this node (its stage, 0-based).
	wave := map[string]int{}
	var waveOf func(id string, stk map[string]bool) int
	waveOf = func(id string, stk map[string]bool) int {
		if w, ok := wave[id]; ok {
			return w
		}
		if stk[id] {
			return 0
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

	// Effort (person-days) per ticket, and effort-weighted earliest finish for
	// the critical path.
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

	esc := html.EscapeString
	trunc := func(s string) string {
		if len(s) > 66 {
			return s[:65] + "…"
		}
		return s
	}

	// Feature parents (containers) aren't schedulable work — keep them out.
	isParent := map[string]bool{}
	for _, t := range byID {
		for _, tag := range t.Tags {
			if strings.HasPrefix(tag, "parent:") {
				isParent[strings.TrimPrefix(tag, "parent:")] = true
			}
		}
	}

	// Group schedulable tickets by stage, tallying per-stage effort.
	stageIDs := map[int][]string{}
	stageDays := map[int]float64{}
	maxWave := 0
	maxStageDays := 0.0
	for _, id := range ids {
		if isParent[id] {
			continue
		}
		w := wave[id]
		stageIDs[w] = append(stageIDs[w], id)
		stageDays[w] += dur(id)
		if w > maxWave {
			maxWave = w
		}
	}
	for _, d := range stageDays {
		if d > maxStageDays {
			maxStageDays = d
		}
	}
	for w := range stageIDs {
		s := stageIDs[w]
		sort.SliceStable(s, func(i, j int) bool {
			a, b := s[i], s[j]
			if critical[a] != critical[b] {
				return critical[a] // critical first
			}
			return a < b
		})
	}

	// One ticket row inside a stage.
	row := func(id string) string {
		t := byID[id]
		star, style := "", "color:#333"
		if critical[id] {
			star, style = "★ ", "color:#dc2626;font-weight:600"
		}
		var needs []string
		for _, p := range preds[id] {
			if !isParent[p] {
				needs = append(needs, fmt.Sprintf("%s (stage %d)", p, wave[p]+1))
			}
		}
		nstr := ""
		if len(needs) > 0 {
			nstr = fmt.Sprintf(` <span style="color:#b45309;font-size:11px">needs %s</span>`, esc(strings.Join(needs, ", ")))
		}
		eff := ""
		if d := dur(id); d > 0 {
			eff = fmt.Sprintf(` <span style="color:#aaa;font-size:11px">%.0fh</span>`, d*HoursPerDay)
		}
		return fmt.Sprintf(`<div style="padding:3px 4px 3px 24px;font-size:13px"><span style="%s">%s%s</span> %s%s%s</div>`,
			style, star, esc(id), esc(trunc(t.Title)), eff, nstr)
	}

	var sb strings.Builder
	for w := 0; w <= maxWave; w++ {
		list := stageIDs[w]
		if len(list) == 0 {
			continue
		}
		barPct := 0.0
		if maxStageDays > 0 {
			barPct = stageDays[w] / maxStageDays * 100
		}
		note := ""
		if w == 0 {
			note = ` &middot; <span style="color:#16a34a">can start now</span>`
		}
		crit := 0
		var body strings.Builder
		for _, id := range list {
			if critical[id] {
				crit++
			}
			body.WriteString(row(id))
		}
		critNote := ""
		if crit > 0 {
			critNote = fmt.Sprintf(` &middot; <span style="color:#dc2626">%d on critical path ★</span>`, crit)
		}
		count := "<strong>1</strong> ticket"
		if len(list) > 1 {
			count = fmt.Sprintf("<strong>%d</strong> tickets, independent (can run at once)", len(list))
		}
		header := fmt.Sprintf(
			`<div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">`+
				`<div style="width:62px;font-weight:600;color:#334155">Stage %d</div>`+
				`<div style="flex:1;min-width:90px;max-width:230px;background:#f1f5f9;border-radius:3px"><div style="height:14px;width:%.1f%%;min-width:3px;background:#3b82f6;border-radius:3px"></div></div>`+
				`<div style="color:#334155;font-size:12.5px">%s &middot; <span style="color:#0d9488;font-weight:600">&Sigma; %.0fh &asymp; %.0fd</span>%s%s</div>`+
				`</div>`,
			w+1, barPct, count, stageDays[w]*HoursPerDay, stageDays[w], note, critNote)
		sb.WriteString(fmt.Sprintf(`<details style="margin:0;border-bottom:1px solid #f1f5f9"><summary style="cursor:pointer;padding:7px 4px">%s</summary><div style="padding:2px 0 10px">%s</div></details>`,
			header, body.String()))
	}

	critCount := len(critical)
	head := fmt.Sprintf(
		`<p style="font-size:13px;color:#555;margin:0 0 8px"><strong>Critical path:</strong> `+
			`<span style="color:#dc2626;font-weight:600">%d tickets ★</span> &middot; ~%.0f effort-days &mdash; the longest dependency chain (the fastest this could finish even with unlimited people).</p>`+
			`<p style="font-size:12px;color:#555;margin:0 0 12px"><strong>A stage groups tickets that don't depend on each other</strong>, so a stage's tickets can run at the same time. A ticket's stage number is how deep its longest chain of prerequisites is &mdash; it can't start until the earlier stages it <span style="color:#b45309">needs</span> are done. Bar length &amp; <span style="color:#0d9488">&Sigma;</span> = the stage's total effort (person-days @ 8h/day). Expand a stage for its tickets; <span style="color:#dc2626">★</span> = critical path.</p>`,
		critCount, maxEF)

	return execPlanCard(head + sb.String())
}

func execPlanCard(inner string) string {
	return fmt.Sprintf(`
<div style="margin:0 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:18px 22px">
  <h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin-bottom:12px">&#127795; Execution plan &mdash; parallel stages</h3>
  %s
</div>`, inner)
}
