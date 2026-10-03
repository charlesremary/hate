// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package merge resolves git conflicts in the files hate keeps in a project
// repo, so a sync never needs a "Git person".
//
// The entry point is ResolveFile: given a conflicted path and its three
// versions it returns what to write. Everything in ResolveFile is pure (no
// disk, no clock); Apply writes a Resolution into a working tree.
//
// How a sync uses it, per conflicted path (under ticket.LockProject):
//
//	base   := `git show :1:<path>` (nil when missing: added on both sides)
//	mine   := this machine's version (nil when deleted here)
//	theirs := the remote's version  (nil when deleted there)
//	res, err := merge.ResolveFile(path, base, mine, theirs)
//	written, err := merge.Apply(repoRoot, path, res)
//	git add -A -- <written...>
//	if res.NeedsAttention { show res.Note to the user }
//
// Then call ticket.RegenerateIndex once (index.json is derived).
//
// Careful with "ours"/"theirs": during `git pull --rebase` (what GitSync
// does) git's stage :2 ("ours") is the UPSTREAM commit and stage :3
// ("theirs") is the local commit being replayed. Here mine is always this
// machine's edit and theirs is always the remote's, whatever git calls them.
//
// Rules (see docs/plan-pm-git-onboarding.md section 6):
//   - slip events: union by id; a resolved event beats an unresolved one; the
//     earliest detected date is kept.
//   - forecast history: union by date; the later computation wins.
//   - tickets: three-way. Lists with ids (time entries, test cases,
//     attachments) are unioned by id; tags and predecessors are merged as
//     sets; the activity log is unioned. A field changed on one side takes
//     that side; changed on both, the newer updated_at wins and an activity
//     note records both values.
//   - config.json: the same three-way merge, with resources by email and
//     contacts / links / instructions by id; on a both-sides change the
//     remote wins (config has no updated_at) and the Note records it.
//   - baseline.json: keep the remote baseline; the local one is archived as
//     an extra archived baseline. Needs attention.
//   - anything else, or anything unparseable: keep the remote version and
//     save the local one under .tkt/conflicts/. Needs attention. The repo is
//     never left half-merged.
package merge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"hate/internal/fsutil"
)

// Kind is what ResolveFile knows about a path.
type Kind string

const (
	KindUnknown         Kind = "unknown"          // anything else: fallback (keep remote, save local)
	KindTicket          Kind = "ticket"           // tickets/<id>.json
	KindConfig          Kind = "config"           // .tkt/config.json
	KindSlipEvents      Kind = "slip_events"      // .tkt/pm/slip_events.json
	KindForecastHistory Kind = "forecast_history" // .tkt/pm/forecast_history.json
	KindBaseline        Kind = "baseline"         // .tkt/pm/baseline.json
	KindBaselineArchive Kind = "baseline_archive" // .tkt/pm/baselines/<id>.json
	KindIndex           Kind = "index"            // index.json: derived, take remote and regenerate
	KindSnapshot        Kind = "snapshot"         // .tkt/pm/snapshots/*: derived and gitignored
)

// ConflictsDir is where an unmergeable local version is saved, relative to
// the repo root.
const ConflictsDir = ".tkt/conflicts"

// Classify maps a repo-relative path (either separator) to its Kind.
func Classify(p string) Kind {
	p = strings.TrimPrefix(path.Clean(filepath.ToSlash(p)), "./")
	dir, file := path.Split(p)
	switch {
	case p == "index.json":
		return KindIndex
	case dir == "tickets/" && strings.HasSuffix(file, ".json"):
		return KindTicket
	case p == ".tkt/config.json":
		return KindConfig
	case p == ".tkt/pm/slip_events.json":
		return KindSlipEvents
	case p == ".tkt/pm/forecast_history.json":
		return KindForecastHistory
	case p == ".tkt/pm/baseline.json":
		return KindBaseline
	case dir == ".tkt/pm/baselines/" && strings.HasSuffix(file, ".json"):
		return KindBaselineArchive
	case strings.HasPrefix(p, ".tkt/pm/snapshots/"):
		return KindSnapshot
	}
	return KindUnknown
}

