// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"hate/internal/fsutil"
)

// Project migrations.
//
// A migration step moves a project from one format major to the next
// (1 -> 2, 2 -> 3, ...). Steps work on the raw JSON of config.json and every
// ticket file, so they never depend on today's structs. Running from an old
// format runs every step in order, so skipping releases is fine.
//
// Nothing migrates on its own: PlanMigration is a dry run that returns what
// would change, and ApplyMigration (the user's choice: a button in the UI,
// hate_migrate_project over MCP) writes every file and commits once, so a
// git revert undoes it. Until then the project is read-only (CheckWritable
// returns a NeedsMigrationError) and reads are translated in memory
// (translateOnRead), so it still looks right.

// MigrateOptions are the user's choices in the preview.
type MigrateOptions struct {
	// BlocksFromPhase: phases like "Block 03 (Nov 30-Dec 11)" become block 3
	// (1 -> 2). nil = the default: yes when the project has block_weeks set.
	BlocksFromPhase *bool `json:"blocks_from_phase,omitempty"`
}

// MigrationChange is one ticket's change.
type MigrationChange struct {
	TicketID  string `json:"ticket_id"`
	Title     string `json:"title"`
	From      string `json:"from"`      // the old phase
	Lifecycle string `json:"lifecycle"` // "" = none
	Block     int    `json:"block"`     // 0 = none
}

