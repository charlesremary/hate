// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"hate/internal/ticket"
)

// The "Schedule vs request" card answers "does the scoped work fit the dates
// the client asked for, and by how much?" — before day 1 and every day after.
// It compares the requested start / end (project config) with:
//   - the actual start (first move to in_progress or first logged time);
//   - the projected finish (likely): the capacity-aware schedule from the
//     later of today and the requested start;
//   - the projected finish (P85): the same schedule with code (CFP-sized)
//     tickets at rate x Monte Carlo code P85 / P50;
//   - needs vs has: remaining hours over the working days left against the
//     team's total daily hours.
// Business days throughout, flat daily capacity (no holidays). Read-only; the
// history file is written separately (see RecordForecastHistory).

// Forecast statuses.
const (
	ForecastOnTrack = "ON TRACK" // P85 finish on or before the requested end
	ForecastAtRisk  = "AT RISK"  // likely on time, P85 late
	ForecastLate    = "LATE"     // likely finish after the requested end
)

// ForecastReport is the Schedule vs request card (also GET /forecast).
type ForecastReport struct {
	RequestedStart string `json:"requested_start"` // "" when unset
	RequestedEnd   string `json:"requested_end"`   // "" when unset (no card, just a hint)
	ActualStart    string `json:"actual_start"`    // "" when no work has started
	// StartVarianceDays is actual start vs requested start in business days
	// (plus = late); nil unless both are known.
	StartVarianceDays *int   `json:"start_variance_days"`
	ScheduleStart     string `json:"schedule_start"` // max(today, requested start), on a weekday
	LikelyFinish      string `json:"likely_finish"`  // "" when no open work
	P85Finish         string `json:"p85_finish"`
	// P85Factor is MC code P85 / code P50 (1 when there's no Monte Carlo result).
	P85Factor float64 `json:"p85_factor"`
	// Finish variances vs the requested end in business days (plus = late).
	FinishVarianceDays    int     `json:"finish_variance_days"`
	P85FinishVarianceDays int     `json:"p85_finish_variance_days"`
	RemainingHours        float64 `json:"remaining_hours"`   // open estimated hours (unsized excluded)
	Unsized               int     `json:"unsized"`           // open tickets with no usable estimate
	WorkingDaysLeft       int     `json:"working_days_left"` // schedule start..requested end, inclusive
	NeedsPerDay           float64 `json:"needs_per_day"`     // remaining / working days left (0 when none left)
	HasPerDay             float64 `json:"has_per_day"`       // Σ daily hours of lanes with open work
	HoursToCut            float64 `json:"hours_to_cut"`      // remaining − has × days, when positive
	SpareHours            float64 `json:"spare_hours"`       // has × days − remaining, when positive
	Status                string  `json:"status"`            // ON TRACK / AT RISK / LATE; "" without a requested end
}

// MCP85Factor returns code P85 / code P50 from a Monte Carlo result, or 1 when
// the result isn't usable.
func MCP85Factor(mc MonteCarloResult) float64 {
	if !mc.OK || mc.Code.P50 <= 0 || mc.Code.P85 <= 0 {
		return 1
	}
	return mc.Code.P85 / mc.Code.P50
}

// signedBusinessDays is the business-day variance of `actual` against `want`:
// working days after want up to actual (plus), or before want back to actual
// (minus); 0 when equal.
func signedBusinessDays(want, actual time.Time) int {
	switch {
	case actual.After(want):
		return businessDaysBetween(want.AddDate(0, 0, 1), actual)
	case actual.Before(want):
		return -businessDaysBetween(actual.AddDate(0, 0, 1), want)
	}
	return 0
}

// planFinish is the business day the last open item ends (zero when none).
func planFinish(plan *CapacityPlan) time.Time {
	var latest time.Time
	for _, it := range plan.Items {
		if it.EndD.After(latest) {
			latest = it.EndD
		}
	}
	return latest
}

