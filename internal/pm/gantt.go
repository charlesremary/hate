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

// The Gantt panel renders the baselined schedule as time-scaled bars — planned
// vs actual/projected, critical path in red, milestones as diamonds, and
// finish-to-start dependency connectors. It is a read-only, self-contained view
// (inline SVG, no external assets), built from the same Snapshot the other
// dashboard panels use. Rescheduling happens by editing tickets, not here.

// ganttRow is a single task with its dates parsed and laid out.
type ganttRow struct {
	task         *SnapshotTask
	plannedStart time.Time
	plannedEnd   time.Time
	actualStart  time.Time
	barEnd       time.Time // actual/projected end when known, else planned end
	milestone    bool
	critical     bool
	y            int
}

const (
	gGutter    = 250 // left label column
	gTopAxis   = 48  // axis header height
	gRowH      = 30
	gBarH      = 14
	gPadDays   = 2
	gRightPad  = 40
	gBottomPad = 28
)

// daysBetween returns whole days from a to b (b−a), truncating.
func daysBetween(a, b time.Time) int {
	return int(b.Sub(a).Hours() / 24)
}

// ganttData parses the snapshot's tasks into laid-out rows and the chart's date
// window. Tasks with no valid planned start are skipped (nothing to place).
func ganttData(snapshot *Snapshot) (rows []ganttRow, chartStart, chartEnd time.Time) {
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
		}
		if t.Current.ActualStart != nil {
			row.actualStart = parseDate(*t.Current.ActualStart)
		}
		// barEnd tracks reality: projected end if set, else actual end, else planned.
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
	// Order by planned start, then title — a readable top-to-bottom timeline.
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].plannedStart.Equal(rows[j].plannedStart) {
			return rows[i].plannedStart.Before(rows[j].plannedStart)
		}
		return rows[i].task.Title < rows[j].task.Title
	})
	for i := range rows {
		rows[i].y = gTopAxis + i*gRowH
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
	return rows, chartStart, chartEnd
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

// renderGanttPanel renders the Gantt tab body (SVG + toolbar + legend).
func renderGanttPanel(snapshot *Snapshot) string {
	rows, chartStart, chartEnd := ganttData(snapshot)
	if len(rows) == 0 {
		return `<div style="padding:24px;color:#9ca3af">No scheduled tasks with baseline dates to chart yet.</div>`
	}
	totalDays := daysBetween(chartStart, chartEnd) + 1
	ppd := ganttPxPerDay(totalDays)
	chartW := int(float64(totalDays)*ppd) + 1
	svgW := gGutter + chartW + gRightPad
	svgH := gTopAxis + len(rows)*gRowH + gBottomPad

	// x maps a date to a pixel column (left edge of that day).
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
	axisBottom := svgH - gBottomPad
	// Walk month starts within [chartStart, chartEnd].
	m := time.Date(chartStart.Year(), chartStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !m.After(chartEnd) {
		gx := x(m)
		if gx >= gGutter {
			sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#e5e7eb" stroke-width="1"/>`, gx, gTopAxis-20, gx, axisBottom))
			sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="11" fill="#6b7280" font-family="sans-serif">%s</text>`, gx+4, gTopAxis-24, esc(m.Format("Jan 2006"))))
		}
		m = m.AddDate(0, 1, 0)
	}
	// Today line.
	today := parseDate(snapshot.SnapshotDate)
	if today.IsZero() {
		today = chartStart
	}
	if !today.Before(chartStart) && !today.After(chartEnd) {
		tx := x(today)
		sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#f59e0b" stroke-width="1.5" stroke-dasharray="3 3"/>`, tx, gTopAxis-20, tx, axisBottom))
		sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-size="10" fill="#f59e0b" font-family="sans-serif">today</text>`, tx+3, axisBottom+12))
	}
	// Header separator + gutter divider.
	sb.WriteString(fmt.Sprintf(`<line x1="0" y1="%d" x2="%d" y2="%d" stroke="#d1d5db" stroke-width="1"/>`, gTopAxis-2, svgW, gTopAxis-2))
	sb.WriteString(fmt.Sprintf(`<line x1="%d" y1="0" x2="%d" y2="%d" stroke="#d1d5db" stroke-width="1"/>`, gGutter, gGutter, axisBottom))

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
			color, marker, opacity, wdt := "#9ca3af", "g-arrow", "0.45", "1"
			if isCP {
				color, marker, opacity, wdt = "#dc2626", "g-arrow-cp", "0.9", "1.5"
			}
			midX := predX + 12
			sb.WriteString(fmt.Sprintf(
				`<path d="M %d %d H %d V %d H %d" fill="none" stroke="%s" stroke-width="%s" opacity="%s" marker-end="url(#%s)"/>`,
				predX, predCY, midX, succCY, succCX, color, wdt, opacity, marker))
		}
	}

	// --- Rows: left label + bar(s) / milestone. ---
	for _, r := range rows {
		yc := r.y + gRowH/2
		// Zebra row background across the chart area.
		if (r.y-gTopAxis)/gRowH%2 == 1 {
			sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="#f9fafb"/>`, gGutter, r.y, chartW, gRowH))
		}
		// Left label: id + truncated title, owner small.
		title := runeTruncate(r.task.Title, 30)
		labelColor := "#111827"
		if r.critical {
			labelColor = "#b91c1c"
		}
		sb.WriteString(fmt.Sprintf(`<text x="10" y="%d" font-size="12" font-family="sans-serif" fill="%s"><tspan font-weight="600">%s</tspan> %s</text>`,
			yc-1, labelColor, esc(r.task.TaskID), esc(title)))
		if owner := ownerShort(r.task.Owner); owner != "" {
			sb.WriteString(fmt.Sprintf(`<text x="10" y="%d" font-size="10" font-family="sans-serif" fill="#9ca3af">%s</text>`, yc+11, esc(owner)))
		}

		if r.milestone {
			// Diamond at the planned start.
			cx := x(r.plannedStart)
			s := 7
			sb.WriteString(fmt.Sprintf(`<path d="M %d %d L %d %d L %d %d L %d %d Z" fill="#7c3aed" stroke="%s" stroke-width="1"/>`,
				cx, yc-s, cx+s, yc, cx, yc+s, cx-s, yc, boolStr(r.critical, "#dc2626", "#5b21b6")))
			continue
		}

		fill, stroke := ganttBarColor(r.task.Status, r.critical)
		by := yc - gBarH/2
		// Planned (baseline) bar — the light backdrop.
		px0 := x(r.plannedStart)
		pw := x(r.plannedEnd) + int(ppd) - px0
		if pw < 3 {
			pw = 3
		}
		sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" rx="3" fill="#e5e7eb"/>`, px0, by, pw, gBarH))
		// Progress/actual bar — from planned start (or actual start) to barEnd.
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
		// Slip: projected end past planned end → hatch the overrun in red.
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

	exportURL := fmt.Sprintf("/api/projects/%s/gantt.drawio", esc(snapshot.ProjectID))
	legend := `<div style="display:flex;gap:16px;flex-wrap:wrap;font-size:12px;color:#6b7280;margin:10px 0 0">
      <span><span style="display:inline-block;width:22px;height:8px;background:#e5e7eb;border-radius:2px;vertical-align:middle"></span> planned</span>
      <span><span style="display:inline-block;width:22px;height:8px;background:#3b82f6;border-radius:2px;vertical-align:middle"></span> actual / projected</span>
      <span><span style="display:inline-block;width:22px;height:8px;background:#ef4444;opacity:.35;border-radius:2px;vertical-align:middle"></span> slip</span>
      <span><span style="color:#dc2626">&#9644;</span> critical path</span>
      <span><span style="color:#7c3aed">&#9670;</span> milestone</span>
    </div>`
	return fmt.Sprintf(`
<div style="padding:16px 24px 8px">
  <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px">
    <div style="font-size:13px;color:#6b7280">Baselined schedule &mdash; read-only. Reschedule by editing tickets.</div>
    <a href="%s" download="gantt.drawio" class="gantt-export-btn" style="text-decoration:none;background:#1976d2;color:#fff;padding:7px 14px;border-radius:6px;font-size:13px">Export to draw.io</a>
  </div>
  <div style="overflow-x:auto;border:1px solid #e5e7eb;border-radius:8px;background:#fff">%s</div>
  %s
</div>`, exportURL, sb.String(), legend)
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

