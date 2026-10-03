// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

// The Gantt panel renders the schedule as time-scaled bars in groups. When the
// project plans in blocks (block_weeks set) the rows are grouped by the block
// each bar starts in and the blocks are drawn as faint labelled bands behind
// the bars; otherwise the rows are grouped by person, matching the capacity
// schedule's lanes (one person works their tickets in order, so a group reads
// top to bottom). Dependency stages are not drawn here: they ignore people and
// time (see stages.go). It is a read-only, self-contained view (inline SVG, no
// external assets). Rescheduling happens by editing tickets, not here.

// ganttRow is a single task with its dates parsed and laid out.
type ganttRow struct {
	task         *SnapshotTask
	plannedStart time.Time
	plannedEnd   time.Time
	actualStart  time.Time
	barEnd       time.Time // actual/projected end when known, else planned end
	milestone    bool
	critical     bool
	phase        string // project phase, for within-group ordering
	group        int    // index into the groups returned by ganttData
	y            int
}

// ganttGroup is one row group: a block or a person.
type ganttGroup struct {
	label string
	count int // tasks
	days  int // Σ planned days
}

const (
	gGutter     = 280 // left label column
	gTopAxis    = 48  // axis header height
	gRowH       = 26
	gBarH       = 13
	gGroupH     = 26 // group-header band height
	gPadDays    = 2
	gRightPad   = 40
	gBottomPad  = 28
	gLabelChars = 34 // label truncation
)

// daysBetween returns whole days from a to b (b−a), truncating.
func daysBetween(a, b time.Time) int {
	return int(b.Sub(a).Hours() / 24)
}

// ganttData parses the snapshot's tasks into rows, groups them (by block when
// blocks are given, else by person) and returns the chart's date window. Rows
// are ordered by group, then planned start, then phase, then title. Tasks with
// no valid planned start are skipped (nothing to place).
func ganttData(snapshot *Snapshot, blocks []Block) (rows []ganttRow, groups []ganttGroup, chartStart, chartEnd time.Time) {
	cp := map[string]bool{}
	for _, id := range snapshot.CriticalPathIDs {
		cp[id] = true
	}
	for i := range snapshot.Tasks {
		t := &snapshot.Tasks[i]
		ps := parseDate(t.Baseline.PlannedStart)
		if ps.IsZero() {
			continue
		}
		pe := parseDate(t.Baseline.PlannedEnd)
		if pe.IsZero() || pe.Before(ps) {
			pe = ps
		}
		row := ganttRow{
			task: t, plannedStart: ps, plannedEnd: pe,
			barEnd: pe, milestone: t.IsMilestone, critical: cp[t.TaskID],
			phase: t.Phase,
		}
		if t.Current.ActualStart != nil {
			row.actualStart = parseDate(*t.Current.ActualStart)
		}
		if t.Current.ProjectedEnd != nil {
			if d := parseDate(*t.Current.ProjectedEnd); !d.IsZero() {
				row.barEnd = d
			}
		} else if t.Current.ActualEnd != nil {
			if d := parseDate(*t.Current.ActualEnd); !d.IsZero() {
				row.barEnd = d
			}
		}
		rows = append(rows, row)
	}

	// Group key per row: an order key and a label.
	type gkey struct {
		rank  int
		label string
	}
	keyOf := func(r ganttRow) gkey {
		if len(blocks) > 0 {
			i := blockIndexAt(blocks, r.plannedStart)
			switch {
			case i < 0:
				return gkey{-1, "Before " + blockShortLabel(blocks[0])}
			case i >= len(blocks):
				return gkey{len(blocks), "After " + blockShortLabel(blocks[len(blocks)-1])}
			}
			return gkey{i, blocks[i].Label}
		}
		if r.task.Lane != "" {
			return gkey{r.task.LaneRank, r.task.Lane}
		}
		// A baselined snapshot: group by owner.
		if o := ownerShort(r.task.Owner); o != "" {
			return gkey{0, o}
		}
		return gkey{1, UnassignedLane}
	}
	keys := map[gkey]bool{}
	rowKey := make([]gkey, len(rows))
	for i := range rows {
		rowKey[i] = keyOf(rows[i])
		keys[rowKey[i]] = true
	}
	ordered := make([]gkey, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].rank != ordered[j].rank {
			return ordered[i].rank < ordered[j].rank
		}
		return ordered[i].label < ordered[j].label
	})
	idx := map[gkey]int{}
	for i, k := range ordered {
		idx[k] = i
		groups = append(groups, ganttGroup{label: k.label})
	}
	for i := range rows {
		g := idx[rowKey[i]]
		rows[i].group = g
		groups[g].count++
		groups[g].days += rows[i].task.Baseline.PlannedDays
	}

	phaseKey := func(p string) string {
		if p == "" {
			return "~" // sort unphased last
		}
		return p
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].group != rows[j].group {
			return rows[i].group < rows[j].group
		}
		if !rows[i].plannedStart.Equal(rows[j].plannedStart) {
			return rows[i].plannedStart.Before(rows[j].plannedStart)
		}
		if pi, pj := phaseKey(rows[i].phase), phaseKey(rows[j].phase); pi != pj {
			return pi < pj
		}
		return rows[i].task.Title < rows[j].task.Title
	})
	for i := range rows {
		if chartStart.IsZero() || rows[i].plannedStart.Before(chartStart) {
			chartStart = rows[i].plannedStart
		}
		end := rows[i].plannedEnd
		if rows[i].barEnd.After(end) {
			end = rows[i].barEnd
		}
		if chartEnd.IsZero() || end.After(chartEnd) {
			chartEnd = end
		}
	}
	if !chartStart.IsZero() {
		chartStart = chartStart.AddDate(0, 0, -gPadDays)
		chartEnd = chartEnd.AddDate(0, 0, gPadDays)
	}
	return rows, groups, chartStart, chartEnd
}