// Handles reports whether ResolveFile merges this path by content (true) or
// only through the keep-remote / save-local fallback (false). ResolveFile
// accepts every path either way.
func Handles(p string) bool { return Classify(p) != KindUnknown }

// SideKind says how Apply places a side file.
type SideKind string

const (
	// SideConflictCopy: the local version of an unmergeable file, written at
	// Path (under ConflictsDir).
	SideConflictCopy SideKind = "conflict_copy"
	// SideBaselineArchive: a baseline archive entry (pm.BaselineArchive JSON);
	// Apply writes it as the next free .tkt/pm/baselines/<today>-<n>.json, so
	// it shows up in the baseline history. Path is empty.
	SideBaselineArchive SideKind = "baseline_archive"
)

// SideFile is an extra file a resolution writes besides the conflicted path.
type SideFile struct {
	Kind SideKind
	Path string // repo-relative, "/"-separated; empty for SideBaselineArchive
	Data []byte
}

// Resolution is the outcome for one conflicted path.
type Resolution struct {
	Kind Kind
	// Merged is the content to write at the path (ignored when Delete).
	Merged []byte
	// Delete: the path ends up deleted.
	Delete bool
	// SideFiles to write too (a saved local copy, an archived baseline).
	SideFiles []SideFile
	// Note says, in plain words, what was decided. Empty for a clean merge.
	Note string
	// NeedsAttention: a person should look (something was set aside rather
	// than combined). Note explains what and where.
	NeedsAttention bool
}

// ResolveFile merges the three versions of the repo-relative path p. base is
// nil when the file was added on both sides; mine / theirs are nil when that
// side deleted the file. It never fails on content: anything it can't merge
// falls back to keeping the remote version and saving the local one. err is
// returned only for an impossible input (e.g. an empty path).
func ResolveFile(p string, base, mine, theirs []byte) (Resolution, error) {
	if strings.TrimSpace(p) == "" {
		return Resolution{}, fmt.Errorf("merge: empty path")
	}
	kind := Classify(p)
	rel := strings.TrimPrefix(path.Clean(filepath.ToSlash(p)), "./")

	// Deletions.
	switch {
	case mine == nil && theirs == nil:
		return Resolution{Kind: kind, Delete: true}, nil
	case theirs == nil:
		if base != nil && sameContent(mine, base) {
			return Resolution{Kind: kind, Delete: true}, nil // deleted remotely, untouched here
		}
		res := fallback(kind, rel, nil, mine, "it was deleted in the shared repo but changed on this machine")
		res.Delete = true
		return res, nil
	case mine == nil:
		if base != nil && sameContent(theirs, base) {
			return Resolution{Kind: kind, Delete: true}, nil // deleted here, untouched remotely
		}
		return Resolution{Kind: kind, Merged: theirs,
			Note: fmt.Sprintf("%s was deleted on this machine but changed in the shared repo; kept the shared version.", rel)}, nil
	}
	if sameContent(mine, theirs) {
		return Resolution{Kind: kind, Merged: theirs}, nil
	}

	switch kind {
	case KindIndex:
		return Resolution{Kind: kind, Merged: theirs, Note: "index.json is derived; regenerate it (ticket.RegenerateIndex) after merging."}, nil
	case KindSnapshot:
		return Resolution{Kind: kind, Merged: theirs}, nil
	case KindTicket:
		merged, notes, err := MergeTicket(base, mine, theirs)
		if err != nil {
			return fallback(kind, rel, theirs, mine, err.Error()), nil
		}
		return Resolution{Kind: kind, Merged: merged, Note: joinNotes(rel, notes)}, nil
	case KindConfig:
		merged, notes, err := MergeConfig(base, mine, theirs)
		if err != nil {
			return fallback(kind, rel, theirs, mine, err.Error()), nil
		}
		return Resolution{Kind: kind, Merged: merged, Note: joinNotes(rel, notes)}, nil
	case KindSlipEvents:
		merged, notes, err := MergeSlipEvents(base, mine, theirs)
		if err != nil {
			return fallback(kind, rel, theirs, mine, err.Error()), nil
		}
		return Resolution{Kind: kind, Merged: merged, Note: joinNotes(rel, notes)}, nil
	case KindForecastHistory:
		merged, err := MergeForecastHistory(base, mine, theirs)
		if err != nil {
			return fallback(kind, rel, theirs, mine, err.Error()), nil
		}
		return Resolution{Kind: kind, Merged: merged}, nil
	case KindBaseline:
		return mergeBaseline(rel, base, mine, theirs), nil
	case KindBaselineArchive:
		return mergeBaselineArchive(rel, mine, theirs), nil
	}
	return fallback(kind, rel, theirs, mine, "hate doesn't know how to combine this file"), nil
}

