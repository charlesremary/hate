package pm

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Types — JSON tags match Python's field names exactly
// ---------------------------------------------------------------------------

// BaselineTask represents a single task in the baseline plan.
type BaselineTask struct {
	TaskID         string   `json:"task_id"`
	Title          string   `json:"title"`
	Owner          string   `json:"owner"`
	PlannedStart   string   `json:"planned_start"`
	PlannedEnd     string   `json:"planned_end"`
	PlannedDays    int      `json:"planned_days"`
	Dependencies   []string `json:"dependencies"`
	IsMilestone    bool     `json:"is_milestone"`
	ProjectID      string   `json:"project_id,omitempty"`
	TemplateTaskID string   `json:"template_task_id,omitempty"`
}

// SlipEvent represents a detected schedule slip for a task.
type SlipEvent struct {
	SlipEventID      string   `json:"slip_event_id"`
	TaskID           string   `json:"task_id"`
	ProjectID        string   `json:"project_id"`
	DetectedDate     string   `json:"detected_date"`
	OriginalDueDate  string   `json:"original_due_date"`
	RevisedDueDate   string   `json:"revised_due_date"`
	SlipDays         int      `json:"slip_days"`
	Status           string   `json:"status"`
	ReasonCategory   *string  `json:"reason_category"`
	ReasonNarrative  *string  `json:"reason_narrative"`
	AcknowledgedBy   *string  `json:"acknowledged_by"`
	AcknowledgedDate *string  `json:"acknowledged_date"`
	ReviewedBy       *string  `json:"reviewed_by"`
	LinkedTickets    []string `json:"linked_tickets"`
}

// Baseline represents the project baseline plan.
type Baseline struct {
	CreatedDate string         `json:"created_date"`
	CreatedBy   string         `json:"created_by"`
	ProjectID   string         `json:"project_id"`
	ProjectName string         `json:"project_name"`
	ProjectType string         `json:"project_type"`
	PlannedStart string        `json:"planned_start"`
	PlannedEnd  string         `json:"planned_end"`
	Tasks       []BaselineTask `json:"tasks"`
}

// BaselineInfo holds baseline date info for a snapshot task.
type BaselineInfo struct {
	PlannedStart string `json:"planned_start"`
	PlannedEnd   string `json:"planned_end"`
	PlannedDays  int    `json:"planned_days"`
}

// CurrentInfo holds current state info for a snapshot task.
type CurrentInfo struct {
	ActualStart  *string `json:"actual_start"`
	ActualEnd    *string `json:"actual_end"`
	ProjectedEnd *string `json:"projected_end"`
}

// SnapshotTask represents a single task in the snapshot.
type SnapshotTask struct {
	TaskID         string       `json:"task_id"`
	Title          string       `json:"title"`
	Owner          string       `json:"owner"`
	Status         string       `json:"status"`
	Dependencies   []string     `json:"dependencies"`
	IsMilestone    bool         `json:"is_milestone"`
	Baseline       BaselineInfo `json:"baseline"`
	Current        CurrentInfo  `json:"current"`
	SlipDays       int          `json:"slip_days"`
	SlipEvents     []SlipEvent  `json:"slip_events"`
	IsCriticalPath bool         `json:"is_critical_path"`
	FloatDays      int          `json:"float_days"`
}

// Snapshot represents the full denormalized project snapshot.
type Snapshot struct {
	SnapshotID           string         `json:"snapshot_id"`
	SnapshotDate         string         `json:"snapshot_date"`
	GeneratedBy          string         `json:"generated_by"`
	ProjectID            string         `json:"project_id"`
	ProjectName          string         `json:"project_name"`
	ComputedHealth       string         `json:"computed_health"`
	ComputedEndDate      string         `json:"computed_end_date"`
	BaselineEndDate      string         `json:"baseline_end_date"`
	TotalSlipDays        int            `json:"total_slip_days"`
	UnresolvedSlipEvents int            `json:"unresolved_slip_events"`
	Tasks                []SnapshotTask `json:"tasks"`
	CriticalPathIDs      []string       `json:"critical_path_ids"`
}

// ---------------------------------------------------------------------------
// Date helpers
// ---------------------------------------------------------------------------

const dateFmt = "2006-01-02"

