# HATE (Human Agent Tracking Engine)

A lightweight ticketing and project-management tool. Each project is a folder of
plain JSON files kept under version control, so the whole team works from the
same source of truth by pushing and pulling.

## Background — why hate exists

hate is the working implementation of an argument made in a series on project
management at [charlesemary.com](https://charlesemary.com). The series isn't about
this tool — it's about why conventional project management is broken, and what to do
instead. hate is the "do instead."

**The diagnosis:**

- **Two systems that never sync.** PMs live in a scheduling tool (MS Project,
  Smartsheet); the people doing the work live in a ticketing system. Both claim to
  describe the same project, yet diverge immediately and neither is authoritative —
  so the PM burns time reconciling them.
- **The PM as nag, not coordinator.** Most tooling optimizes for chasing updates and
  compiling status by hand, instead of the genuinely valuable human work: unblocking
  dependencies and coordinating people.
- **Silent slip.** Dates move with no required reason and no audit trail — a
  year-long project ends up six months late with no record of how. "Death by a
  thousand cuts, with no record of the cuts."
- **Proprietary lock-in.** Ticket data sits in a SaaS database: not diffable, not
  version-controlled, not natively readable by the LLMs now doing much of the work.

**The inversion hate is built around:**

- **Tickets are the source of truth.** Project status is *computed* from the work
  items, not maintained by hand in a parallel schedule. Status emerges from data, not
  meetings.
- **One system, git-native.** Each project is a folder of plain JSON tickets under
  Git — history, diffing, branching, and an immutable audit trail come for free; JSON
  keeps everything transparent and LLM-readable. No proprietary database.
- **Accountability on the work.** The people doing the work keep their tickets
  current; the system surfaces slip automatically as signal; the PM's job narrows to
  coordination and unblocking.
- **Low cognitive overhead.** You *Promote* a ticket instead of picking a state from
  a dropdown, and record only predecessors — successors are derived.
- **LLM-native by design.** Because tickets are git-tracked JSON, an AI agent can
  create tickets for a plan and promote them as it executes — closing the gap where
  an LLM changes 200 files and leaves no record of what it did or why.

**The full argument:**

1. [Rethinking Project Management](https://charlesemary.com/pm-agent-rethinking-project-management/)
2. [Reimagining Project Management, Part 2](https://charlesemary.com/reimagining-project-management-part-2/)
3. [Closing the Loop on AI-Driven Development](https://charlesemary.com/closing-the-loop-on-ai-driven-development/)
4. [Building and Using a Git-Based Ticket System](https://charlesemary.com/building-and-using-a-git-based-ticket-system/)

## On-disk layout

A project is an ordinary Git repository. HATE owns a few paths inside it:

```
<project-repo>/
├── .tkt/
│   └── config.json          # project config: client, prefix, resources, repos…
├── tickets/
│   ├── AMPL-7k3x.json       # one file per ticket (the source of truth)
│   └── AMPL-9f2a.json
├── index.json               # generated summary of all tickets (rebuilt, not edited)
└── attachments/
    └── <ticket-id>/
        └── <attachment-id>-<filename>
```

- **`tickets/<id>.json`** is the canonical record for a ticket. Everything else is
  derived from it.
- **`index.json`** is a regenerated rollup (id, type, status, title, assignee,
  dates, etc.) used for fast listing. Never hand-edit it — it is rebuilt from the
  ticket files by `RegenerateIndex`.
- **`.tkt/config.json`** holds the project config (`ProjectConfig`): client,
  project name/id, ticket-ID `prefix`, team `resources`, linked `repos`,
  `effort_to_days` mapping, optional project-local git identity, and `closed_at`.

Ticket IDs are `<PREFIX>-<4 base36 chars>`, e.g. `AMPL-7k3x`. The prefix comes
from the project config (default `TKT`); the suffix is random and collision-checked
against existing files.

## Ticket structure

Every ticket is a flat JSON object (`Ticket` in `internal/ticket/schema.go`). Key
fields:

| Field | Notes |
| --- | --- |
| `schema_version` | Current schema is `1.0.0`. |
| `id` | `<prefix>-<base36>`, e.g. `AMPL-7k3x`. |
| `type` | One of `task`, `dev_task`, `design_task`, `meeting`, `administration`. |
| `status` | Workflow state (see below). |
| `title`, `description` | Free text; description renders as Markdown in the UI. |
| `priority` | `critical`, `high`, `medium`, `low` (default `medium`). |
| `effort` | T-shirt size `xs`/`s`/`m`/`l`/`xl`, or null. Maps to estimated days via config. |
| `tags` | List of free-text labels. |
| `phase` | Optional grouping/phase label. |
| `assignee` | Person responsible, or null (unassigned). |
| `creator` | Who created the ticket. |
| `predecessors` | List of ticket IDs that must precede this one (dependency links). |
| `repo` | Optional linked code repo. |
| `created_at` / `updated_at` / `closed_at` | ISO-8601 UTC timestamps. |
| `planned_start_date` / `actual_start_date` / `due_date` | `YYYY-MM-DD` dates. |
| `time_entries` | Logged time (`id`, `date`, `hours`, `description`, `author`, `logged_at`). Hours round to the nearest 0.25. |
| `activity` | Append-only audit log (`timestamp`, `author`, `action`, `detail`). |
| `attachments` | Files committed under `attachments/<ticket-id>/`. |
| `cancellation_reason` | Set only when a ticket is force-closed. |

The only type-specific field in active use is:

- **Meeting:** `meeting_attendees`.

> **Legacy:** the struct still carries `defect_*` (`defect_severity`,
> `defect_repro_steps`, `defect_expected_behavior`, `defect_actual_behavior`) and
> `feature_acceptance_criteria` fields from an earlier design where `defect` and
> `feature` were ticket types. Those types are **no longer in the supported set**
> (`task`, `dev_task`, `design_task`, `meeting`, `administration`), so these fields
> are vestigial and slated for removal — don't build on them.

## Status workflow

The full status set is global:

```
not_started → in_progress → dev_complete → qa_testing →
submitted_for_review → approved → complete → closed
(plus rework and blocked)
```

What differs per ticket **type** is the *path* through those statuses. Each type
defines `promote` / `demote` transitions (`internal/ticket/config.go`):

- **`task`** — short path: `not_started → in_progress → complete → closed`.
- **`dev_task`** — full dev + QA cycle with a rework loop:
  `not_started → in_progress → dev_complete → qa_testing → complete → closed`,
  where demoting from `qa_testing` goes to `rework`, and promoting `rework` returns
  to `qa_testing`.
- **`design_task`** — review/approval cycle:
  `not_started → in_progress → submitted_for_review → approved → closed`.
- **`meeting` / `administration`** — no workflow; these **auto-complete on
  creation** (and can capture logged hours at creation time).

Other rules:

- **`blocked`** is reachable from any state via a direct status change; promoting or
  demoting a blocked ticket resolves to `_previous` — the status it held before
  (recovered from the activity log).
- Reaching a **closed status** (`complete`, `closed`) stamps `closed_at`; leaving it
  clears it. `closed` is **terminal** — you cannot promote out of it.
- Moving into `in_progress` stamps `actual_start_date` if not already set.
- **Force-close** (`ForceClose`) jumps a ticket straight to `closed`, skipping the
  workflow, for dropped/duplicate/out-of-scope work. It requires a reason
  (≥5 chars), which is stored in `cancellation_reason`.

## Operations

All mutations go through `internal/ticket` and are recorded in the ticket's
`activity` log:

- **Create** — `CreateTicket` allocates an ID, fills defaults
  (`status: not_started`, `priority: medium`), validates, and writes the file.
- **Promote / Demote** — advance or step back along the type's workflow.
- **Change status** — direct status set for transitions the workflow has no path
  into (e.g. `blocked`).
- **Assign**, **Add comment**, **Edit field** (title, description, priority, effort,
  tags, phase, assignee, dates, type-specific fields).
- **Predecessors** — add/remove dependency links (validated to exist).
- **Time** — add/delete time entries (hours rounded to 0.25).
- **Attachments** — files stored under `attachments/<ticket-id>/` and committed
  alongside the ticket JSON.

Every write runs `ValidateTicket` before hitting disk, and `index.json` is
regenerated from the ticket files so listings stay in sync.

## Resource balancing (work in progress)

> ⚠️ **Work in progress.** Resource balancing is an early, evolving feature. It may
> be incomplete, change without notice, and **may not behave as intended** in all
> cases. Always review a proposed schedule before applying it, and treat the
> generated dates as a suggestion rather than a guarantee. The algorithm itself is
> read-only; nothing is written until you explicitly apply the report.

Balancing tries to produce a feasible schedule across the team by simulating
workdays forward (`internal/pm/balance.go`), exposed at
`POST /api/projects/{projectId}/balance`.

**How it currently works:**

- Tickets that are **terminal** (`complete`, `closed`) are never rescheduled. Their
  existing `due_date` (or `closed_at`) is used only so downstream work doesn't start
  before them.
- A schedulable ticket must have **both an assignee and an effort size**. Anything
  missing either is **skipped** with a reason and left untouched.
- Effort size is converted to days via the project's `effort_to_days` map, then to
  hours at a fixed **8 hours per person-day** (`HoursPerDay`).
- Predecessor links are checked for **cycles** (Kahn topological sort); if a cycle
  is found, balancing aborts and reports the involved ticket IDs instead of
  scheduling.
- The simulator steps day-by-day over **weekdays only** (weekends are skipped). On
  each day, every ticket whose predecessors are satisfied is "ready"; a person's
  ready tickets **equally split that person's daily capacity**
  (`daily_hours_available`, default 8). Within a person's queue, work is ordered by
  **priority, then larger effort, then ticket ID**.
- A ticket's `planned_start_date` is stamped the first day work touches it, and its
  `due_date` the day its hours hit zero. The run is capped at ~5 years of weekdays
  as a runaway guard.

**Output and applying:**

- `BalanceProject` returns a **read-only report** of proposed `start`/`due` changes
  (sorted by largest forward shift), plus skipped tickets, original vs. proposed end
  date, and any detected cycle. Nothing is written.
- `ApplyBalance` (only when the caller opts in) writes the new dates back into each
  ticket file, records a `balanced` activity entry, and returns the touched paths so
  the API can stage them in a single Git commit.

**Known limitations / caveats (why it's WIP):**

- Equal-split capacity is a simplification — it doesn't model partial-day focus,
  context switching, or single-task-at-a-time working.
- Tickets with no assignee or no effort size silently drop out of the schedule (they
  appear under "skipped", not in the plan).
- Capacity assumes a flat daily figure; holidays, PTO, and per-day variation aren't
  modeled.
- Orphaned predecessor references (pointing outside the schedulable set and not
  terminal) are treated as already satisfied.

## Collaboration via Git

There is no server-side database — the Git repo *is* the shared state
(`internal/ticket/git.go`):

- HATE can commit changed ticket files, **push**, and **sync** (`pull --rebase`
  then `push`).
- On a rebase conflict, the sync **aborts the rebase** and leaves the repo
  untouched, reporting that conflicting changes need manual resolution.
- It tracks branch, uncommitted files, and ahead/behind counts so the UI can show
  sync status. A project can also pin a repo-local git identity in its config.

## License

HATE is licensed under the Functional Source License, Version 1.1, Apache 2.0 Future License (FSL-1.1-Apache-2.0).

You can freely use, modify, and redistribute HATE for any purpose except offering a competing commercial product. Two years after each release, that release automatically converts to Apache 2.0.

See [LICENSE.md](LICENSE.md) for the full license text.
</content>
</invoke>
