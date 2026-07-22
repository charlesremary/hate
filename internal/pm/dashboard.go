// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// HTML generation helpers
// ---------------------------------------------------------------------------

var statusColors = map[string][2]string{
	"complete":    {"#22c55e", "#052e16"},
	"in_progress": {"#3b82f6", "#0c1a2e"},
	"blocked":     {"#ef4444", "#2d0a0a"},
	"not_started": {"#6b7280", "#1a1a1a"},
	"cancelled":   {"#6b7280", "#1a1a1a"},
}

var healthColors = map[string]string{
	"green":  "#22c55e",
	"yellow": "#eab308",
	"red":    "#ef4444",
}

var healthLabels = map[string]string{
	"green":  "ON TRACK",
	"yellow": "AT RISK",
	"red":    "CRITICAL",
}

func statusBadge(status string) string {
	colors, ok := statusColors[status]
	if !ok {
		colors = statusColors["not_started"]
	}
	fg, bg := colors[0], colors[1]
	label := strings.ToUpper(strings.ReplaceAll(status, "_", " "))
	return fmt.Sprintf(`<span class="badge" style="color:%s;background:%s;border-color:%s40">%s</span>`, fg, bg, fg, label)
}

func slipBadge(days int) string {
	if days == 0 {
		return `<span class="slip-zero">&mdash;</span>`
	}
	color := "#eab308"
	if days > 14 {
		color = "#ef4444"
	}
	return fmt.Sprintf(`<span class="slip-days" style="color:%s">+%dd</span>`, color, days)
}

// ---------------------------------------------------------------------------
// Status panel
// ---------------------------------------------------------------------------

func resolveButton(row SlipLedgerRow) string {
	if !row.HasUnresolved || len(row.UnresolvedEventIDs) == 0 {
		return ""
	}
	// Create a button for each unresolved event
	var btns []string
	for _, eid := range row.UnresolvedEventIDs {
		btns = append(btns, fmt.Sprintf(
			` <button class="resolve-btn" onclick="openResolveModal('%s','%s')">Resolve</button>`,
			eid, row.TaskID))
	}
	return strings.Join(btns, "")
}

