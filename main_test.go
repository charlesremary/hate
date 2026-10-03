// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import "testing"

// HATE-2yqw tc1: with no flags hate binds 127.0.0.1 only; -listen / $HATE_LISTEN
// opt into other interfaces (and anything non-loopback is warned about).
func TestListenHost(t *testing.T) {
	cases := []struct{ flag, env, want string }{
		{"", "", "127.0.0.1"},
		{"", "0.0.0.0", "0.0.0.0"},
		{"0.0.0.0", "", "0.0.0.0"},
		{"127.0.0.1", "0.0.0.0", "127.0.0.1"}, // the flag wins
		{"all", "", "0.0.0.0"},
		{"*", "", "0.0.0.0"},
		{"0.0.0.0:9000", "", "0.0.0.0"},
		{"[::1]", "", "::1"},
		{"  ", " ", "127.0.0.1"},
	}
	for _, c := range cases {
		if got := listenHost(c.flag, c.env); got != c.want {
			t.Errorf("listenHost(%q, %q) = %q, want %q", c.flag, c.env, got, c.want)
		}
	}
	for host, want := range map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, "0.0.0.0": false, "192.168.1.5": false, "::": false} {
		if isLoopback(host) != want {
			t.Errorf("isLoopback(%q) = %v", host, !want)
		}
	}
}
