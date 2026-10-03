// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"hate/internal/api"
	"hate/internal/config"
	"hate/internal/gitacct"
	"hate/internal/teamsync"
	"hate/internal/ticket"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	// git runs this same binary as its GIT_ASKPASS helper (see
	// gitacct.NetworkCommand): answer the one prompt and exit.
	if code, ok := askpassMode(os.Args, os.Getenv(gitacct.AskpassEnv)); ok {
		os.Exit(code)
	}

	portFlag := flag.Int("port", 0, "HTTP port to listen on (default 8000, or $PORT)")
	listenFlag := flag.String("listen", "", "interface address to listen on (default 127.0.0.1, or $HATE_LISTEN); 0.0.0.0 exposes the API to the network")
	noBrowser := flag.Bool("no-browser", false, "don't open the web browser on start (also $HATE_NO_BROWSER=1)")
	flag.Parse()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// API routes
	api.RegisterProjectRoutes(r)
	api.RegisterTicketRoutes(r)
	api.RegisterPMRoutes(r)
	api.RegisterGitRoutes(r)

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
func shouldOpenBrowser(flagOff bool, env string) bool {
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