func renderStatusPanel(snapshot *Snapshot) string {
	health := snapshot.ComputedHealth
	hcolor := healthColors[health]
	hlabel := healthLabels[health]
	rows := SlipLedgerRows(snapshot)

	totalTasks := len(snapshot.Tasks)
	completeTasks := 0
	for _, t := range snapshot.Tasks {
		if t.Status == "complete" {
			completeTasks++
		}
	}
	pctDone := 0
	if totalTasks > 0 {
		pctDone = completeTasks * 100 / totalTasks
	}

	unresolvedColor := "#22c55e"
	if snapshot.UnresolvedSlipEvents > 0 {
		unresolvedColor = "#ef4444"
	}
	endDateColor := "#22c55e"
	if snapshot.ComputedEndDate != snapshot.BaselineEndDate {
		endDateColor = "#ef4444"
	}

	var sb strings.Builder

	// Summary cards
	sb.WriteString(fmt.Sprintf(`
    <div class="summary-cards">
      <div class="card">
        <div class="card-value" style="color:%s">%s</div>
        <div class="card-label">Project Health</div>
      </div>
      <div class="card">
        <div class="card-value">%d%%</div>
        <div class="card-label">%d of %d tasks done</div>
      </div>
      <div class="card">
        <div class="card-value" style="color:#ef4444">%dd</div>
        <div class="card-label">Total slip days</div>
      </div>
      <div class="card">
        <div class="card-value" style="color:%s">%d</div>
        <div class="card-label">Unresolved slip events</div>
      </div>
      <div class="card">
        <div class="card-value">%s</div>
        <div class="card-label">Baseline end</div>
      </div>
      <div class="card">
        <div class="card-value" style="color:%s">%s</div>
        <div class="card-label">Projected end</div>
      </div>
    </div>`,
		hcolor, hlabel,
		pctDone, completeTasks, totalTasks,
		snapshot.TotalSlipDays,
		unresolvedColor, snapshot.UnresolvedSlipEvents,
		snapshot.BaselineEndDate,
		endDateColor, snapshot.ComputedEndDate,
	))

	// Progress bar
	sb.WriteString(fmt.Sprintf(`
    <div class="progress-wrap">
      <div class="progress-label">
        <span>Completion</span><span>%d%%</span>
      </div>
      <div class="progress-track">
        <div class="progress-fill" style="width:%d%%;background:%s"></div>
      </div>
    </div>`, pctDone, pctDone, hcolor))

	// Task table
	sb.WriteString(`
    <div class="table-wrap">
      <table class="task-table">
        <thead>
          <tr>
            <th>ID</th>
            <th>Task</th>
            <th>Owner</th>
            <th>Status</th>
            <th>Baseline End</th>
            <th>Projected End</th>
            <th>Slip</th>
            <th>Reason</th>
          </tr>
        </thead>
        <tbody>`)

	// Build a map for quick lookup of snapshot tasks
	taskByID := map[string]*SnapshotTask{}
	for i := range snapshot.Tasks {
		taskByID[snapshot.Tasks[i].TaskID] = &snapshot.Tasks[i]
	}

	for _, row := range rows {
		unresolvedFlag := ""
		if row.HasUnresolved {
			unresolvedFlag = ` <span class="warn-flag">&#9888;</span>`
		}
		cpInfo := taskByID[row.TaskID]
		isCP := false
		if cpInfo != nil {
			isCP = cpInfo.IsCriticalPath
		}
		cpRowClass := ""
		if isCP {
			cpRowClass = " cp-row"
		}
		cpDot := ""
		if isCP {
			cpDot = `<span class="cp-dot" title="Critical Path">&#9679;</span>`
		}

		projEnd := row.ProjectedEnd
		if projEnd == "" {
			projEnd = row.BaselineEnd
		}
		if projEnd == "" {
			projEnd = "\u2014"
		}
		baseEnd := row.BaselineEnd
		if baseEnd == "" {
			baseEnd = "\u2014"
		}

		ownerShort := row.Owner
		if idx := strings.Index(ownerShort, "@"); idx >= 0 {
			ownerShort = ownerShort[:idx]
		}

		sb.WriteString(fmt.Sprintf(`
        <tr class="task-row%s">
          <td class="task-id-cell">%s%s</td>
          <td class="task-title-cell">%s%s</td>
          <td>%s</td>
          <td>%s</td>
          <td class="date-cell">%s</td>
          <td class="date-cell">%s</td>
          <td class="slip-cell">%s</td>
          <td class="reason-cell">%s%s</td>
        </tr>`,
			cpRowClass,
			row.TaskID, cpDot,
			row.Title, unresolvedFlag,
			ownerShort,
			statusBadge(row.Status),
			baseEnd,
			projEnd,
			slipBadge(row.SlipDays),
			row.SlipSummary,
			resolveButton(row),
		))
	}

	sb.WriteString(`
        </tbody>
      </table>
    </div>
    <div class="legend-row">
      <span class="cp-dot">&#9679;</span> Critical path task
      &nbsp;&nbsp;
      <span class="warn-flag">&#9888;</span> Unresolved slip event
    </div>`)

	return sb.String()
}

// ---------------------------------------------------------------------------
// Dependency graph panel -- rendered as inline SVG
// ---------------------------------------------------------------------------

