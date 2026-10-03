// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zalando/go-keyring"

	"hate/internal/config"
	"hate/internal/gitacct"
	"hate/internal/gitacct/githubtest"
	"hate/internal/ticket"
)

// gitAccountEnv: an isolated app config dir and projects root, an in-memory
// credential store, git without global config, and a fake GitHub API whose
// "web" side is a folder of bare repos (file://), so clones need no network.
type gitAccountEnv struct {
	h        http.Handler
	gh       *githubtest.Server
	cfgDir   string
	projects string
	webDir   string // <webDir>/<owner>/<name>.git are the "GitHub" repos
}

func setupGitAccount(t *testing.T) *gitAccountEnv {
	t.Helper()
	keyring.MockInit()
	t.Setenv("HATE_CREDENTIAL_STORE", "")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_DIR", "")
	os.Unsetenv("GIT_DIR")
	base := t.TempDir()
	e := &gitAccountEnv{cfgDir: filepath.Join(base, "cfg"), projects: filepath.Join(base, "projects"), webDir: filepath.Join(base, "web")}
	os.MkdirAll(e.cfgDir, 0o755)
	old := config.AppConfigPath
	config.AppConfigPath = filepath.Join(e.cfgDir, "config.json")
	t.Cleanup(func() { config.AppConfigPath = old })
	app, _ := json.Marshal(map[string]interface{}{"projects_root": e.projects})
	os.WriteFile(config.AppConfigPath, app, 0o644)

	e.gh = githubtest.New("good-token")
	t.Cleanup(e.gh.Close)
	t.Setenv("HATE_GITHUB_API_URL", e.gh.URL)
	t.Setenv("HATE_GITHUB_WEB_URL", "file://"+e.webDir)

	r := chi.NewRouter()
	RegisterProjectRoutes(r)
	RegisterGitRoutes(r)
	e.h = r
	return e
}

