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
  hour budget pools, estimate inputs, the legacy `effort_to_days` mapping,
  optional project-local git identity, and `closed_at`.

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
| `estimate_hours` | Hours estimate for a wrap ticket (`config` / `nonfunc` tag), or null. ≥ 0.25 in quarter-hour steps. Code (`functional`) tickets have none; they're sized by the parent's `cfp:`. |
| `effort` | **Legacy.** Old t-shirt size `xs`..`xl`, read-only. Setting it is rejected (400); clearing it is allowed. Used only to convert old tickets that have no estimate. |
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
- **Assign**, **Add comment**, **Edit field** (title, description, priority,
  estimate_hours, tags, phase, assignee, dates, type-specific fields).
- **Predecessors** — add/remove dependency links (validated to exist).
- **Time** — add/delete time entries (hours rounded to 0.25).
- **Attachments** — files stored under `attachments/<ticket-id>/` and committed
  alongside the ticket JSON.

Every write runs `ValidateTicket` before hitting disk, and `index.json` is
regenerated from the ticket files so listings stay in sync.

## Capacity and load

Nothing writes ticket dates automatically. Capacity is answered by two read-only
views (`internal/pm/projschedule.go`, `internal/pm/load.go`), recomputed on
every dashboard view:

- **Capacity-aware projected Gantt.** Before a baseline, the PM dashboard's Gantt
  (and `GET /api/projects/{projectId}/gantt.drawio`) projects the open tickets
  forward from today in hours. Each person is a lane that works their ready
  tickets one at a time — priority (critical > high > medium > low), then
  dependency work order, then ID — burning their `daily_hours_available`
  (default 8). Several small tickets can share a day; there is no whole-day
  minimum. Lanes run in parallel; predecessors are finish-to-start across lanes,
  and an explicit `planned_start_date` is a floor. Business days only.
- **Only people consume capacity.** Unassigned work defaults to the project's
  single resource when there is exactly one; otherwise it gets its own
  `unassigned` lane at 8 h/day. A project with no resources gets one lane per
  assignee. Assignees match resources by email, git user, or name.
- **Unsized tickets** (no estimate) get a 0.25 h placeholder so they still show,
  and are counted in the Gantt header. Done, cancelled, and backlog tickets are
  not scheduled; feature parents consume no capacity and finish with their
  children.
- **Load table** (PM dashboard, both pre- and post-baseline): per lane, remaining
  estimated hours, h/day, days of work (hours ÷ h/day), and the free-from date
  (the lane's last scheduled end). Remaining = tickets still in build (not yet
  dev_complete; QA time burns the separate QA pool), each at its estimate minus
  hours already logged. Tickets with only an old effort size count as
  unestimated (days × 8 is far above real hours).
- **Requested dates.** Optional `requested_start` / `requested_end`
  (`.tkt/config.json`, set in Settings or via
  `PUT /api/projects/{projectId}/requested-dates`, committed to git). The Load
  table adds working days to the requested end and an "over by N days" flag per
  person, plus a project-level line. The old `target_date` (v1.0.6) is still read
  as the requested end; the `/target-date` routes remain as aliases for it.
- **Schedule vs request** card (top of the PM dashboard, with a requested end
  set): actual start (first move to in_progress or first logged time), projected
  finish likely (the capacity schedule from the later of today and the requested
  start) and P85 (the same schedule with code tickets at the reference rate ×
  Monte Carlo code P85 / P50), variances in business days (plus = late), needs vs
  has (remaining hours ÷ working days left vs the team's total daily hours, and
  the hours to cut or spare), and a status: ON TRACK (P85 on time), AT RISK
  (likely on time, P85 late), LATE (likely late). Also `GET …/forecast` (JSON).
- **Forecast history.** At most one entry per day, recorded when the forecast
  changes, in `.tkt/pm/forecast_history.json` (committed), drawn as a small trend
  of the likely and P85 finish against the requested end once there are 2+
  entries.

Known simplifications: a flat daily capacity (no holidays, PTO, or per-day
variation), remaining = estimate − logged (a ticket past its estimate but still
open counts as unestimated), and a greedy one-ticket-at-a-time order rather than an optimiser.

## Estimating

Code is sized in COSMIC function points (`cfp:N` on the feature's parent ticket)
and estimated as a range: CFP × h/CFP rates from reference features you pick
(past projects, all projects, or this project's own finished features), run as a
Monte Carlo on the COSMIC tab (P50 / P85 / P95). With nothing to borrow from, a
**manual baseline** (a typed Low / Likely / High h/CFP range, with Agentic and
Traditional presets) stands in as 5 features of evidence and hands over to the
project's own features as they finish; new projects start on it (Agentic) by
default. Platform work (`config` /
`nonfunc` tickets) is estimated in hours on each ticket and added on top. The
estimate is display-only; the project's max-hours cap is set by hand.

How to tag tickets, count CFP, and use the estimate (including the manual
baseline and the calibration slice for a new kind of project) is in the
[agent guide](docs/ticketing-and-cfp-guide.md). The design and its decisions are
in [docs/plan-estimation-rework.md](docs/plan-estimation-rework.md).

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
