// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"errors"
	"path/filepath"
	"testing"

	"hate/internal/ticket"
)

// setupProject makes a temp project "est" (prefix EST). Git is pointed at a
// nonexistent dir so auto-commits are no-ops.
func setupProject(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(base, "no-git"))
	root := filepath.Join(base, "est")
	if err := ticket.WriteConfig(root, ticket.DefaultConfig("c", "Est", "est", "EST")); err != nil {
		t.Fatal(err)
	}
	return root
}

func create(t *testing.T, root string, p ticket.CreateTicketParams) *ticket.Ticket {
	t.Helper()
	res, err := CreateTicket(root, p)
	if err != nil {
		t.Fatalf("create %q: %v", p.Title, err)
	}
	return res.Ticket
}

func status(t *testing.T, root, id string) string {
	t.Helper()
	tk, err := ticket.ReadTicket(root, id)
	if err != nil {
		t.Fatal(err)
	}
	return tk.Status
}

func TestPromoteNeedsTimeLog(t *testing.T) {
	root := setupProject(t)
	tk := create(t, root, ticket.CreateTicketParams{Type: "dev_task", Title: "Code X", Tags: []string{"functional"}})
	if _, err := ticket.ChangeStatus(root, tk.ID, "in_progress", "a"); err != nil {
		t.Fatal(err)
	}

	_, err := Promote(root, tk.ID, "a")
	if !errors.Is(err, ticket.ErrNeedsTimeLog) {
		t.Fatalf("Promote with no time: err = %v, want ErrNeedsTimeLog", err)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err kind: want ErrInvalidInput, got %v", err)
	}
	if s := status(t, root, tk.ID); s != "in_progress" {
		t.Errorf("status = %q, want in_progress", s)
	}

	if _, err := LogTime(root, tk.ID, LogTimeParams{Date: "2026-11-02", Hours: 0.5, Description: "wired handler", Author: "a"}); err != nil {
		t.Fatal(err)
	}
	res, err := Promote(root, tk.ID, "a")
	if err != nil {
		t.Fatalf("Promote after logging: %v", err)
	}
	if res.Ticket.Status == "in_progress" {
		t.Errorf("status did not advance")
	}
}

func TestClosedProjectRefusesWrites(t *testing.T) {
	root := setupProject(t)
	tk := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "x"})
	before := len(tk.Activity)

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ClosedAt = "2026-11-02T00:00:00Z"
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}

	_, err = AddComment(root, tk.ID, "hello", "a")
	if !errors.Is(err, ErrProjectClosed) {
		t.Fatalf("AddComment on closed project: err = %v, want ErrProjectClosed", err)
	}
	after, _ := ticket.ReadTicket(root, tk.ID)
	if len(after.Activity) != before {
		t.Errorf("activity count %d, want unchanged %d", len(after.Activity), before)
	}
	if _, err := CreateTicket(root, ticket.CreateTicketParams{Type: "task", Title: "y"}); !errors.Is(err, ErrProjectClosed) {
		t.Errorf("CreateTicket on closed project: err = %v, want ErrProjectClosed", err)
	}
	// Reads still work.
	if _, err := GetTicket(root, tk.ID); err != nil {
		t.Errorf("GetTicket on closed project: %v", err)
	}
}

func TestStrictTimeNeedsExtension(t *testing.T) {
	root := setupProject(t)
	cfg, _ := ticket.ReadConfig(root)
	cfg.StrictTimeEnforcement = true
	if err := ticket.WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	one := 1.0
	tk := create(t, root, ticket.CreateTicketParams{Type: "task", Title: "Set up X", Tags: []string{"config"}, EstimateHours: &one})
	if _, err := LogTime(root, tk.ID, LogTimeParams{Date: "2026-11-02", Hours: 1, Description: "setup", Author: "a"}); err != nil {
		t.Fatal(err)
	}

	_, err := LogTime(root, tk.ID, LogTimeParams{Date: "2026-11-02", Hours: 0.5, Description: "more", Author: "a"})
	var ext *TimeExtensionError
	if !errors.As(err, &ext) {
		t.Fatalf("LogTime past allotment: err = %v, want *TimeExtensionError", err)
	}
	if ext.AllottedHours != 1 || ext.SpentHours != 1 || ext.WouldBeHours != 1.5 {
		t.Errorf("extension = %+v, want allotted 1, spent 1, would_be 1.5", ext)
	}

	res, err := LogTime(root, tk.ID, LogTimeParams{Date: "2026-11-02", Hours: 0.5, Description: "more", Author: "a", ExtendAuthorized: true, ExtendReason: "client asked"})
	if err != nil {
		t.Fatalf("authorized extension: %v", err)
	}
	if n := len(res.Ticket.TimeEntries); n != 2 {
		t.Errorf("time entries = %d, want 2", n)
	}
}

func TestNotFoundKinds(t *testing.T) {
	root := setupProject(t)
	if _, err := GetTicket(root, "EST-zzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTicket missing: %v, want ErrNotFound", err)
	}
	if _, err := Promote(root, "EST-zzzz", "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Promote missing: %v, want ErrNotFound", err)
	}
	if _, err := ResolveProject("no-such-project-xyz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveProject missing: %v, want ErrNotFound", err)
	}
}

func TestEffortRetired(t *testing.T) {
	root := setupProject(t)
	_, err := CreateTicket(root, ticket.CreateTicketParams{Type: "task", Title: "x", Effort: "m"})
	if !errors.Is(err, ticket.ErrEffortRetired) || !errors.Is(err, ErrInvalidInput) {
		t.Errorf("effort: err = %v, want ErrEffortRetired + ErrInvalidInput", err)
	}
}
