// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package gitacct

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"hate/internal/config"
	"hate/internal/gitacct/githubtest"
)

// setup isolates the app config dir and the credential store (an in-memory
// keyring) and starts a fake GitHub.
func setup(t *testing.T) (*githubtest.Server, string) {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()
	old := config.AppConfigPath
	config.AppConfigPath = filepath.Join(dir, "config.json")
	t.Cleanup(func() { config.AppConfigPath = old })
	t.Setenv("HATE_CREDENTIAL_STORE", "")
	gh := githubtest.New("good-token")
	t.Cleanup(gh.Close)
	return gh, dir
}

// grepDir reports any file under dir containing needle.
func grepDir(t *testing.T, dir, needle string) []string {
	t.Helper()
	var hits []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(needle)) {
				hits = append(hits, p)
			}
		}
		return nil
	})
	return hits
}

// HATE-oveg tc1: a valid token connects ("Connected as <name>") and is kept in
// the OS credential store, not in any config file.
func TestSignInValidToken(t *testing.T) {
	gh, dir := setup(t)
	a, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "  good-token \n")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Jane PM" || a.Login != "pm-jane" || a.Email != "jane@example.com" || a.Storage != StorageKeyring {
		t.Errorf("account = %+v", a)
	}
	if tok, err := keyring.Get(keyringService, keyringUser); err != nil || tok != "good-token" {
		t.Errorf("keyring has %q, %v", tok, err)
	}
	if hits := grepDir(t, dir, "good-token"); len(hits) > 0 {
		t.Errorf("token written to files: %v", hits)
	}
	st := GetStatus(time.Now())
	if !st.Configured || st.Name != "Jane PM" || st.Warning != "" || !Configured() {
		t.Errorf("status = %+v", st)
	}
	if tok, err := Token(); err != nil || tok != "good-token" {
		t.Errorf("Token() = %q, %v", tok, err)
	}
}

// HATE-oveg tc2: an invalid token is a clear error and nothing is saved (an
// existing sign-in is left as it was).
func TestSignInInvalidToken(t *testing.T) {
	gh, dir := setup(t)
	_, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "wrong")
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "didn't accept this token") {
		t.Fatalf("err = %v", err)
	}
	if Configured() || Load() != nil {
		t.Error("an account was recorded")
	}
	if _, err := keyring.Get(keyringService, keyringUser); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring not empty: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files written: %v", entries)
	}
	// With a working sign-in, a bad replacement keeps the old one.
	if _, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "good-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "wrong"); err == nil {
		t.Fatal("bad replacement accepted")
	}
	if tok, _ := Token(); tok != "good-token" {
		t.Errorf("token after failed replacement = %q", tok)
	}
	if _, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "   "); err == nil {
		t.Error("empty token accepted")
	}
}

// HATE-oveg tc3: a token expiring in 10 days is flagged (the Settings banner
// with Replace token keys off expiring_soon); one in 30 days is not.
func TestExpiryWarning(t *testing.T) {
	gh, _ := setup(t)
	now := time.Now().UTC()
	in10 := now.Add(10*24*time.Hour + time.Hour).Truncate(time.Second)
	gh.SetExpiry(&in10)
	a, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "good-token")
	if err != nil {
		t.Fatal(err)
	}
	if a.ExpiresAt == nil || !a.ExpiresAt.Equal(in10) {
		t.Fatalf("expires_at = %v, want %v", a.ExpiresAt, in10)
	}
	st := GetStatus(now)
	if !st.ExpiringSoon || st.ExpiresInDays == nil || *st.ExpiresInDays != 10 || st.Expired {
		t.Errorf("status = %+v (days %v)", st, st.ExpiresInDays)
	}
	in30 := now.Add(30 * 24 * time.Hour)
	gh.SetExpiry(&in30)
	if _, err := Recheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := GetStatus(now); st.ExpiringSoon {
		t.Errorf("30 days flagged: %+v", st)
	}
	if st := GetStatus(now.Add(31 * 24 * time.Hour)); !st.Expired || st.ExpiringSoon {
		t.Errorf("past expiry: %+v", st)
	}
	exp, err := NewGitHub(gh.URL, "").Expiry(context.Background(), "good-token")
	if err != nil || exp == nil || !exp.Equal(in30.Truncate(time.Second)) {
		t.Errorf("Expiry = %v, %v", exp, err)
	}
}

// HATE-oveg tc4: sign out removes the token from the credential store.
func TestSignOut(t *testing.T) {
	gh, dir := setup(t)
	if _, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "good-token"); err != nil {
		t.Fatal(err)
	}
	if err := SignOut(); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Get(keyringService, keyringUser); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("token still in keyring: %v", err)
	}
	if Configured() || Load() != nil {
		t.Error("still configured")
	}
	if _, err := os.Stat(filepath.Join(dir, "git-account.json")); !os.IsNotExist(err) {
		t.Error("account file left behind")
	}
	if err := SignOut(); err != nil { // twice is fine
		t.Errorf("second sign out: %v", err)
	}
}

// The owner-only file fallback: used when the OS store isn't available, file
// mode 0600, and a warning shown.
func TestFileFallback(t *testing.T) {
	gh, dir := setup(t)
	keyring.MockInitWithError(errors.New("no secret service"))
	t.Cleanup(keyring.MockInit)
	a, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "good-token")
	if err != nil {
		t.Fatal(err)
	}
	if a.Storage != StorageFile {
		t.Fatalf("storage = %s", a.Storage)
	}
	fi, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials.json mode = %v", fi.Mode().Perm())
	}
	if st := GetStatus(time.Now()); !st.Configured || !strings.Contains(st.Warning, "private file") {
		t.Errorf("status = %+v", st)
	}
	if tok, _ := Token(); tok != "good-token" {
		t.Errorf("token = %q", tok)
	}
	if err := SignOut(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.json")); !os.IsNotExist(err) {
		t.Error("credentials.json not removed on sign out")
	}
}

