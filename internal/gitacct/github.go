// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package gitacct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Provider is a Git hosting service hate can sign in to. GitHub is the only
// one today; GitLab can be added behind the same interface without touching
// the callers.
type Provider interface {
	// Name is the provider id stored with the account ("github").
	Name() string
	// Verify checks the token and returns who it belongs to (and its expiry,
	// when the provider reports one). ErrUnauthorized for a bad token.
	Verify(ctx context.Context, token string) (*Identity, error)
	// ListRepos lists every repository the token can see.
	ListRepos(ctx context.Context, token string) ([]Repo, error)
	// IsProject reports whether the repo contains .tkt/config.json.
	IsProject(ctx context.Context, token, fullName string) (bool, error)
	// Expiry returns when the token expires (nil: never, or not reported).
	Expiry(ctx context.Context, token string) (*time.Time, error)
	// CloneURL is the HTTPS clone URL for "owner/name".
	CloneURL(fullName string) string
	// WebBase is the HTTPS base the clone URLs and the SSH rewrite use
	// ("https://github.com").
	WebBase() string
}

// Identity is who a token belongs to.
type Identity struct {
	Login     string     `json:"login"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Repo is one repository the token can see.
type Repo struct {
	FullName    string `json:"full_name"`
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Private     bool   `json:"private"`
	Description string `json:"description"`
	UpdatedAt   string `json:"updated_at"`
	IsProject   bool   `json:"is_project"`
}

// ErrUnauthorized: the provider rejected the token.
var ErrUnauthorized = errors.New("GitHub didn't accept this token. Check that you copied all of it and that it hasn't expired or been revoked")

// Default GitHub endpoints. HATE_GITHUB_API_URL / HATE_GITHUB_WEB_URL point
// hate at another server (a test double, or GitHub Enterprise later).
const (
	DefaultGitHubAPI = "https://api.github.com"
	DefaultGitHubWeb = "https://github.com"
)

// GitHub implements Provider against the GitHub REST API.
type GitHub struct {
	APIBase string // e.g. https://api.github.com (no trailing slash)
	Web     string // e.g. https://github.com (no trailing slash)
	Client  *http.Client
}

// NewGitHub returns a GitHub provider for the given bases ("" = the default
// or the HATE_GITHUB_* environment override).
func NewGitHub(apiBase, webBase string) *GitHub {
	if apiBase == "" {
		apiBase = os.Getenv("HATE_GITHUB_API_URL")
	}
	if apiBase == "" {
		apiBase = DefaultGitHubAPI
	}
	if webBase == "" {
		webBase = os.Getenv("HATE_GITHUB_WEB_URL")
	}
	if webBase == "" {
		webBase = DefaultGitHubWeb
	}
	return &GitHub{
		APIBase: strings.TrimRight(apiBase, "/"),
		Web:     strings.TrimRight(webBase, "/"),
		Client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (g *GitHub) Name() string    { return "github" }
func (g *GitHub) WebBase() string { return g.Web }

// CloneURL returns <web>/<owner>/<name>.git.
func (g *GitHub) CloneURL(fullName string) string {
	return g.Web + "/" + strings.TrimSuffix(strings.Trim(fullName, "/"), ".git") + ".git"
}

// get calls the API and decodes JSON into out (when non-nil). It returns the
// response (body closed) so callers can read headers.
func (g *GitHub) get(ctx context.Context, token, path string, out interface{}) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", g.APIBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "hate")
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return resp, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return resp, &StatusError{Code: resp.StatusCode, Path: path}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return resp, fmt.Errorf("unexpected reply from GitHub: %w", err)
		}
	}
	return resp, nil
}

// StatusError is a non-200, non-401 API reply.
type StatusError struct {
	Code int
	Path string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GitHub replied %d to %s", e.Code, e.Path)
}

// Verify calls GET /user, then GET /user/emails for the commit email (the
// primary verified one). A token without email access falls back to the
// public profile email, then the GitHub noreply address.
func (g *GitHub) Verify(ctx context.Context, token string) (*Identity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrUnauthorized
	}
	var u struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	resp, err := g.get(ctx, token, "/user", &u)
	if err != nil {
		return nil, err
	}
	if u.Login == "" {
		return nil, fmt.Errorf("unexpected reply from GitHub (no login)")
	}
	id := &Identity{Login: u.Login, Name: u.Name, Email: u.Email, ExpiresAt: parseExpiry(resp.Header)}
	if id.Name == "" {
		id.Name = u.Login
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if _, err := g.get(ctx, token, "/user/emails", &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified && e.Email != "" {
				id.Email = e.Email
				break
			}
		}
	}
	if id.Email == "" {
		id.Email = fmt.Sprintf("%d+%s@users.noreply.github.com", u.ID, u.Login)
	}
	return id, nil
}

// Expiry reads the token's expiry from GET /user.
func (g *GitHub) Expiry(ctx context.Context, token string) (*time.Time, error) {
	resp, err := g.get(ctx, token, "/user", nil)
	if err != nil {
		return nil, err
	}
	return parseExpiry(resp.Header), nil
}

// parseExpiry reads github-authentication-token-expiration
// ("2026-10-12 17:00:00 UTC" or "2026-10-12 10:00:00 -0700").
func parseExpiry(h http.Header) *time.Time {
	v := strings.TrimSpace(h.Get("github-authentication-token-expiration"))
	if v == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700", time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// ListRepos pages through GET /user/repos (100 per page, at most 20 pages).
func (g *GitHub) ListRepos(ctx context.Context, token string) ([]Repo, error) {
	var out []Repo
	path := "/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member"
	for page := 0; page < 20 && path != ""; page++ {
		var batch []struct {
			FullName    string `json:"full_name"`
			Name        string `json:"name"`
			Private     bool   `json:"private"`
			Description string `json:"description"`
			UpdatedAt   string `json:"updated_at"`
			Owner       struct {
				Login string `json:"login"`
			} `json:"owner"`
		}
		resp, err := g.get(ctx, token, path, &batch)
		if err != nil {
			return nil, err
		}
		for _, b := range batch {
			out = append(out, Repo{FullName: b.FullName, Name: b.Name, Owner: b.Owner.Login, Private: b.Private,
				Description: b.Description, UpdatedAt: b.UpdatedAt})
		}
		path = ""
		if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil && strings.HasPrefix(m[1], g.APIBase+"/") {
			path = strings.TrimPrefix(m[1], g.APIBase) // only follow links on the same API (the token goes with it)
		}
	}
	return out, nil
}

// IsProject: GET /repos/{owner}/{repo}/contents/.tkt/config.json is 200.
func (g *GitHub) IsProject(ctx context.Context, token, fullName string) (bool, error) {
	_, err := g.get(ctx, token, "/repos/"+strings.Trim(fullName, "/")+"/contents/.tkt/config.json", nil)
	var se *StatusError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusForbidden):
		return false, nil
	}
	return false, err
}

// MarkProjects sets IsProject on each repo (8 checks at a time). A failed
// check leaves the repo unmarked.
func MarkProjects(ctx context.Context, p Provider, token string, repos []Repo) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, _ := p.IsProject(ctx, token, repos[i].FullName)
			repos[i].IsProject = ok
		}(i)
	}
	wg.Wait()
}
