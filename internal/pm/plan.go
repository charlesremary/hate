// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"hate/internal/ticket"
)

// The plan audit trail: baseline.json, slip_events.json and the archive of
// replaced baselines (.tkt/pm/baselines/) are committed to the project repo
// whenever they change, each commit touching only those paths. Daily snapshot
// files are derived and noisy, so .tkt/pm/.gitignore keeps snapshots/ out of
// git. Every read-modify-write of these files runs under a per-project lock.

// MinRebaselineReason is the minimum length (in characters) of a re-baseline reason.
const MinRebaselineReason = 5

var (
	// ErrNoBaseline: re-baseline was asked for before any baseline exists.
	ErrNoBaseline = errors.New("no baseline yet. Use Baseline now first")
	// ErrRebaselineReason: the re-baseline reason is missing or too short.
	ErrRebaselineReason = fmt.Errorf("a re-baseline needs a reason of at least %d characters", MinRebaselineReason)
)

// PMGitignorePath is .tkt/pm/.gitignore.
func PMGitignorePath(projectRoot string) string {
	return filepath.Join(PMDir(projectRoot), ".gitignore")
}

// BaselinesDir is .tkt/pm/baselines/, the archive of replaced baselines.
func BaselinesDir(projectRoot string) string {
	return filepath.Join(PMDir(projectRoot), "baselines")
}

// TodaySnapshotPath is snapshots/<today>.json.
func TodaySnapshotPath(projectRoot string, today time.Time) string {
	return filepath.Join(SnapshotsDir(projectRoot), today.Format("2006-01-02")+".json")
}

// planLocks serialise the baseline / snapshot / slip read-modify-writes (and
// their commits) per project, so concurrent requests can't duplicate a daily
// snapshot or slip events, or interleave commits.
var (
	planLocksMu sync.Mutex
	planLocks   = map[string]*sync.Mutex{}
)

// LockPlan takes the project's plan lock and returns the unlock function.
func LockPlan(projectRoot string) func() {
	key := filepath.Clean(projectRoot)
	planLocksMu.Lock()
	m := planLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		planLocks[key] = m
	}
	planLocksMu.Unlock()
	m.Lock()
	return m.Unlock
}

