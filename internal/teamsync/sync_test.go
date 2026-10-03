// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package teamsync

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"hate/internal/config"
	"hate/internal/merge"
	"hate/internal/pm"
	"hate/internal/ticket"
)

// Two "machines" (Chuck and the PM) sharing one bare repo over file://. The
// Git account is not involved here (file transport needs no token); the
// manager's Enabled switch stands in for "a Git account is configured".

type machine struct {
	t    *testing.T
	root string
	who  string
}

func (m machine) git(args ...string) string {
	m.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = m.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.t.Fatalf("%s: git %v: %v %s", m.who, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_DIR", "")
	os.Unsetenv("GIT_DIR")
	keyring.MockInit()
	old := config.AppConfigPath
	config.AppConfigPath = filepath.Join(t.TempDir(), "config.json") // no Git account
	t.Cleanup(func() { config.AppConfigPath = old })
}

// twoMachines makes a bare "shared copy" holding a hate project with one
// ticket, and two clones of it.
func twoMachines(t *testing.T) (bare string, chuck, pmm machine) {
	t.Helper()
	isolateGit(t)
	base := t.TempDir()
	bare = filepath.Join(base, "shared.git")
	seed := machine{t, filepath.Join(base, "seed"), "seed"}
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run(base, "init", "-q", "--bare", "-b", "main", bare)
	run(base, "init", "-q", "-b", "main", seed.root)
	seed.git("config", "user.name", "Seed")
	seed.git("config", "user.email", "seed@x")
	if err := ticket.WriteConfig(seed.root, ticket.DefaultConfig("Acme", "Sync test", "ST", "ST")); err != nil {
		t.Fatal(err)
	}
	tk := ticket.BlankTicket("ST-0001", "task", "First ticket", "seed@x")
	tk.UpdatedAt = "2026-09-01T08:00:00Z"
	if err := ticket.WriteTicket(seed.root, tk); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(seed.root, ".gitignore"), []byte("index.json\n"), 0644)
	os.WriteFile(filepath.Join(seed.root, "notes.txt"), []byte("shared notes\n"), 0644)
	seed.git("add", "-A")
	seed.git("commit", "-q", "-m", "seed")
	seed.git("remote", "add", "origin", "file://"+bare)
	seed.git("push", "-q", "-u", "origin", "main")

	clone := func(who string) machine {
		dir := filepath.Join(base, who)
		run(base, "clone", "-q", "file://"+bare, dir)
		m := machine{t, dir, who}
		m.git("config", "user.name", who)
		m.git("config", "user.email", who+"@x")
		return m
	}
	return bare, clone("chuck"), clone("pm")
}

func (m machine) sync() Result {
	m.t.Helper()
	defer ticket.LockProject(m.root)()
	return Sync(context.Background(), m.root)
}

func (m machine) commit(msg string, files ...string) {
	m.t.Helper()
	if err := ticket.CommitFiles(m.root, files, msg); err != nil {
		m.t.Fatal(err)
	}
}

func (m machine) editTicket(id string, f func(*ticket.Ticket)) {
	m.t.Helper()
	tk, err := ticket.ReadTicket(m.root, id)
	if err != nil {
		m.t.Fatal(err)
	}
	f(tk)
	if err := ticket.WriteTicket(m.root, tk); err != nil {
		m.t.Fatal(err)
	}
	m.commit("edit "+id, "tickets/"+id+".json")
}

func (m machine) clean() {
	m.t.Helper()
	if _, err := os.Stat(filepath.Join(m.root, ".git", "MERGE_HEAD")); err == nil {
		m.t.Errorf("%s: left mid-merge", m.who)
	}
	if u := m.git("diff", "--name-only", "--diff-filter=U"); u != "" {
		m.t.Errorf("%s: unmerged paths: %s", m.who, u)
	}
}

