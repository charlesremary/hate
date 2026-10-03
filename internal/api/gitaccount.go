// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hate/internal/config"
	"hate/internal/gitacct"
	"hate/internal/teamsync"
	"hate/internal/ticket"
)

// The app-level Git account (Settings > Git account), "Add project from
// GitHub", and the per-project sync status light. The account is per user and
// per machine: nothing here is written to a project.

// RegisterGitRoutes registers the Git account, GitHub and git-check routes.
func RegisterGitRoutes(r chi.Router) {
	r.Get("/api/git/check", getGitCheck)
	r.Get("/api/git-account", getGitAccount)
	r.Post("/api/git-account", signInGitAccount)
	r.Post("/api/git-account/test", testGitAccount)
	r.Delete("/api/git-account", signOutGitAccount)
	r.Get("/api/github/repos", listGitHubRepos)
	r.Post("/api/github/clone", cloneGitHubProject)
}

// SyncManager is the auto-sync manager the handlers use (tests swap it).
var SyncManager = teamsync.Default

// gitAccountResponse is the account status plus the git-installed check.
type gitAccountResponse struct {
	gitacct.Status
	Git gitacct.GitCheck `json:"git"`
}

func accountResponse() gitAccountResponse {
	return gitAccountResponse{Status: gitacct.GetStatus(time.Now()), Git: gitacct.CheckGit()}
}

// getGitCheck handles GET /api/git/check — is git installed (Settings'
// install page and its Re-check button).
func getGitCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, gitacct.CheckGit())
}

// getGitAccount handles GET /api/git-account.
func getGitAccount(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, accountResponse())
}

// GitAccountRequest is the body of POST /api/git-account.
type GitAccountRequest struct {
	Token string `json:"token"`
}

// signInGitAccount handles POST /api/git-account: test the token and, only if
// GitHub accepts it, save it to the credential store.
func signInGitAccount(w http.ResponseWriter, r *http.Request) {
	var req GitAccountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := gitacct.SignIn(ctx, gitacct.NewGitHub("", ""), req.Token); err != nil {
		respondError(w, accountErrorStatus(err), accountErrorText(err))
		return
	}
	respondJSON(w, http.StatusOK, accountResponse())
}

// testGitAccount handles POST /api/git-account/test: check the saved token
// again and refresh the name, email and expiry.
func testGitAccount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := gitacct.Recheck(ctx); err != nil {
		respondError(w, accountErrorStatus(err), accountErrorText(err))
		return
	}
	respondJSON(w, http.StatusOK, accountResponse())
}

