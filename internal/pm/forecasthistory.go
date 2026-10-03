// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hate/internal/fsutil"
	"hate/internal/ticket"
)

// Forecast history: .tkt/pm/forecast_history.json keeps at most one entry per
// calendar day, appended (or that day's entry replaced) only when the forecast
// differs from the last entry, and committed. It feeds the trend chart on the
// Schedule vs request card.

// ForecastHistoryEntry is one day's forecast.
type ForecastHistoryEntry struct {
	Date           string  `json:"date"` // YYYY-MM-DD the forecast was computed
	LikelyFinish   string  `json:"likely_finish"`
	P85Finish      string  `json:"p85_finish"`
	RequestedEnd   string  `json:"requested_end"`
	RemainingHours float64 `json:"remaining_hours"`
	// ComputedAt is when this entry was computed (RFC 3339, UTC). Two machines
	// can each record the same day; a sync merge keeps the later computation.
	// Empty in entries written before it existed.
	ComputedAt string `json:"computed_at,omitempty"`
}

// sameForecast reports whether two entries carry the same forecast (dates aside).
func (e ForecastHistoryEntry) sameForecast(o ForecastHistoryEntry) bool {
	return e.LikelyFinish == o.LikelyFinish && e.P85Finish == o.P85Finish &&
		e.RequestedEnd == o.RequestedEnd && round1(e.RemainingHours) == round1(o.RemainingHours)
}

// ForecastHistoryPath is .tkt/pm/forecast_history.json.
func ForecastHistoryPath(projectRoot string) string {
	return filepath.Join(PMDir(projectRoot), "forecast_history.json")
}

// ReadForecastHistory reads the history (empty when the file doesn't exist).
func ReadForecastHistory(projectRoot string) ([]ForecastHistoryEntry, error) {
	data, err := os.ReadFile(ForecastHistoryPath(projectRoot))
	if os.IsNotExist(err) {
		return []ForecastHistoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var hist []ForecastHistoryEntry
	if err := json.Unmarshal(data, &hist); err != nil {
		return nil, fmt.Errorf("failed to parse forecast history: %w", err)
	}
	if hist == nil {
		hist = []ForecastHistoryEntry{}
	}
	return hist, nil
}

// MergeForecastHistory applies one computation to the history: unchanged when
// it matches the last entry; that day's entry replaced when the last entry is
// from the same day; otherwise appended. Returns the history and whether it
// changed.
func MergeForecastHistory(hist []ForecastHistoryEntry, e ForecastHistoryEntry) ([]ForecastHistoryEntry, bool) {
	if n := len(hist); n > 0 {
		last := hist[n-1]
		if last.sameForecast(e) {
			return hist, false
		}
		if last.Date == e.Date {
			out := append([]ForecastHistoryEntry{}, hist...)
			out[n-1] = e
			return out, true
		}
	}
	return append(append([]ForecastHistoryEntry{}, hist...), e), true
}

// RecordForecastHistory records rep (computed on `today`) in the project's
// history and commits the file when it changed. Nothing is recorded without a
// requested end. Returns the (possibly updated) history and whether it changed.
// Shared by the dashboard and GET /forecast. Takes the project lock (so
// concurrent dashboard loads don't corrupt the file); a failed commit is only
// logged (by ticket.CommitFiles).
func RecordForecastHistory(projectRoot string, rep ForecastReport, today time.Time) ([]ForecastHistoryEntry, bool, error) {
	defer ticket.LockProject(projectRoot)()

	hist, err := ReadForecastHistory(projectRoot)
	if err != nil {
		return nil, false, err
	}
	if rep.RequestedEnd == "" {
		return hist, false, nil
	}
	hist, changed := MergeForecastHistory(hist, ForecastHistoryEntry{
		Date:           fmtDate(dateOnly(today)),
		LikelyFinish:   rep.LikelyFinish,
		P85Finish:      rep.P85Finish,
		RequestedEnd:   rep.RequestedEnd,
		RemainingHours: rep.RemainingHours,
		ComputedAt:     today.UTC().Format(time.RFC3339),
	})
	if !changed {
		return hist, false, nil
	}
	path := ForecastHistoryPath(projectRoot)
	data, err := json.MarshalIndent(hist, "", "  ")
	if err != nil {
		return hist, false, err
	}
	if err := fsutil.WriteFileAtomic(path, append(data, '\n'), 0644); err != nil {
		return hist, false, err
	}
	_ = ticket.CommitFiles(projectRoot, []string{path}, "forecast history")
	return hist, true, nil
}