// ensurePMGitignore makes .tkt/pm/.gitignore ignore snapshots/ (creating the
// file, or appending the line to an existing one). Returns its path.
func ensurePMGitignore(projectRoot string) (string, error) {
	path := PMGitignorePath(projectRoot)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return path, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if l := strings.TrimSpace(line); l == "snapshots/" || l == "snapshots" || l == "/snapshots/" {
			return path, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return path, err
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	data = append(data, []byte("snapshots/\n")...)
	return path, os.WriteFile(path, data, 0644)
}

// commitPlan commits the given (existing) paths with the project's git identity.
func commitPlan(projectRoot string, paths []string, message string) {
	if cfg, err := ticket.ReadConfig(projectRoot); err == nil {
		ticket.EnsureProjectIdentity(projectRoot, cfg)
	}
	var files []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	if len(files) > 0 {
		ticket.GitCommit(projectRoot, files, message)
	}
}

// ResolveAuthor returns the requested author, else the project's git identity
// (email, then name), else "unknown".
func ResolveAuthor(projectRoot, requested string) string {
	if a := strings.TrimSpace(requested); a != "" {
		return a
	}
	if cfg, err := ticket.ReadConfig(projectRoot); err == nil && cfg.GitIdentityV != nil {
		if cfg.GitIdentityV.Email != "" {
			return cfg.GitIdentityV.Email
		}
		if cfg.GitIdentityV.Name != "" {
			return cfg.GitIdentityV.Name
		}
	}
	id := ticket.GitUserIdentity(projectRoot)
	if id["email"] != "" {
		return id["email"]
	}
	if id["name"] != "" {
		return id["name"]
	}
	return "unknown"
}

// BaselineNow creates the baseline from the current tickets and commits it
// (with .tkt/pm/.gitignore).
func BaselineNow(projectRoot, projectID, projectName, author string) (*Baseline, error) {
	defer LockPlan(projectRoot)()
	b, err := CreateBaselineFromTickets(projectRoot, projectID, projectName, author)
	if err != nil {
		return nil, err
	}
	CommitBaseline(projectRoot, fmt.Sprintf("baseline: %d tickets, planned end %s", len(b.Tasks), b.PlannedEnd))
	return b, nil
}

// CommitBaseline commits baseline.json (and the snapshots .gitignore) after a
// baseline was written, e.g. by the template WBS route.
func CommitBaseline(projectRoot, message string) {
	gi, _ := ensurePMGitignore(projectRoot)
	commitPlan(projectRoot, []string{BaselinePath(projectRoot), gi}, message)
}

// CommitSlipEvents commits slip_events.json. Callers hold LockPlan.
func CommitSlipEvents(projectRoot, message string) {
	commitPlan(projectRoot, []string{SlipEventsPath(projectRoot)}, message)
}

// snapshotLocked runs a snapshot and commits slip_events.json when the
// snapshot changed it (plus the .gitignore when it had to be written).
// Callers hold LockPlan.
func snapshotLocked(projectID, projectRoot, generatedBy string) (*Snapshot, error) {
	before, _ := os.ReadFile(SlipEventsPath(projectRoot))
	nBefore := countEvents(before)
	snap, err := runSnapshot(projectID, projectRoot, generatedBy)
	if err != nil {
		return nil, err
	}
	giBefore, _ := os.ReadFile(PMGitignorePath(projectRoot))
	gi, _ := ensurePMGitignore(projectRoot)
	giAfter, _ := os.ReadFile(gi)
	after, _ := os.ReadFile(SlipEventsPath(projectRoot))
	slipsChanged := !bytes.Equal(before, after)
	if !slipsChanged && bytes.Equal(giBefore, giAfter) {
		return snap, nil
	}
	msg := "ignore PM snapshots"
	if slipsChanged {
		n := countEvents(after) - nBefore
		msg = fmt.Sprintf("snapshot %s: %d new slip event%s", snap.SnapshotDate, n, pluralS(n))
	}
	commitPlan(projectRoot, []string{SlipEventsPath(projectRoot), gi}, msg)
	return snap, nil
}

func countEvents(data []byte) int {
	var evs []SlipEvent
	if json.Unmarshal(data, &evs) != nil {
		return 0
	}
	return len(evs)
}

// TakeSnapshot is the manual snapshot (POST /snapshot): it always runs and
// commits slip_events.json when new slip events were detected.
func TakeSnapshot(projectID, projectRoot string) (*Snapshot, error) {
	defer LockPlan(projectRoot)()
	return snapshotLocked(projectID, projectRoot, "manual")
}

// AutoSnapshot takes today's snapshot when the project has a baseline and no
// snapshot for today yet (at most once a calendar day; concurrent callers
// produce one). Reports whether it ran.
func AutoSnapshot(projectID, projectRoot string) (bool, error) {
	defer LockPlan(projectRoot)()
	if !BaselineExists(projectRoot) {
		return false, nil
	}
	if _, err := os.Stat(TodaySnapshotPath(projectRoot, time.Now())); err == nil {
		return false, nil
	}
	if _, err := snapshotLocked(projectID, projectRoot, "auto"); err != nil {
		return false, err
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Re-baseline and the baseline archive
// ---------------------------------------------------------------------------

// BaselineArchive is one archived baseline: .tkt/pm/baselines/<YYYY-MM-DD>-<n>.json.
type BaselineArchive struct {
	ArchivedAt string          `json:"archived_at"`
	ArchivedBy string          `json:"archived_by"`
	Reason     string          `json:"reason"`
	Baseline   json.RawMessage `json:"baseline"`
}

// RebaselineResult is what a re-baseline did.
type RebaselineResult struct {
	Baseline      *Baseline `json:"baseline"`
	ArchiveID     string    `json:"archive_id"`
	ArchivePath   string    `json:"archive_path"` // relative to the project root
	ClosedSlips   int       `json:"closed_slip_events"`
	SupersededAll int       `json:"superseded_slip_events"`
}

// ValidRebaselineReason trims the reason and checks its length.
func ValidRebaselineReason(reason string) (string, error) {
	r := strings.TrimSpace(reason)
	if utf8.RuneCountInString(r) < MinRebaselineReason {
		return "", ErrRebaselineReason
	}
	return r, nil
}

// Rebaseline replaces the baseline: the current one is archived with the
// reason, author and time; unresolved slip events against it are resolved as
// "rebaseline" with the reason as narrative, and every event against it is
// marked superseded; a new baseline is created from the current tickets and
// today's snapshot is retaken against it. All of it is one commit
// "re-baseline: <reason>".
func Rebaseline(projectRoot, projectID, projectName, reason, author string) (*RebaselineResult, error) {
	reason, err := ValidRebaselineReason(reason)
	if err != nil {
		return nil, err
	}
	defer LockPlan(projectRoot)()

	oldRaw, err := os.ReadFile(BaselinePath(projectRoot))
	if os.IsNotExist(err) {
		return nil, ErrNoBaseline
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid(oldRaw) {
		return nil, fmt.Errorf("current baseline.json is not valid JSON")
	}
	// Build first: if the tickets can't be baselined, nothing is touched.
	nb, err := buildBaselineFromTickets(projectRoot, projectID, projectName, author)
	if err != nil {
		return nil, err
	}
	events, err := ReadSlipEvents(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read slip events: %w", err)
	}

	now := time.Now()
	today := fmtDate(now)
	id, archivePath, err := nextArchive(projectRoot, today)
	if err != nil {
		return nil, err
	}
	archive := BaselineArchive{
		ArchivedAt: now.UTC().Format(time.RFC3339),
		ArchivedBy: author,
		Reason:     reason,
		Baseline:   json.RawMessage(bytes.TrimSpace(oldRaw)),
	}
	data, err := json.MarshalIndent(archive, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(archivePath, append(data, '\n'), 0644); err != nil {
		return nil, fmt.Errorf("failed to write baseline archive: %w", err)
	}

	res := &RebaselineResult{Baseline: nb, ArchiveID: id, ArchivePath: filepath.ToSlash(filepath.Join(".tkt", "pm", "baselines", id+".json"))}
	for i := range events {
		if !events[i].Current() {
			continue
		}
		if events[i].Status != "resolved" {
			cat, narr, by, d := SlipCategoryRebaseline, reason, author, today
			events[i].Status = "resolved"
			events[i].ReasonCategory = &cat
			events[i].ReasonNarrative = &narr
			events[i].AcknowledgedBy = &by
			events[i].AcknowledgedDate = &d
			res.ClosedSlips++
		}
		sup := id
		events[i].SupersededBy = &sup
		res.SupersededAll++
	}
	if res.SupersededAll > 0 {
		if err := WriteSlipEvents(projectRoot, events); err != nil {
			return nil, fmt.Errorf("failed to write slip events: %w", err)
		}
	}
	if err := writeBaseline(projectRoot, nb); err != nil {
		return nil, err
	}
	// Today's snapshot was measured against the old baseline; retake it so the
	// dashboard shows the new one (any slip it finds goes into the same
	// commit). If it fails, the file is gone and the dashboard's auto-snapshot
	// retries.
	_ = os.Remove(TodaySnapshotPath(projectRoot, now))
	_, _ = runSnapshot(projectID, projectRoot, "re-baseline")
	gi, _ := ensurePMGitignore(projectRoot)
	commitPlan(projectRoot, []string{archivePath, BaselinePath(projectRoot), SlipEventsPath(projectRoot), gi},
		"re-baseline: "+strings.Join(strings.Fields(reason), " "))
	return res, nil
}

var archiveName = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(\d+)\.json$`)

// nextArchive picks baselines/<date>-<n>.json with the first free n (from 1).
func nextArchive(projectRoot, date string) (string, string, error) {
	dir := BaselinesDir(projectRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create baselines directory: %w", err)
	}
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s-%d", date, n)
		p := filepath.Join(dir, id+".json")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return id, p, nil
		}
	}
}

// ArchivedBaseline summarises one archive entry for GET /baselines.
type ArchivedBaseline struct {
	ID           string `json:"id"` // YYYY-MM-DD-N
	ArchivedAt   string `json:"archived_at"`
	ArchivedBy   string `json:"archived_by"`
	Reason       string `json:"reason"`
	CreatedDate  string `json:"created_date"` // of the archived baseline
	CreatedBy    string `json:"created_by"`
	PlannedStart string `json:"planned_start"`
	PlannedEnd   string `json:"planned_end"`
	TaskCount    int    `json:"task_count"`
}

// ListBaselineArchive returns the archived baselines, oldest first.
func ListBaselineArchive(projectRoot string) ([]ArchivedBaseline, error) {
	entries, err := os.ReadDir(BaselinesDir(projectRoot))
	if os.IsNotExist(err) {
		return []ArchivedBaseline{}, nil
	}
	if err != nil {
		return nil, err
	}
	type keyed struct {
		date string
		n    int
		a    ArchivedBaseline
	}
	var list []keyed
	for _, e := range entries {
		m := archiveName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(BaselinesDir(projectRoot), e.Name()))
		if err != nil {
			return nil, err
		}
		var arc BaselineArchive
		if err := json.Unmarshal(data, &arc); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", e.Name(), err)
		}
		var b Baseline
		_ = json.Unmarshal(arc.Baseline, &b)
		n, _ := strconv.Atoi(m[2])
		list = append(list, keyed{m[1], n, ArchivedBaseline{
			ID: strings.TrimSuffix(e.Name(), ".json"), ArchivedAt: arc.ArchivedAt, ArchivedBy: arc.ArchivedBy,
			Reason: arc.Reason, CreatedDate: b.CreatedDate, CreatedBy: b.CreatedBy,
			PlannedStart: b.PlannedStart, PlannedEnd: b.PlannedEnd, TaskCount: len(b.Tasks),
		}})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].date != list[j].date {
			return list[i].date < list[j].date
		}
		return list[i].n < list[j].n
	})
	out := make([]ArchivedBaseline, len(list))
	for i, k := range list {
		out[i] = k.a
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Plan strip
// ---------------------------------------------------------------------------

// PlanStatus is what the Plan strip shows.
type PlanStatus struct {
	HasBaseline       bool
	BaselineDate      string
	BaselineAuthor    string
	TicketCount       int
	PlannedEnd        string
	LastSnapshot      string // YYYY-MM-DD, "" when none
	LastSnapshotBy    string // auto / manual / re-baseline ("" for older snapshots)
	UnresolvedSlips   int
	ArchivedBaselines int
}

// ReadPlanStatus reads the baseline, latest snapshot and slip events.
func ReadPlanStatus(projectRoot string) PlanStatus {
	var ps PlanStatus
	if data, err := os.ReadFile(BaselinePath(projectRoot)); err == nil {
		var b Baseline
		if json.Unmarshal(data, &b) == nil {
			ps.HasBaseline = true
			ps.BaselineDate, ps.BaselineAuthor = b.CreatedDate, b.CreatedBy
			ps.TicketCount, ps.PlannedEnd = len(b.Tasks), b.PlannedEnd
		}
	}
	if snap, err := LoadLatestSnapshot(projectRoot); err == nil && snap != nil {
		ps.LastSnapshot = snap.SnapshotDate
		switch snap.GeneratedBy {
		case "auto", "manual", "re-baseline":
			ps.LastSnapshotBy = snap.GeneratedBy
		}
	}
	if evs, err := ReadSlipEvents(projectRoot); err == nil {
		for _, e := range evs {
			if e.Current() && e.Status != "resolved" {
				ps.UnresolvedSlips++
			}
		}
	}
	if arcs, err := ListBaselineArchive(projectRoot); err == nil {
		ps.ArchivedBaselines = len(arcs)
	}
	return ps
}

// planStripJS drives the strip's buttons. The dashboard is an iframe on the
// app's origin, so the buttons call the project API and reload.
const planStripJS = `<script>
function planRun(btn, path, body) {
  var id = document.getElementById('plan-strip').getAttribute('data-project');
  btn.disabled = true;
  fetch('/api/projects/' + encodeURIComponent(id) + path, {
    method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body || {})
  }).then(function(r) {
    return r.json().catch(function() { return {}; }).then(function(d) {
      if (!r.ok) throw new Error(d.detail || ('HTTP ' + r.status));
      location.reload();
    });
  }).catch(function(e) { btn.disabled = false; alert(e.message); });
}
function planBaselineNow(btn) { planRun(btn, '/baseline-now'); }
function planSnapshot(btn) { planRun(btn, '/snapshot'); }
function planRebaseline(btn) {
  var r = prompt('Re-baseline: why is the plan being replaced? (at least 5 characters; recorded with the archived baseline and committed)');
  if (r === null) return;
  r = r.trim();
  if (r.length < 5) { alert('Please give a reason of at least 5 characters.'); return; }
  planRun(btn, '/rebaseline', {reason: r});
}
function planShowLedger() {
  if (typeof showTab === 'function') showTab('status');
  var el = document.getElementById('slip-ledger');
  if (el) el.scrollIntoView({behavior: 'smooth'});
  return false;
}
</script>`

// RenderPlanStripHTML renders the Plan strip for the top of both dashboard
// flavors: baseline status (or Baseline now), the last snapshot with Take
// snapshot, the unresolved slip count linking to the slip ledger, and
// Re-baseline.
func RenderPlanStripHTML(projectID string, ps PlanStatus) string {
	esc := html.EscapeString
	btn := func(onclick, label, title string) string {
		return fmt.Sprintf(`<button class="plan-btn" onclick="%s(this)" title="%s" style="padding:5px 12px;border-radius:6px;border:1px solid #ccc;background:#fff;color:#333;cursor:pointer;font-size:12px;font-weight:500">%s</button>`,
			onclick, esc(title), esc(label))
	}
	muted := func(s string) string { return `<span style="color:#888">` + s + `</span>` }
	var parts []string
	if !ps.HasBaseline {
		parts = append(parts,
			`<span class="plan-baseline"><strong>No baseline.</strong> `+muted("Baselining freezes today's plan; slips are tracked against it.")+`</span> `+
				strings.Replace(btn("planBaselineNow", "Baseline now", "Create the baseline from the current tickets and commit it"),
					`background:#fff;color:#333`, `background:#1976d2;color:#fff;border-color:#1976d2`, 1))
	} else {
		by := ""
		if ps.BaselineAuthor != "" {
			by = " by " + esc(ps.BaselineAuthor)
		}
		parts = append(parts, fmt.Sprintf(`<span class="plan-baseline">Baseline <strong>%s</strong>%s &middot; %d ticket%s &middot; planned end <strong>%s</strong></span>`,
			esc(ps.BaselineDate), by, ps.TicketCount, pluralS(ps.TicketCount), esc(ps.PlannedEnd)))
		snap := muted("No snapshot yet")
		if ps.LastSnapshot != "" {
			snap = "Last snapshot <strong>" + esc(ps.LastSnapshot) + "</strong>"
			if ps.LastSnapshotBy != "" {
				snap += " " + muted("("+esc(ps.LastSnapshotBy)+")")
			}
		}
		parts = append(parts, `<span class="plan-snapshot">`+snap+`</span> `+
			btn("planSnapshot", "Take snapshot", "Compare the tickets against the baseline now (also runs automatically once a day)"))
		slipColor := "#16a34a"
		if ps.UnresolvedSlips > 0 {
			slipColor = "#dc2626"
		}
		parts = append(parts, fmt.Sprintf(`<a class="plan-slips" href="#slip-ledger" onclick="return planShowLedger()" style="color:%s;font-weight:600;text-decoration:none">%d unresolved slip%s</a>`,
			slipColor, ps.UnresolvedSlips, pluralS(ps.UnresolvedSlips)))
		rb := btn("planRebaseline", "Re-baseline…", "Archive this baseline with a reason and baseline the current tickets")
		if ps.ArchivedBaselines > 0 {
			rb += " " + muted(fmt.Sprintf("%d earlier baseline%s", ps.ArchivedBaselines, pluralS(ps.ArchivedBaselines)))
		}
		parts = append(parts, rb)
	}
	return fmt.Sprintf(`
<div id="plan-strip" class="plan-strip" data-project="%s" style="margin:20px 24px 0;background:#fff;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:12px 22px;display:flex;flex-wrap:wrap;gap:10px 22px;align-items:center;font-size:13px;color:#222">
<h3 style="font-size:13px;text-transform:uppercase;color:#666;letter-spacing:.5px;margin:0">Plan</h3>
%s
</div>
%s`, esc(projectID), strings.Join(parts, "\n"), planStripJS)
}
