// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package teamsync

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"hate/internal/gitacct"
	"hate/internal/ticket"
)

// Automatic sync.
//
// Only when a Git account is configured (Enabled). Then a project is synced:
//   - when it is opened in the app (Open),
//   - every Interval while the app runs, for each project opened this session,
//   - before the PM dashboard renders (BeforeDashboard), unless it pulled less
//     than MinPullGap ago,
//   - PushDelay after the last local commit (Committed; debounced, so a burst
//     of edits becomes one push).
//
// Each round runs under the project lock. Timed rounds use TryLockProject and
// try again shortly when the project is busy, so they never queue up behind a
// person's edit. Without an account none of this runs; the Sync button (Manual)
// still works.

// Manager runs the automatic sync and keeps each project's status.
type Manager struct {
	Interval    time.Duration
	PushDelay   time.Duration
	MinPullGap  time.Duration
	RetryDelay  time.Duration // after Offline, or when the project was busy
	Enabled     func() bool
	DashTimeout time.Duration // how long the dashboard waits for its pull

	mu       sync.Mutex
	projects map[string]*project
	stop     chan struct{}
}

type project struct {
	root     string
	open     bool // opened in the app this session: included in the periodic rounds
	running  bool
	timer    *time.Timer
	state    State
	message  string
	detail   string
	notes    []string
	lastSync time.Time // last round that reached the shared copy
	lastTry  time.Time
	pulled   time.Time // last round that fetched (successfully)
}

// Default is the app's manager.
var Default = NewManager()

// NewManager returns a manager with the standard timings, enabled when a Git
// account is configured. HATE_SYNC_INTERVAL (a Go duration, at least 5s)
// shortens the periodic round, for testing two instances side by side.
func NewManager() *Manager {
	interval := 5 * time.Minute
	if d, err := time.ParseDuration(os.Getenv("HATE_SYNC_INTERVAL")); err == nil && d >= 5*time.Second {
		interval = d
	}
	return &Manager{
		Interval:    interval,
		PushDelay:   10 * time.Second,
		MinPullGap:  time.Minute,
		RetryDelay:  time.Minute,
		DashTimeout: 20 * time.Second,
		Enabled:     gitacct.Configured,
		projects:    map[string]*project{},
	}
}

func key(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Clean(root)
}

func (m *Manager) get(root string) *project {
	k := key(root)
	p := m.projects[k]
	if p == nil {
		p = &project{root: k, state: StateIdle}
		m.projects[k] = p
	}
	return p
}

// Start runs the periodic rounds until Stop.
func (m *Manager) Start() {
	m.mu.Lock()
	if m.stop != nil {
		m.mu.Unlock()
		return
	}
	m.stop = make(chan struct{})
	stop := m.stop
	m.mu.Unlock()
	go func() {
		t := time.NewTicker(m.Interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				m.tick()
			}
		}
	}()
}

// Stop ends the periodic rounds and pending pushes.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop != nil {
		close(m.stop)
		m.stop = nil
	}
	for _, p := range m.projects {
		if p.timer != nil {
			p.timer.Stop()
			p.timer = nil
		}
	}
}

func (m *Manager) tick() {
	if !m.Enabled() {
		return
	}
	m.mu.Lock()
	var roots []string
	for _, p := range m.projects {
		if p.open {
			roots = append(roots, p.root)
		}
	}
	m.mu.Unlock()
	for _, r := range roots {
		m.trySync(r)
	}
}

// Open marks a project as open in the app and syncs it in the background.
// No-op without an account.
func (m *Manager) Open(root string) {
	if !m.Enabled() {
		return
	}
	m.mu.Lock()
	p := m.get(root)
	p.open = true
	p.running = true // the light shows Syncing straight away
	m.mu.Unlock()
	go m.trySync(root)
}

// Committed schedules a sync PushDelay after a local commit (restarting the
// wait on every commit). No-op without an account. Doesn't block: it is called
// from ticket.CommitFiles with the project lock held.
func (m *Manager) Committed(root string) {
	if !m.Enabled() {
		return
	}
	m.schedule(root, m.PushDelay)
}

