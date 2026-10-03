// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"path/filepath"
	"sync"
)

// The per-project write lock.
//
// Every mutation of a project repo (ticket files, index.json, .tkt/config.json,
// the .tkt/pm files, attachments, git commits, and a sync pull/merge/push) runs
// while holding that project's lock, so a read-modify-write can't interleave
// with another one and two commits can't race on the git index.
//
// THE RULE (the lock is not re-entrant, a second Lock in the same call chain
// deadlocks):
//
//   - The lock is taken exactly once, by the outermost entry point: an HTTP
//     handler, one of the pm entry points that document "takes the project
//     lock" (BaselineNow, TakeSnapshot, AutoSnapshot, Rebaseline,
//     RecordForecastHistory), or the background sync.
//   - Everything below an entry point (ReadTicket/WriteTicket, Promote and the
//     other ticket mutators, WriteConfig, RegenerateIndex, CommitFiles,
//     GitCommit, the pm *Locked helpers, the merge package) never takes it and
//     assumes the caller holds it.
//   - A function holding the lock must not call an entry point. In particular
//     an HTTP handler that calls a pm entry point must not lock first.
//
// Plain reads (GET handlers that only read) don't take the lock: every write is
// atomic (temp file + rename, see fsutil.WriteFileAtomic), so a reader sees
// either the old or the new file, never a partial one.

var (
	projectLocksMu sync.Mutex
	projectLocks   = map[string]*sync.Mutex{}
)

// projectLock returns the mutex for a project root (one per cleaned absolute
// path, created on first use).
func projectLock(repoRoot string) *sync.Mutex {
	key := repoRoot
	if abs, err := filepath.Abs(repoRoot); err == nil {
		key = abs
	}
	key = filepath.Clean(key)
	projectLocksMu.Lock()
	defer projectLocksMu.Unlock()
	m := projectLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		projectLocks[key] = m
	}
	return m
}

// LockProject takes the project's write lock and returns the unlock function:
//
//	defer ticket.LockProject(root)()
//
// See the rule above: take it once, at the entry point.
func LockProject(repoRoot string) (unlock func()) {
	m := projectLock(repoRoot)
	m.Lock()
	return m.Unlock
}

// TryLockProject takes the project's write lock only if it is free. ok is false
// (and unlock nil) when someone else holds it — e.g. a background sync that
// would rather skip a round than wait behind a user's edit.
func TryLockProject(repoRoot string) (unlock func(), ok bool) {
	m := projectLock(repoRoot)
	if !m.TryLock() {
		return nil, false
	}
	return m.Unlock, true
}