// bareProject publishes a hate project (or a plain repo) as <owner>/<name>.
func (e *gitAccountEnv) bareProject(t *testing.T, full string, isProject bool) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "w")
	os.MkdirAll(work, 0o755)
	if isProject {
		if err := ticket.WriteConfig(work, ticket.DefaultConfig("Acme", "Shared "+full, "shared-"+filepath.Base(full), "SH")); err != nil {
			t.Fatal(err)
		}
	} else {
		os.WriteFile(filepath.Join(work, "README.md"), []byte("not hate\n"), 0o644)
	}
	bare := filepath.Join(e.webDir, filepath.FromSlash(full)+".git")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "-A"},
		{"-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "-m", "init"},
		{"init", "-q", "--bare", bare}, {"push", "-q", bare, "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	cmd := exec.Command("git", "--git-dir", bare, "symbolic-ref", "HEAD", "refs/heads/main")
	cmd.Run()
	e.gh.Repos = append(e.gh.Repos, githubtest.Repo{FullName: full, IsProject: isProject})
}

func (e *gitAccountEnv) do(t *testing.T, method, path string, body interface{}) (int, map[string]interface{}, string) {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func (e *gitAccountEnv) signIn(t *testing.T) {
	t.Helper()
	if code, _, body := e.do(t, "POST", "/api/git-account", map[string]string{"token": "good-token"}); code != 200 {
		t.Fatalf("sign in: %d %s", code, body)
	}
}

// HATE-oveg tc1..tc4 through the API the Settings screen uses.
func TestGitAccountAPI(t *testing.T) {
	e := setupGitAccount(t)

	// Signed out.
	code, out, _ := e.do(t, "GET", "/api/git-account", nil)
	if code != 200 || out["configured"] != false || !strings.Contains(out["token_url"].(string), "/settings/personal-access-tokens/new") {
		t.Fatalf("initial: %d %v", code, out)
	}

	// tc2: invalid token -> clear error, nothing saved.
	code, out, _ = e.do(t, "POST", "/api/git-account", map[string]string{"token": "nope"})
	if code != http.StatusUnauthorized || !strings.Contains(out["detail"].(string), "didn't accept this token") {
		t.Errorf("bad token: %d %v", code, out)
	}
	if gitacct.Configured() {
		t.Error("bad token saved")
	}
	if _, err := keyring.Get("hate", "github"); !errors.Is(err, keyring.ErrNotFound) {
		t.Error("keyring written for a bad token")
	}

	// tc1: valid -> connected as <name>; token in the credential store only.
	in10 := time.Now().Add(10*24*time.Hour + time.Hour)
	e.gh.SetExpiry(&in10)
	code, out, body := e.do(t, "POST", "/api/git-account", map[string]string{"token": "good-token"})
	if code != 200 || out["configured"] != true || out["name"] != "Jane PM" || out["email"] != "jane@example.com" {
		t.Fatalf("sign in: %d %s", code, body)
	}
	if strings.Contains(body, "good-token") {
		t.Error("token echoed back to the browser")
	}
	if tok, _ := keyring.Get("hate", "github"); tok != "good-token" {
		t.Errorf("credential store = %q", tok)
	}
	if hits := grepFiles(e.cfgDir, "good-token"); len(hits) > 0 {
		t.Errorf("token in config files: %v", hits)
	}
	if out["storage"] != gitacct.StorageKeyring {
		t.Errorf("storage = %v", out["storage"])
	}

	// tc3: expiring in 10 days -> warning (the banner + Replace token button).
	if out["expiring_soon"] != true || out["expires_in_days"].(float64) != 10 {
		t.Errorf("expiry: %v %v", out["expiring_soon"], out["expires_in_days"])
	}
	// Test connection re-reads the expiry.
	in60 := time.Now().Add(60 * 24 * time.Hour)
	e.gh.SetExpiry(&in60)
	if code, out, _ = e.do(t, "POST", "/api/git-account/test", nil); code != 200 || out["expiring_soon"] != false {
		t.Errorf("test connection: %d %v", code, out)
	}
	// Token revoked on GitHub: Test connection says so.
	e.gh.SetToken("rotated")
	if code, out, _ = e.do(t, "POST", "/api/git-account/test", nil); code != http.StatusUnauthorized {
		t.Errorf("revoked: %d %v", code, out)
	}
	e.gh.SetToken("good-token")

	// tc4: sign out removes the token from the credential store.
	if code, out, _ = e.do(t, "DELETE", "/api/git-account", nil); code != 200 || out["configured"] != false {
		t.Errorf("sign out: %d %v", code, out)
	}
	if _, err := keyring.Get("hate", "github"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("token still stored: %v", err)
	}
}

func grepFiles(dir, needle string) []string {
	var hits []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), needle) {
				hits = append(hits, p)
			}
		}
		return nil
	})
	return hits
}

// HATE-1ffa tc3: git not on the PATH -> Settings gets "not installed" (and the
// install page link), not an error.
func TestGitMissingShowsInstallPage(t *testing.T) {
	e := setupGitAccount(t)
	old := gitacct.LookPath
	gitacct.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { gitacct.LookPath = old })
	code, out, _ := e.do(t, "GET", "/api/git-account", nil)
	g, _ := out["git"].(map[string]interface{})
	if code != 200 || g == nil || g["installed"] != false || !strings.HasPrefix(g["install_url"].(string), "https://git-scm.com/") {
		t.Errorf("account: %d %v", code, out)
	}
	code, out, _ = e.do(t, "GET", "/api/git/check", nil)
	if code != 200 || out["installed"] != false {
		t.Errorf("check: %d %v", code, out)
	}
	// Re-check after installing.
	gitacct.LookPath = old
	if _, out, _ = e.do(t, "GET", "/api/git/check", nil); out["installed"] != true {
		t.Errorf("re-check: %v", out)
	}
	// Cloning without git explains instead of failing obscurely.
	gitacct.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if code, out, _ = e.do(t, "POST", "/api/github/clone", map[string]string{"repo": "acme/x"}); code != http.StatusPreconditionFailed ||
		!strings.Contains(out["detail"].(string), "isn't installed") {
		t.Errorf("clone without git: %d %v", code, out)
	}
}

