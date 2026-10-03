// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hate/internal/config"
	"hate/internal/gitacct"
	"hate/internal/gitacct/githubtest"
)

// End-to-end credential plumbing with the real hate binary as GIT_ASKPASS and
// a token-protected git HTTP server (git http-backend behind basic auth)
// standing in for github.com. The token lives only in the (file) credential
// store of an isolated config dir.

var builtHate string

// buildHate builds the hate binary once per test run.
func buildHate(t *testing.T) string {
	t.Helper()
	if builtHate != "" {
		return builtHate
	}
	dir, err := os.MkdirTemp("", "hate-bin-")
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "hate")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v %s", err, out)
	}
	builtHate = exe
	return exe
}

func TestMain(m *testing.M) {
	code := m.Run()
	if builtHate != "" {
		os.RemoveAll(filepath.Dir(builtHate))
	}
	os.Exit(code)
}

// gitServer serves bare repos under root over HTTP, requiring basic auth
// x-access-token:<token>. auths counts authenticated requests.
func gitServer(t *testing.T, root, token string, auths *int32) *httptest.Server {
	t.Helper()
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git-http-backend not available")
	}
	h := &cgi.Handler{Path: backend, Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "x-access-token" || p != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			http.Error(w, "auth required", http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(auths, 1)
		h.ServeHTTP(w, r)
	}))
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// credSetup: isolated git + config dir, file credential store, a bare repo
// "acme/private.git" on a token-protected server, and a signed-in account
// whose web base is that server.
func credSetup(t *testing.T) (srv *httptest.Server, bare string, auths *int32) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HATE_CREDENTIAL_STORE", "file")
	cfgDir := t.TempDir()
	old := config.AppConfigPath
	config.AppConfigPath = filepath.Join(cfgDir, "config.json")
	t.Cleanup(func() { config.AppConfigPath = old })
	gitacct.AskpassPath = func() string { return buildHate(t) }

	repos := t.TempDir()
	bare = filepath.Join(repos, "acme", "private.git")
	run(t, repos, "init", "-q", "--bare", "-b", "main", bare)
	run(t, bare, "config", "http.receivepack", "true")
	seed := t.TempDir()
	run(t, seed, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(seed, "a.txt"), []byte("one\n"), 0o644)
	run(t, seed, "add", "-A")
	run(t, seed, "-c", "user.name=s", "-c", "user.email=s@x", "commit", "-q", "-m", "seed")
	run(t, seed, "push", "-q", bare, "main")

	auths = new(int32)
	srv = gitServer(t, repos, "s3cret-token", auths)
	t.Cleanup(srv.Close)

	api := githubtest.New("s3cret-token")
	t.Cleanup(api.Close)
	if _, err := gitacct.SignIn(context.Background(), gitacct.NewGitHub(api.URL, srv.URL), "s3cret-token"); err != nil {
		t.Fatal(err)
	}
	return srv, bare, auths
}

func netGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := gitacct.NetworkCommand(ctx, dir, args...).CombinedOutput()
	return string(out), err
}