// fallback keeps theirs and saves mine under ConflictsDir.
func fallback(kind Kind, rel string, theirs, mine []byte, why string) Resolution {
	copyPath := ConflictCopyPath(rel, mine)
	return Resolution{
		Kind:      kind,
		Merged:    theirs,
		SideFiles: []SideFile{{Kind: SideConflictCopy, Path: copyPath, Data: mine}},
		Note: fmt.Sprintf("Couldn't combine the two versions of %s (%s). Kept the version from the shared repo; "+
			"this machine's version is saved as %s.", rel, why, copyPath),
		NeedsAttention: true,
	}
}

// ConflictCopyPath is where the local version of rel is saved:
// .tkt/conflicts/<rel with "/" as "__">.<8 hex of the content><ext>, e.g.
// .tkt/conflicts/tickets__CW-x1y2.3fa2b1c9.json. Deterministic, so a retried
// sync doesn't pile up copies.
func ConflictCopyPath(rel string, data []byte) string {
	flat := strings.ReplaceAll(strings.TrimPrefix(rel, "/"), "/", "__")
	ext := path.Ext(flat)
	sum := sha256.Sum256(data)
	return ConflictsDir + "/" + strings.TrimSuffix(flat, ext) + "." + hex.EncodeToString(sum[:])[:8] + ext
}

func joinNotes(rel string, notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return rel + ": " + strings.Join(notes, "; ")
}

// sameContent compares two versions as JSON when both parse, else as bytes
// (ignoring surrounding whitespace).
func sameContent(a, b []byte) bool {
	if bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) {
		return true
	}
	va, errA := decode(a)
	vb, errB := decode(b)
	return errA == nil && errB == nil && equal(va, vb)
}

// Apply writes a Resolution for the repo-relative path p into the working
// tree at repoRoot (atomically) and returns every repo-relative path it
// created, changed or deleted, for the caller to stage (git add -A -- ...).
// The caller holds the project lock. A conflict copy that already exists with
// the same content is left as is.
func Apply(repoRoot, p string, res Resolution) ([]string, error) {
	rel := strings.TrimPrefix(path.Clean(filepath.ToSlash(p)), "./")
	abs := filepath.Join(repoRoot, filepath.FromSlash(rel))
	var written []string
	if res.Delete {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		if err := fsutil.WriteFileAtomic(abs, res.Merged, 0644); err != nil {
			return nil, err
		}
	}
	written = append(written, rel)
	for _, sf := range res.SideFiles {
		target := sf.Path
		if sf.Kind == SideBaselineArchive {
			t, err := nextArchivePath(repoRoot, time.Now().Format("2006-01-02"))
			if err != nil {
				return written, err
			}
			target = t
		}
		if target == "" {
			continue
		}
		if err := fsutil.WriteFileAtomic(filepath.Join(repoRoot, filepath.FromSlash(target)), sf.Data, 0644); err != nil {
			return written, err
		}
		written = append(written, target)
	}
	return written, nil
}

// nextArchivePath picks .tkt/pm/baselines/<date>-<n>.json with the first free
// n (the same naming as a re-baseline).
func nextArchivePath(repoRoot, date string) (string, error) {
	for n := 1; n < 10000; n++ {
		rel := fmt.Sprintf(".tkt/pm/baselines/%s-%d.json", date, n)
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); os.IsNotExist(err) {
			return rel, nil
		}
	}
	return "", fmt.Errorf("no free baseline archive name for %s", date)
}
