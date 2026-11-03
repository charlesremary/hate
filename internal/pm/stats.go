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

// Project stats: the PM Dashboard's live numbers (HATE-q4h5), also served as
// GET /stats and over MCP. Every number is recomputed from the ticket files
// on each view; a trend compares it with the same number "then" (a week ago,
// or the end of the previous block on a block project), rebuilt from the
// tickets' own dates (created, started, finished, force-closed, time-entry
// dates), so no stored history is needed. Read-only.

// Stat is one number with its comparison.
type Stat struct {
	Value float64 `json:"value"`
	// Prev is the same number then; HasPrev false when there's nothing to
	// compare with (no data then).
	Prev    float64 `json:"prev"`
	HasPrev bool    `json:"has_prev"`
	// N is the sample size behind a ratio or average (0 = no data).
	N int `json:"n,omitempty"`
}

// ProjectStats is the stats panel.
type ProjectStats struct {
	GeneratedAt string `json:"generated_at"`
	// Window is what "then" and "this period" mean: "week" or "block".
	Window      string `json:"window"`
	WindowLabel string `json:"window_label"` // "this week", "this block (Nov 2-13)"
	PeriodStart string `json:"period_start"` // YYYY-MM-DD
	PrevStart   string `json:"prev_start"`   // start of the period before

	// Delivery.
	PercentComplete Stat    `json:"percent_complete"` // hours-weighted, 0..100
	RemainingHours  Stat    `json:"remaining_hours"`  // estimated hours not done
	TicketsDone     int     `json:"tickets_done"`
	TicketsTotal    int     `json:"tickets_total"`
	EstimatedTotal  float64 `json:"estimated_total"`

	// Estimate accuracy: logged / estimated on finished tickets (1.0 = on
	// estimate, above = took longer). Wrap per ticket; code per finished
	// feature (CFP x rate vs functional hours).
	AccuracyWrap Stat `json:"accuracy_wrap"`
	AccuracyCode Stat `json:"accuracy_code"`

	// Flow.
	Finished      Stat    `json:"finished"`       // tickets finished in the period (prev = the period before)
	FinishedHours float64 `json:"finished_hours"` // their estimated hours
	CycleDays     Stat    `json:"cycle_days"`     // avg calendar days start -> done, last 28 days (prev = the 28 before)
	BlockedNow    int     `json:"blocked_now"`
	LongestBlock  string  `json:"longest_blocked,omitempty"` // ticket id
	LongestDays   int     `json:"longest_blocked_days,omitempty"`
	ScopeAdded    int     `json:"scope_added"` // tickets added since the baseline (or in the period without one)
	ScopeHours    float64 `json:"scope_added_hours"`
	ScopeSince    string  `json:"scope_since"` // YYYY-MM-DD the scope count starts from

	// Tests: pass rate over cases with a result (every committed ticket).
	TestPassRate Stat `json:"test_pass_rate"` // 0..100; N = cases with a result
	TestUntested int  `json:"test_untested"`

	// Rework (hate 3.0): hours logged on rework tickets as a share of all
	// hours logged (N = rework tickets), by category and by who introduced it.
	ReworkRate       Stat               `json:"rework_rate"`
	ReworkHours      float64            `json:"rework_hours"`
	ReworkByCategory map[string]ReworkN `json:"rework_by_category"`
	ReworkAgent      int                `json:"rework_agent"`
	ReworkHuman      int                `json:"rework_human"`

	// Force-closed out of scope, by category (descoped, duplicate, wont_do).
	Descoped map[string]int `json:"descoped"`
}

// ReworkN is one rework category's tickets and logged hours.
type ReworkN struct {
	Tickets int     `json:"tickets"`
	Hours   float64 `json:"hours"`
}

// ticketDates are the moments the stats need, from a ticket's own record.
type ticketDates struct {
	created, started, done, cancelled time.Time
}

