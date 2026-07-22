// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"hate/internal/api"
	"hate/internal/config"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	portFlag := flag.Int("port", 0, "HTTP port to listen on (default 8000, or $PORT)")
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
	fmt.Printf("hate v%s running on http://localhost:%d\n", config.AppVersion, port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), r))
}
