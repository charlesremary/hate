// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package teamsync keeps a project repo in step with its shared copy (the
// remote): fetch, merge with hate's own conflict resolver (internal/merge),
// push. Sync is one round; Manager runs rounds automatically (on project open,
// every few minutes, before the PM dashboard, and shortly after local commits)
// when a Git account is configured, and keeps the status light's state.
package teamsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hate/internal/gitacct"
	"hate/internal/merge"
	"hate/internal/ticket"
)

// State is the status light.
type State string

const (
	StateIdle      State = "idle"      // nothing has run yet
	StateSynced    State = "synced"    // up to date with the shared copy
	StateSyncing   State = "syncing"   // a round is running
	StateOffline   State = "offline"   // the shared copy couldn't be reached; changes wait here
	StateAttention State = "attention" // a person should look (see Message / Notes)
	StateLocal     State = "local"     // no shared copy set up for this project
)

// Result is the outcome of one Sync round.
type Result struct {
	State   State    `json:"state"`
	Message string   `json:"message"` // plain words, for the person
	Notes   []string `json:"notes,omitempty"`
	Pulled  int      `json:"pulled"` // commits brought in
	Pushed  int      `json:"pushed"` // commits sent
	Merged  bool     `json:"merged"` // combined both sides' changes
	Detail  string   `json:"detail,omitempty"`
	// Fetched: the round reached the shared copy. Complete: the round ran to
	// the end (in step with the shared copy, maybe with notes to look at).
	Fetched  bool `json:"-"`
	Complete bool `json:"-"`
}

// Success reports whether the round left the project in step (or with
// nothing to do).
func (r Result) Success() bool { return r.State == StateSynced }

// maxRounds: a push rejected because someone pushed in between is retried
// (fetch, merge, push) this many times in all.
const maxRounds = 3