// blockShortLabel is "Block 01" (the label without its dates).
func blockShortLabel(b Block) string {
	return fmt.Sprintf("Block %02d", b.N)
}

// blockBand is a block's span clipped to the chart window: from its Monday to
// the Monday after its last Friday.
type blockBand struct {
	block      Block
	start, end time.Time // end exclusive
	odd        bool
}

// visibleBands returns the blocks overlapping the chart window.
func visibleBands(blocks []Block, chartStart, chartEnd time.Time) []blockBand {
	var out []blockBand
	for i, b := range blocks {
		s, e := parseDate(b.Start), parseDate(b.End).AddDate(0, 0, 3)
		if !e.After(chartStart) || s.After(chartEnd) {
			continue
		}
		if s.Before(chartStart) {
			s = chartStart
		}
		if e.After(chartEnd.AddDate(0, 0, 1)) {
			e = chartEnd.AddDate(0, 0, 1)
		}
		out = append(out, blockBand{block: b, start: s, end: e, odd: i%2 == 1})
	}
	return out
}

// pxPerDay scales the timeline: readable for short projects, compressed (but
// never illegible) for long ones. Chart target width ~1600px.
func ganttPxPerDay(totalDays int) float64 {
	if totalDays <= 0 {
		return 22
	}
	p := 1600.0 / float64(totalDays)
	if p > 22 {
		p = 22
	}
	if p < 6 {
		p = 6
	}
	return p
}

// ganttBarColor returns (fill, stroke) for a task bar by status; critical-path
// tasks get a red stroke to stand out.
func ganttBarColor(status string, critical bool) (string, string) {
	fill := "#9ca3af" // not started / unknown
	switch status {
	case "complete":
		fill = "#22c55e"
	case "in_progress":
		fill = "#3b82f6"
	case "blocked":
		fill = "#ef4444"
	}
	stroke := fill
	if critical {
		stroke = "#dc2626"
	}
	return fill, stroke
}

