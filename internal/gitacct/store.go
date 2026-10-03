// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package gitacct

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"

	"hate/internal/config"
	"hate/internal/fsutil"
)

// Where the token lives.
//
// The token is a secret, per user and per machine. It goes in the OS
// credential store (macOS Keychain, Windows Credential Manager, the Secret
// Service on Linux) through go-keyring. When that store isn't available, it
// falls back to credentials.json in the app config folder with owner-only
// permissions, and the account reports a warning. It is never written to a
// project, .tkt/config.json, .git/config or a remote URL.

const (
	keyringService = "hate"
	keyringUser    = "github"

	// StorageKeyring / StorageFile say where a token was saved.
	StorageKeyring = "keyring"
	StorageFile    = "file"
)

// ErrNoToken: no token is stored.
var ErrNoToken = errors.New("no token stored")

// credentialFile is the owner-only fallback file.
func credentialFile() string { return filepath.Join(config.AppConfigDir(), "credentials.json") }

// forceFileStore: HATE_CREDENTIAL_STORE=file skips the OS store (for a
// headless machine, or to keep test instances out of the real Keychain).
func forceFileStore() bool { return os.Getenv("HATE_CREDENTIAL_STORE") == "file" }

// saveToken stores the token and returns where it went.
func saveToken(token string) (string, error) {
	if !forceFileStore() {
		if err := keyring.Set(keyringService, keyringUser, token); err == nil {
			_ = removeFileToken() // a stale fallback copy must not linger
			return StorageKeyring, nil
		}
	}
	if err := writeFileToken(token); err != nil {
		return "", fmt.Errorf("couldn't save the token: %w", err)
	}
	return StorageFile, nil
}

// loadToken reads the token: the OS store first, then the fallback file.
func loadToken() (string, error) {
	if !forceFileStore() {
		if t, err := keyring.Get(keyringService, keyringUser); err == nil && t != "" {
			return t, nil
		}
	}
	return readFileToken()
}

// deleteToken removes the token from both places.
func deleteToken() error {
	var errs []error
	if !forceFileStore() {
		if err := keyring.Delete(keyringService, keyringUser); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			// An unavailable store (the file fallback was used) can't hold a
			// token; only report a failure when the token is still readable.
			if t, gerr := keyring.Get(keyringService, keyringUser); gerr == nil && t != "" {
				errs = append(errs, err)
			}
		}
	}
	if err := removeFileToken(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func writeFileToken(token string) error {
	p := credentialFile()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"github": token})
	return fsutil.WriteFileAtomic(p, data, 0o600) // created 0600 and renamed: never readable by others
}

func readFileToken() (string, error) {
	data, err := os.ReadFile(credentialFile())
	if os.IsNotExist(err) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("credentials file is damaged: %w", err)
	}
	if m["github"] == "" {
		return "", ErrNoToken
	}
	return m["github"], nil
}

func removeFileToken() error {
	if err := os.Remove(credentialFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
