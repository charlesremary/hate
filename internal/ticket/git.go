// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"hate/internal/fsutil"
)

// GitUserIdentity returns the local Git user identity for the repo.
// Returns {"name": "...", "email": "..."}.
func GitUserIdentity(repoRoot string) map[string]string {
	result := map[string]string{"name": "", "email": ""}
	for _, key := range []string{"user.name", "user.email"} {
		cmd := exec.Command("git", "config", key)
		cmd.Dir = repoRoot
		out, err := cmd.Output()
		field := strings.Split(key, ".")[1]
		if err == nil {
			result[field] = strings.TrimSpace(string(out))
		} else {
			result[field] = ""
		}
	}
	return result
}

// SetGitIdentity sets the repo-local Git identity. Does not affect global config.
func SetGitIdentity(repoRoot, name, email string) error {
	for _, pair := range [][2]string{{"user.name", name}, {"user.email", email}} {
		cmd := exec.Command("git", "config", "--local", pair[0], pair[1])
		cmd.Dir = repoRoot
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to set %s: %w", pair[0], err)
		}
	}
	return nil
}

// EnsureProjectIdentity applies git_identity from config if set.
func EnsureProjectIdentity(repoRoot string, cfg *ProjectConfig) {
	if cfg.GitIdentityV == nil {
		return
	}
	gi := cfg.GitIdentityV
	if gi.Name == "" || gi.Email == "" {
		return
	}
	current := GitUserIdentity(repoRoot)
	if current["name"] != gi.Name || current["email"] != gi.Email {
		_ = SetGitIdentity(repoRoot, gi.Name, gi.Email)
	}
}

// CommitFiles is the one commit path for everything hate writes: it applies
// the project's git identity, stops tracking the derived index.json the first
// time it finds it tracked (see UntrackIndex), drops index.json from files,
// and commits exactly the remaining paths with GitCommit.
//
// A failure (not a git repo, hook rejected, ...) is logged and returned as an
// error; callers pass its text to the API caller as a commit warning. "Nothing
// to commit" is not a failure. The caller holds the project lock.
func CommitFiles(repoRoot string, files []string, message string) error {
	if cfg, err := ReadConfig(repoRoot); err == nil {
		EnsureProjectIdentity(repoRoot, cfg)
	}
	if _, err := UntrackIndex(repoRoot); err != nil {
		log.Printf("commit (%s): untrack index.json: %v", repoRoot, err)
	}
	idx := filepath.Clean(IndexPath(repoRoot))
	kept := make([]string, 0, len(files))
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoRoot, p)
		}
		if filepath.Clean(p) == idx {
			continue
		}
		kept = append(kept, f)
	}
	if len(kept) == 0 {
		return nil
	}
	ok, out := GitCommit(repoRoot, kept, message)
	if ok {
		return nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out = "git commit failed"
	}
	err := fmt.Errorf("commit %q failed: %s", message, out)
	log.Printf("commit warning (%s): %v", repoRoot, err)
	return err
}

// CommitWarning is the API form of a CommitFiles error: "" for success, else
// the error text.
func CommitWarning(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// IndexUntrackMessage is the commit message of the one-off commit that stops
// tracking index.json.
const IndexUntrackMessage = "stop tracking derived index.json"

// UntrackIndex stops tracking the derived index.json in a repo that still
// tracks it: index.json is removed from git (kept on disk), added to the
// repo's .gitignore, and that is committed once on its own
// (IndexUntrackMessage). The commit is built in a temporary index from HEAD,
// so nothing else the user has staged is swept in. Returns whether it made
// the commit. A repo that doesn't track index.json (or isn't a git repo) is
// left alone. The caller holds the project lock.
func UntrackIndex(repoRoot string) (bool, error) {
	cmd := exec.Command("git", "ls-files", "--", "index.json")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return false, nil // not a git repo, or not tracked
	}
	gi := filepath.Join(repoRoot, ".gitignore")
	if err := ensureIgnoreLine(gi, "index.json"); err != nil {
		return false, err
	}

	tmpDir, err := os.MkdirTemp("", "hate-index-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmpDir)
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmpDir, "index"))
	run := func(env []string, args ...string) error {
		c := exec.Command("git", args...)
		c.Dir = repoRoot
		if env != nil {
			c.Env = env
		}
		if o, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(o)))
		}
		return nil
	}
	for _, args := range [][]string{
		{"read-tree", "HEAD"},
		{"rm", "--cached", "-q", "--ignore-unmatch", "--", "index.json"},
		{"add", "--", ".gitignore"},
		{"commit", "-q", "-m", IndexUntrackMessage},
	} {
		if err := run(env, args...); err != nil {
			return false, err
		}
	}
	// Bring the real index in line with the new HEAD for those two paths
	// (-f: the staged index.json now differs from HEAD, which no longer has
	// it; --cached keeps the file on disk).
	if err := run(nil, "rm", "--cached", "-f", "-q", "--ignore-unmatch", "--", "index.json"); err != nil {
		return true, err
	}
	if err := run(nil, "add", "--", ".gitignore"); err != nil {
		return true, err
	}
	log.Printf("%s: %s", repoRoot, IndexUntrackMessage)
	return true, nil
}