// ActualStart is the earliest of any in-scope ticket's first status change into
// in_progress and any time entry's date (zero when work hasn't started).
func ActualStart(tickets []*ticket.Ticket) time.Time {
	var first time.Time
	consider := func(d time.Time) {
		if !d.IsZero() && (first.IsZero() || d.Before(first)) {
			first = d
		}
	}
	for _, t := range tickets {
		if !inHoursScope(t) {
			continue
		}
		for _, a := range t.Activity {
			if a.Action == "status_changed" && strings.HasSuffix(strings.TrimSpace(a.Detail), "-> in_progress") && len(a.Timestamp) >= 10 {
				consider(parseDate(a.Timestamp[:10]))
			}
		}
		for _, te := range t.TimeEntries {
			if len(te.Date) >= 10 {
				consider(parseDate(te.Date[:10]))
			}
		}
	}
	return first
}

// ComputeForecast builds the Schedule vs request card. p85Factor scales the
// code rate for the P85 run (see MCP85Factor; <= 1 or non-finite reuses the
// likely run). reqStart / reqEnd are YYYY-MM-DD or "".
func ComputeForecast(tickets []*ticket.Ticket, resources []ticket.Resource, ctx EstimateContext, p85Factor float64, today time.Time, reqStart, reqEnd string) ForecastReport {
	ctx = ctx.prepared()
	rs, re := parseDate(reqStart), parseDate(reqEnd)
	rep := ForecastReport{RequestedStart: fmtDate(rs), RequestedEnd: fmtDate(re), P85Factor: 1}

	start := dateOnly(today)
	if rs.After(start) {
		start = rs
	}
	plan := ScheduleCapacity(tickets, resources, ctx, start)
	rep.ScheduleStart = fmtDate(plan.Start)
	rep.Unsized = plan.Unsized
	likely := planFinish(plan)
	rep.LikelyFinish = fmtDate(likely)

	p85 := likely
	if p85Factor > 1 && !math.IsInf(p85Factor, 0) && !math.IsNaN(p85Factor) {
		rep.P85Factor = round2(p85Factor)
		pctx := ctx
		pctx.RefMedianRate = ctx.refRate() * p85Factor
		p85 = planFinish(ScheduleCapacity(tickets, resources, pctx, start))
	}
	rep.P85Finish = fmtDate(p85)

	for _, l := range plan.Lanes {
		rep.RemainingHours += l.Hours
		if l.Tickets > 0 {
			rep.HasPerDay += l.DailyHours
		}
	}
	rep.RemainingHours = round1(rep.RemainingHours)

	if as := ActualStart(tickets); !as.IsZero() {
		rep.ActualStart = fmtDate(as)
		if !rs.IsZero() {
			v := signedBusinessDays(rs, as)
			rep.StartVarianceDays = &v
		}
	}

	if re.IsZero() {
		return rep
	}
	if !likely.IsZero() {
		rep.FinishVarianceDays = signedBusinessDays(re, likely)
		rep.P85FinishVarianceDays = signedBusinessDays(re, p85)
	}
	rep.WorkingDaysLeft = businessDaysBetween(plan.Start, re)
	if rep.WorkingDaysLeft > 0 {
		rep.NeedsPerDay = round1(rep.RemainingHours / float64(rep.WorkingDaysLeft))
	}
	capacity := rep.HasPerDay * float64(rep.WorkingDaysLeft)
	if d := rep.RemainingHours - capacity; d > 0 {
		rep.HoursToCut = round1(d)
	} else {
		rep.SpareHours = round1(-d)
	}
	switch {
	case !likely.IsZero() && likely.After(re):
		rep.Status = ForecastLate
	case !p85.IsZero() && p85.After(re):
		rep.Status = ForecastAtRisk
	default:
		rep.Status = ForecastOnTrack
	}
	return rep
}