func TestVerifyEmailFallback(t *testing.T) {
	gh, _ := setup(t)
	gh.NoEmails = true
	id, err := NewGitHub(gh.URL, "").Verify(context.Background(), "good-token")
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "42+pm-jane@users.noreply.github.com" {
		t.Errorf("email = %s", id.Email)
	}
}

func TestListReposPagedAndMarked(t *testing.T) {
	gh, _ := setup(t)
	for i := 0; i < 230; i++ {
		gh.Repos = append(gh.Repos, githubtest.Repo{FullName: fmt.Sprintf("acme/repo-%03d", i), IsProject: i%50 == 7})
	}
	p := NewGitHub(gh.URL, "")
	repos, err := p.ListRepos(context.Background(), "good-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 230 || repos[229].FullName != "acme/repo-229" || repos[0].Owner != "acme" {
		t.Fatalf("got %d repos", len(repos))
	}
	MarkProjects(context.Background(), p, "good-token", repos)
	var marked []string
	for _, r := range repos {
		if r.IsProject {
			marked = append(marked, r.FullName)
		}
	}
	want := []string{"acme/repo-007", "acme/repo-057", "acme/repo-107", "acme/repo-157", "acme/repo-207"}
	if !reflect.DeepEqual(marked, want) {
		t.Errorf("marked = %v", marked)
	}
	if _, err := p.ListRepos(context.Background(), "bad"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("bad token: %v", err)
	}
}

func TestCloneURL(t *testing.T) {
	g := NewGitHub("", "https://github.com/")
	if u := g.CloneURL("acme/tkt-x"); u != "https://github.com/acme/tkt-x.git" {
		t.Error(u)
	}
	if u := g.CloneURL("acme/tkt-x.git"); u != "https://github.com/acme/tkt-x.git" {
		t.Error(u)
	}
}

func TestSSHRewritePrefixes(t *testing.T) {
	aliases := ParseSSHAliases(strings.NewReader(`
# work account
Host github.com-tactic
    HostName github.com
    User git
Host gh-personal other
  HostName=github.com
Host *.internal
  HostName example.com
Host gitlab-box
  HostName gitlab.example.com
`))
	if aliases["gh-personal"] != "github.com" || aliases["github.com-tactic"] != "github.com" || aliases["gitlab-box"] != "gitlab.example.com" {
		t.Fatalf("aliases = %v", aliases)
	}
	got := SSHRewritePrefixes([]string{
		"github.com-tactic:TACTICConsulting/tkt-tactic.git", // alias without user (Chuck's)
		"git@github.com-xyz:owner/repo.git",                 // github.com-* without an ssh config entry
		"git@github.com:owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"gh-personal:me/repo.git", // alias whose HostName is github.com
		"git@gitlab-box:team/repo.git",
		"https://github.com/owner/repo.git",
		"/srv/git/repo.git",
		"file:///srv/git/repo.git",
		"git@github.com:owner/other.git", // same prefix again
	}, aliases)
	want := []string{"github.com-tactic:", "git@github.com-xyz:", "git@github.com:", "ssh://git@github.com/", "gh-personal:"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prefixes = %q\nwant       %q", got, want)
	}
}

func TestAskpassAnswersOnlyForGitHub(t *testing.T) {
	gh, _ := setup(t)
	if _, err := SignIn(context.Background(), NewGitHub(gh.URL, ""), "good-token"); err != nil {
		t.Fatal(err)
	}
	run := func(prompt string) (int, string) {
		var out, errOut bytes.Buffer
		code := Askpass(prompt, &out, &errOut)
		return code, strings.TrimSpace(out.String())
	}
	if c, o := run("Username for 'https://github.com': "); c != 0 || o != "x-access-token" {
		t.Errorf("username: %d %q", c, o)
	}
	if c, o := run("Password for 'https://x-access-token@github.com': "); c != 0 || o != "good-token" {
		t.Errorf("password: %d %q", c, o)
	}
	for _, p := range []string{"Password for 'https://evil.example.com': ", "Are you sure you want to continue connecting (yes/no)?", ""} {
		if c, o := run(p); c == 0 || o != "" {
			t.Errorf("%q answered: %d %q", p, c, o)
		}
	}
	_ = SignOut()
	if c, o := run("Password for 'https://x-access-token@github.com': "); c == 0 || o != "" {
		t.Errorf("signed out but answered: %d %q", c, o)
	}
}

func TestNetworkArgsWithoutAccountAreUnchanged(t *testing.T) {
	setup(t)
	pre, env := NetworkArgs(t.TempDir())
	if pre != nil {
		t.Errorf("pre = %v", pre)
	}
	for _, e := range env {
		if strings.HasPrefix(e, AskpassEnv+"=") {
			t.Errorf("askpass set without an account: %s", e)
		}
	}
}

func TestCheckGitMissing(t *testing.T) {
	old := LookPath
	LookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { LookPath = old })
	c := CheckGit()
	if c.Installed || c.InstallURL == "" {
		t.Errorf("check = %+v", c)
	}
	LookPath = old
	if c := CheckGit(); !c.Installed || c.Version == "" {
		t.Errorf("real git: %+v", c)
	}
}
