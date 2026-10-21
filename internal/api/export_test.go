// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// HATE-t20f tc4/tc5: export.xlsx is a download named <project>-<today>.xlsx;
// an unknown project is a 404.
func TestExportXLSXAPI(t *testing.T) {
	h, _ := setupCosmicProjects(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects/est/export.xlsx", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET export.xlsx: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("Content-Type = %q", ct)
	}
	want := `attachment; filename="est-` + time.Now().Format("2006-01-02") + `.xlsx"`
	if cd := rec.Header().Get("Content-Disposition"); cd != want {
		t.Errorf("Content-Disposition = %q, want %q", cd, want)
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("body is not a zip: %v", err)
	}
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "xl/workbook.xml"
	}
	if !found {
		t.Error("no xl/workbook.xml in the export")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects/NOPE/export.xlsx", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}
}
