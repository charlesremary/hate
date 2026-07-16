// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"fmt"
	"strconv"
	"strings"
)

// nextTestCaseID returns a fresh "tc<N>" id, one past the highest existing
// numeric suffix, so deleting then adding never reuses an id.
func nextTestCaseID(t *Ticket) string {
	max := 0
	for _, c := range t.TestCases {
		if strings.HasPrefix(c.ID, "tc") {
			if n, err := strconv.Atoi(c.ID[2:]); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("tc%d", max+1)
}

// HasFilledTestCases reports whether the ticket has at least one test case with
// both a step and an expected result — the bar for the enforce-QA promotion gate.
func HasFilledTestCases(t *Ticket) bool {
	for _, c := range t.TestCases {
		if strings.TrimSpace(c.Step) != "" && strings.TrimSpace(c.Expected) != "" {
			return true
		}
	}
	return false
}

// AddTestCase appends a test case to a ticket.
func AddTestCase(repoRoot, ticketID, step, expected, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	c := TestCase{
		ID:       nextTestCaseID(t),
		Step:     strings.TrimSpace(step),
		Expected: strings.TrimSpace(expected),
	}
	t.TestCases = append(t.TestCases, c)
	addActivity(t, author, "test_case_added", c.Step)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// AddTestCases appends several test cases in one write — the low-friction path
// for agents (emit them all) and the human paste-to-author box. Empty cases
// (no step and no expected) are skipped.
func AddTestCases(repoRoot, ticketID string, cases []TestCase, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	added := 0
	for _, in := range cases {
		step := strings.TrimSpace(in.Step)
		expected := strings.TrimSpace(in.Expected)
		if step == "" && expected == "" {
			continue
		}
		t.TestCases = append(t.TestCases, TestCase{ID: nextTestCaseID(t), Step: step, Expected: expected})
		added++
	}
	if added == 0 {
		return nil, fmt.Errorf("no test cases to add")
	}
	addActivity(t, author, "test_case_added", fmt.Sprintf("%d test case(s)", added))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// TestCaseUpdate carries partial edits; nil fields are left unchanged.
type TestCaseUpdate struct {
	Step     *string
	Expected *string
	Status   *string
	Comment  *string
}

// UpdateTestCase edits fields of one test case. Status must be "", "pass", or
// "fail".
func UpdateTestCase(repoRoot, ticketID, caseID string, upd TestCaseUpdate, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range t.TestCases {
		if t.TestCases[i].ID == caseID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("test case not found: %s", caseID)
	}
	c := &t.TestCases[idx]
	if upd.Step != nil {
		c.Step = strings.TrimSpace(*upd.Step)
	}
	if upd.Expected != nil {
		c.Expected = strings.TrimSpace(*upd.Expected)
	}
	if upd.Status != nil {
		s := strings.TrimSpace(*upd.Status)
		if s != "" && s != "pass" && s != "fail" {
			return nil, fmt.Errorf("invalid test case status %q (want pass, fail, or empty)", s)
		}
		c.Status = s
		verb := "untested"
		if s != "" {
			verb = s
		}
		addActivity(t, author, "test_case_result", fmt.Sprintf("%s: %s", c.Step, verb))
	}
	if upd.Comment != nil {
		c.Comment = strings.TrimSpace(*upd.Comment)
	}
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// DeleteTestCase removes a test case from a ticket.
func DeleteTestCase(repoRoot, ticketID, caseID, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range t.TestCases {
		if t.TestCases[i].ID == caseID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("test case not found: %s", caseID)
	}
	removed := t.TestCases[idx]
	t.TestCases = append(t.TestCases[:idx], t.TestCases[idx+1:]...)
	addActivity(t, author, "test_case_removed", removed.Step)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}