// signOutGitAccount handles DELETE /api/git-account: remove the token from
// the credential store.
func signOutGitAccount(w http.ResponseWriter, r *http.Request) {
	if err := gitacct.SignOut(); err != nil {
		respondError(w, http.StatusInternalServerError, "Couldn't remove the saved sign-in: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, accountResponse())
}

func accountErrorStatus(err error) int {
	switch {
	case errors.Is(err, gitacct.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, gitacct.ErrNoToken):
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

func accountErrorText(err error) string {
	switch {
	case errors.Is(err, gitacct.ErrUnauthorized):
		return gitacct.ErrUnauthorized.Error() + "."
	case errors.Is(err, gitacct.ErrNoToken):
		return "You're not signed in to GitHub."
	}
	return err.Error()
}

// listGitHubRepos handles GET /api/github/repos: every repo the token can
// see, with the hate projects (those with .tkt/config.json) marked and listed
// first.
func listGitHubRepos(w http.ResponseWriter, r *http.Request) {
	token, err := gitacct.Token()
	if err != nil {
		respondError(w, http.StatusUnauthorized, "Sign in to GitHub first (Settings > Git account).")
		return
	}
	p := gitacct.Current()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	repos, err := p.ListRepos(ctx, token)
	if err != nil {
		respondError(w, accountErrorStatus(err), accountErrorText(err))
		return
	}
	gitacct.MarkProjects(ctx, p, token, repos)
	sort.SliceStable(repos, func(i, j int) bool { return repos[i].IsProject && !repos[j].IsProject })
	if repos == nil {
		repos = []gitacct.Repo{}
	}
	respondJSON(w, http.StatusOK, repos)
}

// CloneRequest is the body of POST /api/github/clone: "owner/name" or a
// GitHub URL (https or ssh).
type CloneRequest struct {
	Repo string `json:"repo"`
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ParseRepoRef turns "owner/name", https://github.com/owner/name(.git) or
// git@github.com:owner/name.git into "owner/name". webBase is the provider's
// web base (its host is accepted in URLs, besides github.com).
func ParseRepoRef(ref, webBase string) (string, bool) {
	s := strings.TrimSpace(ref)
	isURL := false
	if wb := strings.TrimRight(webBase, "/") + "/"; strings.HasPrefix(s, wb) {
		s, isURL = strings.TrimPrefix(s, wb), true
	} else if u, err := url.Parse(s); err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "ssh") {
		if !strings.EqualFold(u.Hostname(), "github.com") && !strings.EqualFold(u.Hostname(), "www.github.com") {
			return "", false
		}
		s, isURL = u.Path, true
	} else if i := strings.Index(s, ":"); i > 0 && strings.Contains(s[:i], "github.com") {
		s = s[i+1:] // git@github.com:owner/name
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if isURL && len(parts) > 2 {
		parts = parts[:2] // a browser address such as .../owner/name/tree/main
	}
	if len(parts) == 2 {
		parts[1] = strings.TrimSuffix(parts[1], ".git")
	}
	if len(parts) != 2 || !repoNameRe.MatchString(parts[0]) || !repoNameRe.MatchString(parts[1]) ||
		parts[1] == "." || parts[1] == ".." {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// cloneGitHubProject handles POST /api/github/clone: clone the repo over HTTPS
// (with the stored token) into the projects root as <repo name> and return the
// project. An existing folder is never overwritten.
func cloneGitHubProject(w http.ResponseWriter, r *http.Request) {
	var req CloneRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !gitacct.CheckGit().Installed {
		respondError(w, http.StatusPreconditionFailed, "Git isn't installed on this computer. Install it (Settings shows how), then try again.")
		return
	}
	p := gitacct.Current()
	full, ok := ParseRepoRef(req.Repo, p.WebBase())
	if !ok {
		respondError(w, http.StatusUnprocessableEntity, "That doesn't look like a GitHub project address. Paste something like https://github.com/owner/project.")
		return
	}
	name := full[strings.Index(full, "/")+1:]
	root := config.GetProjectsRoot()
	target := filepath.Join(root, name)
	if _, err := os.Lstat(target); err == nil {
		respondError(w, http.StatusConflict, "A folder named \""+name+"\" already exists in your projects folder ("+root+"). Nothing was changed. If it's this project, open it from the sidebar.")
		return
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		respondError(w, http.StatusInternalServerError, "Couldn't create the projects folder: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	cmd := gitacct.NetworkCommand(ctx, root, "clone", "--quiet", p.CloneURL(full), name)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(target) // ours: it didn't exist before
		respondError(w, http.StatusBadGateway, teamsync.ExplainNetworkError("download "+full, string(out), err))
		return
	}
	if _, err := os.Stat(filepath.Join(target, ".tkt", "config.json")); err != nil {
		_ = os.RemoveAll(target)
		respondError(w, http.StatusUnprocessableEntity, full+" isn't a hate project (it has no .tkt/config.json). Nothing was kept.")
		return
	}
	func() {
		defer ticket.LockProject(target)()
		cfg, _ := ticket.ReadConfig(target)
		ticket.EnsureProjectIdentity(target, cfg)
		_ = ticket.RegenerateIndex(target)
	}()
	if filepath.Clean(filepath.Dir(target)) != filepath.Clean(root) {
		_ = config.AddExtraProject(target)
	}
	for _, pi := range config.ListProjects() {
		if filepath.Clean(pi.Path) == filepath.Clean(target) {
			respondJSON(w, http.StatusOK, projectSummary(map[string]interface{}{
				"id": pi.ID, "name": pi.Name, "client": pi.Client, "prefix": pi.Prefix, "path": pi.Path,
			}))
			return
		}
	}
	respondError(w, http.StatusInternalServerError, "Downloaded to "+target+" but couldn't load it as a project.")
}

// ── Per-project sync status ─────────────────────────────────────────────────

// getAutoSync handles GET /api/projects/{projectId}/autosync — the header
// light (no network).
func getAutoSync(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, SyncManager.Status(root))
}

// openAutoSync handles POST /api/projects/{projectId}/autosync/open — the app
// opened the project: sync it in the background (with an account).
func openAutoSync(w http.ResponseWriter, r *http.Request) {
	root, ok := getProjectRoot(w, chi.URLParam(r, "projectId"))
	if !ok {
		return
	}
	SyncManager.Open(root)
	respondJSON(w, http.StatusOK, SyncManager.Status(root))
}