// MigrationPlan is the preview (and, after ApplyMigration, the result).
type MigrationPlan struct {
	Needed      bool   `json:"needed"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	// BlocksFromPhase is the choice used; DefaultBlocksFromPhase what the
	// project's settings suggest.
	BlocksFromPhase        bool              `json:"blocks_from_phase"`
	DefaultBlocksFromPhase bool              `json:"default_blocks_from_phase"`
	Tickets                int               `json:"tickets"`
	ToBlock                int               `json:"to_block"`
	ToLifecycle            int               `json:"to_lifecycle"`
	Changes                []MigrationChange `json:"changes"`
	// Applied and CommitWarning are set by ApplyMigration.
	Applied       bool   `json:"applied"`
	CommitWarning string `json:"commit_warning,omitempty"`
}

type rawObject = map[string]any

type migrationStep struct {
	from int
	run  func(cfg rawObject, tickets []rawObject, opts resolvedOpts, note func(t rawObject, detail string)) []MigrationChange
}

type resolvedOpts struct {
	blocksFromPhase bool
}

var migrationSteps = []migrationStep{
	{from: 1, run: migrate1to2},
}

// blockPhaseRe matches a format-1 block phase: "Block 01", "Block 7 (Nov 2-13)".
var blockPhaseRe = regexp.MustCompile(`(?i)^\s*block\s+0*(\d+)\b`)

// PhaseToFormat2 maps a format-1 phase to (lifecycle, block): with
// blocksFromPhase, "Block NN ..." becomes block NN; anything else is kept as
// the lifecycle text.
func PhaseToFormat2(phase string, blocksFromPhase bool) (string, int) {
	phase = strings.TrimSpace(phase)
	if blocksFromPhase {
		if m := blockPhaseRe.FindStringSubmatch(phase); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 1000 {
				return "", n
			}
		}
	}
	return phase, 0
}

// migrate1to2: phase -> lifecycle + block.
func migrate1to2(cfg rawObject, tickets []rawObject, opts resolvedOpts, note func(rawObject, string)) []MigrationChange {
	var changes []MigrationChange
	for _, t := range tickets {
		phase, _ := t["phase"].(string)
		delete(t, "phase")
		if strings.TrimSpace(phase) == "" {
			continue
		}
		lc, block := PhaseToFormat2(phase, opts.blocksFromPhase)
		if lc != "" {
			t["lifecycle"] = lc
		}
		if block > 0 {
			t["block"] = block
		}
		id, _ := t["id"].(string)
		title, _ := t["title"].(string)
		changes = append(changes, MigrationChange{TicketID: id, Title: title, From: phase, Lifecycle: lc, Block: block})
		if block > 0 {
			note(t, fmt.Sprintf("migrated to hate 2.0: phase %q -> block %d", phase, block))
		} else {
			note(t, fmt.Sprintf("migrated to hate 2.0: phase %q -> lifecycle %q", phase, lc))
		}
	}
	return changes
}

type rawProject struct {
	cfg       rawObject
	tickets   []rawObject
	paths     []string // ticket file paths, parallel to tickets
	version   string
	blockWeek bool
}

func loadRawProject(root string) (*rawProject, error) {
	data, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	p := &rawProject{}
	if err := json.Unmarshal(data, &p.cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	p.version, _ = p.cfg["schema_version"].(string)
	if bw, ok := p.cfg["block_weeks"].(float64); ok && (bw == 2 || bw == 3) {
		p.blockWeek = true
	}
	entries, _ := os.ReadDir(TicketsDir(root))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		path := filepath.Join(TicketsDir(root), n)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var t rawObject
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf("ticket file %s is not valid JSON, fix it before updating the project: %v", n, err)
		}
		p.tickets = append(p.tickets, t)
		p.paths = append(p.paths, path)
	}
	return p, nil
}

// runMigration applies every step from the project's format to FormatMajor
// on the in-memory raw project.
func runMigration(p *rawProject, opts MigrateOptions, author, now string) *MigrationPlan {
	ro := resolvedOpts{blocksFromPhase: p.blockWeek}
	if opts.BlocksFromPhase != nil {
		ro.blocksFromPhase = *opts.BlocksFromPhase
	}
	from := FormatMajorOf(p.version)
	plan := &MigrationPlan{
		Needed: from < FormatMajor, FromVersion: p.version, ToVersion: SchemaVersion,
		BlocksFromPhase: ro.blocksFromPhase, DefaultBlocksFromPhase: p.blockWeek,
		Tickets: len(p.tickets), Changes: []MigrationChange{},
	}
	if plan.FromVersion == "" {
		plan.FromVersion = "1.0.0"
	}
	if !plan.Needed {
		return plan
	}
	note := func(t rawObject, detail string) {
		act, _ := t["activity"].([]any)
		t["activity"] = append(act, map[string]any{"timestamp": now, "author": author, "action": ActionMigrated, "detail": detail})
	}
	for _, s := range migrationSteps {
		if s.from >= from && s.from < FormatMajor {
			plan.Changes = append(plan.Changes, s.run(p.cfg, p.tickets, ro, note)...)
		}
	}
	for _, c := range plan.Changes {
		if c.Block > 0 {
			plan.ToBlock++
		} else {
			plan.ToLifecycle++
		}
	}
	p.cfg["schema_version"] = SchemaVersion
	for _, t := range p.tickets {
		t["schema_version"] = SchemaVersion
	}
	return plan
}

// ActionMigrated is the activity action a migration records on a ticket.
const ActionMigrated = "migrated"

// PlanMigration is the dry run: what ApplyMigration would change. Writes
// nothing.
func PlanMigration(root string, opts MigrateOptions) (*MigrationPlan, error) {
	if err := checkNotNewer(root); err != nil {
		return nil, err
	}
	p, err := loadRawProject(root)
	if err != nil {
		return nil, err
	}
	return runMigration(p, opts, "", NowISO()), nil
}

// ApplyMigration migrates the project to FormatMajor: every ticket file and
// config.json are rewritten in hate's format and committed as one commit.
// A project already at FormatMajor is left alone (Needed false). The caller
// holds the project lock.
func ApplyMigration(root string, opts MigrateOptions, author string) (*MigrationPlan, error) {
	if err := checkNotNewer(root); err != nil {
		return nil, err
	}
	p, err := loadRawProject(root)
	if err != nil {
		return nil, err
	}
	plan := runMigration(p, opts, author, NowISO())
	if !plan.Needed {
		return plan, nil
	}
	// Encode everything first, so a bad file stops the migration before any
	// file is written.
	type out struct {
		path string
		data []byte
	}
	var outs []out
	for i, t := range p.tickets {
		raw, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		tk, err := DecodeTicket(raw)
		if err != nil {
			return nil, fmt.Errorf("ticket file %s: %v", filepath.Base(p.paths[i]), err)
		}
		data, err := EncodeTicket(tk)
		if err != nil {
			return nil, err
		}
		outs = append(outs, out{p.paths[i], data})
	}
	raw, err := json.Marshal(p.cfg)
	if err != nil {
		return nil, err
	}
	cfg, err := DecodeConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("config.json: %v", err)
	}
	cdata, err := EncodeConfig(cfg)
	if err != nil {
		return nil, err
	}
	outs = append(outs, out{ConfigPath(root), cdata})

	files := make([]string, 0, len(outs))
	for _, o := range outs {
		if err := fsutil.WriteFileAtomic(o.path, o.data, 0644); err != nil {
			return nil, fmt.Errorf("writing %s: %w (some files may be updated; git checkout -- . undoes it)", o.path, err)
		}
		files = append(files, o.path)
	}
	// index.json is derived: make sure the repo ignores it (older projects
	// may not), so the update leaves a clean working tree.
	gi := filepath.Join(root, ".gitignore")
	if err := ensureIgnoreLine(gi, "index.json"); err == nil {
		files = append(files, gi)
	}
	if err := RegenerateIndex(root); err != nil {
		return nil, err
	}
	plan.Applied = true
	plan.CommitWarning = CommitWarning(CommitFiles(root, files, fmt.Sprintf("Update project to the hate %d.0 format", FormatMajor)))
	return plan, nil
}

func checkNotNewer(root string) error {
	if v := ProjectFormat(root); FormatIsNewer(v) {
		return &NewerFormatError{Root: root, Version: v}
	}
	return nil
}

// ---- reading an unmigrated project ----

// translateOnRead maps a ticket read from an older-format project to today's
// fields in memory (nothing is written): 1 -> 2 turns phase into lifecycle +
// block with the project's default rule. projectMajor and blockWeeks come
// from the project config.
func translateOnRead(t *Ticket, projectMajor int, blockWeeks bool) {
	if projectMajor >= 2 || t.extra == nil {
		return
	}
	raw, ok := t.extra["phase"]
	if !ok {
		return
	}
	delete(t.extra, "phase")
	var phase string
	if json.Unmarshal(raw, &phase) != nil || strings.TrimSpace(phase) == "" {
		return
	}
	lc, block := PhaseToFormat2(phase, blockWeeks)
	if lc != "" {
		t.Lifecycle = StringPtr(lc)
	}
	t.Block = block
}

// projectReadContext is what translateOnRead needs from the project config.
func projectReadContext(root string) (major int, blockWeeks bool) {
	data, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		return FormatMajor, false
	}
	var v struct {
		SchemaVersion string `json:"schema_version"`
		BlockWeeks    int    `json:"block_weeks"`
	}
	_ = json.Unmarshal(data, &v)
	return FormatMajorOf(v.SchemaVersion), v.BlockWeeks == 2 || v.BlockWeeks == 3
}