func (m *Manager) schedule(root string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.get(root)
	if p.timer != nil {
		p.timer.Stop()
	}
	r := p.root
	p.timer = time.AfterFunc(d, func() { m.trySync(r) })
}

// BeforeDashboard pulls before the PM dashboard renders, unless a pull ran in
// the last MinPullGap. It waits for the round (at most DashTimeout for the
// network part). No-op without an account. The caller must not hold the lock.
func (m *Manager) BeforeDashboard(root string) {
	if !m.Enabled() {
		return
	}
	m.mu.Lock()
	p := m.get(root)
	recent := time.Since(p.pulled) < m.MinPullGap
	m.mu.Unlock()
	if recent {
		return
	}
	unlock := ticket.LockProject(root)
	defer unlock()
	m.mu.Lock()
	recent = time.Since(p.pulled) < m.MinPullGap // another request may have pulled while we waited
	m.mu.Unlock()
	if recent {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.DashTimeout)
	defer cancel()
	m.runLocked(ctx, root)
}

// trySync runs a round if the project isn't busy; otherwise it tries again
// after RetryDelay (or sooner for a pending push).
func (m *Manager) trySync(root string) {
	if !m.Enabled() {
		return
	}
	unlock, ok := ticket.TryLockProject(root)
	if !ok {
		m.schedule(root, minDur(m.RetryDelay, 2*time.Second))
		return
	}
	defer unlock()
	res := m.runLocked(context.Background(), root)
	if res.State == StateOffline {
		m.schedule(root, m.RetryDelay)
	}
}

// Manual runs a round for the Sync button (with or without an account) and
// records it for the status light. The caller holds the project lock.
func (m *Manager) Manual(root string) Result {
	return m.runLocked(context.Background(), root)
}

// runLocked runs one round and records the outcome. The caller holds the lock.
func (m *Manager) runLocked(ctx context.Context, root string) Result {
	m.mu.Lock()
	p := m.get(root)
	p.running = true
	p.lastTry = now()
	m.mu.Unlock()

	res := Sync(ctx, root)

	m.mu.Lock()
	defer m.mu.Unlock()
	p.running = false
	p.state, p.message, p.detail, p.notes = res.State, res.Message, res.Detail, res.Notes
	if res.Complete {
		p.lastSync = now()
	}
	if res.Fetched {
		p.pulled = now()
	}
	if res.State != StateSynced {
		log.Printf("sync (%s): %s %s", root, res.Message, res.Detail)
	}
	return res
}

// Status is a project's sync state for the header light.
type Status struct {
	Enabled  bool       `json:"enabled"` // automatic sync is on (a Git account is configured)
	State    State      `json:"state"`
	Label    string     `json:"label"` // "Synced", "Syncing", "Offline", "Needs attention", ...
	Message  string     `json:"message"`
	Detail   string     `json:"detail,omitempty"`
	Notes    []string   `json:"notes,omitempty"`
	LastSync *time.Time `json:"last_sync,omitempty"`
	Head     string     `json:"head"` // current commit; changes when a sync brings something in
}

// Labels for the light (plain words).
var labels = map[State]string{
	StateIdle:      "Not synced yet",
	StateSynced:    "Synced",
	StateSyncing:   "Syncing",
	StateOffline:   "Offline",
	StateAttention: "Needs attention",
	StateLocal:     "Not shared",
}

// Status returns the project's current status (no network).
func (m *Manager) Status(root string) Status {
	m.mu.Lock()
	p := m.get(root)
	st := Status{Enabled: m.Enabled(), State: p.state, Message: p.message, Detail: p.detail, Notes: append([]string(nil), p.notes...)}
	if p.running {
		st.State = StateSyncing
	}
	if !p.lastSync.IsZero() {
		t := p.lastSync
		st.LastSync = &t
	}
	m.mu.Unlock()
	st.Label = labels[st.State]
	st.Head = Head(root)
	return st
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