// HATE-1ffa tc1: clone and push a private repo with only the stored token;
// .git/config holds no token (nor the remote URL).
func TestPushWithStoredTokenOnly(t *testing.T) {
	srv, bare, auths := credSetup(t)
	work := filepath.Join(t.TempDir(), "private")
	if out, err := netGit(t, "", "clone", "-q", srv.URL+"/acme/private.git", work); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	os.WriteFile(filepath.Join(work, "b.txt"), []byte("two\n"), 0o644)
	run(t, work, "add", "-A")
	run(t, work, "-c", "user.name=pm", "-c", "user.email=pm@x", "commit", "-q", "-m", "pm change")
	if out, err := netGit(t, work, "push", "-q", "origin", "main"); err != nil {
		t.Fatalf("push: %v %s", err, out)
	}
	if run(t, bare, "rev-parse", "main") != run(t, work, "rev-parse", "HEAD") {
		t.Error("push didn't land")
	}
	if atomic.LoadInt32(auths) == 0 {
		t.Error("server saw no authenticated request")
	}
	cfg, _ := os.ReadFile(filepath.Join(work, ".git", "config"))
	if bytes.Contains(cfg, []byte("s3cret-token")) || bytes.Contains(cfg, []byte("x-access-token")) || bytes.Contains(cfg, []byte("insteadOf")) {
		t.Errorf(".git/config:\n%s", cfg)
	}
	// Without the account git can't authenticate (and doesn't hang on a prompt).
	if err := gitacct.SignOut(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err == nil {
		t.Error("fetch worked without a token: the server isn't checking")
	}
}

// HATE-1ffa tc2: a repo whose remote is an SSH host alias
// (git@github.com-x:owner/repo.git) fetches over HTTPS with the token, via a
// per-command rewrite; the remote URL is unchanged.
func TestSSHAliasRemoteFetchesOverHTTPS(t *testing.T) {
	srv, bare, _ := credSetup(t)
	gitacct.SSHConfigPath = func() string { return filepath.Join(t.TempDir(), "none") }
	work := t.TempDir()
	run(t, work, "init", "-q", "-b", "main")
	run(t, work, "remote", "add", "origin", "git@github.com-x:acme/private.git")
	if out, err := netGit(t, work, "fetch", "-q", "origin"); err != nil {
		t.Fatalf("fetch: %v %s", err, out)
	}
	if run(t, work, "rev-parse", "origin/main") != run(t, bare, "rev-parse", "main") {
		t.Error("fetched the wrong thing")
	}
	if u := run(t, work, "remote", "get-url", "origin"); u != "git@github.com-x:acme/private.git" {
		t.Errorf("remote URL changed to %s", u)
	}
	// Chuck's alias form (no user) and an alias declared in ~/.ssh/config.
	sshCfg := filepath.Join(t.TempDir(), "config")
	os.WriteFile(sshCfg, []byte("Host work-gh\n  HostName github.com\n  User git\n"), 0o600)
	gitacct.SSHConfigPath = func() string { return sshCfg }
	for _, remote := range []string{"github.com-tactic:acme/private.git", "work-gh:acme/private.git", "ssh://git@github.com/acme/private.git"} {
		run(t, work, "remote", "set-url", "origin", remote)
		if out, err := netGit(t, work, "fetch", "-q", "origin"); err != nil {
			t.Errorf("fetch via %s: %v %s", remote, err, out)
		}
	}
	_ = srv
}

// The askpass entry point of the built binary, answering from a fake (file)
// credential store, only for the account's host.
func TestAskpassEntryOfBinary(t *testing.T) {
	srv, _, _ := credSetup(t)
	exe := buildHate(t)
	host := strings.TrimPrefix(srv.URL, "http://")
	ask := func(env []string, args ...string) (string, error) {
		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), append(env, "HATE_CONFIG_DIR="+config.AppConfigDir())...)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := ask([]string{"HATE_ASKPASS=1"}, "Password for 'http://x-access-token@"+host+"': "); err != nil || out != "s3cret-token" {
		t.Errorf("as GIT_ASKPASS: %q %v", out, err)
	}
	if out, err := ask(nil, "askpass", "Username for 'http://"+host+"': "); err != nil || out != "x-access-token" {
		t.Errorf("hate askpass: %q %v", out, err)
	}
	if out, err := ask([]string{"HATE_ASKPASS=1"}, "Password for 'https://evil.example.com': "); err == nil || out != "" {
		t.Errorf("answered for another host: %q", out)
	}
}

// HATE-81r7 tc1: launching hate opens the browser at the app (and -no-browser
// doesn't).
func TestLaunchOpensBrowser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a fake 'open' on PATH")
	}
	exe := buildHate(t)
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "opened")
	opener := "open"
	if runtime.GOOS != "darwin" {
		opener = "xdg-open"
	}
	os.WriteFile(filepath.Join(bin, opener), []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\n"), 0o755)

	launch := func(extra ...string) {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		cmd := exec.Command(exe, append([]string{"-port", strconv.Itoa(port)}, extra...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"HATE_CONFIG_DIR="+t.TempDir(), "HATE_NO_BROWSER=")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cmd.Process.Kill(); cmd.Wait() }()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/version"); err == nil {
				resp.Body.Close()
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(800 * time.Millisecond)
		os.WriteFile(log+".port", []byte(strconv.Itoa(port)), 0o644)
	}
	launch()
	got, _ := os.ReadFile(log)
	port, _ := os.ReadFile(log + ".port")
	if strings.TrimSpace(string(got)) != "http://localhost:"+string(port)+"/" {
		t.Errorf("browser opened with %q", got)
	}
	os.Remove(log)
	launch("-no-browser")
	if b, err := os.ReadFile(log); err == nil {
		t.Errorf("-no-browser still opened %q", b)
	}
}

func TestShouldOpenBrowser(t *testing.T) {
	for _, c := range []struct {
		flag bool
		env  string
		want bool
	}{{false, "", true}, {true, "", false}, {false, "1", false}, {false, "0", true}, {false, "true", false}} {
		if got := shouldOpenBrowser(c.flag, c.env); got != c.want {
			t.Errorf("shouldOpenBrowser(%v, %q) = %v", c.flag, c.env, got)
		}
	}
}