// Sync runs one round: fetch; merge the shared changes (resolving conflicts
// with internal/merge, never leaving the repo mid-merge); push local commits.
// The caller holds the project lock (ticket.LockProject).
func Sync(ctx context.Context, root string) Result {
	if !isRepo(root) {
		return Result{State: StateLocal, Message: "This project isn't shared (it has no history folder), so there is nothing to sync."}
	}
	if err := abortStaleMerge(root); err != nil {
		return attention("A previous update was interrupted and couldn't be undone.", err)
	}
	upstream := gitOut(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if upstream == "" {
		return Result{State: StateLocal, Message: "This project isn't connected to a shared copy, so there is nothing to sync."}
	}
	remote, branch := upstream, ""
	if i := strings.Index(upstream, "/"); i > 0 {
		remote, branch = upstream[:i], upstream[i+1:]
	}
	pushRef := "HEAD"
	if branch != "" {
		pushRef = "HEAD:refs/heads/" + branch // to the tracked branch, whatever the local one is called
	}
	cfg, _ := ticket.ReadConfig(root)
	ticket.EnsureProjectIdentity(root, cfg)

	var res Result
	for round := 0; round < maxRounds; round++ {
		if out, err := network(ctx, root, "fetch", "--quiet", "--prune", remote); err != nil {
			r := networkFailure("get the latest changes", out, err)
			r.Pulled, r.Merged, r.Notes = res.Pulled, res.Merged, res.Notes
			return r
		}
		res.Fetched = true
		behind, ahead := counts(root)
		if behind > 0 {
			pulled, merged, notes, attn, err := mergeUpstream(root)
			if err != nil {
				r := attention("Couldn't combine the latest shared changes with this computer's. Nothing was changed here.", err)
				r.Fetched = true
				return r
			}
			res.Pulled += pulled
			res.Merged = res.Merged || merged
			res.Notes = append(res.Notes, notes...)
			if attn {
				res.State = StateAttention
			}
			_, ahead = counts(root)
		}
		if ahead == 0 {
			break
		}
		out, err := network(ctx, root, "push", "--quiet", remote, pushRef)
		if err == nil {
			res.Pushed += ahead
			break
		}
		if rejected(out) && round < maxRounds-1 {
			continue // someone shared changes in between: fetch, merge, try again
		}
		r := networkFailure("send this computer's changes", out, err)
		r.Pulled, r.Merged, r.Notes, r.Fetched = res.Pulled, res.Merged, res.Notes, true
		return r
	}
	res.Complete = true
	if res.State == StateAttention {
		res.Message = "Synced, but something needs a look: " + strings.Join(res.Notes, " ")
		return res
	}
	res.State = StateSynced
	switch {
	case res.Pulled > 0 && res.Pushed > 0:
		res.Message = "Synced: got the latest changes and shared yours."
	case res.Pulled > 0:
		res.Message = "Synced: got the latest changes."
	case res.Pushed > 0:
		res.Message = "Synced: shared your changes."
	default:
		res.Message = "Synced: already up to date."
	}
	return res
}

// mergeUpstream merges @{u} into HEAD (stage 2 = ours = this machine, stage 3
// = theirs = the shared copy). On conflicts it resolves each file with
// merge.ResolveFile and commits the merge. Any failure aborts the merge, so
// the repo is back where it started.
func mergeUpstream(root string) (pulled int, merged bool, notes []string, needsAttention bool, err error) {
	behind, _ := counts(root)
	out, mergeErr := git(root, append(identityArgs(root), "merge", "--no-edit", "--no-stat", "-m", "sync: combine shared changes", "@{u}")...)
	if mergeErr == nil {
		regenerateIndex(root)
		return behind, strings.Contains(out, "Merge made"), nil, false, nil
	}
	conflicted := unmergedPaths(root)
	if len(conflicted) == 0 || !mergeInProgress(root) {
		// Refused before starting (e.g. files changed here that aren't
		// committed would be overwritten). Nothing to undo but be sure.
		_ = abortStaleMerge(root)
		return 0, false, nil, false, fmt.Errorf("%s", firstLines(out, 6))
	}
	fail := func(e error) (int, bool, []string, bool, error) {
		if _, aerr := git(root, "merge", "--abort"); aerr != nil {
			log.Printf("sync (%s): merge --abort failed: %v", root, aerr)
		}
		return 0, false, nil, false, e
	}
	for _, p := range conflicted {
		base := stageBlob(root, 1, p)
		mine := stageBlob(root, 2, p)
		theirs := stageBlob(root, 3, p)
		res, err := merge.ResolveFile(p, base, mine, theirs)
		if err != nil {
			return fail(err)
		}
		written, err := merge.Apply(root, p, res)
		if err != nil {
			return fail(err)
		}
		if _, err := git(root, append([]string{"add", "-A", "--"}, written...)...); err != nil {
			return fail(err)
		}
		if res.Note != "" {
			notes = append(notes, res.Note)
		}
		if res.NeedsAttention {
			needsAttention = true
		}
	}
	if left := unmergedPaths(root); len(left) > 0 {
		return fail(fmt.Errorf("still unresolved: %s", strings.Join(left, ", ")))
	}
	if _, err := git(root, append(identityArgs(root), "commit", "--no-edit", "--no-verify")...); err != nil {
		return fail(err)
	}
	regenerateIndex(root)
	return behind, true, notes, needsAttention, nil
}

// identityArgs supplies a fallback commit identity for the merge commit when
// git has none (a PM's machine with no git config and no account identity).
func identityArgs(root string) []string {
	if gitOut(root, "config", "user.email") != "" && gitOut(root, "config", "user.name") != "" {
		return nil
	}
	return []string{"-c", "user.name=hate", "-c", "user.email=hate@localhost"}
}

func regenerateIndex(root string) {
	if err := ticket.RegenerateIndex(root); err != nil {
		log.Printf("sync (%s): regenerate index: %v", root, err)
	}
}

// ── git helpers ────────────────────────────────────────────────────────────

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_MERGE_AUTOEDIT=no")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func gitOut(root string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func network(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ticket.NetworkTimeout)
	defer cancel()
	out, err := gitacct.NetworkCommand(ctx, root, args...).CombinedOutput()
	if ctx.Err() != nil {
		return string(out) + "\n(timed out)", ctx.Err()
	}
	return string(out), err
}

