// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"hate/internal/service"
)

// RegisterGuideRoutes registers GET /api/guide: the embedded agent guide as
// markdown, or one numbered section with ?section=N.
func RegisterGuideRoutes(r chi.Router) {
	r.Get("/api/guide", handleGetGuide)
}

func handleGetGuide(w http.ResponseWriter, r *http.Request) {
	md, err := service.Guide(r.URL.Query().Get("section"))
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(md))
}