// parseDate parses a YYYY-MM-DD string. Returns zero time on failure.
func parseDate(s string) time.Time {
	t, err := time.Parse(dateFmt, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// fmtDate formats a time as YYYY-MM-DD. Returns empty string for zero time.
func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateFmt)
}

// ---------------------------------------------------------------------------
// Slip event detection
// ---------------------------------------------------------------------------

// slipEventSeqCounter tracks the next sequence number for slip event IDs within a project.
var slipEventSeqCounter = map[string]int{}

// DetectSlipEvents compares baseline planned_end vs current due_date for each task.
// Returns a list of NEW slip events to append.
func DetectSlipEvents(baselineTasks []BaselineTask, currentTasks map[string]map[string]interface{}, existingSlipEvents []SlipEvent) []SlipEvent {
	today := time.Now()

	// Build lookup of existing unresolved events by task_id
	unresolvedByTask := map[string][]SlipEvent{}
	for _, ev := range existingSlipEvents {
		if ev.Status == "unresolved" {
			unresolvedByTask[ev.TaskID] = append(unresolvedByTask[ev.TaskID], ev)
		}
	}

	// Find max existing sequence number per project
	seqByProject := map[string]int{}
	for _, ev := range existingSlipEvents {
		// Parse sequence from slip_event_id like "SE-PROJ-001"
		parts := strings.Split(ev.SlipEventID, "-")
		if len(parts) >= 3 {
			var seq int
			fmt.Sscanf(parts[len(parts)-1], "%d", &seq)
			projID := ev.ProjectID
			if seq > seqByProject[projID] {
				seqByProject[projID] = seq
			}
		}
	}

	var newEvents []SlipEvent

	for _, bt := range baselineTasks {
		taskID := bt.TaskID
		current, ok := currentTasks[taskID]
		if !ok {
			continue
		}

		baselineEnd := parseDate(bt.PlannedEnd)
		currentDueStr, _ := current["due_date"].(string)
		currentDue := parseDate(currentDueStr)

		if currentDue.IsZero() || baselineEnd.IsZero() {
			continue
		}

		if currentDue.After(baselineEnd) {
			slipDays := int(currentDue.Sub(baselineEnd).Hours() / 24)

			// Total slip days already explained by resolved events for this task
			explainedDays := 0
			for _, ev := range existingSlipEvents {
				if ev.TaskID == taskID && ev.Status == "resolved" {
					explainedDays += ev.SlipDays
				}
			}

			// Check if there's already an unresolved event for this task
			alreadyUnresolved := len(unresolvedByTask[taskID]) > 0

			// Only create a new event if there's unexplained slip and nothing already open
			unexplainedDays := slipDays - explainedDays
			if unexplainedDays > 0 && !alreadyUnresolved {
				projID := bt.ProjectID
				if projID == "" {
					projID = "UNKNOWN"
				}
				seqByProject[projID]++
				seqNum := seqByProject[projID]

				newEvents = append(newEvents, SlipEvent{
					SlipEventID:     fmt.Sprintf("SE-%s-%03d", projID, seqNum),
					TaskID:          taskID,
					ProjectID:       projID,
					DetectedDate:    fmtDate(today),
					OriginalDueDate: fmtDate(baselineEnd),
					RevisedDueDate:  fmtDate(currentDue),
					SlipDays:        slipDays,
					Status:          "unresolved",
					ReasonCategory:  nil,
					ReasonNarrative: nil,
					AcknowledgedBy:  nil,
					AcknowledgedDate: nil,
					ReviewedBy:      nil,
					LinkedTickets:   []string{},
				})
			}
		}
	}

	return newEvents
}

// ---------------------------------------------------------------------------
// Health computation
// ---------------------------------------------------------------------------

// ComputeHealth determines project health status.
//   - Green: slip < 10% of planned AND no unresolved events
//   - Yellow: slip 10-25% OR has unresolved events
//   - Red: slip > 25% OR unresolved events > 3
func ComputeHealth(totalSlipDays, plannedDurationDays, unresolvedCount int) string {
	if plannedDurationDays == 0 {
		return "yellow"
	}

	slipPct := float64(totalSlipDays) / float64(plannedDurationDays)

	if slipPct > 0.25 || unresolvedCount > 3 {
		return "red"
	} else if slipPct > 0.10 || unresolvedCount > 0 {
		return "yellow"
	}
	return "green"
}

