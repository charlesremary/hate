// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"hate/internal/service"
)

const (
	searchDefaultLimit = service.SearchDefaultLimit
	searchMaxLimit     = service.SearchMaxLimit
)

// SearchResult is one ticket hit from GET /api/search.
type SearchResult = service.SearchResult

// RegisterSearchRoutes registers the cross-project ticket search.
func RegisterSearchRoutes(r chi.Router) {
	r.Get("/api/search", handleSearch)
}

// handleSearch handles GET /api/search?q=&limit= — search-as-you-type over
// every registered, non-hidden project (closed ones included) by ticket ID
// and title (see service.Search). Read-only, no locks: it runs on every
// keystroke.
func handleSearch(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	respondJSON(w, http.StatusOK, service.Search(r.URL.Query().Get("q"), limit))
}
