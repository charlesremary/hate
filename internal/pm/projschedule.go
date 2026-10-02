// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"hate/internal/ticket"
)

// A projected schedule is a "floating" plan: rather than a committed baseline,
// it forward-schedules the open tickets from a start date (default today),
// entirely in memory. Nothing is written and no dates are stored — open it
// tomorrow and it slides a day. This lets a project with no real dates still
// produce a Gantt / draw.io artifact, clearly labelled as a projection.
//
// The projection is capacity-aware and hour-granular. Each person (a "lane")
// works their ready tickets one at a time, in order — priority, then dependency
// work order, then ID — burning their daily hours. Several small tickets can
// share a day; there is no whole-day minimum. Only humans consume capacity.
// Predecessors are finish-to-start across lanes, and an explicit
// planned_start_date is a floor.
//
// Time is measured in business days from the start (a float: 1.5 = midway
// through the second business day). A ticket of H hours on a lane with D daily
// hours takes H/D days. Each lane keeps the time it is next free; a ticket
// starts at the latest of that, its predecessors' ends, and its floor.

// UnsizedPlaceholderHours is the placeholder an unsized ticket (0 estimated
// hours) gets in the capacity schedule, so it still appears.
const UnsizedPlaceholderHours = 0.25

// UnassignedLane is the lane key for work with no owner when it can't default
// to a single resource.
const UnassignedLane = "unassigned"

// schedEps absorbs float noise when comparing schedule times.
const schedEps = 1e-9

// ---------------------------------------------------------------------------
// Business-day and ordering helpers
// ---------------------------------------------------------------------------

// isWeekend reports whether d falls on Saturday or Sunday.
func isWeekend(d time.Time) bool {
	return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
}

