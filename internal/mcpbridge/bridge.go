// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package mcpbridge is `hate mcp`: an MCP stdio server that forwards every
// JSON-RPC message to the running app's HTTP endpoint (/mcp) and writes the
// replies back (HATE-qypd). It never touches project files: the one app
// process keeps the project lock, auto-commit and sync. When no app is
// listening, it starts one in the background and waits for it.
package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Bridge relays newline-delimited JSON-RPC between in/out and an MCP HTTP
// endpoint.
type Bridge struct {
	URL    string // e.g. http://127.0.0.1:8000/mcp
	Client *http.Client
	// protocol is the negotiated protocol version, learned from the
	// initialize result (older clients) or each request's _meta (2026-07-28).
	protocol string
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Run relays until in ends or ctx is done. A failed POST becomes a JSON-RPC
// error for that request (the client sees why), not a dead bridge.
func (b *Bridge) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if b.Client == nil {
		b.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		replies, err := b.forward(ctx, line)
		if err != nil {
			var m message
			if json.Unmarshal(line, &m) == nil && len(m.ID) > 0 {
				replies = [][]byte{rpcError(m.ID, -32603, "hate isn't reachable: "+err.Error())}
			}
			fmt.Fprintln(os.Stderr, "hate mcp:", err)
		}
		for _, r := range replies {
			w.Write(r)
			w.WriteByte('\n')
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return sc.Err()
}

func rpcError(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	return b
}

// forward POSTs one message and returns the reply messages (none for a
// notification).
func (b *Bridge) forward(ctx context.Context, body []byte) ([][]byte, error) {
	var m message
	_ = json.Unmarshal(body, &m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if pv := b.versionFor(m); pv != "" {
		req.Header.Set("MCP-Protocol-Version", pv)
	}
	if m.Method != "" {
		req.Header.Set("Mcp-Method", m.Method)
		if name := nameOf(m); name != "" {
			req.Header.Set("Mcp-Name", name)
		}
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(data)) == 0 {
		return nil, nil // a notification or response: nothing comes back
	}
	var replies [][]byte
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		replies = sseData(data)
	} else {
		replies = [][]byte{bytes.TrimSpace(data)}
	}
	if m.Method == "initialize" {
		for _, r := range replies {
			var res struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if json.Unmarshal(r, &res) == nil && res.Result.ProtocolVersion != "" {
				b.protocol = res.Result.ProtocolVersion
			}
		}
	}
	if resp.StatusCode >= 400 && len(replies) == 0 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return replies, nil
}

// versionFor is the MCP-Protocol-Version header for a message: the version
// in its _meta (2026-07-28 stateless requests), else the negotiated one.
func (b *Bridge) versionFor(m message) string {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(m.Params) > 0 && json.Unmarshal(m.Params, &p) == nil {
		var v string
		if raw, ok := p.Meta["io.modelcontextprotocol/protocolVersion"]; ok && json.Unmarshal(raw, &v) == nil && v != "" {
			return v
		}
	}
	return b.protocol
}

// nameOf is the Mcp-Name header value: the tool or prompt name, or the
// resource URI.
func nameOf(m message) string {
	var p struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	if len(m.Params) == 0 || json.Unmarshal(m.Params, &p) != nil {
		return ""
	}
	switch m.Method {
	case "tools/call", "prompts/get":
		return p.Name
	case "resources/read":
		return p.URI
	}
	return ""
}

// sseData extracts the JSON payloads of an event stream.
func sseData(data []byte) [][]byte {
	var out [][]byte
	var cur []byte
	flush := func() {
		if len(bytes.TrimSpace(cur)) > 0 {
			out = append(out, bytes.TrimSpace(cur))
		}
		cur = nil
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		switch {
		case len(line) == 0:
			flush()
		case bytes.HasPrefix(line, []byte("data:")):
			if cur != nil {
				cur = append(cur, '\n')
			}
			cur = append(cur, bytes.TrimSpace(line[5:])...)
		}
	}
	flush()
	return out
}

// ---- finding or starting the app ----

// Running reports whether a hate app answers on base (http://127.0.0.1:port).
func Running(base string) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(base + "/api/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&v) == nil && v.Version != ""
}

// EnsureRunning returns once a hate app answers on port, starting this binary
// in the background (-no-browser, its log in logPath) when none does. It
// waits up to wait for it to come up.
func EnsureRunning(port int, logPath string, wait time.Duration) error {
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if Running(base) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "-no-browser", "-port", fmt.Sprint(port))
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		logf.Close()
		return fmt.Errorf("starting hate: %w", err)
	}
	logf.Close()
	_ = cmd.Process.Release()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if Running(base) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("started hate, but it didn't answer on port " + fmt.Sprint(port) + " within " + wait.String() + " (log: " + logPath + ")")
}
