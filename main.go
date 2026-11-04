// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"hate/internal/api"
	"hate/internal/config"
	"hate/internal/gitacct"
	"hate/internal/mcpbridge"
	"hate/internal/mcpserver"
	"hate/internal/service"
	"hate/internal/teamsync"
	"hate/internal/ticket"
	"hate/internal/update"
)

//go:embed static/*
var staticFiles embed.FS

// agentGuide is the agent contract for tags, estimates and the API, served at
// GET /api/guide and as the MCP resource hate://guide.
//
//go:embed docs/ticketing-and-cfp-guide.md
var agentGuide string

func main() {
	// git runs this same binary as its GIT_ASKPASS helper (see
	// gitacct.NetworkCommand): answer the one prompt and exit.
	if code, ok := askpassMode(os.Args, os.Getenv(gitacct.AskpassEnv)); ok {
		os.Exit(code)
	}

	// `hate mcp`: the stdio MCP bridge to the running app (HATE-qypd).
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(runMCPBridge(os.Args[2:]))
	}

	portFlag := flag.Int("port", 0, "HTTP port to listen on (default 8000, or $PORT)")
	listenFlag := flag.String("listen", "", "interface address to listen on (default 127.0.0.1, or $HATE_LISTEN); 0.0.0.0 exposes the API to the network")
	noBrowser := flag.Bool("no-browser", false, "don't open the web browser on start (also $HATE_NO_BROWSER=1)")
	noMCP := flag.Bool("no-mcp", false, "don't serve the MCP endpoint at /mcp (also $HATE_NO_MCP=1)")
	flag.Parse()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// API routes
	api.RegisterProjectRoutes(r)
	api.RegisterTicketRoutes(r)
	api.RegisterPMRoutes(r)
	api.RegisterGitRoutes(r)
	api.RegisterSearchRoutes(r)
	service.SetGuide(agentGuide)
	update.Start(context.Background()) // daily new-version check; off via Settings or HATE_NO_UPDATE_CHECK=1
	api.RegisterGuideRoutes(r)

	// Serve embedded static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	r.Handle("/static/*", http.StripPrefix("/static/", fileServer))

	// Serve index.html at root
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "Not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	// Port: -port flag wins, else $PORT, else 8000.
	port := 8000
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	if *portFlag > 0 {
		port = *portFlag
	}
	host := listenHost(*listenFlag, os.Getenv("HATE_LISTEN"))
	if !isLoopback(host) {
		log.Printf("WARNING: listening on %s: anyone who can reach this machine on port %d can use the hate API "+
			"(read and change every project, and commit/push as you). Use the default (127.0.0.1) unless you mean it.", host, port)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	// MCP endpoint (loopback clients only, whatever -listen says).
	mcpOn := envOn(*noMCP, os.Getenv("HATE_NO_MCP"))
	if mcpOn {
		r.Handle(mcpserver.Path, mcpserver.Handler(port))
	}

	if c := gitacct.CheckGit(); !c.Installed {
		log.Printf("Git isn't installed (or not on the PATH). Projects can't be shared until it is: %s "+
			"(Settings shows the steps).", c.InstallURL)
	}

	// Automatic sync (runs only while a Git account is configured): a push
	// shortly after each commit, and periodic pulls of the open projects.
	ticket.AfterCommit = teamsync.Default.Committed
	teamsync.Default.Start()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("can't listen on %s: %v (is hate already running? try -port)", addr, err)
	}
	appURL := fmt.Sprintf("http://localhost:%d/", port)
	fmt.Printf("hate v%s running on %s (listening on %s)\n", config.AppVersion, appURL, addr)
	if mcpOn {
		fmt.Printf("MCP endpoint: http://127.0.0.1:%d%s\n", port, mcpserver.Path)
	}
	if shouldOpenBrowser(*noBrowser, os.Getenv("HATE_NO_BROWSER")) {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openBrowser(appURL); err != nil {
				log.Printf("couldn't open the browser (%v); open %s yourself", err, appURL)
			}
		}()
	}
	log.Fatal(http.Serve(ln, r))
}

// askpassMode handles "hate <prompt>" run by git as GIT_ASKPASS (with
// HATE_ASKPASS=1 set by hate), or "hate askpass <prompt>" by hand. ok is false
// for a normal start.
func askpassMode(args []string, env string) (code int, ok bool) {
	switch {
	case len(args) == 3 && args[1] == "askpass":
		return gitacct.Askpass(args[2], os.Stdout, os.Stderr), true
	case env == "1" && len(args) == 2:
		return gitacct.Askpass(args[1], os.Stdout, os.Stderr), true
	}
	return 0, false
}

// shouldOpenBrowser: on unless -no-browser or HATE_NO_BROWSER is set.
func shouldOpenBrowser(flagOff bool, env string) bool { return envOn(flagOff, env) }

// envOn: a feature is on unless its -no-X flag is set or its $HATE_NO_X env
// var is set to anything but "", "0", "false" or "no".
func envOn(flagOff bool, env string) bool {
	if flagOff {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", "0", "false", "no":
		return true
	}
	return false
}

// openBrowser opens url in the default browser (a variable so tests can
// capture the call).
var openBrowser = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// defaultListenHost is where hate listens unless told otherwise: loopback
// only, so nobody else on the network can call the API.
const defaultListenHost = "127.0.0.1"

// listenHost picks the interface to bind: the -listen flag wins, then
// $HATE_LISTEN, then 127.0.0.1. "all" / "*" mean every interface (0.0.0.0).
// A value with a port ("0.0.0.0:9000") keeps only the host; the port comes
// from -port / $PORT.
func listenHost(flagVal, envVal string) string {
	v := strings.TrimSpace(flagVal)
	if v == "" {
		v = strings.TrimSpace(envVal)
	}
	if h, _, err := net.SplitHostPort(v); err == nil {
		v = h
	}
	v = strings.Trim(v, "[]")
	switch strings.ToLower(v) {
	case "":
		return defaultListenHost
	case "all", "*":
		return "0.0.0.0"
	}
	return v
}

// isLoopback reports whether host only accepts local connections.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// runMCPBridge is `hate mcp [-port N]`: an MCP stdio server for clients that
// only speak stdio. It forwards to the running app's /mcp (starting the app in
// the background if none is listening), so the one app process keeps the
// project lock. Errors go to stderr; stdout carries only MCP messages.
func runMCPBridge(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	port := fs.Int("port", 0, "port of the running hate app (default 8000, or $PORT)")
	noStart := fs.Bool("no-start", false, "don't start hate when it isn't running; fail instead")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p := 8000
	if v := os.Getenv("PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p = n
		}
	}
	if *port > 0 {
		p = *port
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", p)
	if *noStart {
		if !mcpbridge.Running(base) {
			fmt.Fprintf(os.Stderr, "hate mcp: no hate app is answering on port %d; start hate first\n", p)
			return 1
		}
	} else if err := mcpbridge.EnsureRunning(p, filepath.Join(os.TempDir(), "hate-mcp-app.log"), 10*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "hate mcp:", err)
		return 1
	}
	b := &mcpbridge.Bridge{URL: base + "/mcp"}
	if err := b.Run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hate mcp:", err)
		return 1
	}
	return 0
}