// HATE-51kw tc1: the dialog lists the repos the token can access, hate
// projects marked (and first).
func TestListGitHubRepos(t *testing.T) {
	e := setupGitAccount(t)
	if code, _, _ := e.do(t, "GET", "/api/github/repos", nil); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
	e.gh.Repos = []githubtest.Repo{{FullName: "acme/website"}, {FullName: "acme/tkt-zapnet", IsProject: true, Private: true}, {FullName: "jane/notes"}}
	e.signIn(t)
	req := httptest.NewRequest("GET", "/api/github/repos", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var repos []gitacct.Repo
	if err := json.Unmarshal(rec.Body.Bytes(), &repos); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(repos) != 3 || repos[0].FullName != "acme/tkt-zapnet" || !repos[0].IsProject || repos[1].IsProject || repos[2].IsProject {
		t.Errorf("repos = %+v", repos)
	}
}

// HATE-51kw tc2: picking a hate project clones it into the projects root and
// it opens (it's in the project list, with the account's identity).
func TestCloneFromGitHub(t *testing.T) {
	e := setupGitAccount(t)
	e.bareProject(t, "acme/tkt-zapnet", true)
	e.signIn(t)
	code, out, body := e.do(t, "POST", "/api/github/clone", map[string]string{"repo": "acme/tkt-zapnet"})
	if code != 200 {
		t.Fatalf("clone: %d %s", code, body)
	}
	target := filepath.Join(e.projects, "tkt-zapnet")
	if out["path"] != target || out["id"] != "shared-tkt-zapnet" {
		t.Errorf("project = %v", out)
	}
	if _, err := os.Stat(filepath.Join(target, ".tkt", "config.json")); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range config.ListProjects() {
		found = found || p.Path == target
	}
	if !found {
		t.Error("cloned project not in the project list")
	}
	cfg, _ := exec.Command("git", "-C", target, "config", "--local", "--list").Output()
	if strings.Contains(string(cfg), "good-token") {
		t.Error("token in .git/config")
	}
	// A pasted URL for an already-cloned repo hits the exists check.
	code, out, _ = e.do(t, "POST", "/api/github/clone", map[string]string{"repo": "https://github.com/acme/tkt-zapnet.git"})
	if code != http.StatusConflict {
		t.Errorf("second clone: %d %v", code, out)
	}
}

// HATE-51kw tc3: the target folder already exists -> a clear error, nothing
// overwritten. Also: a repo that isn't a hate project isn't kept.
func TestCloneRefusesExistingFolder(t *testing.T) {
	e := setupGitAccount(t)
	e.bareProject(t, "acme/tkt-zapnet", true)
	e.bareProject(t, "acme/website", false)
	e.signIn(t)
	existing := filepath.Join(e.projects, "tkt-zapnet")
	os.MkdirAll(existing, 0o755)
	os.WriteFile(filepath.Join(existing, "mine.txt"), []byte("keep me"), 0o644)
	code, out, _ := e.do(t, "POST", "/api/github/clone", map[string]string{"repo": "acme/tkt-zapnet"})
	if code != http.StatusConflict || !strings.Contains(out["detail"].(string), "already exists") || !strings.Contains(out["detail"].(string), "Nothing was changed") {
		t.Errorf("exists: %d %v", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(existing, "mine.txt")); string(b) != "keep me" {
		t.Error("existing folder touched")
	}
	if entries, _ := os.ReadDir(existing); len(entries) != 1 {
		t.Errorf("existing folder changed: %v", entries)
	}

	code, out, _ = e.do(t, "POST", "/api/github/clone", map[string]string{"repo": "acme/website"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(out["detail"].(string), "isn't a hate project") {
		t.Errorf("non-project: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(e.projects, "website")); !os.IsNotExist(err) {
		t.Error("non-project clone left behind")
	}
	code, out, _ = e.do(t, "POST", "/api/github/clone", map[string]string{"repo": "acme/missing"})
	if code != http.StatusBadGateway {
		t.Errorf("missing repo: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(e.projects, "missing")); !os.IsNotExist(err) {
		t.Error("failed clone left a folder")
	}
	for _, bad := range []string{"", "acme", "https://gitlab.com/acme/x", "acme/../x", "../../etc"} {
		if code, _, _ := e.do(t, "POST", "/api/github/clone", map[string]string{"repo": bad}); code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d", bad, code)
		}
	}
}

func TestParseRepoRef(t *testing.T) {
	ok := map[string]string{
		"acme/tkt-x":                                   "acme/tkt-x",
		" https://github.com/acme/tkt-x ":              "acme/tkt-x",
		"https://github.com/acme/tkt-x.git":            "acme/tkt-x",
		"https://github.com/acme/tkt-x/":               "acme/tkt-x",
		"git@github.com:acme/tkt-x.git":                "acme/tkt-x",
		"github.com-tactic:acme/tkt-x.git":             "acme/tkt-x",
		"ssh://git@github.com/acme/tkt-x.git":          "acme/tkt-x",
		"https://github.com/acme/tkt-x/tree/main/docs": "acme/tkt-x",
	}
	for in, want := range ok {
		if got, good := ParseRepoRef(in, "https://github.com"); !good || got != want {
			t.Errorf("%q -> %q %v", in, got, good)
		}
	}
	for _, in := range []string{"https://github.com/acme", "https://evil.com/acme/x", "acme/x/y", "acme/.git", "acme/..", "a b/c"} {
		if got, good := ParseRepoRef(in, "https://github.com"); good {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

// The header light's API: without an account automatic sync is off.
func TestAutoSyncStatusWithoutAccount(t *testing.T) {
	e := setupGitAccount(t)
	root := filepath.Join(e.projects, "p1")
	if err := ticket.WriteConfig(root, ticket.DefaultConfig("c", "P1", "p1", "P1")); err != nil {
		t.Fatal(err)
	}
	code, out, _ := e.do(t, "POST", "/api/projects/p1/autosync/open", nil)
	if code != 200 || out["enabled"] != false {
		t.Errorf("open: %d %v", code, out)
	}
	code, out, _ = e.do(t, "GET", "/api/projects/p1/autosync", nil)
	if code != 200 || out["enabled"] != false || out["state"] != "idle" {
		t.Errorf("status: %d %v", code, out)
	}
}

// The Git account lives in app Settings (never in a project's team settings),
// "Add from GitHub" sits with the other project actions, the header has the
// status light, and the PM-facing text avoids git jargon.
func TestGitAccountUIPlacement(t *testing.T) {
	idxB, err := os.ReadFile("../../static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	idx := string(idxB)
	settings := strings.Index(idx, `id="settings-view"`)
	section := strings.Index(idx, `id="git-account-section"`)
	form := strings.Index(idx, `id="settings-form"`)
	team := strings.Index(idx, `id="team-modal"`)
	if !(settings < section && section < form) {
		t.Errorf("Git account section misplaced (settings %d, section %d, form %d)", settings, section, form)
	}
	if team < 0 || strings.Contains(idx[team:], "ga-token") || strings.Contains(idx[team:], "git-account") {
		t.Error("Git account in the project's team dialog")
	}
	if strings.Contains(idx[form:], `id="ga-token"`) {
		t.Error("token field inside the project settings form")
	}
	open := strings.Index(idx, `id="btn-open-project"`)
	gh := strings.Index(idx, `id="btn-github-project"`)
	if gh < 0 || gh-open > 400 {
		t.Error("Add from GitHub isn't next to Open Existing")
	}
	if !strings.Contains(idx, `id="sync-light"`) {
		t.Error("no status light in the header")
	}
	end := strings.Index(idx[section:], "</section>")
	text := strings.ToLower(idx[section : section+end])
	for _, w := range []string{"rebase", "merge", "fetch", "push", "pull", "commit", "remote", "askpass", "insteadof", "ssh"} {
		if strings.Contains(text, w) {
			t.Errorf("Git account section uses %q", w)
		}
	}
}
