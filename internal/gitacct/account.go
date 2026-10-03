// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package gitacct is the app-level Git account: the GitHub sign-in (token in
// the OS credential store), the provider API (verify, list repos, clone URL),
// and the plumbing that hands the token to the git CLI (askpass, SSH-to-HTTPS
// rewrite, git-installed check).
//
// The account is per user and per machine. It lives in the app config folder
// (~/.pm-agent) and the OS credential store, never in a project: team
// settings are committed and pushed to every teammate.
package gitacct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hate/internal/config"
	"hate/internal/fsutil"
)

// ExpiryWarnDays: warn this many days before the token expires.
const ExpiryWarnDays = 14

// Account is the non-secret record of the signed-in account
// (<app config dir>/git-account.json). The token itself is not in it.
type Account struct {
	Provider   string     `json:"provider"` // "github"
	Login      string     `json:"login"`
	Name       string     `json:"name"`
	Email      string     `json:"email"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Storage    string     `json:"storage"` // StorageKeyring or StorageFile
	VerifiedAt time.Time  `json:"verified_at"`
	// APIURL / WebURL are recorded only when not the github.com defaults.
	APIURL string `json:"api_url,omitempty"`
	WebURL string `json:"web_url,omitempty"`
}

func accountFile() string { return filepath.Join(config.AppConfigDir(), "git-account.json") }

// Load returns the signed-in account, or nil when there is none.
func Load() *Account {
	data, err := os.ReadFile(accountFile())
	if err != nil {
		return nil
	}
	var a Account
	if json.Unmarshal(data, &a) != nil || a.Login == "" {
		return nil
	}
	return &a
}

// Configured reports whether a Git account is set up on this machine (an
// account record and a stored token). Auto-sync runs only then.
func Configured() bool {
	if Load() == nil {
		return false
	}
	_, err := loadToken()
	return err == nil
}

// Token returns the stored token.
func Token() (string, error) {
	if Load() == nil {
		return "", ErrNoToken
	}
	return loadToken()
}

// ProviderFor returns the provider an account uses (nil: the default, honoring
// the HATE_GITHUB_* overrides).
func ProviderFor(a *Account) Provider {
	if a == nil {
		return NewGitHub("", "")
	}
	return NewGitHub(a.APIURL, a.WebURL)
}

// Current returns the provider of the signed-in account (or the default).
func Current() Provider { return ProviderFor(Load()) }

// SignIn verifies the token with p and, only when it is valid, stores it in
// the credential store and records the account. An invalid token changes
// nothing (a previous sign-in stays as it was).
func SignIn(ctx context.Context, p Provider, token string) (*Account, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("paste a token first")
	}
	id, err := p.Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	storage, err := saveToken(token)
	if err != nil {
		return nil, err
	}
	a := &Account{
		Provider:   p.Name(),
		Login:      id.Login,
		Name:       id.Name,
		Email:      id.Email,
		ExpiresAt:  id.ExpiresAt,
		Storage:    storage,
		VerifiedAt: time.Now().UTC(),
	}
	if g, ok := p.(*GitHub); ok {
		if g.APIBase != DefaultGitHubAPI {
			a.APIURL = g.APIBase
		}
		if g.Web != DefaultGitHubWeb {
			a.WebURL = g.Web
		}
	}
	if err := save(a); err != nil {
		return nil, err
	}
	return a, nil
}

// Recheck verifies the stored token again (Test connection) and refreshes the
// name, email and expiry.
func Recheck(ctx context.Context) (*Account, error) {
	a := Load()
	if a == nil {
		return nil, ErrNoToken
	}
	token, err := loadToken()
	if err != nil {
		return nil, err
	}
	id, err := ProviderFor(a).Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	a.Login, a.Name, a.Email, a.ExpiresAt = id.Login, id.Name, id.Email, id.ExpiresAt
	a.VerifiedAt = time.Now().UTC()
	return a, save(a)
}

// SignOut removes the token from the credential store and forgets the account.
func SignOut() error {
	errTok := deleteToken()
	if err := os.Remove(accountFile()); err != nil && !os.IsNotExist(err) {
		return errors.Join(errTok, err)
	}
	return errTok
}

func save(a *Account) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(accountFile(), append(data, '\n'), 0o600)
}

// Status is the account as the Settings screen shows it.
type Status struct {
	Configured    bool       `json:"configured"`
	Provider      string     `json:"provider,omitempty"`
	Login         string     `json:"login,omitempty"`
	Name          string     `json:"name,omitempty"`
	Email         string     `json:"email,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	ExpiresInDays *int       `json:"expires_in_days,omitempty"`
	ExpiringSoon  bool       `json:"expiring_soon"`
	Expired       bool       `json:"expired"`
	Storage       string     `json:"storage,omitempty"`
	Warning       string     `json:"warning,omitempty"`
	TokenURL      string     `json:"token_url"`
}

// TokenPageURL is where a PM creates a token: a fine-grained token, named,
// with a 90-day expiry (Contents: read and write is picked per repo on the
// page).
func TokenPageURL(p Provider) string {
	return p.WebBase() + "/settings/personal-access-tokens/new?name=hate&expires_in=90&description=hate+project+sync"
}

// GetStatus describes the current account (now is the clock, for tests).
func GetStatus(now time.Time) Status {
	a := Load()
	st := Status{TokenURL: TokenPageURL(ProviderFor(a))}
	if a == nil {
		return st
	}
	if _, err := loadToken(); err != nil {
		st.Warning = "The saved sign-in is missing its token. Sign in again."
		return st
	}
	st.Configured = true
	st.Provider, st.Login, st.Name, st.Email, st.Storage = a.Provider, a.Login, a.Name, a.Email, a.Storage
	if a.ExpiresAt != nil {
		st.ExpiresAt = a.ExpiresAt
		days := int(a.ExpiresAt.Sub(now).Hours() / 24)
		if a.ExpiresAt.Before(now) {
			st.Expired = true
			days = 0
		}
		st.ExpiresInDays = &days
		st.ExpiringSoon = !st.Expired && a.ExpiresAt.Sub(now) <= ExpiryWarnDays*24*time.Hour
	}
	if a.Storage == StorageFile {
		st.Warning = fmt.Sprintf("This computer's password store wasn't available, so the token is saved in a private file (%s) that only your user account can read.", credentialFile())
	}
	return st
}
