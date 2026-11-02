// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package mcpserver

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// guard applies the localhost rules in front of the MCP handler, in order:
//
//  1. The TCP peer must be a loopback address (MCP never leaves the machine,
//     even when the REST API listens on 0.0.0.0).
//  2. The Host header must name localhost / a loopback IP (DNS rebinding). The
//     SDK checks this too, but only when it can see the local address.
//  3. An Origin header, when present, must be the hate UI itself
//     (http://localhost|127.0.0.1|[::1]:<port>). CLI clients send none.
//  4. A process-wide rate limit (the spec requires one).
func guard(port int, rl *rateLimiter, next http.Handler) http.Handler {
	p := strconv.Itoa(port)
	allowed := map[string]bool{
		"http://localhost:" + p: true,
		"http://127.0.0.1:" + p: true,
		"http://[::1]:" + p:     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackPeer(r.RemoteAddr) {
			rpcError(w, http.StatusForbidden, "MCP is only available from this machine")
			return
		}
		if !loopbackHost(r.Host) {
			rpcError(w, http.StatusForbidden, "invalid Host header: "+r.Host)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !allowed[o] {
			rpcError(w, http.StatusForbidden, "Origin not allowed: "+o)
			return
		}
		if !rl.allow() {
			rpcError(w, http.StatusTooManyRequests, "rate limit exceeded; slow down and retry")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackPeer reports whether remoteAddr ("host:port") is a loopback address.
func loopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loopbackHost reports whether a Host header ("host[:port]") names this
// machine: localhost or a loopback IP.
func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// rpcError writes an HTTP error with a JSON-RPC error body that has no id
// (the request was refused before it was read).
func rpcError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": -32600, "message": msg},
	})
}

// rateLimiter is a token bucket: rate tokens per second, up to burst.
type rateLimiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newRateLimiter(rate, burst float64) *rateLimiter {
	return &rateLimiter{rate: rate, burst: burst, tokens: burst, now: time.Now}
}

func (l *rateLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.last.IsZero() {
		l.tokens += now.Sub(l.last).Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