func renderDependencyPanel(snapshot *Snapshot) string {
	tasks := snapshot.Tasks
	taskMap := map[string]*SnapshotTask{}
	for i := range tasks {
		taskMap[tasks[i].TaskID] = &tasks[i]
	}

	cpIDs := map[string]bool{}
	for _, id := range snapshot.CriticalPathIDs {
		cpIDs[id] = true
	}

	// Layout: assign column (level) via longest-path-from-root
	levels := map[string]int{}
	memo := map[string]int{}

	var getLevel func(string) int
	getLevel = func(tid string) int {
		if v, ok := memo[tid]; ok {
			return v
		}
		t, ok := taskMap[tid]
		if !ok {
			memo[tid] = 0
			return 0
		}
		deps := []string{}
		for _, d := range t.Dependencies {
			if _, ok := taskMap[d]; ok {
				deps = append(deps, d)
			}
		}
		if len(deps) == 0 {
			memo[tid] = 0
			return 0
		}
		maxLvl := 0
		for _, d := range deps {
			lvl := getLevel(d)
			if lvl+1 > maxLvl {
				maxLvl = lvl + 1
			}
		}
		memo[tid] = maxLvl
		return maxLvl
	}

	for _, t := range tasks {
		levels[t.TaskID] = getLevel(t.TaskID)
	}

	// Group by level
	byLevel := map[int][]string{}
	maxLevel := 0
	for tid, lv := range levels {
		byLevel[lv] = append(byLevel[lv], tid)
		if lv > maxLevel {
			maxLevel = lv
		}
	}

	// Geometry
	const (
		nodeW   = 180
		nodeH   = 64
		hGap    = 80
		vGap    = 28
		marginX = 40
		marginY = 40
	)

	colX := map[int]int{}
	for lv := 0; lv <= maxLevel; lv++ {
		colX[lv] = marginX + lv*(nodeW+hGap)
	}

	nodePos := map[string][2]int{}
	for lv := 0; lv <= maxLevel; lv++ {
		tids := byLevel[lv]
		startY := marginY
		for i, tid := range tids {
			nodePos[tid] = [2]int{colX[lv], startY + i*(nodeH+vGap)}
		}
	}

	// SVG canvas size
	svgW := marginX + (maxLevel+1)*(nodeW+hGap) + marginX
	maxNodesInCol := 1
	for _, v := range byLevel {
		if len(v) > maxNodesInCol {
			maxNodesInCol = len(v)
		}
	}
	svgH := marginY + maxNodesInCol*(nodeH+vGap) + marginY + 20

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(`<svg viewBox="0 0 %d %d" width="%d" height="%d" xmlns="http://www.w3.org/2000/svg" class="dep-svg" id="dep-graph">`, svgW, svgH, svgW, svgH))

	// Defs: arrowheads
	sb.WriteString(`
    <defs>
      <marker id="arrow-normal" markerWidth="10" markerHeight="7"
              refX="9" refY="3.5" orient="auto">
        <polygon points="0 0, 10 3.5, 0 7" fill="#374151"/>
      </marker>
      <marker id="arrow-cp" markerWidth="10" markerHeight="7"
              refX="9" refY="3.5" orient="auto">
        <polygon points="0 0, 10 3.5, 0 7" fill="#ef4444"/>
      </marker>
      <filter id="glow">
        <feGaussianBlur stdDeviation="3" result="coloredBlur"/>
        <feMerge><feMergeNode in="coloredBlur"/><feMergeNode in="SourceGraphic"/></feMerge>
      </filter>
    </defs>`)

	// Draw edges
	for _, t := range tasks {
		tid := t.TaskID
		pos, ok := nodePos[tid]
		if !ok {
			continue
		}
		tx, ty := pos[0], pos[1]
		for _, dep := range t.Dependencies {
			dpos, ok := nodePos[dep]
			if !ok {
				continue
			}
			dx, dy := dpos[0], dpos[1]
			x1 := dx + nodeW
			y1 := dy + nodeH/2
			x2 := tx
			y2 := ty + nodeH/2
			isCPEdge := cpIDs[dep] && cpIDs[tid]
			midX := (x1 + x2) / 2

			stroke := "#374151"
			width := "1.5"
			marker := "url(#arrow-normal)"
			dash := `stroke-dasharray="6,3"`
			gfilter := ""
			if isCPEdge {
				stroke = "#ef4444"
				width = "2.5"
				marker = "url(#arrow-cp)"
				dash = ""
				gfilter = `filter="url(#glow)"`
			}

			sb.WriteString(fmt.Sprintf(
				`<path d="M%d,%d C%d,%d %d,%d %d,%d" stroke="%s" stroke-width="%s" fill="none" marker-end="%s" %s %s/>`,
				x1, y1, midX, y1, midX, y2, x2, y2,
				stroke, width, marker, dash, gfilter,
			))
		}
	}

	// Status node colors: border, text, bg
	type nodeColors struct {
		border, text, bg string
	}
	statusNodeColors := map[string]nodeColors{
		"complete":    {"#166534", "#4ade80", "#052e16"},
		"in_progress": {"#1e40af", "#60a5fa", "#0c1a2e"},
		"blocked":     {"#991b1b", "#f87171", "#2d0a0a"},
		"not_started": {"#374151", "#9ca3af", "#111827"},
		"cancelled":   {"#374151", "#6b7280", "#111827"},
	}

	// Draw nodes
	for _, t := range tasks {
		tid := t.TaskID
		pos, ok := nodePos[tid]
		if !ok {
			continue
		}
		nx, ny := pos[0], pos[1]
		isCP := cpIDs[tid]
		isMS := t.IsMilestone
		status := t.Status
		slip := t.SlipDays
		floatDays := t.FloatDays

		nc, ok := statusNodeColors[status]
		if !ok {
			nc = statusNodeColors["not_started"]
		}
		borderC := nc.border
		textC := nc.text
		bgC := nc.bg

		borderW := "1"
		if isCP {
			borderC = "#ef4444"
			borderW = "2"
		}

		extraStyle := ""
		if isMS {
			extraStyle = `stroke-dasharray="4,2"`
		}

		// Node rectangle
		sb.WriteString(fmt.Sprintf(
			`<rect x="%d" y="%d" width="%d" height="%d" rx="6" fill="%s" stroke="%s" stroke-width="%s" %s/>`,
			nx, ny, nodeW, nodeH, bgC, borderC, borderW, extraStyle,
		))

		// CP indicator bar
		if isCP {
			sb.WriteString(fmt.Sprintf(
				`<rect x="%d" y="%d" width="3" height="%d" rx="2" fill="#ef4444"/>`,
				nx, ny+6, nodeH-12,
			))
		}

		// Task ID
		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" font-family="monospace" font-size="10" fill="#6b7280" font-weight="600">%s</text>`,
			nx+12, ny+18, tid,
		))

		// Milestone star
		if isMS {
			sb.WriteString(fmt.Sprintf(
				`<text x="%d" y="%d" font-size="11" fill="#eab308">&#9733;</text>`,
				nx+nodeW-16, ny+18,
			))
		}

		// Title (truncated) — slice by runes, not bytes, so multibyte
		// characters (em dashes, smart quotes) aren't cut in half.
		title := t.Title
		if r := []rune(title); len(r) > 22 {
			title = string(r[:21]) + "..."
		}
		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" font-family="sans-serif" font-size="12" fill="%s" font-weight="600">%s</text>`,
			nx+12, ny+34, textC, title,
		))

		// Status + slip row
		statusLabel := strings.ToUpper(strings.ReplaceAll(status, "_", " "))
		sb.WriteString(fmt.Sprintf(
			`<text x="%d" y="%d" font-family="monospace" font-size="9" fill="%s" opacity="0.7">%s</text>`,
			nx+12, ny+52, textC, statusLabel,
		))
		if slip > 0 {
			sb.WriteString(fmt.Sprintf(
				`<text x="%d" y="%d" font-family="monospace" font-size="9" fill="#ef4444" text-anchor="end">+%dd slip</text>`,
				nx+nodeW-12, ny+52, slip,
			))
		} else if floatDays > 0 {
			sb.WriteString(fmt.Sprintf(
				`<text x="%d" y="%d" font-family="monospace" font-size="9" fill="#6b7280" text-anchor="end">%dd float</text>`,
				nx+nodeW-12, ny+52, floatDays,
			))
		}
	}

	sb.WriteString("</svg>")
	svg := sb.String()

	// Legend
	legend := `
    <div class="dep-legend">
      <span class="dep-legend-item">
        <svg width="32" height="12"><path d="M0,6 L28,6" stroke="#ef4444" stroke-width="2.5"
          marker-end="url(#arrow-cp)"/></svg>
        Critical path
      </span>
      <span class="dep-legend-item">
        <svg width="32" height="12"><path d="M0,6 L28,6" stroke="#374151" stroke-width="1.5"
          stroke-dasharray="6,3"/></svg>
        Float path
      </span>
      <span class="dep-legend-item">
        <svg width="16" height="16">
          <rect x="1" y="1" width="14" height="14" rx="3" fill="#0c1a2e" stroke="#1e40af"/>
        </svg>
        In progress
      </span>
      <span class="dep-legend-item">
        <svg width="16" height="16">
          <rect x="1" y="1" width="14" height="14" rx="3" fill="#052e16" stroke="#166534"/>
        </svg>
        Complete
      </span>
      <span class="dep-legend-item">
        <svg width="16" height="16">
          <rect x="1" y="1" width="14" height="14" rx="3" fill="#2d0a0a" stroke="#991b1b"/>
        </svg>
        Blocked
      </span>
      <span class="dep-legend-item">&#9733; Milestone</span>
    </div>`

	// Critical path task list
	cpList := ""
	if len(snapshot.CriticalPathIDs) > 0 {
		var items strings.Builder
		for _, tid := range snapshot.CriticalPathIDs {
			t := taskMap[tid]
			slipTxt := ""
			if t != nil && t.SlipDays > 0 {
				slipTxt = fmt.Sprintf(` <span style="color:#ef4444">+%dd slip</span>`, t.SlipDays)
			}
			title := ""
			if t != nil {
				title = t.Title
			}
			items.WriteString(fmt.Sprintf(`<li><code>%s</code> &mdash; %s%s</li>`, tid, title, slipTxt))
		}
		cpList = fmt.Sprintf(`
        <div class="cp-summary">
          <div class="cp-summary-title">Critical Path Sequence</div>
          <ol>%s</ol>
          <div class="cp-note">Any slip on these tasks directly extends the project end date.</div>
        </div>`, items.String())
	}

	scrollWrap := fmt.Sprintf(`
    <div class="zoom-controls">
      <button onclick="zoomGraph(-1)" title="Zoom out">−</button>
      <span id="zoom-level">100%%</span>
      <button onclick="zoomGraph(1)" title="Zoom in">+</button>
      <button onclick="zoomGraph(0)" title="Fit to width">Fit</button>
    </div>
    <div class="dep-scroll" id="dep-scroll-container">%s</div>`, svg)
	return legend + scrollWrap + cpList
}