func remoteHead(t *testing.T, bare string) string {
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testManager(enabled bool) *Manager {
	m := NewManager()
	m.Interval = 100 * time.Millisecond
	m.PushDelay = 300 * time.Millisecond
	m.RetryDelay = 200 * time.Millisecond
	m.MinPullGap = time.Minute
	m.Enabled = func() bool { return enabled }
	return m
}

// HATE-4u0j tc1: a ticket created on the other machine appears within one
// sync cycle, without clicking anything (the project is open; periodic round).
func TestRemoteTicketAppearsWithoutClicking(t *testing.T) {
	_, chuck, pmm := twoMachines(t)
	m := testManager(true)
	m.Open(pmm.root)
	waitFor(t, "first sync", 5*time.Second, func() bool { return m.Status(pmm.root).State == StateSynced })
	m.Start()
	defer m.Stop()

	tk := ticket.BlankTicket("ST-0002", "task", "Made by Chuck", "chuck@x")
	if err := ticket.WriteTicket(chuck.root, tk); err != nil {
		t.Fatal(err)
	}
	chuck.commit("new ticket", "tickets/ST-0002.json")
	if r := chuck.sync(); r.State != StateSynced || r.Pushed != 1 {
		t.Fatalf("chuck sync: %+v", r)
	}
	before := m.Status(pmm.root).Head
	waitFor(t, "ticket on the PM's machine", 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(pmm.root, "tickets", "ST-0002.json"))
		return err == nil
	})
	st := m.Status(pmm.root)
	if st.Head == before || st.LastSync == nil || st.Label != "Synced" && st.Label != "Syncing" {
		t.Errorf("status = %+v", st)
	}
	// index.json was regenerated with the new ticket.
	waitFor(t, "index", 2*time.Second, func() bool {
		b, _ := os.ReadFile(filepath.Join(pmm.root, "index.json"))
		return strings.Contains(string(b), "ST-0002")
	})
	pmm.clean()
}

// HATE-4u0j tc2: a local edit is pushed shortly after the last commit
// (debounced: a burst of commits becomes one push), and the light is Synced.
func TestLocalEditPushedAfterDebounce(t *testing.T) {
	bare, _, pmm := twoMachines(t)
	m := testManager(true)
	var pushes int32
	old := ticket.AfterCommit
	ticket.AfterCommit = func(root string) { atomic.AddInt32(&pushes, 1); m.Committed(root) }
	t.Cleanup(func() { ticket.AfterCommit = old })
	defer m.Stop()

	start := remoteHead(t, bare)
	for i := 0; i < 3; i++ {
		pmm.editTicket("ST-0001", func(tk *ticket.Ticket) { tk.Title = "edit " + string(rune('A'+i)) })
	}
	if atomic.LoadInt32(&pushes) != 3 {
		t.Fatalf("AfterCommit called %d times", pushes)
	}
	if remoteHead(t, bare) != start {
		t.Fatal("pushed before the debounce delay")
	}
	waitFor(t, "push", 5*time.Second, func() bool { return remoteHead(t, bare) == pmm.git("rev-parse", "HEAD") })
	waitFor(t, "synced light", 2*time.Second, func() bool { return m.Status(pmm.root).Label == "Synced" })
}

// HATE-4u0j tc3: both machines detected the same slip while offline (on
// different days), then sync: one slip event, no needs-attention.
func TestSameSlipBothMachinesConverges(t *testing.T) {
	_, chuck, pmm := twoMachines(t)
	b := pm.Baseline{CreatedDate: "2026-09-01", CreatedBy: "chuck@x", ProjectID: "ST", PlannedEnd: "2026-10-01",
		Tasks: []pm.BaselineTask{{TaskID: "ST-0001", PlannedEnd: "2026-09-20", ProjectID: "ST"}}}
	cur := map[string]map[string]interface{}{"ST-0001": {"due_date": "2026-09-27"}}
	write := func(mc machine, day time.Time) {
		evs := pm.DetectSlipEvents(pm.BaselineKey(b), b.Tasks, cur, nil, day)
		data, _ := json.MarshalIndent(evs, "", "  ")
		p := filepath.Join(mc.root, ".tkt", "pm", "slip_events.json")
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
		mc.commit("slip detected", ".tkt/pm/slip_events.json")
	}
	write(chuck, time.Date(2026, 9, 22, 9, 0, 0, 0, time.Local))
	write(pmm, time.Date(2026, 9, 23, 9, 0, 0, 0, time.Local))
	if r := chuck.sync(); r.State != StateSynced {
		t.Fatalf("chuck: %+v", r)
	}
	r := pmm.sync()
	if r.State != StateSynced || !r.Merged || r.Pushed == 0 {
		t.Fatalf("pm: %+v", r)
	}
	pmm.clean()
	var evs []pm.SlipEvent
	data, _ := os.ReadFile(filepath.Join(pmm.root, ".tkt", "pm", "slip_events.json"))
	if err := json.Unmarshal(data, &evs); err != nil || len(evs) != 1 || evs[0].DetectedDate != "2026-09-22" {
		t.Fatalf("slip events = %s", data)
	}
	// Chuck gets the same single event.
	if r := chuck.sync(); r.State != StateSynced {
		t.Fatalf("chuck again: %+v", r)
	}
	other, _ := os.ReadFile(filepath.Join(chuck.root, ".tkt", "pm", "slip_events.json"))
	if string(other) != string(data) {
		t.Errorf("machines differ:\n%s\n%s", data, other)
	}
}