// ensureIgnoreLine appends line to the .gitignore at path unless an equivalent
// entry is already there.
func ensureIgnoreLine(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(l); t == line || t == "/"+line {
			return nil
		}
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	data = append(data, []byte(line+"\n")...)
	return fsutil.WriteFileAtomic(path, data, 0644)
}

// GitCommit stages specific files and commits with the given message.
// Returns (success, output_or_error). Prefer CommitFiles, which also applies
// the identity, keeps index.json out and reports failures.
func GitCommit(repoRoot string, files []string, message string) (bool, string) {
	args := append([]string{"add"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, string(out)
	}

	// Commit only these paths, so anything else already staged in the repo
	// isn't swept into this commit.
	cmd = exec.Command("git", append([]string{"commit", "-m", message, "--"}, files...)...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(outStr, "nothing to commit") || strings.Contains(outStr, "no changes added to commit") {
			return true, "nothing to commit"
		}
		return false, outStr
	}
	return true, outStr
}

// GitPush pushes to the remote. Returns (success, output_or_error).
func GitPush(repoRoot string) (bool, string) {
	cmd := exec.Command("git", "push")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		return false, outStr
	}
	return true, outStr
}

// GitStatus returns git status info: branch, uncommitted files, unpushed commits.
func GitStatus(repoRoot string) map[string]interface{} {
	result := map[string]interface{}{
		"branch":           "unknown",
		"uncommitted":      []string{},
		"unpushed_commits": 0,
		"has_remote":       false,
	}

	// Get branch
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err == nil {
		result["branch"] = strings.TrimSpace(string(out))
	}

	// Get uncommitted files
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoRoot
	out, err = cmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		uncommitted := []string{}
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				uncommitted = append(uncommitted, l)
			}
		}
		result["uncommitted"] = uncommitted
	}

	// Check unpushed commits
	cmd = exec.Command("git", "log", "--oneline", "@{u}..HEAD")
	cmd.Dir = repoRoot
	out, err = cmd.Output()
	if err == nil {
		result["has_remote"] = true
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		count := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		result["unpushed_commits"] = count
	}

	return result
}

// GitFetchStatus fetches from remote and returns sync status.
func GitFetchStatus(repoRoot string) map[string]interface{} {
	result := map[string]interface{}{
		"status":     "unknown",
		"ahead":      0,
		"behind":     0,
		"has_remote": false,
	}

	// Fetch latest from remote
	cmd := exec.Command("git", "fetch")
	cmd.Dir = repoRoot
	_ = cmd.Run()

	// Count commits ahead/behind
	cmd = exec.Command("git", "rev-list", "--left-right", "--count", "@{u}...HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	behind, ahead := 0, 0
	if err == nil {
		result["has_remote"] = true
		parts := strings.Fields(strings.TrimSpace(string(out)))
		if len(parts) == 2 {
			behind, _ = strconv.Atoi(parts[0])
			ahead, _ = strconv.Atoi(parts[1])
		}
	}

	result["ahead"] = ahead
	result["behind"] = behind

	if ahead == 0 && behind == 0 {
		result["status"] = "up_to_date"
	} else if ahead > 0 && behind == 0 {
		result["status"] = "ahead"
	} else if behind > 0 && ahead == 0 {
		result["status"] = "behind"
	} else {
		result["status"] = "diverged"
	}

	return result
}

// GitSync pulls (rebase) then pushes. Aborts rebase on conflict. The caller
// holds the project lock.
func GitSync(repoRoot string) map[string]interface{} {
	fetchStatus := GitFetchStatus(repoRoot)
	hasRemote, _ := fetchStatus["has_remote"].(bool)
	if !hasRemote {
		return map[string]interface{}{
			"success": false,
			"action":  "none",
			"message": "No remote configured for this repository.",
		}
	}

	result := map[string]interface{}{
		"success": false,
		"action":  "sync",
		"pulled":  0,
		"pushed":  0,
	}

	behindVal, _ := fetchStatus["behind"].(int)
	statusVal, _ := fetchStatus["status"].(string)

	// Pull with rebase if needed
	if behindVal > 0 || statusVal == "diverged" {
		cmd := exec.Command("git", "pull", "--rebase")
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Conflict -- abort rebase
			abortCmd := exec.Command("git", "rebase", "--abort")
			abortCmd.Dir = repoRoot
			_ = abortCmd.Run()

			result["message"] = "Sync failed -- conflicting changes detected. Rebase aborted, your repo is safe. Ask your team's Git person to resolve this."
			result["conflict_detail"] = strings.TrimSpace(string(out))
			return result
		}
		result["pulled"] = behindVal
	}

	// Push
	cmd := exec.Command("git", "push")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		result["message"] = fmt.Sprintf("Pull succeeded but push failed: %s", strings.TrimSpace(string(out)))
		return result
	}

	aheadVal, _ := fetchStatus["ahead"].(int)
	result["pushed"] = aheadVal
	result["success"] = true
	result["message"] = "Synced successfully."
	return result
}