// ---------------------------------------------------------------------------
// draw.io export
// ---------------------------------------------------------------------------

// RenderGanttDrawio renders the Gantt as an uncompressed draw.io (mxGraph) file:
// left task labels, time-scaled planned bars (with the slipped overrun and a
// critical-path red stroke), milestone diamonds, month gridlines, a today line,
// and finish-to-start dependency edges. It's an editable, printable picture —
// draw.io reads this XML directly (no deflate needed).
func RenderGanttDrawio(snapshot *Snapshot) string {
	const (
		gut   = 220
		rowH  = 28
		barH  = 14
		top   = 40
		lineW = 1
	)
	rows, chartStart, chartEnd := ganttData(snapshot)
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<mxfile host="hate" type="device"><diagram name="%s Gantt"><mxGraphModel dx="800" dy="600" grid="1" gridSize="10" guides="1" tooltips="1" connect="1" arrows="1" fold="1" page="1" pageScale="1" pageWidth="1600" pageHeight="1100" math="0" shadow="0"><root><mxCell id="0"/><mxCell id="1" parent="0"/>`,
		esc(snapshot.ProjectName)))

	if len(rows) == 0 {
		b.WriteString(`<mxCell id="empty" value="No scheduled tasks with baseline dates." style="text;html=1;align=left;" vertex="1" parent="1"><mxGeometry x="20" y="20" width="400" height="24" as="geometry"/></mxCell></root></mxGraphModel></diagram></mxfile>`)
		return b.String()
	}

	totalDays := daysBetween(chartStart, chartEnd) + 1
	ppd := ganttPxPerDay(totalDays)
	x := func(d time.Time) int { return gut + int(float64(daysBetween(chartStart, d))*ppd) }
	chartRight := x(chartEnd) + int(ppd) + 40
	chartBottom := top + len(rows)*rowH + 20

	// Month gridlines + labels.
	m := time.Date(chartStart.Year(), chartStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	gi := 0
	for !m.After(chartEnd) {
		gx := x(m)
		if gx >= gut {
			b.WriteString(fmt.Sprintf(`<mxCell id="grid%d" value="" style="line;strokeColor=#e0e0e0;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="1" height="%d" as="geometry"/></mxCell>`,
				gi, gx, top, len(rows)*rowH))
			b.WriteString(fmt.Sprintf(`<mxCell id="mon%d" value="%s" style="text;html=1;align=left;fontSize=10;fontColor=#666666;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="80" height="16" as="geometry"/></mxCell>`,
				gi, esc(m.Format("Jan 2006")), gx+2, top-18))
		}
		m = m.AddDate(0, 1, 0)
		gi++
	}
	// Today line.
	today := parseDate(snapshot.SnapshotDate)
	if !today.IsZero() && !today.Before(chartStart) && !today.After(chartEnd) {
		b.WriteString(fmt.Sprintf(`<mxCell id="today" value="today" style="line;strokeColor=#f59e0b;dashed=1;html=1;verticalAlign=bottom;fontColor=#f59e0b;fontSize=9;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="1" height="%d" as="geometry"/></mxCell>`,
			x(today), top, len(rows)*rowH))
	}

	cellID := map[string]string{}
	for i, r := range rows {
		cellID[r.task.TaskID] = fmt.Sprintf("t%d", i)
	}

	for i, r := range rows {
		y := top + i*rowH + (rowH-barH)/2
		// Left label.
		label := r.task.TaskID + "  " + r.task.Title
		b.WriteString(fmt.Sprintf(`<mxCell id="lbl%d" value="%s" style="text;html=1;align=left;fontSize=11;whiteSpace=nowrap;" vertex="1" parent="1"><mxGeometry x="6" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
			i, esc(label), y-1, gut-12, barH+2))

		if r.milestone {
			cx := x(r.plannedStart)
			b.WriteString(fmt.Sprintf(`<mxCell id="%s" value="" style="rhombus;fillColor=#7c3aed;strokeColor=%s;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
				cellID[r.task.TaskID], boolStr(r.critical, "#dc2626", "#5b21b6"), cx-8, y-1, 16, barH+2))
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
			cellID[r.task.TaskID], fill, stroke, sw, bx, y, bw, barH))
		// Slip overrun.
		if r.barEnd.After(r.plannedEnd) {
			sx := x(r.plannedEnd) + int(ppd)
			swid := x(r.barEnd) + int(ppd) - sx
			if swid > 0 {
				b.WriteString(fmt.Sprintf(`<mxCell id="slip%d" value="+%dd" style="rounded=1;fillColor=#ef4444;opacity=40;strokeColor=none;fontSize=9;fontColor=#b91c1c;html=1;" vertex="1" parent="1"><mxGeometry x="%d" y="%d" width="%d" height="%d" as="geometry"/></mxCell>`,
					i, daysBetween(r.plannedEnd, r.barEnd), sx, y, swid, barH))
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

	// Reference the computed extents so linters/tools see intended page size.
	_ = chartRight
	_ = chartBottom
	b.WriteString(`</root></mxGraphModel></diagram></mxfile>`)
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
