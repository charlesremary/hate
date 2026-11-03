// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package update tells the user when a newer hate is published. It reads the
// version line of the Homebrew cask on main (no GitHub API, no token, nothing
// sent but the GET), at most once a day, in the background. Failures are
// silent. Off with the Settings toggle or HATE_NO_UPDATE_CHECK=1.
package update

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"hate/internal/config"
	"hate/internal/fsutil"
)

// CaskURL is where the published version is read from.
var CaskURL = "https://raw.githubusercontent.com/charlesremary/hate/main/Casks/hate.rb"

// Interval between checks.
const Interval = 24 * time.Hour

var (
	mu        sync.Mutex
	latest    string
	checkedAt time.Time
	versionRe = regexp.MustCompile(`(?m)^\s*version\s+"(\d+\.\d+\.\d+)"`)
	client    = &http.Client{Timeout: 10 * time.Second}
)

// Disabled reports whether the user turned the check off.
func Disabled() bool {
	if v := strings.TrimSpace(os.Getenv("HATE_NO_UPDATE_CHECK")); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return true
	}
	return config.LoadConfig().DisableUpdateCheck
}

// Status is what GET /api/version reports.
type Status struct {
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
}

// Current is the last known published version compared with this build.
func Current() Status {
	if Disabled() {
		return Status{}
	}
	mu.Lock()
	l := latest
	mu.Unlock()
	return Status{Latest: l, UpdateAvailable: l != "" && Newer(l, config.AppVersion)}
}

// Newer reports whether version a is newer than b (MAJOR.MINOR.PATCH).
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	var out [3]int
	for i, s := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		out[i], _ = strconv.Atoi(s)
	}
	return out
}

// Check fetches the published version once and remembers it.
func Check(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CaskURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "hate/"+config.AppVersion)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &httpError{resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	m := versionRe.FindSubmatch(body)
	if m == nil {
		return "", errNoVersion
	}
	v := string(m[1])
	mu.Lock()
	latest, checkedAt = v, time.Now()
	mu.Unlock()
	saveCache()
	return v, nil
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "update check: HTTP " + strconv.Itoa(e.code) }

type errString string

func (e errString) Error() string { return string(e) }

const errNoVersion = errString("update check: no version line in the cask")

// Start runs the daily check in the background until ctx ends. The last
// result is cached in the app config folder, so a restart doesn't re-check.
func Start(ctx context.Context) {
	loadCache()
	go func() {
		for {
			mu.Lock()
			due := time.Since(checkedAt) >= Interval
			mu.Unlock()
			if due && !Disabled() {
				if _, err := Check(ctx); err != nil {
					log.Printf("%v", err) // silent to the user
					mu.Lock()
					checkedAt = time.Now() // don't retry until the next interval
					mu.Unlock()
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Hour):
			}
		}
	}()
}

type cache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

func cachePath() string { return filepath.Join(config.AppConfigDir(), "update-check.json") }

func loadCache() {
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return
	}
	var c cache
	if json.Unmarshal(data, &c) == nil {
		mu.Lock()
		latest, checkedAt = c.Latest, c.CheckedAt
		mu.Unlock()
	}
}

func saveCache() {
	mu.Lock()
	c := cache{Latest: latest, CheckedAt: checkedAt}
	mu.Unlock()
	data, _ := json.Marshal(c)
	if err := os.MkdirAll(config.AppConfigDir(), 0o755); err == nil {
		_ = fsutil.WriteFileAtomic(cachePath(), data, 0o644)
	}
}

// reset clears the remembered state (tests).
func reset() {
	mu.Lock()
	latest, checkedAt = "", time.Time{}
	mu.Unlock()
}
