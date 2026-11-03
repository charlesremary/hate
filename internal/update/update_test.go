// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"hate/internal/config"
)

func setup(t *testing.T, body string, status int) *int32 {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	oldURL, oldCfg := CaskURL, config.AppConfigPath
	CaskURL = srv.URL
	config.AppConfigPath = filepath.Join(t.TempDir(), "config.json")
	t.Setenv("HATE_NO_UPDATE_CHECK", "")
	t.Cleanup(func() { CaskURL, config.AppConfigPath = oldURL, oldCfg; reset() })
	reset()
	return &hits
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.1.0", "1.0.20", true}, {"1.0.20", "1.1.0", false}, {"2.0.0", "1.9.9", true}, {"1.0.10", "1.0.9", true}, {"1.0.0", "1.0.0", false}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// HATE-xmat tc1: a higher published version is reported.
func TestCheckFindsNewer(t *testing.T) {
	setup(t, "cask \"hate\" do\n  version \"99.0.0\"\n  sha256 \"x\"\nend\n", 200)
	if v, err := Check(context.Background()); err != nil || v != "99.0.0" {
		t.Fatalf("Check = %q, %v", v, err)
	}
	if st := Current(); !st.UpdateAvailable || st.Latest != "99.0.0" {
		t.Errorf("Current = %+v", st)
	}
	// The cache survives a restart.
	reset()
	loadCache()
	if st := Current(); st.Latest != "99.0.0" {
		t.Errorf("after reload: %+v", st)
	}
}

func TestSameVersionNoBanner(t *testing.T) {
	setup(t, "  version \""+config.AppVersion+"\"\n", 200)
	Check(context.Background())
	if st := Current(); st.UpdateAvailable {
		t.Errorf("Current = %+v", st)
	}
}

// HATE-xmat tc3: failures are errors for the log, never a banner.
func TestCheckFailureIsSilent(t *testing.T) {
	setup(t, "nope", 500)
	if _, err := Check(context.Background()); err == nil {
		t.Error("want an error for HTTP 500")
	}
	if st := Current(); st.UpdateAvailable || st.Latest != "" {
		t.Errorf("Current = %+v", st)
	}
}

// HATE-xmat tc4: off means no request and no banner.
func TestDisabled(t *testing.T) {
	setup(t, "  version \"99.0.0\"\n", 200)
	Check(context.Background())
	t.Setenv("HATE_NO_UPDATE_CHECK", "1")
	if st := Current(); st.UpdateAvailable {
		t.Errorf("disabled but Current = %+v", st)
	}
	if !Disabled() {
		t.Error("Disabled() = false")
	}
}

func TestStartDisabledMakesNoRequest(t *testing.T) {
	hits := setup(t, "  version \"99.0.0\"\n", 200)
	t.Setenv("HATE_NO_UPDATE_CHECK", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Start(ctx)
	time.Sleep(200 * time.Millisecond)
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("%d requests while disabled", n)
	}
}

func TestStartChecksWhenDue(t *testing.T) {
	hits := setup(t, "  version \"99.0.0\"\n", 200)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(hits) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(hits) != 1 || Current().Latest != "99.0.0" {
		t.Errorf("hits=%d current=%+v", atomic.LoadInt32(hits), Current())
	}
}
