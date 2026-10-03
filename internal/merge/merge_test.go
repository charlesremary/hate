// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package merge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hate/internal/pm"
	"hate/internal/ticket"
)

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestClassify(t *testing.T) {
	cases := map[string]Kind{
		"tickets/CW-1a2b.json":                KindTicket,
		"./tickets/CW-1a2b.json":              KindTicket,
		`tickets\CW-1a2b.json`:                KindTicket,
		"tickets/sub/x.json":                  KindUnknown,
		".tkt/config.json":                    KindConfig,
		".tkt/pm/slip_events.json":            KindSlipEvents,
		".tkt/pm/forecast_history.json":       KindForecastHistory,
		".tkt/pm/baseline.json":               KindBaseline,
		".tkt/pm/baselines/2026-10-02-1.json": KindBaselineArchive,
		".tkt/pm/snapshots/2026-10-02.json":   KindSnapshot,
		"index.json":                          KindIndex,
		"attachments/CW-1/abc-shot.png":       KindUnknown,
		".gitignore":                          KindUnknown,
		".tkt/pm/.gitignore":                  KindUnknown,
	}
	for p, want := range cases {
		if got := Classify(filepath.FromSlash(p)); got != want && !(strings.Contains(p, `\`) && got == KindUnknown) {
			t.Errorf("Classify(%q) = %s, want %s", p, got, want)
		}
	}
	if !Handles("tickets/X-1.json") || Handles("README.md") {
		t.Error("Handles")
	}
}

// slipFile builds a slip_events.json body.
func slipFile(t *testing.T, evs ...pm.SlipEvent) []byte {
	t.Helper()
	if evs == nil {
		evs = []pm.SlipEvent{}
	}
	data, _ := json.MarshalIndent(evs, "", "  ")
	return data
}

// HATE-wn0y tc1: two machines detecting the same slip produce an identical
// event (same id, same content); merging their files yields one event.
func TestSameSlipOnTwoMachinesMergesToOne(t *testing.T) {
	b := pm.Baseline{CreatedDate: "2026-09-01", CreatedBy: "chuck@x", ProjectID: "CW", PlannedEnd: "2026-10-01",
		Tasks: []pm.BaselineTask{{TaskID: "CW-1", PlannedEnd: "2026-09-20", ProjectID: "CW"}}}
	cur := map[string]map[string]interface{}{"CW-1": {"due_date": "2026-09-27"}}
	day := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)
	pmMachine := pm.DetectSlipEvents(pm.BaselineKey(b), b.Tasks, cur, nil, day)
	chuckMachine := pm.DetectSlipEvents(pm.BaselineKey(b), b.Tasks, cur, nil, day.Add(2*time.Hour))
	mine, theirs := slipFile(t, pmMachine...), slipFile(t, chuckMachine...)
	if string(mine) != string(theirs) {
		t.Fatalf("the two machines wrote different files:\n%s\n%s", mine, theirs)
	}
	base := slipFile(t)
	res, err := ResolveFile(".tkt/pm/slip_events.json", base, mine, theirs)
	if err != nil || res.NeedsAttention {
		t.Fatalf("resolve: %v %+v", err, res)
	}
	var evs []pm.SlipEvent
	if err := json.Unmarshal(res.Merged, &evs); err != nil || len(evs) != 1 {
		t.Fatalf("merged = %s (%v)", res.Merged, err)
	}

	// Detected a day apart and resolved on one side: one event, resolved,
	// with the earlier detection date.
	later := pm.DetectSlipEvents(pm.BaselineKey(b), b.Tasks, cur, nil, day.AddDate(0, 0, 1))
	cat, narr, by, d := "client_delay", "waiting", "pm@x", "2026-09-23"
	later[0].Status, later[0].ReasonCategory, later[0].ReasonNarrative, later[0].AcknowledgedBy, later[0].AcknowledgedDate = "resolved", &cat, &narr, &by, &d
	merged, notes, err := MergeSlipEvents(base, slipFile(t, later...), slipFile(t, pmMachine...))
	if err != nil || len(notes) != 0 {
		t.Fatal(err, notes)
	}
	evs = nil
	_ = json.Unmarshal(merged, &evs)
	if len(evs) != 1 || evs[0].Status != "resolved" || evs[0].DetectedDate != "2026-09-22" || *evs[0].ReasonCategory != "client_delay" {
		t.Errorf("merged = %+v", evs)
	}
}

// Legacy sequential ids reused by two machines for different slips: both
// events survive, this machine's under a new id.
func TestLegacySlipIDCollision(t *testing.T) {
	a := pm.SlipEvent{SlipEventID: "SE-CW-001", TaskID: "CW-1", ProjectID: "CW", DetectedDate: "2026-09-01", OriginalDueDate: "2026-09-01", RevisedDueDate: "2026-09-05", Status: "unresolved", LinkedTickets: []string{}}
	b := a
	b.TaskID, b.RevisedDueDate = "CW-2", "2026-09-09"
	merged, notes, err := MergeSlipEvents(slipFile(t), slipFile(t, b), slipFile(t, a))
	if err != nil {
		t.Fatal(err)
	}
	var evs []pm.SlipEvent
	_ = json.Unmarshal(merged, &evs)
	if len(evs) != 2 || evs[0].SlipEventID != "SE-CW-001" || evs[0].TaskID != "CW-1" ||
		!strings.HasPrefix(evs[1].SlipEventID, "SE-CW-001-") || evs[1].TaskID != "CW-2" || len(notes) != 1 {
		t.Errorf("merged = %+v, notes %v", evs, notes)
	}
}

func baseTicket() *ticket.Ticket {
	tk := ticket.BlankTicket("CW-1", "dev_task", "Original title", "chuck@x")
	tk.CreatedAt, tk.UpdatedAt = "2026-09-01T10:00:00Z", "2026-09-01T10:00:00Z"
	tk.Activity[0].Timestamp = tk.CreatedAt
	tk.Tags = []string{"functional", "parent:CW-0"}
	tk.TimeEntries = []ticket.TimeEntry{{ID: "t1", Date: "2026-09-01", Hours: 1, Description: "start", Author: "chuck@x", LoggedAt: "2026-09-01T10:00:00Z"}}
	return tk
}

func clone(t *testing.T, tk *ticket.Ticket) *ticket.Ticket {
	t.Helper()
	var c ticket.Ticket
	if err := json.Unmarshal(mustJSON(t, tk), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func logTime(tk *ticket.Ticket, id, desc, author, at string) {
	tk.TimeEntries = append(tk.TimeEntries, ticket.TimeEntry{ID: id, Date: at[:10], Hours: 0.5, Description: desc, Author: author, LoggedAt: at})
	tk.Activity = append(tk.Activity, ticket.Activity{Timestamp: at, Author: author, Action: "time_logged", Detail: desc})
	tk.UpdatedAt = at
}

// HATE-wn0y tc2: time entries added on both sides (each machine called its
// new entry "t2") are both in the merged ticket.
func TestTicketTimeEntriesFromBothSides(t *testing.T) {
	base := baseTicket()
	mine, theirs := clone(t, base), clone(t, base)
	logTime(mine, "t2", "pm review", "pm@x", "2026-09-02T09:00:00Z")
	logTime(theirs, "t2", "chuck coding", "chuck@x", "2026-09-02T11:00:00Z")
	res, err := ResolveFile("tickets/CW-1.json", mustJSON(t, base), mustJSON(t, mine), mustJSON(t, theirs))
	if err != nil || res.NeedsAttention {
		t.Fatalf("resolve: %v %+v", err, res)
	}
	var got ticket.Ticket
	if err := json.Unmarshal(res.Merged, &got); err != nil {
		t.Fatal(err)
	}
	byDesc := map[string]string{}
	for _, e := range got.TimeEntries {
		byDesc[e.Description] = e.ID
	}
	if len(got.TimeEntries) != 3 || byDesc["start"] != "t1" || byDesc["chuck coding"] != "t2" || byDesc["pm review"] != "t3" {
		t.Errorf("time entries = %+v", got.TimeEntries)
	}
	if len(got.Activity) != 3 || got.Activity[1].Detail != "pm review" || got.Activity[2].Detail != "chuck coding" {
		t.Errorf("activity = %+v", got.Activity)
	}
	if got.UpdatedAt != "2026-09-02T11:00:00Z" || res.Note != "" {
		t.Errorf("updated_at %s, note %q", got.UpdatedAt, res.Note)
	}
	// The output is hate's own format.
	if !strings.HasPrefix(string(res.Merged), "{\n  \"schema_version\": ") || !strings.HasSuffix(string(res.Merged), "}\n") {
		t.Errorf("format:\n%s", res.Merged)
	}
}

// HATE-wn0y tc3: the same field edited on both sides: the newer updated_at
// wins and an activity note records both values. One-sided changes, tag
// additions/removals and deletions merge cleanly.
func TestTicketSameFieldBothSides(t *testing.T) {
	base := baseTicket()
	mine, theirs := clone(t, base), clone(t, base)
	mine.Title, mine.UpdatedAt = "PM title", "2026-09-03T08:00:00Z"
	theirs.Title, theirs.UpdatedAt = "Chuck title", "2026-09-02T08:00:00Z"
	d := "2026-10-01"
	theirs.DueDate = &d                          // one side only
	mine.Tags = []string{"functional", "urgent"} // removed parent:CW-0, added urgent
	theirs.Tags = append(theirs.Tags, "qa")      // added qa
	theirs.TimeEntries = nil                     // deleted t1 remotely (unchanged here)

	merged, notes, err := MergeTicket(mustJSON(t, base), mustJSON(t, mine), mustJSON(t, theirs))
	if err != nil {
		t.Fatal(err)
	}
	var got ticket.Ticket
	_ = json.Unmarshal(merged, &got)
	if got.Title != "PM title" {
		t.Errorf("title = %q, want the newer edit's", got.Title)
	}
	if got.DueDate == nil || *got.DueDate != d {
		t.Error("one-sided due date change lost")
	}
	if strings.Join(got.Tags, ",") != "functional,qa,urgent" {
		t.Errorf("tags = %v", got.Tags)
	}
	if len(got.TimeEntries) != 0 {
		t.Errorf("deleted time entry came back: %+v", got.TimeEntries)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v", notes)
	}
	last := got.Activity[len(got.Activity)-1]
	if last.Action != ticket.ActionSyncMerge || last.Author != SyncAuthor || last.Timestamp != "2026-09-03T08:00:00Z" ||
		!strings.Contains(last.Detail, `"PM title"`) || !strings.Contains(last.Detail, `"Chuck title"`) || !strings.Contains(last.Detail, "this machine") {
		t.Errorf("note = %+v", last)
	}
	if got.UpdatedAt != "2026-09-03T08:00:00Z" {
		t.Errorf("updated_at = %s", got.UpdatedAt)
	}

	// Remote newer: the remote value wins.
	theirs.UpdatedAt = "2026-09-04T08:00:00Z"
	merged, _, _ = MergeTicket(mustJSON(t, base), mustJSON(t, mine), mustJSON(t, theirs))
	_ = json.Unmarshal(merged, &got)
	if got.Title != "Chuck title" {
		t.Errorf("title = %q, want the remote (newer) one", got.Title)
	}

	// Different tickets don't merge.
	other := clone(t, theirs)
	other.ID = "CW-2"
	if _, _, err := MergeTicket(nil, mustJSON(t, mine), mustJSON(t, other)); err == nil {
		t.Error("merged two different tickets")
	}
}

// HATE-wn0y tc4: an unparseable remote file keeps the remote version, saves
// the local one under .tkt/conflicts/ and reports needs-attention; Apply
// writes both.
func TestUnparseableRemoteFallsBack(t *testing.T) {
	base := baseTicket()
	mine := clone(t, base)
	mine.Title = "local edit"
	theirs := []byte("{\"id\": \"CW-1\", <<<<<<< broken")
	res, err := ResolveFile("tickets/CW-1.json", mustJSON(t, base), mustJSON(t, mine), theirs)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Merged) != string(theirs) || !res.NeedsAttention || len(res.SideFiles) != 1 {
		t.Fatalf("resolution = %+v", res)
	}
	sf := res.SideFiles[0]
	if sf.Kind != SideConflictCopy || !strings.HasPrefix(sf.Path, ".tkt/conflicts/tickets__CW-1.") || !strings.HasSuffix(sf.Path, ".json") ||
		string(sf.Data) != string(mustJSON(t, mine)) {
		t.Errorf("side file = %s %s", sf.Kind, sf.Path)
	}
	if !strings.Contains(res.Note, "Kept the version from the shared repo") || !strings.Contains(res.Note, sf.Path) {
		t.Errorf("note = %q", res.Note)
	}

	root := t.TempDir()
	written, err := Apply(root, "tickets/CW-1.json", res)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || written[0] != "tickets/CW-1.json" || written[1] != sf.Path {
		t.Errorf("written = %v", written)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "tickets", "CW-1.json")); string(got) != string(theirs) {
		t.Error("remote not kept on disk")
	}
	if got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(sf.Path))); string(got) != string(mustJSON(t, mine)) {
		t.Error("local copy not saved")
	}

	// An unknown file type takes the same fallback.
	res, _ = ResolveFile("notes/readme.txt", []byte("a"), []byte("b"), []byte("c"))
	if string(res.Merged) != "c" || !res.NeedsAttention || res.SideFiles[0].Path != ConflictCopyPath("notes/readme.txt", []byte("b")) {
		t.Errorf("unknown kind = %+v", res)
	}
}

func TestDeletions(t *testing.T) {
	a, b := []byte(`{"id":"X"}`), []byte(`{"id":"X","title":"t"}`)
	if res, _ := ResolveFile("tickets/X.json", a, a, nil); !res.Delete || res.NeedsAttention {
		t.Errorf("deleted remotely, unchanged here: %+v", res)
	}
	if res, _ := ResolveFile("tickets/X.json", a, b, nil); !res.Delete || !res.NeedsAttention || len(res.SideFiles) != 1 {
		t.Errorf("deleted remotely, changed here: %+v", res)
	}
	if res, _ := ResolveFile("tickets/X.json", a, nil, b); res.Delete || string(res.Merged) != string(b) {
		t.Errorf("deleted here, changed remotely: %+v", res)
	}
	if _, err := ResolveFile("", a, a, a); err == nil {
		t.Error("empty path accepted")
	}
}

func TestConfigMerge(t *testing.T) {
	base := ticket.DefaultConfig("c", "Proj", "p", "P")
	base.Resources = []ticket.Resource{{Name: "Chuck", Email: "chuck@x", Role: "dev"}}
	base.Links = []ticket.Link{{ID: "l1", Description: "repo", URL: "https://a"}}
	mine, theirs := *base, *base
	mine.Resources = append(append([]ticket.Resource{}, base.Resources...), ticket.Resource{Name: "PM", Email: "pm@x", Role: "pm"})
	theirs.Resources = append(append([]ticket.Resource{}, base.Resources...), ticket.Resource{Name: "QA", Email: "qa@x", Role: "qa"})
	mine.ProjectName, theirs.ProjectName = "PM name", "Chuck name"
	mine.EffortToDays = map[string]float64{"xs": 1, "s": 2, "m": 4, "l": 5, "xl": 8}
	theirs.AutoPush = true
	theirs.Links = nil // removed remotely

	res, err := ResolveFile(".tkt/config.json", mustJSON(t, base), mustJSON(t, &mine), mustJSON(t, &theirs))
	if err != nil || res.NeedsAttention {
		t.Fatalf("%v %+v", err, res)
	}
	var got ticket.ProjectConfig
	if err := json.Unmarshal(res.Merged, &got); err != nil {
		t.Fatal(err)
	}
	emails := []string{}
	for _, r := range got.Resources {
		emails = append(emails, r.Email)
	}
	if strings.Join(emails, ",") != "chuck@x,qa@x,pm@x" {
		t.Errorf("resources = %v", emails)
	}
	if got.ProjectName != "Chuck name" || !strings.Contains(res.Note, "project_name") || !strings.Contains(res.Note, `"PM name"`) {
		t.Errorf("project_name %q, note %q", got.ProjectName, res.Note)
	}
	if got.EffortToDays["m"] != 4 || !got.AutoPush || len(got.Links) != 0 {
		t.Errorf("merged config = %+v", got)
	}
}

func TestForecastHistoryMerge(t *testing.T) {
	e := func(date, likely, at string) pm.ForecastHistoryEntry {
		return pm.ForecastHistoryEntry{Date: date, LikelyFinish: likely, P85Finish: likely, RequestedEnd: "2026-12-01", ComputedAt: at}
	}
	base := []pm.ForecastHistoryEntry{e("2026-09-01", "2026-11-01", "2026-09-01T08:00:00Z")}
	mine := append(append([]pm.ForecastHistoryEntry{}, base...), e("2026-09-02", "2026-11-03", "2026-09-02T15:00:00Z"), e("2026-09-04", "2026-11-04", "2026-09-04T08:00:00Z"))
	theirs := append(append([]pm.ForecastHistoryEntry{}, base...), e("2026-09-02", "2026-11-02", "2026-09-02T09:00:00Z"), e("2026-09-03", "2026-11-02", "2026-09-03T09:00:00Z"))
	res, err := ResolveFile(".tkt/pm/forecast_history.json", mustJSON(t, base), mustJSON(t, mine), mustJSON(t, theirs))
	if err != nil {
		t.Fatal(err)
	}
	var got []pm.ForecastHistoryEntry
	_ = json.Unmarshal(res.Merged, &got)
	if len(got) != 4 || got[1].Date != "2026-09-02" || got[1].LikelyFinish != "2026-11-03" || got[2].Date != "2026-09-03" || got[3].Date != "2026-09-04" {
		t.Errorf("merged = %+v", got)
	}
}

// Re-baselined on both machines: the remote baseline is kept, the local one
// becomes an archived baseline (listed by pm.ListBaselineArchive).
func TestBaselineBothSides(t *testing.T) {
	b0 := pm.Baseline{CreatedDate: "2026-09-01", CreatedBy: "chuck@x", ProjectID: "p", PlannedEnd: "2026-10-01", Tasks: []pm.BaselineTask{}}
	mine, theirs := b0, b0
	mine.CreatedDate, mine.CreatedBy, mine.PlannedEnd = "2026-09-10", "pm@x", "2026-10-15"
	theirs.CreatedDate, theirs.PlannedEnd = "2026-09-10", "2026-10-20"
	res, err := ResolveFile(".tkt/pm/baseline.json", mustJSON(t, b0), mustJSON(t, mine), mustJSON(t, theirs))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Merged) != string(mustJSON(t, theirs)) || !res.NeedsAttention || len(res.SideFiles) != 1 || res.SideFiles[0].Kind != SideBaselineArchive {
		t.Fatalf("resolution = %+v", res)
	}
	root := t.TempDir()
	written, err := Apply(root, ".tkt/pm/baseline.json", res)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")
	if len(written) != 2 || written[1] != ".tkt/pm/baselines/"+today+"-1.json" {
		t.Errorf("written = %v", written)
	}
	list, err := pm.ListBaselineArchive(root)
	if err != nil || len(list) != 1 || list[0].CreatedBy != "pm@x" || list[0].PlannedEnd != "2026-10-15" || !strings.HasPrefix(list[0].Reason, "sync:") {
		t.Errorf("archive = %+v (%v)", list, err)
	}

	// Changed on one side only: that side, no attention needed.
	res, _ = ResolveFile(".tkt/pm/baseline.json", mustJSON(t, b0), mustJSON(t, b0), mustJSON(t, theirs))
	if res.NeedsAttention || string(res.Merged) != string(mustJSON(t, theirs)) {
		t.Errorf("one-sided baseline = %+v", res)
	}

	// Two different archives under one name: the local one gets the next name.
	arcA, _ := json.Marshal(pm.BaselineArchive{Reason: "a", Baseline: json.RawMessage(`{}`)})
	arcB, _ := json.Marshal(pm.BaselineArchive{Reason: "b", Baseline: json.RawMessage(`{}`)})
	res, _ = ResolveFile(".tkt/pm/baselines/"+today+"-1.json", nil, arcA, arcB)
	written, _ = Apply(root, ".tkt/pm/baselines/"+today+"-1.json", res)
	if string(res.Merged) != string(arcB) || len(written) != 2 || written[1] != ".tkt/pm/baselines/"+today+"-2.json" {
		t.Errorf("archive collision: %+v %v", res, written)
	}
}