func datesOf(t *ticket.Ticket) ticketDates {
	var d ticketDates
	d.created = parseISO(t.CreatedAt)
	if t.ActualStartDate != nil {
		d.started = parseDate(*t.ActualStartDate)
	}
	for _, a := range t.Activity {
		ts := parseISO(a.Timestamp)
		switch {
		case a.Action == "force_closed":
			// Force-closed as done is finished work (hate 3.0); the other
			// categories leave scope.
			if isCancelled(t) && d.cancelled.IsZero() {
				d.cancelled = ts
			} else if !isCancelled(t) && d.done.IsZero() {
				d.done = ts
			}
		case a.Action == "status_changed" && (strings.HasSuffix(a.Detail, "-> complete") || strings.HasSuffix(a.Detail, "-> closed")):
			if d.done.IsZero() {
				d.done = ts
			}
		case a.Action == "status_changed" && strings.HasSuffix(a.Detail, "-> in_progress"):
			if d.started.IsZero() {
				d.started = ts
			}
		}
	}
	if isComplete(t) && d.done.IsZero() && t.ClosedAt != nil {
		d.done = parseISO(*t.ClosedAt)
	}
	if !isComplete(t) {
		d.done = time.Time{} // reopened by a demote, or force-closed
	}
	if isCancelled(t) && d.cancelled.IsZero() && t.ClosedAt != nil {
		d.cancelled = parseISO(*t.ClosedAt)
	}
	return d
}

