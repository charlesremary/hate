// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"fmt"
	"os/exec"
	"strings"
)

// Who this machine commits and authors as (HATE-5t80).
//
// The identity is per machine only: it lives in the repo-local git config
// (.git/config, never committed), not in the shared .tkt/config.json. An older
// hate kept a git_identity in the shared config; that value is now only a
// fill-in for a machine with no identity at all, and EncodeConfig drops it so
// the next config write removes it from the shared file.
//
// Resolution order (EffectiveIdentity):
//  1. the signed-in Git account, when this repo syncs through it;
//  2. the repo-local git user (git config --local);
//  3. the global git user;
//  4. the legacy shared git_identity, only when 1-3 are all empty.

// Identity is a git user. Source says where it came from: "account",
// "local", "global", "shared" (the legacy shared config value) or "".
type Identity struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Source string `json:"source,omitempty"`
}

// IsZero reports whether neither name nor email is set.
func (i Identity) IsZero() bool { return i.Name == "" && i.Email == "" }

// String is "Name <email>" (or whichever half is set).
func (i Identity) String() string {
	switch {
	case i.Name != "" && i.Email != "":
		return i.Name + " <" + i.Email + ">"
	case i.Email != "":
		return "<" + i.Email + ">"
	}
	return i.Name
}

// Author is the form ticket author fields use: the email, else the name.
func (i Identity) Author() string {
	if i.Email != "" {
		return i.Email
	}
	return i.Name
}

// sameIdentity compares two identities by email (case-insensitive), or by
// name when either has no email.
func sameIdentity(a, b Identity) bool {
	if a.Email != "" && b.Email != "" {
		return strings.EqualFold(a.Email, b.Email)
	}
	return a.Name == b.Name
}

// confirmedKey is the repo-local git config key recording the email the user
// chose on this machine (Team & Identity > You on this project, or "keep" on a mismatch). A
// local identity that differs from the machine's own but was chosen here on
// purpose (a work email for one repo) is not a mismatch.
const confirmedKey = "hate.identity"

func gitConfigGet(repoRoot string, args ...string) string {
	cmd := exec.Command("git", append([]string{"config"}, args...)...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func scopedIdentity(repoRoot, scope string) Identity {
	return Identity{
		Name:   gitConfigGet(repoRoot, scope, "user.name"),
		Email:  gitConfigGet(repoRoot, scope, "user.email"),
		Source: strings.TrimPrefix(scope, "--"),
	}
}

// LocalIdentity is the repo-local git user (git config --local).
func LocalIdentity(repoRoot string) Identity { return scopedIdentity(repoRoot, "--local") }

// GlobalIdentity is this computer's global git user (git config --global).
func GlobalIdentity(repoRoot string) Identity { return scopedIdentity(repoRoot, "--global") }

// LegacySharedIdentity is the git_identity an older hate stored in the shared
// config, or a zero Identity.
func LegacySharedIdentity(cfg *ProjectConfig) Identity {
	if cfg == nil || cfg.LegacyGitIdentity == nil {
		return Identity{}
	}
	return Identity{Name: cfg.LegacyGitIdentity.Name, Email: cfg.LegacyGitIdentity.Email, Source: "shared"}
}

// accountIdentity is the signed-in Git account as an Identity, when it
// applies to this repo.
func accountIdentity(repoRoot string) (Identity, bool) {
	name, email, ok := AccountIdentity(repoRoot)
	if !ok {
		return Identity{}, false
	}
	return Identity{Name: name, Email: email, Source: "account"}, true
}

// MachineIdentity is who the person at this computer is, independent of the
// repo-local setting: the signed-in Git account for this repo, else the
// global git user. Zero when neither is known.
func MachineIdentity(repoRoot string) Identity {
	if id, ok := accountIdentity(repoRoot); ok {
		return id
	}
	if g := GlobalIdentity(repoRoot); !g.IsZero() {
		return g
	}
	return Identity{}
}

// EffectiveIdentity is who hate commits and authors as on this machine (see
// the resolution order above). cfg may be nil.
func EffectiveIdentity(repoRoot string, cfg *ProjectConfig) Identity {
	if id, ok := accountIdentity(repoRoot); ok {
		return id
	}
	if l := LocalIdentity(repoRoot); !l.IsZero() {
		return l
	}
	if g := GlobalIdentity(repoRoot); !g.IsZero() {
		return g
	}
	return LegacySharedIdentity(cfg)
}

// IdentityCheck is the Working-as state: who hate works as, who this
// computer's user is, and whether they disagree.
type IdentityCheck struct {
	Working Identity `json:"working"`
	Machine Identity `json:"machine"`
	// Mismatch: the repo-local identity is someone other than this machine's
	// own (the global git user), and it wasn't chosen on this machine. An
	// older hate copied a teammate's identity from the shared config into the
	// repo-local git config this way.
	Mismatch bool `json:"mismatch"`
	// FromShared: no account or global user to compare with, and the
	// repo-local identity equals the legacy shared one and wasn't chosen on
	// this machine, so it may have been copied from a teammate.
	FromShared bool   `json:"from_shared"`
	Message    string `json:"message,omitempty"`
}

// CheckIdentity compares the identity hate works as with this machine's own.
// cfg may be nil.
func CheckIdentity(repoRoot string, cfg *ProjectConfig) IdentityCheck {
	c := IdentityCheck{Working: EffectiveIdentity(repoRoot, cfg), Machine: MachineIdentity(repoRoot)}
	if c.Working.Source != "local" {
		// The account always wins (EnsureProjectIdentity applies it), and a
		// global or fill-in identity is the machine's own by definition.
		return c
	}
	confirmed := gitConfigGet(repoRoot, "--local", confirmedKey)
	if confirmed != "" && strings.EqualFold(confirmed, c.Working.Email) {
		return c
	}
	if !c.Machine.IsZero() {
		if !sameIdentity(c.Working, c.Machine) {
			c.Mismatch = true
			c.Message = fmt.Sprintf("hate is set to work as %s, but you are %s on this computer.", c.Working, c.Machine)
		}
		return c
	}
	if shared := LegacySharedIdentity(cfg); !shared.IsZero() && sameIdentity(c.Working, shared) {
		c.FromShared = true
		c.Message = fmt.Sprintf("hate is working as %s, copied from the shared project settings by an older hate. If that isn't you, set your own identity.", c.Working)
	}
	return c
}

// SetMachineIdentity is Team & Identity > You on this project: it sets the repo-local git
// user and records it as chosen on this machine. It never touches the shared
// .tkt/config.json.
func SetMachineIdentity(repoRoot, name, email string) error {
	if err := SetGitIdentity(repoRoot, name, email); err != nil {
		return err
	}
	return ConfirmLocalIdentity(repoRoot, email)
}

// ConfirmLocalIdentity records email as the identity the user chose for this
// repo on this machine, so CheckIdentity stops reporting a mismatch for it.
func ConfirmLocalIdentity(repoRoot, email string) error {
	cmd := exec.Command("git", "config", "--local", confirmedKey, email)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to record the identity: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