// ProjectForecast computes the card for a project on disk: its estimate
// context, Monte Carlo P85 factor and requested dates from the config (cfg may
// be nil).
func ProjectForecast(projectID, projectRoot string, tickets []*ticket.Ticket, cfg *ticket.ProjectConfig, today time.Time) ForecastReport {
	ctx := ProjectEstimateContext(projectRoot, tickets, cfg)
	factor := 1.0
	var resources []ticket.Resource
	reqStart, reqEnd := "", ""
	if cfg != nil {
		factor = MCP85Factor(ProjectMonteCarlo(projectID, projectRoot, tickets, cfg))
		resources = cfg.Resources
		reqStart, reqEnd = cfg.RequestedStart, cfg.EffectiveRequestedEnd()
	}
	return ComputeForecast(tickets, resources, ctx, factor, today, reqStart, reqEnd)
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// fmtShortDate renders YYYY-MM-DD as "Mon Jan 2, 2006" ("—" when empty;
// anything unparsable is escaped).
func fmtShortDate(s string) string {
	if d := parseDate(s); !d.IsZero() {
		return d.Format("Mon Jan 2, 2006")
	}
	if s == "" {
		return "&mdash;"
	}
	return html.EscapeString(s)
}

// fmtVariance renders a business-day variance: "+3 days late", "2 days early",
// "on the day".
func fmtVariance(v int) string {
	switch {
	case v > 0:
		return fmt.Sprintf(`<span style="color:#b91c1c">+%d business day%s late</span>`, v, pluralS(v))
	case v < 0:
		return fmt.Sprintf(`<span style="color:#16a34a">%d business day%s early</span>`, -v, pluralS(-v))
	}
	return `<span style="color:#16a34a">on the day</span>`
}

// forecastStatusBadge is the coloured status pill.
func forecastStatusBadge(s string) string {
	bg, fg := "#dcfce7", "#166534"
	switch s {
	case ForecastAtRisk:
		bg, fg = "#fef3c7", "#92400e"
	case ForecastLate:
		bg, fg = "#fee2e2", "#b91c1c"
	}
	return fmt.Sprintf(`<span class="forecast-status" style="background:%s;color:%s;font-weight:700;font-size:12px;letter-spacing:.5px;padding:3px 10px;border-radius:12px">%s</span>`, bg, fg, html.EscapeString(s))
}

// settingsLink opens the app's Settings from inside the dashboard iframe.
const settingsLink = `<a href="#" onclick="try{parent.document.getElementById('btn-settings').click()}catch(e){};return false" style="color:#1976d2">Settings</a>`

// RenderForecastHTML renders the Schedule vs request card (or the set-dates
// hint when no requested end is set), with the trend chart from history.
func RenderForecastHTML(rep ForecastReport, history []ForecastHistoryEntry) string {
	esc := html.EscapeString
	header := `<h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin:0">Schedule vs request</h3>`
	wrap := func(inner string) string {
		return fmt.Sprintf(`
<div class="forecast-card" style="margin:20px 24px 20px;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:16px 22px">
%s
</div>`, inner)
	}
	if rep.RequestedEnd == "" {
		return wrap(`<div style="display:flex;gap:12px;align-items:baseline">` + header +
			`<span class="forecast-hint" style="font-size:13px;color:#999">Set a requested start and end in ` + settingsLink + ` to see whether the scoped work fits.</span></div>`)
	}

	cell := func(label, value, sub string) string {
		if sub != "" {
			sub = `<div style="font-size:12px;margin-top:2px">` + sub + `</div>`
		}
		return fmt.Sprintf(`<div style="min-width:150px"><div style="font-size:11px;color:#888;text-transform:uppercase;letter-spacing:.4px">%s</div><div style="font-size:14px;font-weight:600;color:#222;margin-top:2px">%s</div>%s</div>`, label, value, sub)
	}

	// Start.
	startSub := ""
	if rep.RequestedStart != "" {
		startSub = `<span style="color:#888">requested ` + fmtShortDate(rep.RequestedStart) + `</span>`
	}
	actual := `<span style="color:#999;font-weight:400">not started</span>`
	if rep.ActualStart != "" {
		actual = fmtShortDate(rep.ActualStart)
		if rep.StartVarianceDays != nil {
			startSub += ` &middot; ` + fmtVariance(*rep.StartVarianceDays)
		}
	}

	likely, p85 := `<span style="color:#999;font-weight:400">no open work</span>`, ""
	likelySub, p85Sub := "", ""
	if rep.LikelyFinish != "" {
		likely = fmtShortDate(rep.LikelyFinish)
		likelySub = fmtVariance(rep.FinishVarianceDays)
		p85 = fmtShortDate(rep.P85Finish)
		p85Sub = fmtVariance(rep.P85FinishVarianceDays)
	}

	var cells strings.Builder
	cells.WriteString(cell("Requested end", fmtShortDate(rep.RequestedEnd), fmt.Sprintf(`<span style="color:#888">%d working day%s left from %s</span>`, rep.WorkingDaysLeft, pluralS(rep.WorkingDaysLeft), esc(fmtNiceDate(rep.ScheduleStart)))))
	cells.WriteString(cell("Actual start", actual, startSub))
	cells.WriteString(cell("Projected finish (likely)", likely, likelySub))
	if rep.LikelyFinish != "" {
		cells.WriteString(cell("Projected finish (P85)", p85, p85Sub))
	}

	// Needs vs has.
	needs := fmt.Sprintf(`%.1fh remaining`, rep.RemainingHours)
	if rep.WorkingDaysLeft > 0 {
		needs += fmt.Sprintf(` &divide; %d day%s = <strong>%.1f h/day</strong> needed vs <strong>%g h/day</strong> available`, rep.WorkingDaysLeft, pluralS(rep.WorkingDaysLeft), rep.NeedsPerDay, rep.HasPerDay)
	} else {
		needs += ` with no working days left before the requested end`
	}
	fit := ""
	if rep.HoursToCut > 0 {
		fit = fmt.Sprintf(` &mdash; <span style="color:#b91c1c;font-weight:600">cut %.1fh</span> (or add capacity) to fit.`, rep.HoursToCut)
	} else if rep.RemainingHours > 0 {
		fit = fmt.Sprintf(` &mdash; <span style="color:#16a34a;font-weight:600">%.1fh spare</span>.`, rep.SpareHours)
		if rep.Status == ForecastLate || rep.Status == ForecastAtRisk {
			fit += ` The hours fit, so the finish is held by predecessors, planned start dates, or one person's load &mdash; see the Load table and Gantt.`
		}
	}
	unsized := ""
	if rep.Unsized > 0 {
		unsized = fmt.Sprintf(` <span style="color:#999">+%d unsized ticket%s not counted.</span>`, rep.Unsized, pluralS(rep.Unsized))
	}

	inner := fmt.Sprintf(`<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">%s%s</div>
<div style="display:flex;flex-wrap:wrap;gap:14px 28px">%s</div>
<p class="forecast-needs" style="font-size:13px;color:#333;margin:12px 0 0">%s%s%s</p>
%s
<p style="font-size:12px;color:#999;margin:8px 0 0">Likely = the capacity-aware schedule from %s. P85 = the same schedule with code tickets at the Monte Carlo P85 rate (&times;%.2f). ON TRACK: P85 by the requested end; AT RISK: likely on time, P85 late; LATE: likely late. Business days, flat daily capacity (no holidays).</p>`,
		header, forecastStatusBadge(rep.Status), cells.String(), needs, fit, unsized,
		renderForecastTrend(history), esc(fmtNiceDate(rep.ScheduleStart)), rep.P85Factor)
	return wrap(inner)
}

// renderForecastTrend draws the history as a small inline SVG: x = entry date,
// y = finish date; likely and P85 lines plus a dashed requested-end line.
// Empty with fewer than 2 entries.
func renderForecastTrend(history []ForecastHistoryEntry) string {
	type pt struct{ x, likely, p85, req time.Time }
	var pts []pt
	for _, e := range history {
		x := parseDate(e.Date)
		if x.IsZero() {
			continue
		}
		pts = append(pts, pt{x, parseDate(e.LikelyFinish), parseDate(e.P85Finish), parseDate(e.RequestedEnd)})
	}
	if len(pts) < 2 {
		return ""
	}
	var yMin, yMax time.Time
	for _, p := range pts {
		for _, d := range []time.Time{p.likely, p.p85, p.req} {
			if d.IsZero() {
				continue
			}
			if yMin.IsZero() || d.Before(yMin) {
				yMin = d
			}
			if d.After(yMax) {
				yMax = d
			}
		}
	}
	if yMin.IsZero() {
		return ""
	}
	const w, h, padL, padR, padT, padB = 560.0, 150.0, 92.0, 12.0, 10.0, 22.0
	xMin, xMax := pts[0].x, pts[len(pts)-1].x
	xSpan := xMax.Sub(xMin).Hours()
	ySpan := yMax.Sub(yMin).Hours()
	xPos := func(d time.Time) float64 {
		if xSpan <= 0 {
			return padL + (w-padL-padR)/2
		}
		return padL + d.Sub(xMin).Hours()/xSpan*(w-padL-padR)
	}
	yPos := func(d time.Time) float64 {
		if ySpan <= 0 {
			return padT + (h-padT-padB)/2
		}
		return padT + (1-d.Sub(yMin).Hours()/ySpan)*(h-padT-padB)
	}
	line := func(get func(pt) time.Time, color, dash, class string) string {
		var sb strings.Builder
		var coords []string
		for _, p := range pts {
			if d := get(p); !d.IsZero() {
				coords = append(coords, fmt.Sprintf("%.1f,%.1f", xPos(p.x), yPos(d)))
			}
		}
		if len(coords) == 0 {
			return ""
		}
		fmt.Fprintf(&sb, `<polyline class="%s" points="%s" fill="none" stroke="%s" stroke-width="2"%s/>`, class, strings.Join(coords, " "), color, dash)
		if dash == "" {
			for _, c := range coords {
				xy := strings.SplitN(c, ",", 2)
				fmt.Fprintf(&sb, `<circle cx="%s" cy="%s" r="2.5" fill="%s"/>`, xy[0], xy[1], color)
			}
		}
		return sb.String()
	}
	label := func(x, y float64, anchor, s string) string {
		return fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="10" fill="#888" text-anchor="%s">%s</text>`, x, y, anchor, html.EscapeString(s))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `<div style="margin-top:12px"><div style="font-size:11px;color:#888;text-transform:uppercase;letter-spacing:.4px;margin-bottom:4px">Finish-date trend</div>`)
	fmt.Fprintf(&sb, `<svg class="forecast-trend" viewBox="0 0 %g %g" width="100%%" style="max-width:%gpx;height:auto" role="img" aria-label="Projected finish over time">`, w, h, w)
	fmt.Fprintf(&sb, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#e5e7eb"/>`, padL, h-padB, w-padR, h-padB)
	fmt.Fprintf(&sb, `<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#e5e7eb"/>`, padL, padT, padL, h-padB)
	sb.WriteString(label(padL-6, yPos(yMax)+4, "end", yMax.Format("Jan 2, 2006")))
	if ySpan > 0 {
		sb.WriteString(label(padL-6, yPos(yMin)+4, "end", yMin.Format("Jan 2, 2006")))
	}
	sb.WriteString(label(padL, h-6, "start", xMin.Format("Jan 2")))
	sb.WriteString(label(w-padR, h-6, "end", xMax.Format("Jan 2")))
	sb.WriteString(line(func(p pt) time.Time { return p.req }, "#6b7280", ` stroke-dasharray="5 4"`, "trend-requested"))
	sb.WriteString(line(func(p pt) time.Time { return p.p85 }, "#f59e0b", "", "trend-p85"))
	sb.WriteString(line(func(p pt) time.Time { return p.likely }, "#1976d2", "", "trend-likely"))
	sb.WriteString(`</svg>`)
	sb.WriteString(`<div style="font-size:11px;color:#666;display:flex;gap:14px"><span><span style="color:#1976d2">&#9644;</span> Likely</span><span><span style="color:#f59e0b">&#9644;</span> P85</span><span><span style="color:#6b7280">- -</span> Requested end</span></div></div>`)
	return sb.String()
}