func isRepo(root string) bool {
	return gitOut(root, "rev-parse", "--git-dir") != ""
}

func mergeInProgress(root string) bool {
	return gitOut(root, "rev-parse", "-q", "--verify", "MERGE_HEAD") != ""
}

// abortStaleMerge undoes a merge left in progress (a crash mid-sync).
func abortStaleMerge(root string) error {
	if !mergeInProgress(root) {
		return nil
	}
	_, err := git(root, "merge", "--abort")
	return err
}

// counts returns how many commits HEAD is behind and ahead of @{u}.
func counts(root string) (behind, ahead int) {
	f := strings.Fields(gitOut(root, "rev-list", "--left-right", "--count", "@{u}...HEAD"))
	if len(f) == 2 {
		behind, _ = strconv.Atoi(f[0])
		ahead, _ = strconv.Atoi(f[1])
	}
	return
}

func unmergedPaths(root string) []string {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U", "-z")
	cmd.Dir = root
	out, _ := cmd.Output()
	var paths []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			paths = append(paths, string(p))
		}
	}
	return paths
}

// stageBlob returns the content of path at an index stage (1 base, 2 ours =
// this machine, 3 theirs = the shared copy), or nil when that stage is absent
// (added on one side only, or deleted).
func stageBlob(root string, stage int, path string) []byte {
	cmd := exec.Command("git", "show", fmt.Sprintf(":%d:%s", stage, filepath.ToSlash(path)))
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return out
}

func rejected(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "[rejected]") || strings.Contains(o, "non-fast-forward") ||
		strings.Contains(o, "fetch first") || strings.Contains(o, "updates were rejected")
}

// networkFailure turns a failed fetch/push into Offline (couldn't reach the
// server) or Needs attention (reached it, but it said no).
func networkFailure(what, out string, err error) Result {
	o := strings.ToLower(out)
	detail := firstLines(out, 6)
	for _, s := range []string{"could not resolve host", "failed to connect", "couldn't connect", "connection refused",
		"connection timed out", "operation timed out", "timed out", "network is unreachable", "no route to host",
		"could not read from remote repository", "temporary failure in name resolution", "connection reset",
		"unable to access", "ssl_connect", "proxy"} {
		if strings.Contains(o, s) && !authProblem(o) {
			return Result{State: StateOffline, Detail: detail,
				Message: "Offline: couldn't reach GitHub to " + what + ". Your changes are saved on this computer and will be shared automatically when the connection is back."}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Result{State: StateOffline, Detail: detail,
			Message: "Offline: GitHub took too long to answer. Your changes are saved on this computer and will be shared later."}
	}
	if authProblem(o) {
		return Result{State: StateAttention, Detail: detail,
			Message: "GitHub didn't accept your sign-in, or you don't have access to this project. Check Settings > Git account (the token may have expired), or ask the project owner to give you access."}
	}
	return Result{State: StateAttention, Detail: detail, Message: "Couldn't " + what + ". Details: " + detail}
}

// ExplainNetworkError describes a failed network git command (what = "download
// owner/repo", ...) in plain words.
func ExplainNetworkError(what, out string, err error) string {
	return networkFailure(what, out, err).Message
}

func authProblem(o string) bool {
	for _, s := range []string{"authentication failed", "could not read username", "could not read password",
		"invalid username or password", "permission denied", "403", "401", "repository not found", "terminal prompts disabled"} {
		if strings.Contains(o, s) {
			return true
		}
	}
	return false
}

func attention(msg string, err error) Result {
	detail := ""
	if err != nil {
		detail = firstLines(err.Error(), 6)
		log.Printf("sync: %s: %s", msg, detail)
	}
	return Result{State: StateAttention, Message: msg, Detail: detail}
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " ")
}

// Head is the current commit (empty when unknown); the UI watches it to
// refresh after a sync brings in changes.
func Head(root string) string { return gitOut(root, "rev-parse", "HEAD") }

// now is the clock (tests).
var now = time.Now
