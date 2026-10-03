// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package gitacct

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"hate/internal/config"
)

// How hate gives the token to git.
//
// Every network git command (fetch, pull, push, clone) runs through
// NetworkCommand. With a Git account configured it adds:
//
//   - GIT_ASKPASS = the hate executable itself, with HATE_ASKPASS=1 in the
//     environment, so git asks hate for the username and password and hate
//     answers from the credential store (see Askpass). GIT_TERMINAL_PROMPT=0
//     so git never waits on a terminal prompt.
//   - -c credential.helper= (clears the helper list for this one command), so
//     a credential helper (osxkeychain, Git Credential Manager) neither answers
//     with other credentials nor saves the token.
//   - one -c url.https://github.com/.insteadOf=<ssh prefix> per SSH GitHub
//     remote of the repo (git@github.com:, or a host alias such as
//     github.com-work: whose HostName is github.com), so a repo cloned over
//     SSH syncs over HTTPS with the token. Per command only: .git/config and
//     the remote URLs are never changed.
//
// Without an account, the command runs exactly as before (Chuck's SSH flow).

// AskpassEnv marks a hate process started by git as an askpass helper.
const AskpassEnv = "HATE_ASKPASS"

// AskpassPath is the executable git runs as GIT_ASKPASS (default: this
// binary). Tests point it at a freshly built hate binary.
var AskpassPath = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}

// SSHConfigPath is the ssh client config read for host aliases.
var SSHConfigPath = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "config")
}

// NetworkCommand builds a git command that talks to a remote. dir may be ""
// (clone). The context bounds how long it may run.
func NetworkCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	pre, env := NetworkArgs(dir)
	cmd := exec.CommandContext(ctx, "git", append(pre, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

// NetworkArgs returns the -c options to put before the git subcommand and the
// environment for a network git command in dir. Without an account: no
// options and the plain environment.
func NetworkArgs(dir string) (pre []string, env []string) {
	env = os.Environ()
	a := Load()
	if a == nil {
		return nil, env
	}
	if _, err := loadToken(); err != nil {
		return nil, env
	}
	env = append(env,
		"GIT_ASKPASS="+AskpassPath(),
		AskpassEnv+"=1",
		"HATE_CONFIG_DIR="+config.AppConfigDir(),
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
	pre = []string{"-c", "credential.helper="}
	web := strings.TrimRight(ProviderFor(a).WebBase(), "/") + "/"
	if dir != "" && (strings.HasPrefix(web, "https://") || strings.HasPrefix(web, "http://")) {
		for _, prefix := range SSHRewritePrefixes(RemoteURLs(dir), readSSHAliases()) {
			pre = append(pre, "-c", "url."+web+".insteadOf="+prefix)
		}
	}
	return pre, env
}

// RemoteURLs lists the repo's remote URLs (fetch and push).
func RemoteURLs(dir string) []string {
	cmd := exec.Command("git", "config", "--get-regexp", `^remote\..*\.(push)?url$`)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var urls []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			urls = append(urls, f[1])
		}
	}
	return urls
}

// SSHRewritePrefixes returns, for each SSH remote URL that points at GitHub,
// the prefix to rewrite to https://github.com/ (deduplicated):
//
//	git@github.com:owner/repo.git         -> "git@github.com:"
//	github.com-work:owner/repo.git        -> "github.com-work:"
//	ssh://git@github.com/owner/repo.git   -> "ssh://git@github.com/"
//
// aliases maps an ssh Host alias (lower case) to its HostName.
func SSHRewritePrefixes(urls []string, aliases map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urls {
		prefix, host, ok := sshPrefix(u)
		if !ok || !isGitHubHost(host, aliases) || seen[prefix] {
			continue
		}
		seen[prefix] = true
		out = append(out, prefix)
	}
	return out
}

// sshPrefix splits an SSH remote URL into its "user@host:" (scp form) or
// "ssh://user@host[:port]/" prefix and the host.
func sshPrefix(u string) (prefix, host string, ok bool) {
	if strings.HasPrefix(u, "ssh://") || strings.HasPrefix(u, "git+ssh://") {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" {
			return "", "", false
		}
		i := strings.Index(u, "://") + 3
		rest := u[i:]
		j := strings.Index(rest, "/")
		if j < 0 {
			return "", "", false
		}
		return u[:i+j+1], pu.Hostname(), true
	}
	if strings.Contains(u, "://") {
		return "", "", false // https, file, ...
	}
	// scp-like: [user@]host:path, where the colon comes before any slash.
	colon := strings.Index(u, ":")
	if colon <= 0 || strings.Contains(u[:colon], "/") {
		return "", "", false // a local path
	}
	if runtime.GOOS == "windows" && colon == 1 {
		return "", "", false // C:\path
	}
	h := u[:colon]
	if at := strings.LastIndex(h, "@"); at >= 0 {
		h = h[at+1:]
	}
	return u[:colon+1], h, h != ""
}

// isGitHubHost: github.com itself, an ssh alias whose HostName is github.com,
// or any "github.com-*" alias (the usual multi-account convention).
func isGitHubHost(host string, aliases map[string]string) bool {
	h := strings.ToLower(host)
	if h == "github.com" || h == "ssh.github.com" || strings.HasPrefix(h, "github.com-") {
		return true
	}
	if hn, ok := aliases[h]; ok {
		hn = strings.ToLower(hn)
		return hn == "github.com" || hn == "ssh.github.com"
	}
	return false
}

