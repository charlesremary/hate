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
	TicketID string `json:"ticket_id"`
	Title    string `json:"title"`
	// Kind: "phase" (1 -> 2: From is the old phase, Lifecycle / Block the
	// result), "blocked" (2 -> 3: a blocked status becomes the flag),
	// "close_category" (2 -> 3: a force-close gets its category).
	Kind      string `json:"kind"`
	From      string `json:"from"`
	Lifecycle string `json:"lifecycle"` // "" = none
	Block     int    `json:"block"`     // 0 = none
	// To describes the result for people ("in_progress, flagged blocked",
	// "Done").
	To string `json:"to"`
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
	Unblocked              int               `json:"blocked_to_flag"`
	ClosedDone             int               `json:"force_closed_done"`
	ClosedDescoped         int               `json:"force_closed_descoped"`
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
	{from: 2, run: migrate2to3},
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
		to := fmt.Sprintf("lifecycle %q", lc)
		if block > 0 {
			to = fmt.Sprintf("block %d", block)
		}
		changes = append(changes, MigrationChange{TicketID: id, Title: title, Kind: "phase", From: phase, Lifecycle: lc, Block: block, To: to})
		if block > 0 {
			note(t, fmt.Sprintf("migrated: phase %q -> block %d", phase, block))
		} else {
			note(t, fmt.Sprintf("migrated: phase %q -> lifecycle %q", phase, lc))
		}
	}
	return changes
}

// statusBeforeBlocked finds the status a format-2 blocked ticket had before
// it was blocked, from its activity log ("in_progress -> blocked: ...").
func statusBeforeBlocked(activity []any) (status, since string) {
	for i := len(activity) - 1; i >= 0; i-- {
		a, _ := activity[i].(map[string]any)
		if a == nil || a["action"] != "status_changed" {
			continue
		}
		d, _ := a["detail"].(string)
		before, after, ok := strings.Cut(d, " -> ")
		if !ok {
			continue
		}
		after, _, _ = strings.Cut(after, ":")
		if strings.TrimSpace(after) == "blocked" && strings.TrimSpace(before) != "blocked" {
			ts, _ := a["timestamp"].(string)
			return strings.TrimSpace(before), ts
		}
	}
	return "", ""
}

// closeCategoryFor guesses a force-close's category from its reason: "Done"
// first means done (finished outside the workflow), anything else descoped.
func closeCategoryFor(reason string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(reason)), "done") {
		return "done"
	}
	return "descoped"
}

// migrate2to3: blocked status -> blocked flag; force-closes get a category.
func migrate2to3(cfg rawObject, tickets []rawObject, opts resolvedOpts, note func(rawObject, string)) []MigrationChange {
	var changes []MigrationChange
	for _, t := range tickets {
		id, _ := t["id"].(string)
		title, _ := t["title"].(string)
		reason, _ := t["block_reason"].(string)
		delete(t, "block_reason")
		if t["status"] == "blocked" {
			act, _ := t["activity"].([]any)
			prev, since := statusBeforeBlocked(act)
			if prev == "" || !Contains(Statuses, prev) {
				prev = "in_progress"
			}
			if since == "" {
				since, _ = t["updated_at"].(string)
			}
			if strings.TrimSpace(reason) == "" {
				reason = "(no reason recorded)"
			}
			t["status"] = prev
			t["blocked"] = map[string]any{"reason": reason, "since": since}
			changes = append(changes, MigrationChange{TicketID: id, Title: title, Kind: "blocked", From: "blocked", To: prev + ", flagged blocked"})
			note(t, fmt.Sprintf("migrated to hate 3.0: status blocked -> %s with the blocked flag", prev))
		}
		cr, _ := t["cancellation_reason"].(string)
		if t["status"] == "closed" && strings.TrimSpace(cr) != "" {
			if c, _ := t["close_category"].(string); c == "" {
				cat := closeCategoryFor(cr)
				t["close_category"] = cat
				changes = append(changes, MigrationChange{TicketID: id, Title: title, Kind: "close_category", From: "force-closed: " + cr, To: CloseCategoryLabels[cat]})
				note(t, "migrated to hate 3.0: force-close category "+CloseCategoryLabels[cat])
			}
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
		switch {
		case c.Kind == "blocked":
			plan.Unblocked++
		case c.Kind == "close_category" && c.To == CloseCategoryLabels["done"]:
			plan.ClosedDone++
		case c.Kind == "close_category":
			plan.ClosedDescoped++
		case c.Block > 0:
			plan.ToBlock++
		default:
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
// block with the project's default rule; 2 -> 3 turns a blocked status into
// the flag and gives force-closes a category. projectMajor and blockWeeks come
// from the project config.
func translateOnRead(t *Ticket, projectMajor int, blockWeeks bool) {
	if projectMajor >= FormatMajor {
		return
	}
	if projectMajor < 2 && t.extra != nil {
		if raw, ok := t.extra["phase"]; ok {
			delete(t.extra, "phase")
			var phase string
			if json.Unmarshal(raw, &phase) == nil && strings.TrimSpace(phase) != "" {
				lc, block := PhaseToFormat2(phase, blockWeeks)
				if lc != "" {
					t.Lifecycle = StringPtr(lc)
				}
				t.Block = block
			}
		}
	}
	if projectMajor < 3 {
		var reason string
		if t.extra != nil {
			if raw, ok := t.extra["block_reason"]; ok {
				_ = json.Unmarshal(raw, &reason)
				delete(t.extra, "block_reason")
			}
		}
		if t.Status == "blocked" {
			act := make([]any, len(t.Activity))
			for i, a := range t.Activity {
				act[i] = map[string]any{"action": a.Action, "detail": a.Detail, "timestamp": a.Timestamp}
			}
			prev, since := statusBeforeBlocked(act)
			if prev == "" || !Contains(Statuses, prev) {
				prev = "in_progress"
			}
			if strings.TrimSpace(reason) == "" {
				reason = "(no reason recorded)"
			}
			t.Status = prev
			t.Blocked = &BlockedFlag{Reason: reason, Since: since}
		}
		if t.Status == "closed" && t.CancellationReason != nil && *t.CancellationReason != "" && t.CloseCategory == "" {
			t.CloseCategory = closeCategoryFor(*t.CancellationReason)
		}
	}
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