// A ticket edited on both machines merges by the three-way rule: the newer
// edit wins and an activity note records both values. Pushed, not mid-merge.
func TestConcurrentTicketEditMerges(t *testing.T) {
	bare, chuck, pmm := twoMachines(t)
	chuck.editTicket("ST-0001", func(tk *ticket.Ticket) {
		tk.Title, tk.UpdatedAt = "Chuck's title", "2026-09-02T08:00:00Z"
		tk.Tags = append(tk.Tags, "qa")
	})
	pmm.editTicket("ST-0001", func(tk *ticket.Ticket) { tk.Title, tk.UpdatedAt = "PM's title", "2026-09-03T08:00:00Z" })
	if r := chuck.sync(); r.State != StateSynced {
		t.Fatalf("chuck: %+v", r)
	}
	r := pmm.sync()
	if r.State != StateSynced || !r.Merged || len(r.Notes) != 1 {
		t.Fatalf("pm: %+v", r)
	}
	pmm.clean()
	tk, err := ticket.ReadTicket(pmm.root, "ST-0001")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "PM's title" || !strings.Contains(strings.Join(tk.Tags, ","), "qa") {
		t.Errorf("merged ticket: title %q tags %v", tk.Title, tk.Tags)
	}
	found := false
	for _, a := range tk.Activity {
		if a.Action == ticket.ActionSyncMerge && strings.Contains(a.Detail, "Chuck's title") && strings.Contains(a.Detail, "PM's title") {
			found = true
		}
	}
	if !found {
		t.Errorf("no activity note recording both titles: %+v", tk.Activity)
	}
	if remoteHead(t, bare) != pmm.git("rev-parse", "HEAD") {
		t.Error("merge not pushed")
	}
	if parents := strings.Fields(pmm.git("log", "-1", "--format=%P")); len(parents) != 2 {
		t.Errorf("not a merge commit: %v", parents)
	}
}

// A file hate can't combine: keep the shared version, save this machine's
// under .tkt/conflicts/, Needs attention; still committed and pushed.
func TestUnmergeableFileNeedsAttention(t *testing.T) {
	bare, chuck, pmm := twoMachines(t)
	os.WriteFile(filepath.Join(chuck.root, "notes.txt"), []byte("chuck's notes\n"), 0644)
	chuck.commit("notes", "notes.txt")
	os.WriteFile(filepath.Join(pmm.root, "notes.txt"), []byte("pm's notes\n"), 0644)
	pmm.commit("notes", "notes.txt")
	chuck.sync()
	r := pmm.sync()
	if r.State != StateAttention || !r.Complete || len(r.Notes) != 1 || !strings.Contains(r.Message, "needs a look") {
		t.Fatalf("pm: %+v", r)
	}
	pmm.clean()
	if b, _ := os.ReadFile(filepath.Join(pmm.root, "notes.txt")); string(b) != "chuck's notes\n" {
		t.Errorf("notes.txt = %q", b)
	}
	copyPath := merge.ConflictCopyPath("notes.txt", []byte("pm's notes\n"))
	if b, _ := os.ReadFile(filepath.Join(pmm.root, filepath.FromSlash(copyPath))); string(b) != "pm's notes\n" {
		t.Errorf("conflict copy %s = %q", copyPath, b)
	}
	if remoteHead(t, bare) != pmm.git("rev-parse", "HEAD") {
		t.Error("not pushed")
	}
	m := testManager(true)
	defer ticket.LockProject(pmm.root)()
	if st := m.Manual(pmm.root); st.State != StateSynced { // nothing new: back to Synced
		t.Errorf("next round: %+v", st)
	}
}