// ganttSVG builds the grouped, time-scaled SVG for a set of rows (already
// grouped by ganttData), with block bands behind the bars when blocks are
// given. Returns "" when there are no rows.
func ganttSVG(rows []ganttRow, groups []ganttGroup, blocks []Block, chartStart, chartEnd time.Time, snapshotDate string) string {
	if len(rows) == 0 {
		return ""
	}
	totalDays := daysBetween(chartStart, chartEnd) + 1
	ppd := ganttPxPerDay(totalDays)
	chartW := int(float64(totalDays)*ppd) + 1

	// Lay out rows with a group-header band whenever the group changes.
	type groupHdr struct {
		group, y int
	}
	var headers []groupHdr
	y := gTopAxis
	cur := -1
	for i := range rows {
		if rows[i].group != cur {
			headers = append(headers, groupHdr{rows[i].group, y})
			y += gGroupH
			cur = rows[i].group
		}
		rows[i].y = y
		y += gRowH
	}
	svgW := gGutter + chartW + gRightPad
	svgH := y + gBottomPad
	axisBottom := svgH - gBottomPad

	x := func(d time.Time) int {
		return gGutter + int(float64(daysBetween(chartStart, d))*ppd)
	}
	posByID := map[string]ganttRow{}
	for _, r := range rows {
		posByID[r.task.TaskID] = r
	}

	esc := html.EscapeString
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<svg viewBox="0 0 %d %d" width="%d" height="%d" xmlns="http://www.w3.org/2000/svg" class="gantt-svg">`, svgW, svgH, svgW, svgH))
	sb.WriteString(`<defs>
      <marker id="g-arrow" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto"><polygon points="0 0, 8 3, 0 6" fill="#9ca3af"/></marker>
      <marker id="g-arrow-cp" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto"><polygon points="0 0, 8 3, 0 6" fill="#dc2626"/></marker>
    </defs>`)

	// --- Axis: month gridlines + labels, plus a "today" marker. ---
	m := time.Date(chartStart.Year(), chartStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !m.After(chartEnd) {
		gx := x(m)
		if gx >= gGutter {
			sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#e5e7eb" stroke-width="1"/>`, gx, gTopAxis-20, gx, axisBottom))
			sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="11" fill="#6b7280" font-family="sans-serif">%s</text>`, gx+4, gTopAxis-24, esc(m.Format("Jan 2006"))))
		}
		m = m.AddDate(0, 1, 0)
	}
	today := parseDate(snapshotDate)
	if today.IsZero() {
		today = chartStart
	}
	if !today.Before(chartStart) && !today.After(chartEnd) {
		tx := x(today)
		sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#f59e0b" stroke-width="1.5" stroke-dasharray="3 3"/>`, tx, gTopAxis-20, tx, axisBottom))
		sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="10" fill="#f59e0b" font-family="sans-serif">today</text>`, tx+3, axisBottom+12))
	}
	sb.WriteString(fmt.Sprintf(`<line x1="0" y1="%d" x2="%d" y2="%d" stroke="#d1d5db" stroke-width="1"/>`, gTopAxis-2, svgW, gTopAxis-2))
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="0" x2="%d" y2="%d" stroke="#d1d5db" stroke-width="1"/>`, gGutter, gGutter, axisBottom))

	// --- Zebra row backgrounds (drawn first, so connectors/bars sit on top). ---
	for i := range rows {
		if i%2 == 1 {
			sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="#f9fafb"/>`, gGutter, rows[i].y, chartW, gRowH))
		}
	}

	// --- Group header bands. ---
	for _, h := range headers {
		g := groups[h.group]
		sb.WriteString(fmt.Sprintf(`<rect x="0" y="%d" width="%d" height="%d" fill="#eef2f7"/>`, h.y, svgW, gGroupH))
		sb.WriteString(fmt.Sprintf(`<text x="10" y="%d" font-size="12" font-weight="700" fill="#334155" font-family="sans-serif" class="gantt-group">%s<tspan font-size="11" font-weight="400" fill="#64748b"> &middot; %d task%s &middot; &Sigma; %dd</tspan></text>`,
			h.y+17, esc(g.label), g.count, plif(g.count), g.days))
	}

	// --- Block bands: faint alternating columns with a boundary line and a
	// label in the axis header (drawn over the zebra, under the bars). ---
	for _, b := range visibleBands(blocks, chartStart, chartEnd) {
		bx, bw := x(b.start), x(b.end)-x(b.start)
		if bw < 1 {
			continue
		}
		if b.odd {
			sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="#7c3aed" opacity="0.06" class="gantt-block-band"/>`, bx, gTopAxis-2, bw, axisBottom-gTopAxis+2))
		} else {
			sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="#7c3aed" opacity="0.02" class="gantt-block-band"/>`, bx, gTopAxis-2, bw, axisBottom-gTopAxis+2))
		}
		sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#a78bfa" stroke-width="1" stroke-dasharray="2 3"/>`, bx, gTopAxis-12, bx, axisBottom))
		lbl := b.block.Label
		if float64(bw) < float64(len(lbl))*5.6 {
			lbl = blockShortLabel(b.block)
		}
		sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="10" fill="#6d28d9" font-family="sans-serif" class="gantt-block-label">%s</text>`, bx+3, gTopAxis-6, esc(lbl)))
	}

	// --- Dependency connectors (finish-to-start): pred end → succ start. ---
	for _, r := range rows {
		succCX := x(r.plannedStart)
		succCY := r.y + gRowH/2
		for _, dep := range r.task.Dependencies {
			p, ok := posByID[dep]
			if !ok {
				continue
			}
			predX := x(p.plannedEnd) + int(ppd)
			predCY := p.y + gRowH/2
			isCP := r.critical && p.critical
			color, marker, opacity, wdt := "#9ca3af", "g-arrow", "0.4", "1"
			if isCP {
				color, marker, opacity, wdt = "#dc2626", "g-arrow-cp", "0.9", "1.5"
			}
			midX := predX + 10
			sb.WriteString(fmt.Sprintf(
				`<path d="M %d %d H %d V %d H %d" fill="none" stroke="%s" stroke-width="%s" opacity="%s" marker-end="url(#%s)"/>`,
				predX, predCY, midX, succCY, succCX, color, wdt, opacity, marker))
		}
	}

	// --- Rows: left label + bar(s) / milestone. ---
	for i := range rows {
		r := rows[i]
		yc := r.y + gRowH/2
		title := runeTruncate(r.task.Title, gLabelChars)
		labelColor := "#111827"
		if r.critical {
			labelColor = "#b91c1c"
		}
		owner := ownerShort(r.task.Owner)
		suffix := ""
		if owner != "" {
			suffix = "  " + owner
		}
		sb.WriteString(fmt.Sprintf(`<text x="10" y="%d" font-size="11.5" font-family="sans-serif" fill="%s"><tspan font-weight="600">%s</tspan> %s<tspan fill="#9ca3af"> %s</tspan></text>`,
			yc+4, labelColor, esc(r.task.TaskID), esc(title), esc(suffix)))

		if r.milestone {
			cx := x(r.plannedStart)
			s := 6
			sb.WriteString(fmt.Sprintf(`<path d="M %d %d L %d %d L %d %d L %d %d Z" fill="#7c3aed" stroke="%s" stroke-width="1"/>`,
				cx, yc-s, cx+s, yc, cx, yc+s, cx-s, yc, boolStr(r.critical, "#dc2626", "#5b21b6")))
			continue
		}

		fill, stroke := ganttBarColor(r.task.Status, r.critical)
		by := yc - gBarH/2
		px0 := x(r.plannedStart)
		pw := x(r.plannedEnd) + int(ppd) - px0
		if pw < 3 {
			pw = 3
		}
		sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" rx="3" fill="#e5e7eb"/>`, px0, by, pw, gBarH))
		start := r.plannedStart
		if !r.actualStart.IsZero() {
			start = r.actualStart
		}
		ax0 := x(start)
		aw := x(r.barEnd) + int(ppd) - ax0
		if aw < 3 {
			aw = 3
		}
		sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" rx="3" fill="%s" stroke="%s" stroke-width="%s"/>`,
			ax0, by, aw, gBarH, fill, stroke, boolStr(r.critical, "1.5", "1")))
		if r.barEnd.After(r.plannedEnd) {
			sx0 := x(r.plannedEnd) + int(ppd)
			sw := x(r.barEnd) + int(ppd) - sx0
			if sw > 0 {
				sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" rx="2" fill="#ef4444" opacity="0.35"/>`, sx0, by, sw, gBarH))
			}
			sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="10" fill="#dc2626" font-family="sans-serif">+%dd</text>`,
				x(r.barEnd)+int(ppd)+3, yc+3, daysBetween(r.plannedEnd, r.barEnd)))
		}
	}

	sb.WriteString(`</svg>`)
	return sb.String()
}

// criticalOnly returns a snapshot filtered to the critical-path tasks (and the
// dependencies among them) — the source for the critical-path-only view.
func criticalOnly(s *Snapshot) *Snapshot {
	cp := map[string]bool{}
	for _, id := range s.CriticalPathIDs {
		cp[id] = true
	}
	out := &Snapshot{ProjectID: s.ProjectID, ProjectName: s.ProjectName, SnapshotDate: s.SnapshotDate, CriticalPathIDs: s.CriticalPathIDs}
	for _, t := range s.Tasks {
		if !cp[t.TaskID] {
			continue
		}
		tt := t
		var deps []string
		for _, d := range t.Dependencies {
			if cp[d] {
				deps = append(deps, d)
			}
		}
		tt.Dependencies = deps
		out.Tasks = append(out.Tasks, tt)
	}
	return out
}

// renderGanttPanel renders the Gantt tab body: a Full plan / Critical path
// toggle over two SVGs, the draw.io export button, and a legend.
func renderGanttPanel(snapshot *Snapshot, blocks []Block, note, exportURL string) string {
	rows, groups, cs, ce := ganttData(snapshot, blocks)
	if len(rows) == 0 {
		return `<div style="padding:24px;color:#9ca3af">No scheduled tasks to chart yet — add estimates (and dependencies) to your tickets.</div>`
	}
	full := ganttSVG(rows, groups, blocks, cs, ce, snapshot.SnapshotDate)
	cpRows, cpGroups, ccs, cce := ganttData(criticalOnly(snapshot), blocks)
	cp := ganttSVG(cpRows, cpGroups, blocks, ccs, cce, snapshot.SnapshotDate)
	grouping := `<span>Rows grouped by person: each works their tickets in order, top to bottom</span>`
	if len(blocks) > 0 {
		grouping = `<span><span style="display:inline-block;width:22px;height:8px;background:#7c3aed;opacity:.15;border-radius:2px;vertical-align:middle"></span> planning block (rows grouped by the block they start in)</span>`
	}
	if cp == "" {
		cp = `<div style="padding:24px;color:#9ca3af">No critical path identified for this schedule.</div>`
	}
	legend := `<div style="display:flex;gap:16px;flex-wrap:wrap;font-size:12px;color:#6b7280;margin:10px 0 0">
      <span><span style="display:inline-block;width:22px;height:8px;background:#e5e7eb;border-radius:2px;vertical-align:middle"></span> planned</span>
      <span><span style="display:inline-block;width:22px;height:8px;background:#3b82f6;border-radius:2px;vertical-align:middle"></span> actual / projected</span>
      <span><span style="display:inline-block;width:22px;height:8px;background:#ef4444;opacity:.35;border-radius:2px;vertical-align:middle"></span> slip</span>
      <span><span style="color:#dc2626">&#9644;</span> critical path</span>
      <span><span style="color:#7c3aed">&#9670;</span> milestone</span>
      %s
    </div>`
	legend = fmt.Sprintf(legend, grouping)
	const btn = `border:none;padding:6px 13px;font-size:13px;cursor:pointer;`
	return fmt.Sprintf(`
<div style="padding:16px 24px 8px">
  <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;gap:12px;flex-wrap:wrap">
    <div style="font-size:13px;color:#6b7280">%s</div>
    <div style="display:flex;gap:8px;align-items:center">
      <div style="display:inline-flex;border:1px solid #d1d5db;border-radius:6px;overflow:hidden">
        <button type="button" onclick="ganttView(this,'full')" style="%sbackground:#1976d2;color:#fff">Full plan</button>
        <button type="button" onclick="ganttView(this,'cp')" style="%sbackground:#fff;color:#374151;border-left:1px solid #d1d5db">Critical path</button>
      </div>
      <a href="%s" download="gantt.drawio" style="text-decoration:none;background:#1976d2;color:#fff;padding:7px 14px;border-radius:6px;font-size:13px;white-space:nowrap">Export to draw.io</a>
    </div>
  </div>
  <div id="gantt-full" style="overflow-x:auto;border:1px solid #e5e7eb;border-radius:8px;background:#fff">%s</div>
  <div id="gantt-cp" style="display:none;overflow-x:auto;border:1px solid #e5e7eb;border-radius:8px;background:#fff">%s</div>
  %s
  <script>
  function ganttView(btn, which){
    document.getElementById('gantt-full').style.display = which==='full'?'block':'none';
    document.getElementById('gantt-cp').style.display = which==='cp'?'block':'none';
    var bs = btn.parentNode.querySelectorAll('button');
    for (var i=0;i<bs.length;i++){ var on = bs[i]===btn; bs[i].style.background = on?'#1976d2':'#fff'; bs[i].style.color = on?'#fff':'#374151'; }
  }
  </script>
</div>`, html.EscapeString(note), btn, btn, exportURL, full, cp, legend)
}

// runeTruncate shortens s to n runes with an ellipsis.
func runeTruncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ownerShort returns the local part of an email owner (before @), or the owner.
func ownerShort(owner string) string {
	if i := strings.Index(owner, "@"); i >= 0 {
		return owner[:i]
	}
	return owner
}

func boolStr(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func plif(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------------------------------------------------------------------------
// draw.io export
// ---------------------------------------------------------------------------

// RenderGanttDrawio renders the Gantt as an uncompressed draw.io (mxGraph) file:
// task rows grouped by block (with block bands and labels) or by person, with
// truncated labels in a left column, time-scaled planned bars (critical-path
// red stroke), milestone diamonds, month gridlines, a today line, and
// finish-to-start dependency edges. draw.io reads this XML directly (no
// deflate needed). blocks may be empty (no planning blocks).
func RenderGanttDrawio(snapshot *Snapshot, blocks []Block) string {
	return `<mxfile host="hate" type="device">` +
		ganttDrawioDiagram(snapshot.ProjectName+" — Full plan", snapshot, blocks) +
		ganttDrawioDiagram("Critical path", criticalOnly(snapshot), blocks) +
		`</mxfile>`
}

// ganttDrawioDiagram builds one <diagram> (a draw.io tab) for the given
// snapshot's rows.
func ganttDrawioDiagram(name string, snapshot *Snapshot, blocks []Block) string {
	const (
		gut  = 340
		rowH = 24
		barH = 13
	)
	top := 40
	if len(blocks) > 0 {
		top = 56 // room for the block labels above the month labels
	}
	rows, groups, chartStart, chartEnd := ganttData(snapshot, blocks)
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<diagram name="%s"><mxGraphModel dx="800" dy="600" grid="1" gridSize="10" guides="1" tooltips="1" connect="1" arrows="1" fold="1" page="1" pageScale="1" pageWidth="1600" pageHeight="1100" math="0" shadow="0"><root><mxCell id="0"/><mxCell id="1" parent="0"/>`,
		esc(name)))

	if len(rows) == 0 {
		b.WriteString(`<mxCell id="empty" value="No tasks." style="text;html=1;align=left;" vertex="1" parent="1"><mxGeometry x="20" y="20" width="400" height="24" as="geometry"/></mxCell></root></mxGraphModel></diagram>`)
		return b.String()
	}

	totalDays := daysBetween(chartStart, chartEnd) + 1
	ppd := ganttPxPerDay(totalDays)
	x := func(d time.Time) int { return gut + int(float64(daysBetween(chartStart, d))*ppd) }

	// Lay out rows with a group-header band whenever the group changes.
	rowY := make([]int, len(rows))
	type groupHdr struct{ group, y int }
	var headers []groupHdr
	y := top
	cur := -1
	for i := range rows {
		if rows[i].group != cur {
			headers = append(headers, groupHdr{rows[i].group, y})
			y += 22
			cur = rows[i].group
		}
		rowY[i] = y
		y += rowH
	}
	chartW := x(chartEnd) + int(ppd) + 40

	// Block bands first, so everything else draws over them.
	for i, bb := range visibleBands(blocks, chartStart, chartEnd) {
		bx, bw := x(bb.start), x(bb.end)-x(bb.start)
		if bw < 1 {
			continue
		}
		opacity := 4
		if bb.odd {
			opacity = 10
		}
		b.WriteString(fmt.Sprintf(`<mxCell id="block%d" value="" style="rounded=0;fillColor=#7c3aed;opacity=%d;strokeColor=#a78bfa;dashed=1;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
			i, opacity, bx, top, bw, y-top))
		b.WriteString(fmt.Sprintf(`<mxCell id="blocklbl%d" value="%s" style="text;html=1;align=left;fontSize=10;fontColor=#6d28d9;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="%d" height="16" as="geometry"/></mxCell>`,
			i, esc(bb.block.Label), bx+2, top-36, bw))
	}

	// Month gridlines + labels.
	m := time.Date(chartStart.Year(), chartStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	gi := 0
	for !m.After(chartEnd) {
		gx := x(m)
		if gx >= gut {
			b.WriteString(fmt.Sprintf(`<mxCell id="grid%d" value="" style="line;strokeColor=#e0e0e0;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="1" height="%d" as="geometry"/></mxCell>`,
				gi, gx, top, y-top))
			b.WriteString(fmt.Sprintf(`<mxCell id="mon%d" value="%s" style="text;html=1;align=left;fontSize=10;fontColor=#666666;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="80" height="16" as="geometry"/></mxCell>`,
				gi, esc(m.Format("Jan 2006")), gx+2, top-18))
		}
		m = m.AddDate(0, 1, 0)
		gi++
	}
	today := parseDate(snapshot.SnapshotDate)
	if !today.IsZero() && !today.Before(chartStart) && !today.After(chartEnd) {
		b.WriteString(fmt.Sprintf(`<mxCell id="today" value="today" style="line;strokeColor=#f59e0b;dashed=1;html=1;verticalAlign=bottom;fontColor=#f59e0b;fontSize=9;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="1" height="%d" as="geometry"/></mxCell>`,
			x(today), top, y-top))
	}

	// Group header bands (span the label column + chart).
	for i, h := range headers {
		g := groups[h.group]
		b.WriteString(fmt.Sprintf(`<mxCell id="group%d" value="%s — %d task%s, Σ %dd" style="text;html=1;align=left;fontStyle=1;fontSize=11;fontColor=#334155;fillColor=#eef2f7;strokeColor=none;verticalAlign=middle;spacingLeft=8;" vertex="1" parent="1"><mxGeometry x="0" y="%d" width="%d" height="20" as="geometry"/></mxCell>`,
			i, esc(g.label), g.count, plif(g.count), g.days, h.y, chartW))
	}

	cellID := map[string]string{}
	for i, r := range rows {
		cellID[r.task.TaskID] = fmt.Sprintf("t%d", i)
	}
	for i, r := range rows {
		yy := rowY[i] + (rowH-barH)/2
		label := r.task.TaskID + "  " + runeTruncate(r.task.Title, gLabelChars)
		b.WriteString(fmt.Sprintf(`<mxCell id="lbl%d" value="%s" style="text;html=1;align=left;fontSize=11;whiteSpace=nowrap;verticalAlign=middle;" vertex="1" parent="1"><mxGeometry x="6" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
			i, esc(label), rowY[i], gut-14, rowH))

		if r.milestone {
			cx := x(r.plannedStart)
			b.WriteString(fmt.Sprintf(`<mxCell id="%s" value="" style="rhombus;fillColor=#7c3aed;strokeColor=%s;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
				cellID[r.task.TaskID], boolStr(r.critical, "#dc2626", "#5b21b6"), cx-8, yy-1, 16, barH+2))
			continue
		}

		fill, stroke := ganttBarColor(r.task.Status, r.critical)
		bx := x(r.plannedStart)
		bw := x(r.plannedEnd) + int(ppd) - bx
		if bw < 4 {
			bw = 4
		}
		sw := "1"
		if r.critical {
			sw = "2"
		}
		b.WriteString(fmt.Sprintf(`<mxCell id="%s" value="" style="rounded=1;fillColor=%s;strokeColor=%s;strokeWidth=%s;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
			cellID[r.task.TaskID], fill, stroke, sw, bx, yy, bw, barH))
		if r.barEnd.After(r.plannedEnd) {
			sx := x(r.plannedEnd) + int(ppd)
			swid := x(r.barEnd) + int(ppd) - sx
			if swid > 0 {
				b.WriteString(fmt.Sprintf(`<mxCell id="slip%d" value="+%dd" style="rounded=1;fillColor=#ef4444;opacity=40;strokeColor=none;fontSize=9;fontColor=#b91c1c;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
					i, daysBetween(r.plannedEnd, r.barEnd), sx, yy, swid, barH))
			}
		}
	}

	// Dependency edges (finish-to-start).
	ei := 0
	for _, r := range rows {
		for _, dep := range r.task.Dependencies {
			src, ok := cellID[dep]
			if !ok {
				continue
			}
			p := posByIDLookup(rows, dep)
			color := "#9ca3af"
			if r.critical && p != nil && p.critical {
				color = "#dc2626"
			}
			b.WriteString(fmt.Sprintf(`<mxCell id="e%d" style="edgeStyle=orthogonalEdgeStyle;rounded=0;endArrow=classic;strokeColor=%s;html=1;exitX=1;exitY=0.5;entryX=0;entryY=0.5;" edge="1" parent="1" source="%s" target="%s"><mxGeometry relative="1" as="geometry"/></mxCell>`,
				ei, color, src, cellID[r.task.TaskID]))
			ei++
		}
	}

	b.WriteString(`</root></mxGraphModel></diagram>`)
	return b.String()
}

// posByIDLookup finds a laid-out row by task id (small n, linear is fine).
func posByIDLookup(rows []ganttRow, id string) *ganttRow {
	for i := range rows {
		if rows[i].task.TaskID == id {
			return &rows[i]
		}
	}
	return nil
}
