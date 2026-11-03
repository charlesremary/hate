// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"hate/internal/config"
	"hate/internal/ticket"
)

// NewProject is the input to CreateProject.
type NewProject struct {
	// FolderName is the folder under the projects root: one path segment.
	// Empty means "tkt-" + the lower-cased project id.
	FolderName  string `json:"folder_name"`
	Client      string `json:"client"`
	ProjectName string `json:"project_name"`
	ProjectID   string `json:"project_id"`
	// Prefix is the ticket id prefix (PREFIX-xxxx). Empty means the
	// upper-cased project id.
	Prefix string `json:"prefix"`
}

// CreatedProject is a project CreateProject made.
type CreatedProject struct {
	ProjectSummary
	// Git is true when the folder was made a git repo with an initial commit.
	Git bool `json:"git"`
	// CommitWarning explains why the initial commit didn't happen ("" = it did,
	// or git isn't installed).
	CommitWarning string `json:"commit_warning,omitempty"`
}

var (
	projectIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	prefixRe    = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,7}$`)
	folderRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$`)
	createMu    sync.Mutex // serializes the uniqueness checks and mkdir
)

func invalid(format string, a ...any) error {
	return &Error{Kind: ErrInvalidInput, Msg: fmt.Sprintf(format, a...)}
}

func conflict(format string, a ...any) error {
	return &Error{Kind: ErrConflict, Msg: fmt.Sprintf(format, a...)}
}

// normalize trims and defaults p, then checks every rule that doesn't need
// the disk.
func (p *NewProject) normalize() error {
	p.FolderName = strings.TrimSpace(p.FolderName)
	p.Client = strings.TrimSpace(p.Client)
	p.ProjectName = strings.TrimSpace(p.ProjectName)
	p.ProjectID = strings.TrimSpace(p.ProjectID)
	p.Prefix = strings.ToUpper(strings.TrimSpace(p.Prefix))

	if p.ProjectName == "" {
		return invalid("Project name is required.")
	}
	if !projectIDRe.MatchString(p.ProjectID) {
		return invalid("Project ID %q is not valid: use 1 to 32 letters, digits, - or _, starting with a letter or digit.", p.ProjectID)
	}
	if p.Prefix == "" {
		p.Prefix = strings.ToUpper(p.ProjectID)
	}
	if !prefixRe.MatchString(p.Prefix) {
		return invalid("Ticket prefix %q is not valid: use 1 to 8 letters or digits, starting with a letter (e.g. HATE).", p.Prefix)
	}
	if p.FolderName == "" {
		p.FolderName = "tkt-" + strings.ToLower(p.ProjectID)
	}
	if !folderRe.MatchString(p.FolderName) || strings.Contains(p.FolderName, "..") {
		return invalid("Folder name %q is not valid: one folder name (no slashes or ..), using letters, digits, '.', '_', '-' or spaces.", p.FolderName)
	}
	return nil
}

// CreateProject makes a new project folder under the projects root: .tkt/
// config.json, tickets/, an empty index, and (when git is installed) a git
// repo with an initial commit of the config. The project id and ticket prefix
// must not be used by another project on this machine, hidden ones included.
func CreateProject(p NewProject) (*CreatedProject, error) {
	if err := p.normalize(); err != nil {
		return nil, err
	}
	root := config.GetProjectsRoot()
	if root == "" {
		return nil, &Error{Kind: ErrInternal, Msg: "No projects folder is set: choose one in Settings."}
	}

	createMu.Lock()
	defer createMu.Unlock()

	existing := append(config.ListProjects(), config.ListHiddenProjects()...)
	for _, e := range existing {
		if strings.EqualFold(e.ID, p.ProjectID) {
			return nil, conflict("A project with ID %s already exists (%s).", e.ID, e.Path)
		}
		if strings.EqualFold(e.Prefix, p.Prefix) {
			return nil, conflict("Ticket prefix %s is already used by project %s (%s).", e.Prefix, e.ID, e.Path)
		}
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, wrap(ErrInternal, err)
	}
	dir := filepath.Join(root, p.FolderName)
	// Mkdir (not MkdirAll) fails if the folder exists, so an existing folder
	// is never written into.
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, conflict("Folder already exists: %s", dir)
		}
		return nil, wrap(ErrInternal, err)
	}
	fail := func(err error) (*CreatedProject, error) {
		_ = os.RemoveAll(dir) // only ever the folder created just above
		return nil, wrap(ErrInternal, err)
	}
	for _, d := range []string{".tkt", "tickets"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return fail(err)
		}
	}
	if err := ticket.WriteConfig(dir, ticket.DefaultConfig(p.Client, p.ProjectName, p.ProjectID, p.Prefix)); err != nil {
		return fail(err)
	}
	if err := ticket.RegenerateIndex(dir); err != nil {
		return fail(err)
	}

	out := &CreatedProject{ProjectSummary: ProjectSummary{
		ID: p.ProjectID, Name: p.ProjectName, Client: p.Client, Prefix: p.Prefix, Path: dir,
	}}
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			out.CommitWarning = "git init failed: " + strings.TrimSpace(string(b))
		} else {
			out.Git = true
			out.CommitWarning = ticket.CommitWarning(ticket.CommitFiles(dir,
				[]string{filepath.Join(".tkt", "config.json")}, "Create project "+p.ProjectName))
		}
	}
	return out, nil
}