// A merge git refuses to start (an uncommitted local change in the way) and a
// merge left in progress are both handled without leaving the repo mid-merge.
func TestMergeNeverLeftHalfDone(t *testing.T) {
	_, chuck, pmm := twoMachines(t)
	os.WriteFile(filepath.Join(chuck.root, "notes.txt"), []byte("chuck's notes\n"), 0644)
	chuck.commit("notes", "notes.txt")
	chuck.sync()
	pmm.editTicket("ST-0001", func(tk *ticket.Ticket) { tk.Title = "pm" })
	os.WriteFile(filepath.Join(pmm.root, "notes.txt"), []byte("uncommitted\n"), 0644) // not committed
	r := pmm.sync()
	if r.State != StateAttention || r.Complete {
		t.Fatalf("pm: %+v", r)
	}
	pmm.clean()
	if b, _ := os.ReadFile(filepath.Join(pmm.root, "notes.txt")); string(b) != "uncommitted\n" {
		t.Errorf("local file touched: %q", b)
	}

	// Simulate a crash mid-merge: the next round undoes it and carries on.
	pmm.git("checkout", "--", "notes.txt")
	os.WriteFile(filepath.Join(pmm.root, "notes.txt"), []byte("pm's notes\n"), 0644)
	pmm.commit("notes", "notes.txt")
	cmd := exec.Command("git", "merge", "@{u}")
	cmd.Dir = pmm.root
	_ = cmd.Run() // conflicts: now mid-merge
	if _, err := os.Stat(filepath.Join(pmm.root, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("setup: expected a merge in progress")
	}
	r = pmm.sync()
	if !r.Complete {
		t.Fatalf("after crash: %+v", r)
	}
	pmm.clean()
}

// HATE-4u0j tc4: no network: Offline; local commits wait and go out once the
// connection is back.
func TestOfflineThenReconnect(t *testing.T) {
	bare, _, pmm := twoMachines(t)
	good := pmm.git("remote", "get-url", "origin")
	pmm.git("remote", "set-url", "origin", "http://127.0.0.1:1/shared.git") // nothing listens on port 1
	pmm.editTicket("ST-0001", func(tk *ticket.Ticket) { tk.Title = "offline edit" })
	m := testManager(true)
	func() {
		defer ticket.LockProject(pmm.root)()
		r := m.Manual(pmm.root)
		if r.State != StateOffline || !strings.Contains(r.Message, "saved on this computer") {
			t.Fatalf("offline: %+v", r)
		}
	}()
	if st := m.Status(pmm.root); st.Label != "Offline" {
		t.Errorf("light = %+v", st)
	}
	if remoteHead(t, bare) == pmm.git("rev-parse", "HEAD") {
		t.Fatal("pushed while offline?")
	}
	pmm.git("remote", "set-url", "origin", good)
	// The manager retries on its own after Offline.
	m.trySync(pmm.root) // first retry happens via the scheduled timer too; force one now
	defer m.Stop()
	waitFor(t, "push after reconnect", 5*time.Second, func() bool { return remoteHead(t, bare) == pmm.git("rev-parse", "HEAD") })
	if st := m.Status(pmm.root); st.Label != "Synced" {
		t.Errorf("light after reconnect = %+v", st)
	}
}

// The scheduled retry after Offline (no manual nudge).
func TestOfflineRetriesOnItsOwn(t *testing.T) {
	bare, _, pmm := twoMachines(t)
	good := pmm.git("remote", "get-url", "origin")
	pmm.git("remote", "set-url", "origin", "http://127.0.0.1:1/shared.git")
	pmm.editTicket("ST-0001", func(tk *ticket.Ticket) { tk.Title = "offline edit" })
	m := testManager(true)
	defer m.Stop()
	m.trySync(pmm.root)
	if st := m.Status(pmm.root); st.State != StateOffline {
		t.Fatalf("state = %+v", st)
	}
	pmm.git("remote", "set-url", "origin", good)
	waitFor(t, "retry push", 5*time.Second, func() bool { return remoteHead(t, bare) == pmm.git("rev-parse", "HEAD") })
}

// HATE-4u0j tc5: without a Git account nothing runs in the background; the
// Sync button (Manual) still works.
func TestNoAccountNoBackgroundSync(t *testing.T) {
	bare, chuck, pmm := twoMachines(t)
	m := testManager(false)
	old := ticket.AfterCommit
	ticket.AfterCommit = m.Committed
	t.Cleanup(func() { ticket.AfterCommit = old })
	m.Start()
	defer m.Stop()

	tk := ticket.BlankTicket("ST-0002", "task", "Made by Chuck", "chuck@x")
	ticket.WriteTicket(chuck.root, tk)
	chuck.commit("new", "tickets/ST-0002.json")
	chuck.sync()

	m.Open(pmm.root)
	m.BeforeDashboard(pmm.root)
	pmm.editTicket("ST-0001", func(tk *ticket.Ticket) { tk.Title = "local" })
	before := remoteHead(t, bare)
	time.Sleep(700 * time.Millisecond) // several intervals and past the push delay
	if _, err := os.Stat(filepath.Join(pmm.root, "tickets", "ST-0002.json")); err == nil {
		t.Error("pulled in the background without an account")
	}
	if remoteHead(t, bare) != before {
		t.Error("pushed in the background without an account")
	}
	st := m.Status(pmm.root)
	if st.Enabled || st.State != StateIdle {
		t.Errorf("status = %+v", st)
	}
	func() {
		defer ticket.LockProject(pmm.root)()
		if r := m.Manual(pmm.root); r.State != StateSynced || r.Pulled == 0 || r.Pushed == 0 {
			t.Errorf("manual sync: %+v", r)
		}
	}()
	if _, err := os.Stat(filepath.Join(pmm.root, "tickets", "ST-0002.json")); err != nil {
		t.Error("manual sync didn't pull")
	}
}

// BeforeDashboard pulls, but not again within MinPullGap.
func TestBeforeDashboardThrottled(t *testing.T) {
	_, chuck, pmm := twoMachines(t)
	m := testManager(true)
	m.BeforeDashboard(pmm.root)
	first := m.Status(pmm.root).LastSync
	if first == nil {
		t.Fatal("no pull before the dashboard")
	}
	tk := ticket.BlankTicket("ST-0003", "task", "x", "chuck@x")
	ticket.WriteTicket(chuck.root, tk)
	chuck.commit("new", "tickets/ST-0003.json")
	chuck.sync()
	m.BeforeDashboard(pmm.root) // within a minute: skipped
	if _, err := os.Stat(filepath.Join(pmm.root, "tickets", "ST-0003.json")); err == nil {
		t.Error("pulled again within a minute")
	}
	m.MinPullGap = 0
	m.BeforeDashboard(pmm.root)
	if _, err := os.Stat(filepath.Join(pmm.root, "tickets", "ST-0003.json")); err != nil {
		t.Error("didn't pull after the gap")
	}
}

func TestNoRemoteIsLocal(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	cmd.Run()
	if r := Sync(context.Background(), dir); r.State != StateLocal {
		t.Errorf("%+v", r)
	}
	if r := Sync(context.Background(), t.TempDir()); r.State != StateLocal {
		t.Errorf("not a repo: %+v", r)
	}
}

func TestNetworkFailureWording(t *testing.T) {
	cases := map[string]State{
		"fatal: unable to access 'https://github.com/a/b.git/': Could not resolve host: github.com":             StateOffline,
		"fatal: unable to access 'https://github.com/a/b.git/': Failed to connect to github.com port 443":       StateOffline,
		"remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/a/b.git/'": StateAttention,
		"remote: Repository not found.\nfatal: repository 'https://github.com/a/b.git/' not found":              StateAttention,
		"fatal: unable to access 'https://github.com/a/b.git/': The requested URL returned error: 403":          StateAttention,
	}
	for out, want := range cases {
		r := networkFailure("get the latest changes", out, nil)
		if r.State != want {
			t.Errorf("%q -> %s, want %s", out, r.State, want)
		}
		for _, jargon := range []string{"rebase", "merge", "fetch", "upstream"} {
			if strings.Contains(strings.ToLower(r.Message), jargon) {
				t.Errorf("jargon %q in %q", jargon, r.Message)
			}
		}
	}
}
