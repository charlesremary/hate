// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"hate/internal/ticket"
)

// OverviewRequest is the replace-all payload for the Project Overview tab.
type OverviewRequest struct {
	Contacts     []ticket.Contact     `json:"contacts"`
	Links        []ticket.Link        `json:"links"`
	Instructions []ticket.Instruction `json:"instructions"`
}

// overviewID returns a short random id for a new overview item.
func overviewID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "ov"
	}
	return "ov" + hex.EncodeToString(b[:])
}

// getOverview handles GET /api/projects/{projectId}/overview.
func getOverview(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, OverviewRequest{
		Contacts:     nonNilContacts(cfg.Contacts),
		Links:        nonNilLinks(cfg.Links),
		Instructions: nonNilInstructions(cfg.Instructions),
	})
}

// updateOverview handles PUT /api/projects/{projectId}/overview. Replaces the
// whole overview; the client sends the full lists on every add/edit/delete.
func updateOverview(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	root, ok := getProjectRoot(w, projectID)
	if !ok {
		return
	}
	var req OverviewRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	contacts := make([]ticket.Contact, 0, len(req.Contacts))
	for _, c := range req.Contacts {
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			continue // a contact with no name is nothing
		}
		if c.ID == "" {
			c.ID = overviewID()
		}
		if !ticket.Contains(ticket.ContactTypes, c.Type) {
			c.Type = "internal"
		}
		c.ChatPlatform = strings.ToLower(strings.TrimSpace(c.ChatPlatform))
		if c.ChatPlatform != "" && !ticket.Contains(ticket.ChatPlatforms, c.ChatPlatform) {
			c.ChatPlatform = "other"
		}
		contacts = append(contacts, c)
	}

	links := make([]ticket.Link, 0, len(req.Links))
	for _, l := range req.Links {
		l.URL = strings.TrimSpace(l.URL)
		l.Description = strings.TrimSpace(l.Description)
		if l.URL == "" && l.Description == "" {
			continue
		}
		if l.ID == "" {
			l.ID = overviewID()
		}
		links = append(links, l)
	}

	instructions := make([]ticket.Instruction, 0, len(req.Instructions))
	for _, in := range req.Instructions {
		in.Title = strings.TrimSpace(in.Title)
		if in.Title == "" && strings.TrimSpace(in.Body) == "" {
			continue
		}
		if in.ID == "" {
			in.ID = overviewID()
		}
		instructions = append(instructions, in)
	}

	cfg, err := ticket.ReadConfig(root)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.Contacts = contacts
	cfg.Links = links
	cfg.Instructions = instructions
	if err := ticket.WriteConfig(root, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, OverviewRequest{
		Contacts:     nonNilContacts(contacts),
		Links:        nonNilLinks(links),
		Instructions: nonNilInstructions(instructions),
	})
}

func nonNilContacts(c []ticket.Contact) []ticket.Contact {
	if c == nil {
		return []ticket.Contact{}
	}
	return c
}
func nonNilLinks(l []ticket.Link) []ticket.Link {
	if l == nil {
		return []ticket.Link{}
	}
	return l
}
func nonNilInstructions(i []ticket.Instruction) []ticket.Instruction {
	if i == nil {
		return []ticket.Instruction{}
	}
	return i
}