func parseISO(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, f := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func before(t, at time.Time) bool { return !t.IsZero() && !t.After(at) }

// loggedBy sums a ticket's time entries dated on or before at.
func loggedBy(t *ticket.Ticket, at time.Time) float64 {
	day := fmtDate(at)
	var h float64
	for _, te := range t.TimeEntries {
		if te.Date == "" || te.Date <= day {
			h += te.Hours
		}
	}
	return h
}

// deliveryAt is % complete, remaining and wrap accuracy as of at.
func deliveryAt(tickets []*ticket.Ticket, ctx EstimateContext, dates map[string]ticketDates, at time.Time) (pct, remaining, total float64, done, count int, wrapLogged, wrapEst float64, wrapN int) {
	var doneH float64
	for _, t := range tickets {
		d := dates[t.ID]
		if ticket.IsBacklog(t) || ctx.idx.hasChildren[t.ID] || !before(d.created, at) || before(d.cancelled, at) {
			continue
		}
		h, _ := EstimatedHours(t, ctx)
		if h < 0 {
			h = 0
		}
		total += h
		count++
		if before(d.done, at) {
			doneH += h
			done++
			if est, ok := WrapAllotment(t, ctx); ok && est > 0 && !ticket.IsRework(t) {
				if l := loggedBy(t, at); l > 0 {
					wrapLogged += l
					wrapEst += est
					wrapN++
				}
			}
		}
	}
	if total > 0 {
		pct = round1(doneH / total * 100)
	} else if count > 0 {
		pct = round1(float64(done) / float64(count) * 100)
	}
	return pct, total - doneH, total, done, count, wrapLogged, wrapEst, wrapN
}

// ComputeStats builds the stats panel. blocks may be empty (no blocks: the
// period is the last 7 days); baselineDate is "" without a baseline.
func ComputeStats(tickets []*ticket.Ticket, ctx EstimateContext, blocks []Block, baselineDate string, now time.Time) ProjectStats {
	ctx = ctx.prepared()
	now = now.UTC()
	today := dateOnly(now)
	st := ProjectStats{GeneratedAt: now.Format(time.RFC3339), Window: "week", WindowLabel: "this week"}

	// The period: the current block, else the last 7 days.
	periodStart, prevStart := today.AddDate(0, 0, -6), today.AddDate(0, 0, -13)
	for i, b := range blocks {
		if b.State == BlockCurrent {
			periodStart = parseDate(b.Start)
			prevStart = periodStart.AddDate(0, 0, -7*blockWeeksOf(b))
			if i > 0 {
				prevStart = parseDate(blocks[i-1].Start)
			}
			st.Window, st.WindowLabel = "block", "this block ("+b.Dates+")"
			break
		}
	}
	st.PeriodStart, st.PrevStart = fmtDate(periodStart), fmtDate(prevStart)
	then := periodStart.Add(-time.Second) // end of the previous period

	dates := make(map[string]ticketDates, len(tickets))
	for _, t := range tickets {
		dates[t.ID] = datesOf(t)
	}

	// Delivery, now and then.
	pct, rem, total, done, count, wl, we, wn := deliveryAt(tickets, ctx, dates, now)
	ppct, prem, _, _, pcount, pwl, pwe, _ := deliveryAt(tickets, ctx, dates, then)
	st.PercentComplete = Stat{Value: pct, Prev: ppct, HasPrev: pcount > 0}
	st.RemainingHours = Stat{Value: round1(rem), Prev: round1(prem), HasPrev: pcount > 0}
	st.TicketsDone, st.TicketsTotal, st.EstimatedTotal = done, count, round1(total)
	if we > 0 {
		st.AccuracyWrap = Stat{Value: round2(wl / we), N: wn}
		if pwe > 0 {
			st.AccuracyWrap.Prev, st.AccuracyWrap.HasPrev = round2(pwl/pwe), true
		}
	}
	// Code: finished features, from the existing variance report.
	var cs, ce float64
	var cn int
	for _, f := range ComputeEstimateVariance(tickets, ctx).Features {
		if f.EstimateHours > 0 {
			cs += f.SpentHours
			ce += f.EstimateHours
			cn++
		}
	}
	if ce > 0 {
		st.AccuracyCode = Stat{Value: round2(cs / ce), N: cn}
	}

	// Flow: finished this period vs the period before; cycle time over the
	// last 28 days vs the 28 before.
	var fin, pfin int
	var cyc, pcyc float64
	var cn28, pcn28 int
	c0, c1 := today.AddDate(0, 0, -27), today.AddDate(0, 0, -55)
	for _, t := range tickets {
		d := dates[t.ID]
		if d.done.IsZero() || ticket.IsBacklog(t) || ctx.idx.hasChildren[t.ID] {
			continue
		}
		dd := dateOnly(d.done)
		switch {
		case !dd.Before(periodStart):
			fin++
			h, _ := EstimatedHours(t, ctx)
			st.FinishedHours += math.Max(h, 0)
		case !dd.Before(prevStart):
			pfin++
		}
		if !d.started.IsZero() && !d.done.Before(d.started) {
			days := d.done.Sub(d.started).Hours() / 24
			switch {
			case !dd.Before(c0):
				cyc += days
				cn28++
			case !dd.Before(c1):
				pcyc += days
				pcn28++
			}
		}
	}
	st.Finished = Stat{Value: float64(fin), Prev: float64(pfin), HasPrev: true}
	st.FinishedHours = round1(st.FinishedHours)
	if cn28 > 0 {
		st.CycleDays = Stat{Value: round1(cyc / float64(cn28)), N: cn28}
		if pcn28 > 0 {
			st.CycleDays.Prev, st.CycleDays.HasPrev = round1(pcyc/float64(pcn28)), true
		}
	}

	for _, b := range ComputeBlocked(tickets) {
		st.BlockedNow++
		if s := parseISO(b.Since); !s.IsZero() {
			if days := int(today.Sub(dateOnly(s)).Hours() / 24); st.LongestBlock == "" || days > st.LongestDays {
				st.LongestBlock, st.LongestDays = b.TicketID, days
			}
		}
	}

	// Scope added since the baseline (or in this period without one).
	since := periodStart
	if bd := parseDate(baselineDate); !bd.IsZero() {
		since = bd.AddDate(0, 0, 1)
	}
	st.ScopeSince = fmtDate(since)
	for _, t := range tickets {
		d := dates[t.ID]
		if ticket.IsBacklog(t) || isCancelled(t) || d.created.IsZero() || dateOnly(d.created).Before(since) {
			continue
		}
		st.ScopeAdded++
		h, _ := EstimatedHours(t, ctx)
		st.ScopeHours += math.Max(h, 0)
	}
	st.ScopeHours = round1(st.ScopeHours)

	// Tests.
	var pass, fail int
	for _, t := range tickets {
		if ticket.IsBacklog(t) || isCancelled(t) {
			continue
		}
		for _, c := range t.TestCases {
			switch c.Status {
			case "pass":
				pass++
			case "fail":
				fail++
			default:
				st.TestUntested++
			}
		}
	}
	if pass+fail > 0 {
		st.TestPassRate = Stat{Value: round1(float64(pass) / float64(pass+fail) * 100), N: pass + fail}
	}

	// Rework: share of logged hours, now and at the end of the last period.
	st.ReworkByCategory = map[string]ReworkN{}
	st.Descoped = map[string]int{}
	var allNow, rwNow, allThen, rwThen float64
	for _, t := range tickets {
		if isCancelled(t) {
			cat := t.CloseCategory
			if cat == "" {
				cat = "descoped"
			}
			st.Descoped[cat]++
			continue
		}
		if ticket.IsBacklog(t) {
			continue
		}
		hNow, hThen := loggedBy(t, now), loggedBy(t, then)
		allNow += hNow
		allThen += hThen
		if !ticket.IsRework(t) {
			continue
		}
		rwNow += hNow
		rwThen += hThen
		st.ReworkRate.N++
		c := st.ReworkByCategory[t.ReworkCategory]
		c.Tickets++
		c.Hours = round1(c.Hours + hNow)
		st.ReworkByCategory[t.ReworkCategory] = c
		switch t.IntroducedBy {
		case "agent":
			st.ReworkAgent++
		case "human":
			st.ReworkHuman++
		}
	}
	st.ReworkHours = round1(rwNow)
	if allNow > 0 {
		st.ReworkRate.Value = round1(rwNow / allNow * 100)
	}
	if allThen > 0 {
		st.ReworkRate.Prev, st.ReworkRate.HasPrev = round1(rwThen/allThen*100), true
	}
	return st
}

func blockWeeksOf(b Block) int {
	days := parseDate(b.End).Sub(parseDate(b.Start)).Hours() / 24
	return int(math.Round((days + 3) / 7))
}

// ---- HTML ----

// trend renders "▲ 12 pts" style text. goodUp says whether an increase is
// good (green) or bad (red); neutral colors it grey either way.
func trend(s Stat, unit string, goodUp, neutral bool, digits int) string {
	return trendWas(s, unit, strings.TrimSpace(unit), goodUp, neutral, digits)
}

// trendWas is trend with a different unit for the earlier value ("+12 pts
// (was 40%)").
func trendWas(s Stat, unit, wasUnit string, goodUp, neutral bool, digits int) string {
	if !s.HasPrev {
		return `<span class="st-trend">no earlier data</span>`
	}
	d := s.Value - s.Prev
	if math.Abs(d) < math.Pow(10, -float64(digits))/2 {
		return `<span class="st-trend">no change</span>`
	}
	arrow, cls := "▲", "st-good"
	if d < 0 {
		arrow = "▼"
	}
	if (d > 0) != goodUp {
		cls = "st-bad"
	}
	if neutral {
		cls = ""
	}
	return fmt.Sprintf(`<span class="st-trend %s">%s %s%s <span class="st-was">(was %s)</span></span>`,
		cls, arrow, fmtNum(math.Abs(d), digits), unit, fmtNum(s.Prev, digits)+wasUnit)
}

func fmtNum(v float64, digits int) string {
	s := fmt.Sprintf("%.*f", digits, v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// RenderStatsHTML is the stats panel. Self-styled for either dashboard.
func RenderStatsHTML(st ProjectStats, scheduleAnchor string) string {
	esc := html.EscapeString
	card := func(label, value, sub string) string {
		info := ""
		if tip := statTips[strings.SplitN(label, " ", 2)[0]]; tip != "" {
			if strings.HasPrefix(label, "Accuracy: code") {
				tip = statTips["AccuracyCode"]
			}
			info = fmt.Sprintf(`<span class="st-i" tabindex="0" role="note" aria-label="%s">i<span class="st-tip">%s</span></span>`, esc(tip), esc(tip))
		}
		return fmt.Sprintf(`<div class="st-card"><div class="st-label">%s%s</div><div class="st-value">%s</div><div class="st-sub">%s</div></div>`, label, info, value, sub)
	}
	ratio := func(s Stat, what string) string {
		if s.N == 0 {
			return card("Accuracy: "+what, `<span class="st-none">no data yet</span>`, "needs finished, estimated tickets with time logged")
		}
		tone := "st-good"
		switch {
		case s.Value > 1.2:
			tone = "st-bad"
		case s.Value > 1.05 || s.Value < 0.8:
			tone = "st-warn"
		}
		sub := fmt.Sprintf("actual / estimate, %d %s", s.N, map[bool]string{true: "ticket", false: "tickets"}[s.N == 1])
		if what == "code" {
			sub = fmt.Sprintf("actual / estimate, %d finished %s", s.N, map[bool]string{true: "feature", false: "features"}[s.N == 1])
		}
		t := ""
		if s.HasPrev {
			t = "<br>" + trend(s, "x", false, true, 2)
		}
		return card("Accuracy: "+what, fmt.Sprintf(`<span class="%s">%sx</span>`, tone, fmtNum(s.Value, 2)), sub+t)
	}

	var b strings.Builder
	b.WriteString(`<style>
.st-wrap { padding: 16px 24px 4px; }
.st-head { font-size: 12px; color: #64748b; letter-spacing: .06em; text-transform: uppercase; margin-bottom: 8px; }
.st-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 12px; }
.st-card { background: #fff; border-radius: 8px; padding: 14px 16px; box-shadow: 0 1px 4px rgba(0,0,0,.08); }
.st-label { font-size: 11px; color: #64748b; text-transform: uppercase; letter-spacing: .05em; }
.st-value { font-size: 24px; font-weight: 700; margin: 4px 0 2px; color: #0f172a; }
.st-sub { font-size: 12px; color: #64748b; line-height: 1.4; }
.st-trend { font-size: 12px; color: #64748b; }
.st-was { color: #94a3b8; }
.st-good { color: #15803d; } .st-bad { color: #b91c1c; } .st-warn { color: #b45309; }
.st-none { font-size: 15px; color: #94a3b8; font-weight: 400; }
.st-sub a { color: #1565c0; }
.st-card { position: relative; }
.st-i { display: inline-flex; align-items: center; justify-content: center; width: 15px; height: 15px; margin-left: 6px;
  border: 1px solid #94a3b8; border-radius: 50%; font: italic 600 10px Georgia, serif; color: #64748b; cursor: help;
  text-transform: none; letter-spacing: 0; vertical-align: 1px; }
.st-i:hover, .st-i:focus { border-color: #1565c0; color: #1565c0; outline: none; }
.st-tip { display: none; position: absolute; top: 34px; left: 6px; right: 6px; z-index: 50; padding: 10px 12px;
  background: #0f172a; color: #f1f5f9; border-radius: 6px; font: 400 12px/1.45 -apple-system, BlinkMacSystemFont, sans-serif;
  text-transform: none; letter-spacing: 0; box-shadow: 0 6px 18px rgba(0,0,0,.25); }
.st-i:hover .st-tip, .st-i:focus .st-tip { display: block; }
</style>
<div class="st-wrap"><div class="st-head">Project stats &middot; `)
	b.WriteString(esc(st.WindowLabel))
	b.WriteString(` &middot; trends vs the period before</div><div class="st-grid">`)

	// Delivery.
	b.WriteString(card("Complete",
		fmt.Sprintf(`<span class="st-good">%s%%</span>`, fmtNum(st.PercentComplete.Value, 1)),
		fmt.Sprintf("%d of %d tickets, hours-weighted<br>%s", st.TicketsDone, st.TicketsTotal, trendWas(st.PercentComplete, " pts", "%", true, false, 1))))
	rem := fmt.Sprintf("of %sh estimated", fmtNum(st.EstimatedTotal, 1))
	if scheduleAnchor != "" {
		rem += fmt.Sprintf(` &middot; <a href="%s">finish date</a>`, esc(scheduleAnchor))
	}
	b.WriteString(card("Remaining", fmtNum(st.RemainingHours.Value, 1)+"h", rem+"<br>"+trend(st.RemainingHours, "h", false, false, 1)))

	// Accuracy.
	b.WriteString(ratio(st.AccuracyWrap, "wrap"))
	b.WriteString(ratio(st.AccuracyCode, "code"))

	// Flow.
	b.WriteString(card("Finished "+esc(strings.SplitN(st.WindowLabel, " (", 2)[0]),
		fmtNum(st.Finished.Value, 0),
		fmt.Sprintf("tickets, %sh estimated<br>%s", fmtNum(st.FinishedHours, 1), trend(st.Finished, "", true, false, 0))))
	if st.CycleDays.N > 0 {
		b.WriteString(card("Cycle time", fmtNum(st.CycleDays.Value, 1)+" days",
			fmt.Sprintf("start to done, avg of %d in the last 28 days<br>%s", st.CycleDays.N, trend(st.CycleDays, " days", false, false, 1))))
	} else {
		b.WriteString(card("Cycle time", `<span class="st-none">no data yet</span>`, "no tickets finished in the last 28 days"))
	}
	blocked := "nothing blocked"
	if st.BlockedNow > 0 {
		blocked = fmt.Sprintf("longest: %s, %d %s", esc(st.LongestBlock), st.LongestDays, map[bool]string{true: "day", false: "days"}[st.LongestDays == 1])
	}
	val := fmtNum(float64(st.BlockedNow), 0)
	if st.BlockedNow > 0 {
		val = `<span class="st-bad">` + val + `</span>`
	}
	b.WriteString(card("Blocked now", val, blocked))
	b.WriteString(card("Scope added", fmtNum(float64(st.ScopeAdded), 0),
		fmt.Sprintf("tickets (%sh) since %s", fmtNum(st.ScopeHours, 1), esc(st.ScopeSince))))

	// Tests.
	if st.TestPassRate.N > 0 {
		tone := "st-good"
		if st.TestPassRate.Value < 90 {
			tone = "st-warn"
		}
		b.WriteString(card("Tests passing", fmt.Sprintf(`<span class="%s">%s%%</span>`, tone, fmtNum(st.TestPassRate.Value, 1)),
			fmt.Sprintf("of %d run, %d not run yet", st.TestPassRate.N, st.TestUntested)))
	} else {
		b.WriteString(card("Tests passing", `<span class="st-none">no results</span>`, fmt.Sprintf("%d test cases not run yet", st.TestUntested)))
	}
	// Rework.
	if st.ReworkRate.N > 0 {
		top, topN := "", 0
		for c, n := range st.ReworkByCategory {
			if n.Tickets > topN || (n.Tickets == topN && c < top) {
				top, topN = c, n.Tickets
			}
		}
		tone := "st-good"
		switch {
		case st.ReworkRate.Value >= 20:
			tone = "st-bad"
		case st.ReworkRate.Value >= 10:
			tone = "st-warn"
		}
		b.WriteString(card("Rework", fmt.Sprintf(`<span class="%s">%s%%</span>`, tone, fmtNum(st.ReworkRate.Value, 1)),
			fmt.Sprintf("of hours logged: %sh on %d %s<br>most: %s (%d) &middot; agent %d, human %d<br>%s",
				fmtNum(st.ReworkHours, 1), st.ReworkRate.N, map[bool]string{true: "ticket", false: "tickets"}[st.ReworkRate.N == 1],
				esc(ticket.ReworkCategoryLabels[top]), topN, st.ReworkAgent, st.ReworkHuman,
				trend(st.ReworkRate, " pts", false, false, 1))))
	} else {
		b.WriteString(card("Rework", `<span class="st-good">0%</span>`, "no rework tickets"))
	}
	if n := st.Descoped["descoped"] + st.Descoped["duplicate"] + st.Descoped["wont_do"]; n > 0 {
		b.WriteString(card("Closed unfinished", fmtNum(float64(n), 0),
			fmt.Sprintf("descoped %d, duplicate %d, won't do %d", st.Descoped["descoped"], st.Descoped["duplicate"], st.Descoped["wont_do"])))
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// statTips explain each card (the ⓘ), keyed by the label's first word.
var statTips = map[string]string{
	"Complete": "Estimated hours of finished tickets divided by estimated hours of every ticket in scope, so a big ticket counts more than a small one. " +
		"Backlog and force-closed tickets are left out. The arrow compares with the end of last week (or the previous block).",
	"Remaining": "Estimated hours of the tickets not finished yet. It's the estimate, not hours still to log: a ticket counts in full until it's done.",
	"Accuracy:": "Wrap work (config and non-functional tickets, estimated by hand in hours): hours actually logged divided by hours estimated, " +
		"over every finished wrap ticket with time logged. 1.0x is on estimate, 1.5x took 50% longer, 0.5x took half as long.",
	"AccuracyCode": "Code work, per finished feature: hours logged on its code tickets divided by the feature's estimate (its CFP times the hours-per-CFP rate). " +
		"A feature counts once all its code tickets are done. Above 1.0x means the code took longer than the CFP estimate.",
	"Finished": "Tickets that reached complete or closed in this period (this week, or this block on a block project), and their estimated hours. " +
		"The arrow compares with the period before.",
	"Cycle": "Average calendar days from starting a ticket (in progress) to finishing it, over tickets finished in the last 28 days. Lower is faster; " +
		"the arrow compares with the 28 days before.",
	"Blocked": "Tickets in the blocked state right now, and the one that has been blocked the longest.",
	"Scope": "Tickets created since the baseline was taken (or this week, without a baseline), backlog excluded. Steady growth here is scope creep.",
	"Tests": "Of the test cases that have a result, the share that passed. Cases not run yet are counted separately.",
	"Rework": "Hours logged on rework tickets (work redone after a ticket was closed) as a share of all hours logged. Shows the most common cause " +
		"and who introduced the problems: an agent or a person. Rework is kept out of estimate accuracy and calibration.",
	"Closed": "Tickets force-closed without being delivered: descoped, duplicate or won't do. They're left out of every other number. " +
		"(Force-closed as done counts as finished.)",
}
