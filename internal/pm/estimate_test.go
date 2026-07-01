// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import "testing"

func f64(v float64) *float64 { return &v }

func TestBuildCosmicEstimate(t *testing.T) {
	// 120 CFP × 0.17 h/CFP = 20.4 code; ×35% wrap = 7.14 wrap; total 27.54
	e := BuildCosmicEstimate(120, f64(0.17), f64(35))
	if !approx(e.CodeHours, 20.4) {
		t.Errorf("code = %v, want 20.4", e.CodeHours)
	}
	if !approx(e.WrapHours, 20.4*0.35) {
		t.Errorf("wrap = %v, want %v", e.WrapHours, 20.4*0.35)
	}
	if !approx(e.TotalHours, 20.4*1.35) {
		t.Errorf("total = %v, want %v", e.TotalHours, 20.4*1.35)
	}

	// no wrap given → total = code only
	e2 := BuildCosmicEstimate(100, f64(0.2), nil)
	if !approx(e2.TotalHours, 20) {
		t.Errorf("no-wrap total = %v, want 20", e2.TotalHours)
	}

	// no rate set → zero hours, inputs preserved
	e3 := BuildCosmicEstimate(100, nil, f64(50))
	if e3.CodeHours != 0 || e3.TotalHours != 0 {
		t.Errorf("unset rate should give 0 hours, got %+v", e3)
	}
	if e3.TotalCFP != 100 {
		t.Errorf("total_cfp should pass through, got %d", e3.TotalCFP)
	}
}
