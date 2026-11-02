// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"errors"
	"fmt"
	"strings"
)

// Error kinds. A service error matches exactly one with errors.Is; the
// underlying cause (ticket.ErrNeedsTimeLog, *ticket.EstimateValidationError,
// ticket.ErrEffortRetired, ...) stays reachable through errors.Is / errors.As
// too, so each transport picks the most specific mapping it knows.
var (
	// ErrNotFound: the project or ticket doesn't exist.
	ErrNotFound = errors.New("not found")
	// ErrProjectClosed: the project is closed, so writes are refused.
	ErrProjectClosed = errors.New("Project is closed — reopen it to make changes.")
	// ErrInvalidInput: the request broke a rule (bad value, gate, workflow).
	ErrInvalidInput = errors.New("invalid input")
	// ErrInternal: the change could not be completed for a reason the caller
	// can't fix (disk, index regeneration).
	ErrInternal = errors.New("internal error")
)

// Error is a service error: Kind is one of the Err* sentinels above, Msg is
// the user-facing text (the same text the REST API has always returned) and
// Cause the original error, if any.
type Error struct {
	Kind  error
	Msg   string
	Cause error
}

func (e *Error) Error() string { return e.Msg }

// Unwrap exposes both the kind and the cause to errors.Is / errors.As.
func (e *Error) Unwrap() []error {
	out := []error{e.Kind}
	if e.Cause != nil {
		out = append(out, e.Cause)
	}
	return out
}

// wrap tags cause with kind, keeping cause's message.
func wrap(kind, cause error) error {
	if cause == nil {
		return nil
	}
	var se *Error
	if errors.As(cause, &se) {
		return cause // already classified
	}
	return &Error{Kind: kind, Msg: cause.Error(), Cause: cause}
}

// classify tags an error from an internal/ticket mutator: "... not found ..."
// is ErrNotFound, anything else ErrInvalidInput (the ticket package reports
// both rule failures and missing tickets as plain errors).
func classify(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "not found") {
		return wrap(ErrNotFound, err)
	}
	return wrap(ErrInvalidInput, err)
}

// TimeExtensionError: under strict time enforcement, a time log would push a
// wrap ticket past its hours allotment and the caller didn't authorize an
// extension with a reason.
type TimeExtensionError struct {
	TicketID      string
	AllottedHours float64
	SpentHours    float64
	WouldBeHours  float64
}

func (e *TimeExtensionError) Error() string {
	return fmt.Sprintf("This log brings %s to %gh, past its %gh allotment. Authorization required to extend.",
		e.TicketID, e.WouldBeHours, e.AllottedHours)
}