// ---------------------------------------------------------------------------
// Snapshot assembly
// ---------------------------------------------------------------------------

// BuildSnapshot assembles the full denormalized snapshot from baseline + current task state + slip events.
func BuildSnapshot(baseline Baseline, currentTasks map[string]map[string]interface{}, slipEvents []SlipEvent, snapshotDate time.Time) Snapshot {
	if snapshotDate.IsZero() {
		snapshotDate = time.Now()
	}

	// Tag baseline tasks with project_id
	for i := range baseline.Tasks {
		baseline.Tasks[i].ProjectID = baseline.ProjectID
	}

	// Group slip events by task
	slipByTask := map[string][]SlipEvent{}
	for _, ev := range slipEvents {
		slipByTask[ev.TaskID] = append(slipByTask[ev.TaskID], ev)
	}

	// Build task list
	var snapshotTasks []SnapshotTask
	totalSlip := 0
	unresolvedCount := 0
	latestProjectedEnd := parseDate(baseline.PlannedEnd)

	for _, bt := range baseline.Tasks {
		taskID := bt.TaskID
		current := currentTasks[taskID]
		taskSlips := slipByTask[taskID]
		if taskSlips == nil {
			taskSlips = []SlipEvent{}
		}

		// Calculate slip days for this task
		baselineEnd := parseDate(bt.PlannedEnd)
		currentDueStr := getStringField(current, "due_date")
		currentDue := parseDate(currentDueStr)
		taskSlipDays := 0
		if !currentDue.IsZero() && !baselineEnd.IsZero() && currentDue.After(baselineEnd) {
			taskSlipDays = int(currentDue.Sub(baselineEnd).Hours() / 24)
		}
		totalSlip += taskSlipDays

		// Track unresolved
		taskUnresolved := 0
		for _, ev := range taskSlips {
			if ev.Status == "unresolved" {
				taskUnresolved++
			}
		}
		unresolvedCount += taskUnresolved

		// Track latest projected end
		projected := currentDue
		if projected.IsZero() {
			projected = baselineEnd
		}
		if !projected.IsZero() && projected.After(latestProjectedEnd) {
			latestProjectedEnd = projected
		}

		// Build current info
		owner := getStringField(current, "owner")
		if owner == "" {
			owner = bt.Owner
		}
		status := getStringField(current, "status")
		if status == "" {
			status = "not_started"
		}

		actualStart := getStringPtrField(current, "actual_start")
		actualEnd := getStringPtrField(current, "actual_end")
		projectedEnd := getStringPtrField(current, "due_date")

		deps := bt.Dependencies
		if deps == nil {
			deps = []string{}
		}

		snapshotTasks = append(snapshotTasks, SnapshotTask{
			TaskID:       taskID,
			Title:        bt.Title,
			Owner:        owner,
			Status:       status,
			Dependencies: deps,
			IsMilestone:  bt.IsMilestone,
			Baseline: BaselineInfo{
				PlannedStart: bt.PlannedStart,
				PlannedEnd:   bt.PlannedEnd,
				PlannedDays:  bt.PlannedDays,
			},
			Current: CurrentInfo{
				ActualStart:  actualStart,
				ActualEnd:    actualEnd,
				ProjectedEnd: projectedEnd,
			},
			SlipDays:   taskSlipDays,
			SlipEvents: taskSlips,
		})
	}

	// Recalculate projected end dates based on actual progress
	latestProjectedEnd = recalcProjectedEnds(snapshotTasks, snapshotDate)

	// Planned duration for health calc
	plannedStart := parseDate(baseline.PlannedStart)
	plannedEnd := parseDate(baseline.PlannedEnd)
	plannedDuration := 1
	if !plannedStart.IsZero() && !plannedEnd.IsZero() {
		plannedDuration = int(plannedEnd.Sub(plannedStart).Hours() / 24)
		if plannedDuration <= 0 {
			plannedDuration = 1
		}
	}

	health := ComputeHealth(totalSlip, plannedDuration, unresolvedCount)
	snapshotID := fmt.Sprintf("%s-SNP-%s", baseline.ProjectID, snapshotDate.Format("20060102"))

	return Snapshot{
		SnapshotID:           snapshotID,
		SnapshotDate:         fmtDate(snapshotDate),
		GeneratedBy:          "snapshot-agent",
		ProjectID:            baseline.ProjectID,
		ProjectName:          baseline.ProjectName,
		ComputedHealth:       health,
		ComputedEndDate:      fmtDate(latestProjectedEnd),
		BaselineEndDate:      baseline.PlannedEnd,
		TotalSlipDays:        totalSlip,
		UnresolvedSlipEvents: unresolvedCount,
		Tasks:                snapshotTasks,
		CriticalPathIDs:      []string{},
	}
}

