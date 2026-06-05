// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

// GitCommit stages specific files and commits with the given message.
// Returns (success, output_or_error).
func GitCommit(repoRoot string, files []string, message string) (bool, string) {
	args := append([]string{"add"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, string(out)
	}

	cmd = exec.Command("git", "commit", "-m", message)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(outStr, "nothing to commit") {
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

// GitSync pulls (rebase) then pushes. Aborts rebase on conflict.
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
