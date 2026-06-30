// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"testing"

	"hate/internal/ticket"
)

func TestComputeProjectStats(t *testing.T) {
	tickets := []*ticket.Ticket{
		tkt("P", []string{"cfp:10"}),                          // feature, 0 hours
		tkt("c1", []string{"parent:P", "functional"}, 20),     // functional 20h
		tkt("c2", []string{"parent:P", "config", "wt:flow"}, 4), // config 4h, also tagged flow
		tkt("c3", []string{"parent:P", "nonfunc"}, 2),         // nonfunc 2h
		tkt("c4", []string{"parent:P", "author"}, 5),          // authored IaC 5h
		tkt("c5", []string{"parent:P"}, 1),                    // unclassed 1h
	}

	s := ComputeProjectStats(tickets)

	if s.TotalCFP != 10 {
		t.Errorf("cfp = %d, want 10", s.TotalCFP)
	}
	if s.FunctionalHours != 20 || s.ConfigHours != 4 || s.NonfuncHours != 2 || s.AuthorHours != 5 || s.UnclassedHours != 1 {
		t.Errorf("class hours wrong: %+v", s)
	}
	// class cut reconciles to total logged hours: 20+4+2+5+1 = 32
	if s.TotalLoggedHours != 32 {
		t.Errorf("total logged = %v, want 32", s.TotalLoggedHours)
	}
	if s.HPerCFP == nil || *s.HPerCFP != 2.0 {
		t.Errorf("h/CFP = %v, want 2.0", s.HPerCFP)
	}
	if s.WrapPct == nil || *s.WrapPct != 100.0*6/20 {
		t.Errorf("wrap = %v, want 30", s.WrapPct)
	}
	if s.TicketsWithHours != 5 {
		t.Errorf("tickets with hours = %d, want 5", s.TicketsWithHours)
	}

	// Tag cut: descriptive tags only — structural parent:/cfp:/wtn: excluded.
	byTag := map[string]TagStat{}
	for _, ts := range s.TagStats {
		byTag[ts.Tag] = ts
	}
	if _, ok := byTag["parent:P"]; ok {
		t.Errorf("structural parent: tag should be excluded from the breakdown")
	}
	if _, ok := byTag["cfp:10"]; ok {
		t.Errorf("structural cfp: tag should be excluded from the breakdown")
	}
	if byTag["wt:flow"].Tickets != 1 || byTag["wt:flow"].Hours != 4 {
		t.Errorf("wt:flow tag = %+v, want 1 ticket / 4h", byTag["wt:flow"])
	}
	if byTag["functional"].AvgHoursPerTicket != 20 {
		t.Errorf("functional avg = %v, want 20", byTag["functional"].AvgHoursPerTicket)
	}
	// sorted by hours desc — first entry is the biggest
	if s.TagStats[0].Hours < s.TagStats[len(s.TagStats)-1].Hours {
		t.Errorf("tag stats not sorted by hours desc")
	}
}