// ---------------------------------------------------------------------------
// CSS
// ---------------------------------------------------------------------------

const dashboardCSS = `
  :root {
    --bg:       #0d0d0f;
    --surface:  #13131a;
    --border:   #1f2937;
    --text:     #e5e7eb;
    --muted:    #6b7280;
    --accent:   #e8ff47;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    background: var(--bg);
    color: var(--text);
    font-size: 14px;
    min-height: 100vh;
  }

  /* Header */
  .header {
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 18px 32px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    position: sticky;
    top: 0;
    z-index: 100;
  }
  .header-left { display: flex; align-items: center; gap: 16px; }
  .project-id {
    font-family: monospace;
    font-size: 11px;
    letter-spacing: 0.15em;
    color: var(--accent);
    background: rgba(232,255,71,0.08);
    border: 1px solid rgba(232,255,71,0.2);
    padding: 3px 10px;
    border-radius: 3px;
  }
  .project-name { font-size: 18px; font-weight: 700; color: var(--text); }
  .header-meta { font-size: 12px; color: var(--muted); }

  /* Tabs */
  .tabs {
    display: flex;
    gap: 2px;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
    padding: 0 32px;
  }
  .tab {
    padding: 12px 24px;
    font-size: 13px;
    font-weight: 600;
    letter-spacing: 0.04em;
    color: var(--muted);
    cursor: pointer;
    border-bottom: 2px solid transparent;
    transition: all 0.15s;
    user-select: none;
  }
  .tab:hover { color: var(--text); }
  .tab.active {
    color: var(--accent);
    border-bottom-color: var(--accent);
  }

  /* Panels */
  .panel { display: none; padding: 28px 32px 48px; }
  .panel.active { display: block; }

  /* Summary cards */
  .summary-cards {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    margin-bottom: 24px;
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px 20px;
    flex: 1;
    min-width: 140px;
  }
  .card-value {
    font-size: 22px;
    font-weight: 700;
    color: var(--text);
    margin-bottom: 4px;
    font-variant-numeric: tabular-nums;
  }
  .card-label { font-size: 11px; color: var(--muted); letter-spacing: 0.06em; text-transform: uppercase; }

  /* Progress bar */
  .progress-wrap { margin-bottom: 24px; }
  .progress-label {
    display: flex;
    justify-content: space-between;
    font-size: 12px;
    color: var(--muted);
    margin-bottom: 6px;
  }
  .progress-track {
    height: 6px;
    background: var(--border);
    border-radius: 3px;
    overflow: hidden;
  }
  .progress-fill {
    height: 100%;
    border-radius: 3px;
    transition: width 0.6s ease;
  }

  /* Task table */
  .table-wrap {
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .task-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }
  .task-table thead tr {
    background: #0a0a0c;
    border-bottom: 1px solid var(--border);
  }
  .task-table th {
    padding: 10px 14px;
    text-align: left;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .task-table tbody tr {
    border-bottom: 1px solid rgba(31,41,55,0.6);
    transition: background 0.1s;
  }
  .task-table tbody tr:last-child { border-bottom: none; }
  .task-table tbody tr:hover { background: rgba(255,255,255,0.03); }
  .task-table td { padding: 11px 14px; vertical-align: middle; }
  .task-row.cp-row td:first-child { border-left: 2px solid #ef444460; }

  .task-id-cell { font-family: monospace; font-size: 12px; color: var(--muted); white-space: nowrap; }
  .task-title-cell { font-weight: 500; }
  .date-cell { font-family: monospace; font-size: 12px; white-space: nowrap; color: var(--muted); }
  .slip-cell { font-family: monospace; font-size: 13px; font-weight: 700; }
  .reason-cell { font-size: 11px; color: var(--muted); max-width: 200px; }
  .slip-zero { color: var(--muted); }
  .cp-dot { color: #ef4444; font-size: 10px; margin-left: 4px; }
  .warn-flag { color: #eab308; }

  .badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 3px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.08em;
    border: 1px solid transparent;
  }

  .legend-row {
    font-size: 11px;
    color: var(--muted);
    margin-top: 12px;
    padding: 0 4px;
  }

  /* Dependency panel */
  .zoom-controls {
    display: flex; align-items: center; gap: 8px; margin: 12px 0 4px;
  }
  .zoom-controls button {
    background: var(--surface); color: var(--text); border: 1px solid var(--border);
    border-radius: 4px; padding: 4px 12px; cursor: pointer; font-size: 14px; font-weight: 700;
  }
  .zoom-controls button:hover { background: #374151; }
  .zoom-controls span { font-size: 12px; color: var(--muted); min-width: 40px; text-align: center; }
  .dep-scroll {
    overflow: auto;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    margin: 4px 0 16px;
    max-height: 70vh;
  }
  .dep-svg {
    display: block;
    transform-origin: top left;
  }
  .dep-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 20px;
    align-items: center;
    font-size: 12px;
    color: var(--muted);
    margin-bottom: 16px;
  }
  .dep-legend-item {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .cp-summary {
    background: rgba(239,68,68,0.05);
    border: 1px solid rgba(239,68,68,0.2);
    border-radius: 8px;
    padding: 20px 24px;
    margin-top: 8px;
  }
  .cp-summary-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: #ef4444;
    margin-bottom: 12px;
  }
  .cp-summary ol {
    padding-left: 20px;
    line-height: 2;
    font-size: 13px;
  }
  .cp-summary code {
    font-size: 11px;
    color: var(--muted);
    background: rgba(255,255,255,0.05);
    padding: 1px 6px;
    border-radius: 3px;
  }
  .cp-note {
    margin-top: 12px;
    font-size: 12px;
    color: var(--muted);
    font-style: italic;
  }
  .resolve-btn {
    display: inline-block;
    margin-left: 6px;
    padding: 2px 8px;
    font-size: 11px;
    background: #ef4444;
    color: #fff;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-weight: 500;
  }
  .resolve-btn:hover {
    background: #dc2626;
  }
`