// ---------------------------------------------------------------------------
// Projected end recalculation based on actual velocity
// ---------------------------------------------------------------------------

// addBusinessDaysToDate adds n business days to a date, skipping weekends.
func addBusinessDaysToDate(start time.Time, days int) time.Time {
	d := start
	added := 0
	for added < days {
		d = d.AddDate(0, 0, 1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			added++
		}
	}
	return d
}

// recalcProjectedEnds recalculates projected end dates for incomplete tasks
// based on actual velocity (how fast completed work went vs plan).
// Returns the latest projected end date across all tasks.
func recalcProjectedEnds(tasks []SnapshotTask, today time.Time) time.Time {
	taskByID := map[string]*SnapshotTask{}
	for i := range tasks {
		taskByID[tasks[i].TaskID] = &tasks[i]
	}

	// Calculate velocity ratio: how fast are we going vs plan?
	// velocity > 1 means faster than planned
	var completedPlannedDays, completedActualDays int
	baselineStart := time.Time{}
	for _, t := range tasks {
		bs := parseDate(t.Baseline.PlannedStart)
		if !bs.IsZero() && (baselineStart.IsZero() || bs.Before(baselineStart)) {
			baselineStart = bs
		}
		if t.Status == "complete" {
			completedPlannedDays += t.Baseline.PlannedDays
		}
	}

	// Actual days elapsed since baseline start
	if !baselineStart.IsZero() && today.After(baselineStart) {
		completedActualDays = int(today.Sub(baselineStart).Hours()/24) + 1
	}
	if completedActualDays <= 0 {
		completedActualDays = 1
	}

	velocity := 1.0
	if completedPlannedDays > 0 && completedActualDays > 0 {
		velocity = float64(completedPlannedDays) / float64(completedActualDays)
		if velocity < 0.25 {
			velocity = 0.25 // floor to prevent absurd projections
		}
		if velocity > 10.0 {
			velocity = 10.0 // cap
		}
	}

	// Use memoization for DAG traversal
	projectedEndCache := map[string]time.Time{}

	var getProjectedEnd func(string) time.Time
	getProjectedEnd = func(tid string) time.Time {
		if cached, ok := projectedEndCache[tid]; ok {
			return cached
		}
		t, ok := taskByID[tid]
		if !ok {
			projectedEndCache[tid] = today
			return today
		}

		// Complete: use actual end
		if t.Status == "complete" {
			end := today
			if t.Current.ActualEnd != nil && *t.Current.ActualEnd != "" {
				parsed := parseDate(*t.Current.ActualEnd)
				if !parsed.IsZero() {
					end = parsed
				}
			}
			projectedEndCache[tid] = end
			return end
		}

		// Find latest predecessor projected end
		latestPredEnd := time.Time{}
		for _, dep := range t.Dependencies {
			predEnd := getProjectedEnd(dep)
			if predEnd.After(latestPredEnd) {
				latestPredEnd = predEnd
			}
		}

		// Projected start
		projStart := today
		if latestPredEnd.After(projStart) {
			projStart = latestPredEnd
		}
		if t.Status == "in_progress" {
			projStart = today
		}

		// Scale duration by velocity
		duration := t.Baseline.PlannedDays
		if duration <= 0 {
			duration = 1
		}
		scaledDuration := int(math.Ceil(float64(duration) / velocity))
		if scaledDuration <= 0 {
			scaledDuration = 1
		}

		projEnd := addBusinessDaysToDate(projStart, scaledDuration)

		projEndStr := fmtDate(projEnd)
		t.Current.ProjectedEnd = &projEndStr

		projectedEndCache[tid] = projEnd
		return projEnd
	}

	latest := time.Time{}
	for _, t := range tasks {
		end := getProjectedEnd(t.TaskID)
		if end.After(latest) {
			latest = end
		}
	}

	if latest.IsZero() {
		latest = today
	}
	return latest
}

