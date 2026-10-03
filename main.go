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
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"hate/internal/api"
	"hate/internal/config"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	portFlag := flag.Int("port", 0, "HTTP port to listen on (default 8000, or $PORT)")
	listenFlag := flag.String("listen", "", "interface address to listen on (default 127.0.0.1, or $HATE_LISTEN); 0.0.0.0 exposes the API to the network")
	flag.Parse()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// API routes
	api.RegisterProjectRoutes(r)
	api.RegisterTicketRoutes(r)
	api.RegisterPMRoutes(r)

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
	fmt.Printf("hate v%s running on http://localhost:%d (listening on %s)\n", config.AppVersion, port, addr)
	log.Fatal(http.ListenAndServe(addr, r))
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
