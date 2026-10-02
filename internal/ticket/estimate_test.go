// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"errors"
	"testing"
)

func fp(v float64) *float64 { return &v }

func TestClassOf(t *testing.T) {
	cases := map[string][]string{
		"":           {"parent:X", "cfp:3"},
		"functional": {"parent:X", "functional"},
		"config":     {"config", "nonfunc"}, // first wins
		"nonfunc":    {"qa", "nonfunc"},
	}
	for want, tags := range cases {
		if got := ClassOf(&Ticket{Tags: tags}); got != want {
			t.Errorf("ClassOf(%v) = %q, want %q", tags, got, want)
		}
	}
}

func TestValidateEstimateHours(t *testing.T) {
	for _, ok := range []*float64{nil, fp(0.25), fp(0.5), fp(1), fp(7.75), fp(40)} {
		if err := ValidateEstimateHours(ok); err != nil {
			t.Errorf("%v: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []float64{0, -1, 0.1, 0.3, 1.1, 2.6} {
		if err := ValidateEstimateHours(fp(bad)); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
}

// TestValidatePromoteEstimate covers rules a)-d) and the exemptions.
func TestValidatePromoteEstimate(t *testing.T) {
	child := func(tags []string, est *float64) *Ticket {
		return &Ticket{ID: "T-1", Type: "dev_task", Status: "not_started", Tags: append([]string{"parent:F-1"}, tags...), EstimateHours: est}
	}
	bad := map[string]*Ticket{
		"a) no class":           child(nil, nil),
		"a) two classes":        child([]string{"functional", "config"}, nil),
		"b) config no hours":    child([]string{"config"}, nil),
		"b) nonfunc no hours":   child([]string{"nonfunc"}, nil),
		"c) functional + hours": child([]string{"functional"}, fp(2)),
		"d) cfp on child":       child([]string{"functional", "cfp:4"}, nil),
		"d) cfp on wrap child":  child([]string{"config", "cfp:4"}, fp(1)),
	}
	for name, tk := range bad {
		err := ValidatePromoteEstimate(tk)
		var ev *EstimateValidationError
		if !errors.As(err, &ev) {
			t.Errorf("%s: got %v, want an EstimateValidationError", name, err)
		}
	}

	good := map[string]*Ticket{
		"functional child":       child([]string{"functional"}, nil),
		"config with hours":      child([]string{"config"}, fp(0.5)),
		"parent / no parent tag": {ID: "F-1", Type: "task", Status: "not_started", Tags: []string{"cfp:8"}},
		"self-contained feature": {ID: "S", Type: "dev_task", Status: "not_started", Tags: []string{"cfp:8", "functional"}},
		"meeting child":          {ID: "M", Type: "meeting", Status: "not_started", Tags: []string{"parent:F-1"}},
		"admin child":            {ID: "A", Type: "administration", Status: "not_started", Tags: []string{"parent:F-1"}},
		// Only the first promote (out of not_started) is checked.
		"already started": {ID: "T-2", Type: "dev_task", Status: "in_progress", Tags: []string{"parent:F-1"}},
	}
	for name, tk := range good {
		if err := ValidatePromoteEstimate(tk); err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
}

// TestSetFieldEffortAndEstimate: effort can only be cleared; estimate_hours is
// validated and clearable.
func TestSetFieldEffortAndEstimate(t *testing.T) {
	m := "m"
	tk := &Ticket{Effort: &m}
	if err := setFieldValue(tk, "effort", "l"); !errors.Is(err, ErrEffortRetired) {
		t.Errorf("setting effort: got %v, want ErrEffortRetired", err)
	}
	if err := setFieldValue(tk, "effort", nil); err != nil || tk.Effort != nil {
		t.Errorf("clearing effort: err=%v effort=%v, want nil/nil", err, tk.Effort)
	}
	if err := setFieldValue(tk, "estimate_hours", 1.5); err != nil || tk.EstimateHours == nil || *tk.EstimateHours != 1.5 {
		t.Errorf("set estimate_hours 1.5: err=%v value=%v", err, tk.EstimateHours)
	}
	if err := setFieldValue(tk, "estimate_hours", 0.1); err == nil {
		t.Error("estimate_hours 0.1 should be rejected")
	}
	if err := setFieldValue(tk, "estimate_hours", "2"); err == nil {
		t.Error("estimate_hours as a string should be rejected")
	}
	if err := setFieldValue(tk, "estimate_hours", nil); err != nil || tk.EstimateHours != nil {
		t.Errorf("clear estimate_hours: err=%v value=%v", err, tk.EstimateHours)
	}
	if _, ok := EditableFields["effort"]; ok {
		t.Error("effort must not be an editable field")
	}
	if _, ok := EditableFields["estimate_hours"]; !ok {
		t.Error("estimate_hours must be an editable field")
	}
}

// newTestRepo creates a minimal project repo (config only) in a temp dir.
func newTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := WriteConfig(root, DefaultConfig("c", "p", "p", "TST")); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestCreateAndPromoteEstimateRules exercises CreateTicket and Promote on disk:
// effort is rejected on create, estimate_hours is stored, the first promote
// enforces the rules, and force-close bypasses them.
func TestCreateAndPromoteEstimateRules(t *testing.T) {
	root := newTestRepo(t)

	if _, err := CreateTicket(root, CreateTicketParams{Type: "task", Title: "x", Effort: "m"}); !errors.Is(err, ErrEffortRetired) {
		t.Errorf("create with effort: got %v, want ErrEffortRetired", err)
	}
	if _, err := CreateTicket(root, CreateTicketParams{Type: "task", Title: "x", EstimateHours: fp(0.3)}); err == nil {
		t.Error("create with estimate_hours 0.3 should fail")
	}

	wrap, err := CreateTicket(root, CreateTicketParams{Type: "task", Title: "deploy", Tags: []string{"parent:F", "config"}, EstimateHours: fp(2)})
	if err != nil {
		t.Fatal(err)
	}
	if wrap.EstimateHours == nil || *wrap.EstimateHours != 2 {
		t.Fatalf("estimate_hours not stored: %v", wrap.EstimateHours)
	}
	if p, err := Promote(root, wrap.ID, "me"); err != nil || p.Status != "in_progress" {
		t.Errorf("promote valid wrap: status=%v err=%v", p, err)
	}

	bare, err := CreateTicket(root, CreateTicketParams{Type: "dev_task", Title: "code", Tags: []string{"parent:F"}})
	if err != nil {
		t.Fatal(err) // create never blocks on the estimate rules
	}
	_, err = Promote(root, bare.ID, "me")
	var ev *EstimateValidationError
	if !errors.As(err, &ev) {
		t.Fatalf("promote unclassed child: got %v, want EstimateValidationError", err)
	}
	if fc, err := ForceClose(root, bare.ID, "scoped out", "me"); err != nil || fc.Status != "closed" {
		t.Errorf("force-close should bypass the estimate rules: %v / %v", fc, err)
	}
}