// ---------------------------------------------------------------------------
// Critical path computation (CPM)
// ---------------------------------------------------------------------------

// ComputeCriticalPath computes the critical path using forward/backward pass CPM.
// It modifies the snapshot's Tasks in place, setting IsCriticalPath and FloatDays.
func ComputeCriticalPath(snapshot *Snapshot) {
	tasks := snapshot.Tasks
	if len(tasks) == 0 {
		return
	}

	// Build lookup by task_id
	taskMap := map[string]*SnapshotTask{}
	var ids []string
	for i := range tasks {
		taskMap[tasks[i].TaskID] = &tasks[i]
		ids = append(ids, tasks[i].TaskID)
	}

	// Compute durations
	durations := map[string]int{}
	for _, t := range tasks {
		start := parseDate(t.Baseline.PlannedStart)
		end := parseDate(t.Baseline.PlannedEnd)
		if !start.IsZero() && !end.IsZero() {
			d := int(end.Sub(start).Hours() / 24)
			if d < 1 {
				d = 1
			}
			durations[t.TaskID] = d
		} else if t.Baseline.PlannedDays > 0 {
			durations[t.TaskID] = t.Baseline.PlannedDays
		} else {
			durations[t.TaskID] = 1
		}
	}

	// Topological sort (Kahn's algorithm)
	inDegree := map[string]int{}
	children := map[string][]string{}
	for _, tid := range ids {
		inDegree[tid] = 0
		children[tid] = []string{}
	}
	for _, t := range tasks {
		tid := t.TaskID
		for _, dep := range t.Dependencies {
			if _, ok := taskMap[dep]; ok {
				inDegree[tid]++
				children[dep] = append(children[dep], tid)
			}
		}
	}

	var queue []string
	for _, tid := range ids {
		if inDegree[tid] == 0 {
			queue = append(queue, tid)
		}
	}

	var topo []string
	tempIn := map[string]int{}
	for k, v := range inDegree {
		tempIn[k] = v
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		topo = append(topo, node)
		for _, child := range children[node] {
			tempIn[child]--
			if tempIn[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	// Handle cycles: add missing nodes at end
	if len(topo) < len(ids) {
		topoSet := map[string]bool{}
		for _, t := range topo {
			topoSet[t] = true
		}
		for _, tid := range ids {
			if !topoSet[tid] {
				topo = append(topo, tid)
			}
		}
	}

	// Forward pass: ES, EF
	es := map[string]int{}
	ef := map[string]int{}
	for _, tid := range topo {
		t := taskMap[tid]
		deps := []string{}
		for _, d := range t.Dependencies {
			if _, ok := taskMap[d]; ok {
				deps = append(deps, d)
			}
		}
		if len(deps) == 0 {
			es[tid] = 0
		} else {
			maxEF := 0
			for _, d := range deps {
				if v, ok := ef[d]; ok && v > maxEF {
					maxEF = v
				}
			}
			es[tid] = maxEF
		}
		ef[tid] = es[tid] + durations[tid]
	}

	// Project early finish
	projectEF := 0
	for _, v := range ef {
		if v > projectEF {
			projectEF = v
		}
	}
	if projectEF == 0 {
		projectEF = 1
	}

	// Backward pass: LS, LF
	lf := map[string]int{}
	ls := map[string]int{}
	for i := len(topo) - 1; i >= 0; i-- {
		tid := topo[i]
		successors := children[tid]
		validSuccessors := []string{}
		for _, s := range successors {
			if _, ok := taskMap[s]; ok {
				validSuccessors = append(validSuccessors, s)
			}
		}
		if len(validSuccessors) == 0 {
			lf[tid] = projectEF
		} else {
			minLS := math.MaxInt32
			for _, s := range validSuccessors {
				if v, ok := ls[s]; ok && v < minLS {
					minLS = v
				}
			}
			if minLS == math.MaxInt32 {
				lf[tid] = projectEF
			} else {
				lf[tid] = minLS
			}
		}
		ls[tid] = lf[tid] - durations[tid]
	}

	// Set float and critical path
	var criticalPathIDs []string
	for i := range snapshot.Tasks {
		tid := snapshot.Tasks[i].TaskID
		floatDays := ls[tid] - es[tid]
		if floatDays < 0 {
			floatDays = 0
		}
		snapshot.Tasks[i].FloatDays = floatDays
		snapshot.Tasks[i].IsCriticalPath = floatDays == 0
		if floatDays == 0 {
			criticalPathIDs = append(criticalPathIDs, tid)
		}
	}

	if criticalPathIDs == nil {
		criticalPathIDs = []string{}
	}
	snapshot.CriticalPathIDs = criticalPathIDs
}

// EnrichSnapshotWithCriticalPath calls ComputeCriticalPath and updates the snapshot in place.
func EnrichSnapshotWithCriticalPath(snapshot *Snapshot) {
	ComputeCriticalPath(snapshot)
}

// ---------------------------------------------------------------------------
// Slip ledger rows (for report table)
// ---------------------------------------------------------------------------

// SlipLedgerRow represents a flattened row for the report table.
type SlipLedgerRow struct {
	TaskID              string   `json:"task_id"`
	Title               string   `json:"title"`
	Owner               string   `json:"owner"`
	Status              string   `json:"status"`
	BaselineStart       string   `json:"baseline_start"`
	BaselineEnd         string   `json:"baseline_end"`
	BaselineDays        int      `json:"baseline_days"`
	ActualStart         string   `json:"actual_start"`
	ActualEnd           string   `json:"actual_end"`
	ProjectedEnd        string   `json:"projected_end"`
	SlipDays            int      `json:"slip_days"`
	SlipSummary         string   `json:"slip_summary"`
	HasUnresolved       bool     `json:"has_unresolved"`
	UnresolvedEventIDs  []string `json:"unresolved_event_ids"`
}

// SlipLedgerRows flattens snapshot tasks into rows for the report table.
func SlipLedgerRows(snapshot *Snapshot) []SlipLedgerRow {
	var rows []SlipLedgerRow
	for _, task := range snapshot.Tasks {
		var slipSummaryParts []string
		hasUnresolved := false
		var unresolvedIDs []string
		for _, ev := range task.SlipEvents {
			if ev.Status == "resolved" {
				cat := "unknown"
				if ev.ReasonCategory != nil && *ev.ReasonCategory != "" {
					cat = *ev.ReasonCategory
				}
				slipSummaryParts = append(slipSummaryParts, fmt.Sprintf("%s (%dd)", cat, ev.SlipDays))
			} else {
				slipSummaryParts = append(slipSummaryParts, fmt.Sprintf("UNRESOLVED (%dd)", ev.SlipDays))
				hasUnresolved = true
				unresolvedIDs = append(unresolvedIDs, ev.SlipEventID)
			}
		}

		slipSummary := "\u2014" // em dash
		if len(slipSummaryParts) > 0 {
			slipSummary = strings.Join(slipSummaryParts, "; ")
		}

		actualStart := ""
		if task.Current.ActualStart != nil {
			actualStart = *task.Current.ActualStart
		}
		actualEnd := ""
		if task.Current.ActualEnd != nil {
			actualEnd = *task.Current.ActualEnd
		}
		projectedEnd := ""
		if task.Current.ProjectedEnd != nil {
			projectedEnd = *task.Current.ProjectedEnd
		}

		rows = append(rows, SlipLedgerRow{
			TaskID:        task.TaskID,
			Title:         task.Title,
			Owner:         task.Owner,
			Status:        task.Status,
			BaselineStart: task.Baseline.PlannedStart,
			BaselineEnd:   task.Baseline.PlannedEnd,
			BaselineDays:  task.Baseline.PlannedDays,
			ActualStart:   actualStart,
			ActualEnd:     actualEnd,
			ProjectedEnd:  projectedEnd,
			SlipDays:      task.SlipDays,
			SlipSummary:        slipSummary,
			HasUnresolved:      hasUnresolved,
			UnresolvedEventIDs: unresolvedIDs,
		})
	}
	return rows
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getStringField(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func getStringPtrField(m map[string]interface{}, key string) *string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}

// Suppress unused import warnings — these are used in this file.
var (
	_ = sort.Strings
	_ = math.MaxInt32
)
