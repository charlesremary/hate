// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package githubtest is a fake GitHub REST API for tests (and the two-machine
// end-to-end check): /user, /user/emails, /user/repos (paged) and the
// contents API for .tkt/config.json. Nothing talks to the real GitHub.
package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is a repository the fake account can see.
type Repo struct {
	FullName    string
	Private     bool
	IsProject   bool // has .tkt/config.json
	Description string
}

// Server is the fake API. Fields may be changed between requests.
type Server struct {
	*httptest.Server
	mu sync.Mutex

	Token     string     // the one valid token
	Login     string     // default "pm-jane"
	Name      string     // default "Jane PM"
	Email     string     // primary verified email; default "jane@example.com"
	ID        int64      // default 42
	ExpiresAt *time.Time // sent as github-authentication-token-expiration
	NoEmails  bool       // /user/emails answers 403 (token without email access)
	Repos     []Repo

	Requests []string // "GET /user", ... in order
}

// New starts a fake GitHub accepting token.
func New(token string) *Server {
	s := &Server{Token: token, Login: "pm-jane", Name: "Jane PM", Email: "jane@example.com", ID: 42}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Handler returns the API handler (to serve it on a chosen address).
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serve) }

// NewUnstarted returns a fake whose handler the caller serves itself.
func NewUnstarted(token string) *Server {
	return &Server{Token: token, Login: "pm-jane", Name: "Jane PM", Email: "jane@example.com", ID: 42}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	tok, login, name, email, id, exp, noEmails := s.Token, s.Login, s.Name, s.Email, s.ID, s.ExpiresAt, s.NoEmails
	repos := append([]Repo(nil), s.Repos...)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+tok {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}
	if exp != nil {
		w.Header().Set("github-authentication-token-expiration", exp.UTC().Format("2006-01-02 15:04:05 MST"))
	}
	p := r.URL.Path
	switch {
	case p == "/user":
		json.NewEncoder(w).Encode(map[string]interface{}{"login": login, "id": id, "name": name, "email": nil})
	case p == "/user/emails":
		if noEmails {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
			return
		}
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"email": "old@example.com", "primary": false, "verified": true},
			{"email": email, "primary": true, "verified": true},
		})
	case p == "/user/repos":
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if per <= 0 {
			per = 30
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page <= 0 {
			page = 1
		}
		start, end := (page-1)*per, page*per
		if start > len(repos) {
			start = len(repos)
		}
		if end > len(repos) {
			end = len(repos)
		}
		if end < len(repos) {
			q := r.URL.Query()
			q.Set("page", strconv.Itoa(page+1))
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next"`, "http://"+r.Host, p, q.Encode()))
		}
		var out []map[string]interface{}
		for _, rp := range repos[start:end] {
			owner, name, _ := strings.Cut(rp.FullName, "/")
			out = append(out, map[string]interface{}{
				"full_name": rp.FullName, "name": name, "private": rp.Private, "description": rp.Description,
				"updated_at": "2026-09-30T10:00:00Z", "owner": map[string]string{"login": owner},
			})
		}
		if out == nil {
			out = []map[string]interface{}{}
		}
		json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(p, "/repos/") && strings.HasSuffix(p, "/contents/.tkt/config.json"):
		full := strings.TrimSuffix(strings.TrimPrefix(p, "/repos/"), "/contents/.tkt/config.json")
		for _, rp := range repos {
			if rp.FullName == full && rp.IsProject {
				fmt.Fprint(w, `{"type":"file","name":"config.json","path":".tkt/config.json"}`)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
}

// SetToken changes the valid token.
func (s *Server) SetToken(t string) {
	s.mu.Lock()
	s.Token = t
	s.mu.Unlock()
}

// SetExpiry changes the reported expiry.
func (s *Server) SetExpiry(t *time.Time) {
	s.mu.Lock()
	s.ExpiresAt = t
	s.mu.Unlock()
}