const dashboardJS = `
  function showTab(name) {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
    document.querySelector('.tab[data-tab="' + name + '"]').classList.add('active');
    document.getElementById('panel-' + name).classList.add('active');
  }

  var graphZoom = 100;
  function zoomGraph(dir) {
    var svg = document.getElementById('dep-graph');
    if (!svg) return;
    if (dir === 0) {
      // Fit to container width
      var container = document.getElementById('dep-scroll-container');
      var svgNatW = parseInt(svg.getAttribute('width'));
      graphZoom = Math.round((container.clientWidth - 32) / svgNatW * 100);
      if (graphZoom < 30) graphZoom = 30;
      if (graphZoom > 200) graphZoom = 200;
    } else {
      graphZoom += dir * 20;
      if (graphZoom < 30) graphZoom = 30;
      if (graphZoom > 200) graphZoom = 200;
    }
    svg.style.transform = 'scale(' + (graphZoom/100) + ')';
    svg.style.width = svg.getAttribute('width') + 'px';
    svg.style.height = svg.getAttribute('height') + 'px';
    document.getElementById('zoom-level').textContent = graphZoom + '%';
  }

  var currentSlipEventID = null;
  function openResolveModal(eventId, taskId) {
    currentSlipEventID = eventId;
    document.getElementById('resolve-info').textContent = 'Event: ' + eventId + ' (Task: ' + taskId + ')';
    document.getElementById('resolve-category').value = 'estimation_error';
    document.getElementById('resolve-narrative').value = '';
    var overlay = document.getElementById('resolve-overlay');
    overlay.style.display = 'flex';
  }
  function closeResolveModal() {
    document.getElementById('resolve-overlay').style.display = 'none';
    currentSlipEventID = null;
  }
  document.getElementById('resolve-overlay').addEventListener('click', function(e) {
    if (e.target === document.getElementById('resolve-overlay')) closeResolveModal();
  });
  async function submitResolve() {
    if (!currentSlipEventID) return;
    var cat = document.getElementById('resolve-category').value;
    var narr = document.getElementById('resolve-narrative').value.trim();
    if (!narr) { alert('Please provide an explanation.'); return; }
    try {
      var resp = await fetch('/api/projects/' + PROJECT_ID + '/slip/' + currentSlipEventID, {
        method: 'PATCH',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({reason_category: cat, reason_narrative: narr})
      });
      var data = await resp.json();
      if (!resp.ok) { alert(data.detail || 'Error'); return; }
      closeResolveModal();
      // The resolution is saved to the slip ledger, but the dashboard's health and
      // unresolved count come from the latest snapshot — ask whether to refresh now
      // or batch up more resolutions and snapshot once at the end.
      document.getElementById('snapshot-prompt-overlay').style.display = 'flex';
    } catch(e) { alert(e.message); }
  }
  function closeSnapshotPrompt() {
    document.getElementById('snapshot-prompt-overlay').style.display = 'none';
  }
  async function runSnapshotNow() {
    try {
      var resp = await fetch('/api/projects/' + PROJECT_ID + '/snapshot', { method: 'POST' });
      var data = await resp.json();
      if (!resp.ok) { alert(data.detail || 'Snapshot failed'); return; }
      location.reload();
    } catch(e) { alert(e.message); }
  }
  document.getElementById('snapshot-prompt-overlay').addEventListener('click', function(e) {
    if (e.target === document.getElementById('snapshot-prompt-overlay')) closeSnapshotPrompt();
  });
`