// nextWeekday advances d by one day, skipping Saturday and Sunday.
func nextWeekday(d time.Time) time.Time {
	d = d.AddDate(0, 0, 1)
	for isWeekend(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// alignToWeekday returns d if it's a weekday, otherwise the next weekday.
func alignToWeekday(d time.Time) time.Time {
	for isWeekend(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// businessDaysBetween counts inclusive business days from start to end (skips Sat/Sun).
func businessDaysBetween(start, end time.Time) int {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	n := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !isWeekend(d) {
			n++
		}
	}
	return n
}

// dateOnly truncates t to its calendar date at UTC midnight.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// priorityRank for sort: critical=0, high=1, medium=2, low=3, unknown=4.
func priorityRank(p string) int {
	switch p {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// ganttStatus maps a ticket status to the coarse status the Gantt colours by.
func ganttStatus(s string) string {
	switch s {
	case "complete", "closed":
		return "complete"
	case "blocked":
		return "blocked"
	case "not_started":
		return "not_started"
	default:
		return "in_progress"
	}
}

// ownerOf returns a task owner from assignee, falling back to creator.
func ownerOf(t *ticket.Ticket) string {
	if t.Assignee != nil && *t.Assignee != "" {
		return *t.Assignee
	}
	return t.Creator
}

// isOpenWork reports whether a ticket still needs its owner's hours: committed
// (not cancelled or backlog) and not yet past the build stage. dev_complete and
// later count as done here; QA time burns the separate QA pool.
func isOpenWork(t *ticket.Ticket) bool {
	return !IsFunctionalDoneStatus(t.Status) && !isCancelled(t) && !ticket.IsBacklog(t)
}

// remainingHours is the capacity a ticket still needs: its estimate minus the
// hours already logged. Converted / legacy effort (days x 8, far above real
// hours) counts as no estimate, as in the COSMIC estimate. A ticket already at
// or past its estimate but still open gets 0 (the caller treats it as unsized).
func remainingHours(t *ticket.Ticket, ctx EstimateContext) float64 {
	h, src := EstimatedHours(t, ctx)
	if src == SourceConverted || src == SourceLegacy {
		return 0
	}
	if r := h - cosmicLoggedHours(t); r > 0 {
		return r
	}
	return 0
}

// ---------------------------------------------------------------------------
// Capacity plan
// ---------------------------------------------------------------------------

// CapacityLane is one person's (or the unassigned) queue of work.
type CapacityLane struct {
	Key        string  `json:"key"`         // resource email, assignee string, or "unassigned"
	Label      string  `json:"label"`       // display name
	DailyHours float64 `json:"daily_hours"` // capacity per business day
	IsResource bool    `json:"is_resource"` // a configured project resource
	Hours      float64 `json:"hours"`       // Σ estimated hours (placeholders excluded)
	Tickets    int     `json:"tickets"`     // open tickets in the lane
	Unsized    int     `json:"unsized"`     // of which unsized (placeholder hours)
	FreeAt     float64 `json:"free_at"`     // business days from start when the lane's work ends
}

// CapacityItem is one scheduled ticket.
type CapacityItem struct {
	Ticket  *ticket.Ticket
	Lane    string  // "" for a parent (container; consumes no capacity)
	Hours   float64 // hours scheduled (estimate, or the unsized placeholder)
	Unsized bool
	Parent  bool
	Start   float64 // business days from the plan start
	End     float64
	StartD  time.Time // business day the first hour falls on
	EndD    time.Time // business day the last hour falls on
	Days    int       // StartD..EndD inclusive, in business days
}

// CapacityPlan is the output of ScheduleCapacity.
type CapacityPlan struct {
	Start   time.Time
	Lanes   []*CapacityLane // resources first (config order), then other lanes by key
	Items   []*CapacityItem // open in-scope tickets, in input order
	Unsized int             // open tickets with 0 estimated hours
	Done    int             // in-scope tickets already complete/closed (not scheduled)
}

// dayAt returns the business-day date of schedule time t (whole days from start).
func (p *CapacityPlan) dayAt(idx int) time.Time {
	d := p.Start
	for i := 0; i < idx; i++ {
		d = nextWeekday(d)
	}
	return d
}

// EndDate is the business day on which the last hour ending at time t falls
// (the start day when t is 0).
func (p *CapacityPlan) EndDate(t float64) time.Time {
	idx := int(math.Ceil(t-schedEps)) - 1
	if idx < 0 {
		idx = 0
	}
	return p.dayAt(idx)
}

// Lane returns the lane with key k, or nil.
func (p *CapacityPlan) Lane(k string) *CapacityLane {
	for _, l := range p.Lanes {
		if l.Key == k {
			return l
		}
	}
	return nil
}

// findResource matches an assignee to a configured resource by email, git user,
// or name (case-insensitive).
func findResource(assignee string, resources []ticket.Resource) (ticket.Resource, bool) {
	a := strings.TrimSpace(assignee)
	if a == "" {
		return ticket.Resource{}, false
	}
	for _, r := range resources {
		if strings.EqualFold(r.Email, a) {
			return r, true
		}
	}
	for _, r := range resources {
		if (r.GitUser != "" && strings.EqualFold(r.GitUser, a)) || (r.Name != "" && strings.EqualFold(r.Name, a)) {
			return r, true
		}
	}
	return ticket.Resource{}, false
}

// laneResolver assigns tickets to lanes: a configured resource (matched by
// email, git user, or name); unassigned work defaults to the single resource
// when there is exactly one, otherwise to the "unassigned" lane; any other
// assignee string is its own lane. Non-resource lanes run at the default daily
// hours.
type laneResolver struct {
	resources []ticket.Resource
	lanes     map[string]*CapacityLane
	order     []string
}

func newLaneResolver(resources []ticket.Resource) *laneResolver {
	lr := &laneResolver{resources: resources, lanes: map[string]*CapacityLane{}}
	for _, r := range resources {
		if r.Email == "" || lr.lanes[r.Email] != nil {
			continue
		}
		label := r.Name
		if label == "" {
			label = r.Email
		}
		lr.lanes[r.Email] = &CapacityLane{Key: r.Email, Label: label, DailyHours: r.EffectiveDailyHours(), IsResource: true}
		lr.order = append(lr.order, r.Email)
	}
	return lr
}

func (lr *laneResolver) laneFor(t *ticket.Ticket) *CapacityLane {
	a := ""
	if t.Assignee != nil {
		a = strings.TrimSpace(*t.Assignee)
	}
	if a != "" {
		if r, ok := findResource(a, lr.resources); ok && lr.lanes[r.Email] != nil {
			return lr.lanes[r.Email]
		}
	} else if len(lr.order) == 1 {
		return lr.lanes[lr.order[0]]
	}
	key := a
	if key == "" {
		key = UnassignedLane
	}
	if l := lr.lanes[key]; l != nil {
		return l
	}
	l := &CapacityLane{Key: key, Label: key, DailyHours: ticket.DefaultDailyHours}
	lr.lanes[key] = l
	return l
}

// sortedLanes returns resources first (config order), then the other lanes by
// key with "unassigned" last.
func (lr *laneResolver) sortedLanes() []*CapacityLane {
	out := make([]*CapacityLane, 0, len(lr.lanes))
	isRes := map[string]bool{}
	for _, k := range lr.order {
		out = append(out, lr.lanes[k])
		isRes[k] = true
	}
	var others []string
	for k := range lr.lanes {
		if !isRes[k] {
			others = append(others, k)
		}
	}
	sort.Slice(others, func(i, j int) bool {
		if (others[i] == UnassignedLane) != (others[j] == UnassignedLane) {
			return others[j] == UnassignedLane
		}
		return others[i] < others[j]
	})
	for _, k := range others {
		out = append(out, lr.lanes[k])
	}
	return out
}

// ScheduleCapacity forward-schedules the open, in-scope tickets from `start`
// onto per-person lanes (see the package comment above). Feature parents are
// containers: they consume no capacity and finish when their open children and
// predecessors finish. Unsized tickets get UnsizedPlaceholderHours and are
// counted. A dependency cycle is broken by scheduling a stuck ticket as if its
// unscheduled predecessors were done. Read-only: nothing is written.
func ScheduleCapacity(tickets []*ticket.Ticket, resources []ticket.Resource, ctx EstimateContext, start time.Time) *CapacityPlan {
	ctx = ctx.prepared()
	plan := &CapacityPlan{Start: alignToWeekday(dateOnly(start))}
	lr := newLaneResolver(resources)

	open := map[string]*CapacityItem{}
	var items []*CapacityItem
	for _, t := range tickets {
		if ticket.IsBacklog(t) {
			continue
		}
		if !isOpenWork(t) {
			plan.Done++
			continue
		}
		it := &CapacityItem{Ticket: t, Parent: ctx.idx.hasChildren[t.ID]}
		if !it.Parent {
			lane := lr.laneFor(t)
			it.Lane = lane.Key
			h := remainingHours(t, ctx)
			lane.Tickets++
			if h <= 0 {
				it.Unsized = true
				h = UnsizedPlaceholderHours
				lane.Unsized++
				plan.Unsized++
			} else {
				lane.Hours += h
			}
			it.Hours = h
		}
		open[t.ID] = it
		items = append(items, it)
	}
	plan.Items = items
	plan.Lanes = lr.sortedLanes()
	if len(items) == 0 {
		return plan
	}

	// Scheduling deps: open, in-scope predecessors; a parent also waits for
	// its open children.
	deps := map[string][]string{}
	for _, it := range items {
		for _, p := range it.Ticket.Predecessors {
			if _, ok := open[p]; ok && p != it.Ticket.ID {
				deps[it.Ticket.ID] = append(deps[it.Ticket.ID], p)
			}
		}
		if pid := ticket.ParentID(it.Ticket); pid != "" {
			if par, ok := open[pid]; ok && par.Parent {
				deps[pid] = append(deps[pid], it.Ticket.ID)
			}
		}
	}

	// Work order: longest chain of open explicit predecessors (ready = 0).
	depth := map[string]int{}
	var depthOf func(id string, stk map[string]bool) int
	depthOf = func(id string, stk map[string]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if stk[id] {
			return 0
		}
		stk[id] = true
		best := 0
		for _, p := range open[id].Ticket.Predecessors {
			if _, ok := open[p]; ok {
				if d := depthOf(p, stk) + 1; d > best {
					best = d
				}
			}
		}
		delete(stk, id)
		depth[id] = best
		return best
	}
	less := func(a, b *CapacityItem) bool {
		if ra, rb := priorityRank(a.Ticket.Priority), priorityRank(b.Ticket.Priority); ra != rb {
			return ra < rb
		}
		if da, db := depthOf(a.Ticket.ID, map[string]bool{}), depthOf(b.Ticket.ID, map[string]bool{}); da != db {
			return da < db
		}
		return a.Ticket.ID < b.Ticket.ID
	}

	// Floors from explicit planned_start_date (business-day index from start).
	floor := map[string]float64{}
	for _, it := range items {
		if ps := it.Ticket.PlannedStartDate; ps != nil && *ps != "" {
			if d := parseDate(*ps); !d.IsZero() && d.After(plan.Start) {
				floor[it.Ticket.ID] = float64(businessDaysBetween(plan.Start, alignToWeekday(d)) - 1)
			}
		}
	}

	laneByKey := map[string]*CapacityLane{}
	for _, l := range plan.Lanes {
		laneByKey[l.Key] = l
	}
	scheduled := map[string]bool{}
	ignored := map[string]map[string]bool{} // cycle breaks: deps treated as done
	// depsReady reports whether all deps are scheduled and returns their latest end.
	depsReady := func(id string) (bool, float64) {
		end := floor[id]
		for _, p := range deps[id] {
			if ignored[id][p] {
				continue
			}
			if !scheduled[p] {
				return false, 0
			}
			if e := open[p].End; e > end {
				end = e
			}
		}
		return true, end
	}

	remaining := len(items)
	for remaining > 0 {
		// Parents finish as soon as everything they wait on has.
		progressed := true
		for progressed {
			progressed = false
			for _, it := range items {
				if !it.Parent || scheduled[it.Ticket.ID] {
					continue
				}
				ok, end := depsReady(it.Ticket.ID)
				if !ok {
					continue
				}
				it.End = end
				it.Start = end
				for _, c := range deps[it.Ticket.ID] {
					if ch := open[c]; scheduled[c] && ticket.ParentID(ch.Ticket) == it.Ticket.ID && ch.Start < it.Start {
						it.Start = ch.Start
					}
				}
				scheduled[it.Ticket.ID] = true
				remaining--
				progressed = true
			}
		}
		if remaining == 0 {
			break
		}

		// Each lane's next ticket: the best ready-now ticket by order, else the
		// one that can start soonest. Commit the globally earliest start.
		var best *CapacityItem
		bestStart := 0.0
		for _, l := range plan.Lanes {
			var pick *CapacityItem
			pickStart, pickNow := 0.0, false
			for _, it := range items {
				if it.Lane != l.Key || scheduled[it.Ticket.ID] {
					continue
				}
				ok, es := depsReady(it.Ticket.ID)
				if !ok {
					continue
				}
				if es < l.FreeAt {
					es = l.FreeAt
				}
				now := es <= l.FreeAt+schedEps
				switch {
				case pick == nil,
					now && !pickNow,
					now == pickNow && now && less(it, pick),
					!now && !pickNow && (es < pickStart-schedEps || (math.Abs(es-pickStart) <= schedEps && less(it, pick))):
					pick, pickStart, pickNow = it, es, now
				}
			}
			if pick == nil {
				continue
			}
			if best == nil || pickStart < bestStart-schedEps || (math.Abs(pickStart-bestStart) <= schedEps && less(pick, best)) {
				best, bestStart = pick, pickStart
			}
		}

		if best == nil {
			// Everything left waits on something unscheduled: a cycle. Break it
			// at the first stuck ticket by order.
			var stuck *CapacityItem
			for _, it := range items {
				if !scheduled[it.Ticket.ID] && (stuck == nil || less(it, stuck)) {
					stuck = it
				}
			}
			ig := map[string]bool{}
			for _, p := range deps[stuck.Ticket.ID] {
				if !scheduled[p] {
					ig[p] = true
				}
			}
			ignored[stuck.Ticket.ID] = ig
			continue
		}

		lane := laneByKey[best.Lane]
		best.Start = bestStart
		best.End = bestStart + best.Hours/lane.DailyHours
		lane.FreeAt = best.End
		scheduled[best.Ticket.ID] = true
		remaining--
	}

	for _, it := range items {
		sIdx := int(math.Floor(it.Start + schedEps))
		eIdx := int(math.Ceil(it.End-schedEps)) - 1
		if eIdx < sIdx {
			eIdx = sIdx
		}
		it.StartD = plan.dayAt(sIdx)
		it.EndD = plan.dayAt(eIdx)
		it.Days = eIdx - sIdx + 1
	}
	return plan
}

// Snapshot converts the plan into an in-memory projected Snapshot (open tickets
// only, with critical path computed) for the Gantt / draw.io renderers.
func (p *CapacityPlan) Snapshot(projectID, projectName string) *Snapshot {
	snap := &Snapshot{
		ProjectID:    projectID,
		ProjectName:  projectName,
		SnapshotDate: fmtDate(p.Start),
	}
	inSet := map[string]bool{}
	for _, it := range p.Items {
		inSet[it.Ticket.ID] = true
	}
	for _, it := range p.Items {
		t := it.Ticket
		phase := ""
		if t.Phase != nil {
			phase = *t.Phase
		}
		var deps []string
		for _, d := range t.Predecessors {
			if inSet[d] && d != t.ID {
				deps = append(deps, d)
			}
		}
		snap.Tasks = append(snap.Tasks, SnapshotTask{
			TaskID:       t.ID,
			Title:        t.Title,
			Owner:        ownerOf(t),
			Status:       ganttStatus(t.Status),
			Phase:        phase,
			Dependencies: deps,
			Baseline: BaselineInfo{
				PlannedStart: fmtDate(it.StartD),
				PlannedEnd:   fmtDate(it.EndD),
				PlannedDays:  it.Days,
			},
		})
	}
	ComputeCriticalPath(snap)
	return snap
}

// ProjectSchedule builds the capacity-aware projected Snapshot from `start`
// (see ScheduleCapacity) and returns it with the number of unsized tickets.
// Nothing is persisted.
func ProjectSchedule(projectID, projectName string, tickets []*ticket.Ticket, resources []ticket.Resource, ctx EstimateContext, start time.Time) (*Snapshot, int) {
	plan := ScheduleCapacity(tickets, resources, ctx, start)
	return plan.Snapshot(projectID, projectName), plan.Unsized
}

// projectedNote builds the descriptor shown above a projected Gantt.
func projectedNote(plan *CapacityPlan) string {
	note := fmt.Sprintf("Capacity-aware projection from %s — each person works their tickets in order at their daily hours. Not baselined; recomputes each view.", plan.Start.Format("Jan 2, 2006"))
	if plan.Unsized > 0 {
		note += fmt.Sprintf(" %d unsized task%s given a %gh placeholder.", plan.Unsized, pluralS(plan.Unsized), UnsizedPlaceholderHours)
	}
	if plan.Done > 0 {
		note += fmt.Sprintf(" %d done ticket%s not shown.", plan.Done, pluralS(plan.Done))
	}
	return note
}

// RenderProjectedGanttHTML builds the floating schedule and renders the Gantt
// panel for the pre-baseline dashboard.
func RenderProjectedGanttHTML(projectID, projectName string, tickets []*ticket.Ticket, resources []ticket.Resource, ctx EstimateContext, start time.Time, exportURL string) string {
	plan := ScheduleCapacity(tickets, resources, ctx, start)
	return renderGanttPanel(plan.Snapshot(projectID, projectName), projectedNote(plan), exportURL)
}