func readSSHAliases() map[string]string {
	f, err := os.Open(SSHConfigPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	return ParseSSHAliases(f)
}

// ParseSSHAliases reads an ssh client config and maps each literal Host alias
// (lower case; wildcard patterns skipped) to its HostName.
func ParseSSHAliases(r io.Reader) map[string]string {
	out := map[string]string{}
	var hosts []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := line, ""
		if i := strings.IndexAny(line, " \t="); i >= 0 {
			key, val = line[:i], strings.TrimLeft(line[i:], " \t=")
		}
		switch strings.ToLower(key) {
		case "host":
			hosts = hosts[:0]
			for _, h := range strings.Fields(val) {
				if !strings.ContainsAny(h, "*?!") {
					hosts = append(hosts, strings.ToLower(h))
				}
			}
		case "match":
			hosts = hosts[:0]
		case "hostname":
			for _, h := range hosts {
				if _, set := out[h]; !set { // ssh uses the first value
					out[h] = strings.Trim(val, `"`)
				}
			}
		}
	}
	return out
}

// IsGitHubRemote reports whether any remote of the repo is on the account's
// GitHub (HTTPS on its web host, or SSH to github.com / an alias of it).
func IsGitHubRemote(dir string, p Provider) bool {
	web := strings.TrimRight(p.WebBase(), "/") + "/"
	aliases := readSSHAliases()
	for _, u := range RemoteURLs(dir) {
		if strings.HasPrefix(u, web) {
			return true
		}
		if _, host, ok := sshPrefix(u); ok && isGitHubHost(host, aliases) {
			return true
		}
	}
	return false
}

// CommitIdentity returns the signed-in account's name and email when the repo
// syncs through that account (a GitHub remote). ok is false otherwise.
func CommitIdentity(dir string) (name, email string, ok bool) {
	a := Load()
	if a == nil || a.Email == "" {
		return "", "", false
	}
	if !IsGitHubRemote(dir, ProviderFor(a)) {
		return "", "", false
	}
	name = a.Name
	if name == "" {
		name = a.Login
	}
	return name, a.Email, true
}

// ── Askpass ─────────────────────────────────────────────────────────────────

// Askpass answers one git credential prompt (the single argument git passes to
// GIT_ASKPASS) on out: "x-access-token" for the username, the stored token for
// the password. It answers only for the account's own host, so the token never
// goes to another server. Returns the process exit code.
func Askpass(prompt string, out, errOut io.Writer) int {
	a := Load()
	if a == nil {
		fmt.Fprintln(errOut, "hate: no Git account is set up (Settings > Git account)")
		return 1
	}
	web, err := url.Parse(ProviderFor(a).WebBase())
	if err != nil {
		return 1
	}
	if h := promptHost(prompt); h == "" || !strings.EqualFold(h, web.Host) {
		fmt.Fprintf(errOut, "hate: not answering a credential prompt for %q\n", h)
		return 1
	}
	lower := strings.ToLower(prompt)
	switch {
	case strings.HasPrefix(lower, "username"):
		fmt.Fprintln(out, "x-access-token")
		return 0
	case strings.HasPrefix(lower, "password"):
		token, err := loadToken()
		if err != nil {
			fmt.Fprintln(errOut, "hate: the GitHub token is missing; sign in again in Settings")
			return 1
		}
		fmt.Fprintln(out, token)
		return 0
	}
	fmt.Fprintf(errOut, "hate: unexpected prompt %q\n", prompt)
	return 1
}

// promptHost pulls the host out of "Username for 'https://github.com': " or
// "Password for 'https://x-access-token@github.com': ".
func promptHost(prompt string) string {
	i := strings.Index(prompt, "'")
	j := strings.LastIndex(prompt, "'")
	if i < 0 || j <= i {
		return ""
	}
	u, err := url.Parse(prompt[i+1 : j])
	if err != nil {
		return ""
	}
	return u.Host
}

// ── Git installed? ──────────────────────────────────────────────────────────

// LookPath finds git (a variable so tests can pretend it's missing).
var LookPath = exec.LookPath

// GitCheck is the result of looking for the git program.
type GitCheck struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	OS         string `json:"os"`
	InstallURL string `json:"install_url"`
}

// CheckGit reports whether git is on the PATH, and where to get it.
func CheckGit() GitCheck {
	c := GitCheck{OS: runtime.GOOS, InstallURL: InstallURL(runtime.GOOS)}
	p, err := LookPath("git")
	if err != nil {
		return c
	}
	out, err := exec.Command(p, "--version").Output()
	if err != nil {
		return c
	}
	c.Installed = true
	c.Version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "git version"))
	return c
}

// InstallURL is the plain download page for Git on an OS.
func InstallURL(goos string) string {
	switch goos {
	case "windows":
		return "https://git-scm.com/download/win"
	case "darwin":
		return "https://git-scm.com/download/mac"
	}
	return "https://git-scm.com/downloads"
}
