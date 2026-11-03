// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"fmt"
	"strings"
)

// BlockedFlag marks a ticket that can't move forward. Since hate 3.0 blocked
// is a flag, not a status: the ticket keeps its status, unblocking is
// clearing the flag, and the time gate isn't reset.
type BlockedFlag struct {
	Reason string `json:"reason"`
	// WaitsOn: what it's waiting for. Ticket ids clear the flag on their own
	// when they all finish (ClearWaitsOn); anything else (a person, "client
	// sign-off") is just shown.
	WaitsOn []string `json:"waits_on,omitempty"`
	Since   string   `json:"since"`
	By      string   `json:"by,omitempty"`
}

// IsBlocked reports whether the ticket is flagged blocked.
func IsBlocked(t *Ticket) bool { return t != nil && t.Blocked != nil }

// Block flags a ticket blocked with a reason and, optionally, what it waits
// on. A closed ticket can't be blocked. The caller holds the project lock.
func Block(repoRoot, ticketID, reason string, waitsOn []string, author string) (*Ticket, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("a reason is required to block a ticket")
	}
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	if Contains(ClosedStatuses, t.Status) {
		return nil, fmt.Errorf("ticket is %s: a finished ticket can't be blocked", t.Status)
	}
	var waits []string
	for _, w := range waitsOn {
		if w = strings.TrimSpace(w); w != "" && w != ticketID {
			waits = append(waits, w)
		}
	}
	t.Blocked = &BlockedFlag{Reason: reason, WaitsOn: waits, Since: NowISO(), By: author}
	detail := reason
	if len(waits) > 0 {
		detail += " (waiting on " + strings.Join(waits, ", ") + ")"
	}
	addActivity(t, author, "blocked", detail)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// Unblock clears the flag. note (optional) goes in the activity log.
func Unblock(repoRoot, ticketID, note, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	if t.Blocked == nil {
		return nil, fmt.Errorf("ticket %s isn't blocked", ticketID)
	}
	t.Blocked = nil
	addActivity(t, author, "unblocked", strings.TrimSpace(note))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// ClearWaitsOn unblocks every ticket whose waits_on lists finishedID once all
// the tickets it waits on are finished. Returns the ids it unblocked (their
// files are written; the caller commits them). The caller holds the lock.
func ClearWaitsOn(repoRoot, finishedID, author string) ([]string, error) {
	all, err := ReadAllTickets(repoRoot)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Ticket{}
	for _, t := range all {
		byID[t.ID] = t
	}
	var cleared []string
	for _, t := range all {
		if t.Blocked == nil || !Contains(t.Blocked.WaitsOn, finishedID) {
			continue
		}
		done := true
		for _, w := range t.Blocked.WaitsOn {
			if o, ok := byID[w]; !ok || !Contains(ClosedStatuses, o.Status) {
				done = false // a person or an open ticket: still waiting
				break
			}
		}
		if !done {
			continue
		}
		t.Blocked = nil
		addActivity(t, author, "unblocked", finishedID+" finished")
		if err := WriteTicket(repoRoot, t); err != nil {
			return cleared, err
		}
		cleared = append(cleared, t.ID)
	}
	return cleared, nil
}