// ---------------------------------------------------------------------------
// Full page assembly
// ---------------------------------------------------------------------------

// GenerateDashboard returns a complete self-contained HTML string for the PM dashboard.
func GenerateDashboard(snapshot *Snapshot, costHTML string) string {
	health := snapshot.ComputedHealth
	hcolor := healthColors[health]
	hlabel := healthLabels[health]
	snapDate := snapshot.SnapshotDate
	projID := snapshot.ProjectID
	projName := snapshot.ProjectName

	statusHTML := renderStatusPanel(snapshot)
	depHTML := renderDependencyPanel(snapshot)
	ganttHTML := renderGanttPanel(snapshot)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s Dashboard &mdash; PM Agent</title>
  <style>%s</style>
</head>
<body>

<div class="header">
  <div class="header-left">
    <span class="project-id">%s</span>
    <span class="project-name">%s</span>
  </div>
  <div class="header-meta">
    Snapshot: %s &nbsp;&middot;&nbsp;
    Health: <strong style="color:%s">%s</strong>
  </div>
</div>

<div class="tabs">
  <div class="tab active" data-tab="status" onclick="showTab('status')">Project Status</div>
  <div class="tab" data-tab="gantt" onclick="showTab('gantt')">Gantt</div>
  <div class="tab" data-tab="deps" onclick="showTab('deps')">Dependencies &amp; Critical Path</div>
</div>

<div id="panel-status" class="panel active">
  %s
</div>

<div id="panel-gantt" class="panel">
  %s
</div>

<div id="panel-deps" class="panel">
  %s
</div>

%s

<!-- Resolve slip modal -->
<div id="resolve-overlay" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:100;align-items:center;justify-content:center">
  <div style="background:#fff;border-radius:8px;padding:24px;width:440px;max-width:90vw;box-shadow:0 8px 32px rgba(0,0,0,.2)">
    <h3 style="margin-bottom:12px;font-size:16px">Resolve Slip Event</h3>
    <p id="resolve-info" style="font-size:13px;color:#666;margin-bottom:12px"></p>
    <label style="display:block;margin-bottom:10px;font-size:13px;color:#666">Reason Category
      <select id="resolve-category" style="display:block;width:100%%;margin-top:4px;padding:7px 10px;border:1px solid #ddd;border-radius:6px;font-size:14px">
        <option value="estimation_error">Estimation Error</option>
        <option value="scope_change">Scope Change</option>
        <option value="technical_blocker">Technical Blocker</option>
        <option value="external_dependency">External Dependency</option>
        <option value="resource_diversion">Resource Diversion</option>
        <option value="environment_tooling">Environment / Tooling</option>
        <option value="client_delay">Client Delay</option>
        <option value="requirements_change">Requirements Change</option>
      </select>
    </label>
    <label style="display:block;margin-bottom:14px;font-size:13px;color:#666">Explanation
      <textarea id="resolve-narrative" rows="3" style="display:block;width:100%%;margin-top:4px;padding:7px 10px;border:1px solid #ddd;border-radius:6px;font-size:14px" placeholder="What caused this slip?"></textarea>
    </label>
    <div style="display:flex;gap:8px">
      <button onclick="submitResolve()" style="background:#1976d2;color:#fff;border:none;padding:8px 18px;border-radius:6px;cursor:pointer;font-size:13px">Resolve</button>
      <button onclick="closeResolveModal()" style="background:#fff;color:#333;border:1px solid #ccc;padding:8px 18px;border-radius:6px;cursor:pointer;font-size:13px">Cancel</button>
    </div>
  </div>
</div>

<!-- Snapshot prompt modal (shown after a slip event is resolved) -->
<div id="snapshot-prompt-overlay" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:100;align-items:center;justify-content:center">
  <div style="background:#fff;border-radius:8px;padding:24px;width:460px;max-width:90vw;box-shadow:0 8px 32px rgba(0,0,0,.2)">
    <h3 style="margin-bottom:12px;font-size:16px">Resolution saved</h3>
    <p style="font-size:13px;color:#555;line-height:1.6;margin-bottom:16px">The slip event has been resolved in the ledger. The dashboard's health and unresolved-event count won't reflect it until a new snapshot is run.<br><br>Run a snapshot now, or keep resolving events and snapshot once you're done?</p>
    <div style="display:flex;gap:8px">
      <button onclick="runSnapshotNow()" style="background:#1976d2;color:#fff;border:none;padding:8px 18px;border-radius:6px;cursor:pointer;font-size:13px">Run Snapshot Now</button>
      <button onclick="closeSnapshotPrompt()" style="background:#fff;color:#333;border:1px solid #ccc;padding:8px 18px;border-radius:6px;cursor:pointer;font-size:13px">Wait</button>
    </div>
  </div>
</div>

<script>
var PROJECT_ID = '%s';
%s
</script>
</body>
</html>`,
		projID, dashboardCSS,
		projID, projName,
		snapDate, hcolor, hlabel,
		statusHTML,
		ganttHTML,
		depHTML,
		costHTML,
		projID,
		dashboardJS,
	)
}
