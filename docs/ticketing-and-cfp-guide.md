# Agent guide: auto-building tickets & CFP in HATE

Instructions for Claude (or any agent) creating tickets for a project in HATE.
Follow these conventions exactly. The estimate (h/CFP for code, hours for wrap,
cost-per-deliverable) only works if the tags and estimates are applied
consistently.

For the data model, statuses, and on-disk layout, see [`../README.md`](../README.md).
This file is about **how to structure, tag, and estimate** the tickets you create.
The design behind the estimation model is in
[`plan-estimation-rework.md`](./plan-estimation-rework.md).

---

## 1. Creating a ticket

`POST /api/projects/{projectId}/tickets` with a JSON body. A wrap ticket (config
or nonfunc) carries an hours estimate:

```json
{
  "type": "task",
  "title": "Provision the Bedrock knowledge base",
  "description": "Markdown is supported and rendered.",
  "priority": "medium",
  "estimate_hours": 2,
  "assignee": "dev@example.com",
  "tags": ["parent:AMPL-7k3x", "config"],
  "phase": "02 - Build",
  "predecessors": ["AMPL-2b8c"],
  "planned_start_date": "2026-07-01",
  "due_date": "2026-07-02",
  "creator": "claude@example.com"
}
```

A code ticket (functional) carries **no** hours estimate. It is sized by its
parent's `cfp:`:

```json
{
  "type": "dev_task",
  "title": "Wire Bedrock KB retrieval into the chat handler",
  "description": "…",
  "tags": ["parent:AMPL-7k3x", "functional"],
  "phase": "02 - Build",
  "creator": "claude@example.com"
}
```

- `type`: one of `task`, `dev_task`, `design_task`, `meeting`, `administration`.
- `priority`: `critical` | `high` | `medium` | `low` (default `medium`).
- `estimate_hours`: hours estimate for a **wrap** ticket (`config` / `nonfunc`).
  Any value ≥ 0.25 in quarter-hour steps; the UI picker offers 0.25, 0.5, 1, 2,
  4, 8. Leave it out on code tickets, parents, meetings, and administration.
- `effort` is **retired**. Sending a non-empty `effort` on create or edit returns
  400: *"effort sizing is retired; use estimate_hours on config/nonfunc tickets
  (code tickets are sized by the parent's cfp:)"*. Old tickets keep their effort
  value as read-only legacy data (see §11).
- All conventions below are expressed through the **`tags`** array.

Create and edit never block on missing class or estimate. The rules in §3 are
enforced at the **first promote** out of `not_started`.

### Writing a ticket a human or agent can act on

The ticket is the **source of authority** for its work — a developer or an agent
should be able to build it without hunting elsewhere. Every ticket you create
(**including agent-created ones**) must carry these four things:

1. **A verb-first, descriptive title.** Say what to do, in the imperative — name
   the action or the deliverable.
   - ✅ "Build the customer entry screen" · "Build the one-click installer"
   - ❌ `VA2-R2-04 Coordinator: health/heartbeat reporting` — an ID prefix plus a
     noun fragment; it never says what to *do*. Don't prefix titles with a
     workbook/ticket ID; the system assigns the real ID.

2. **A real description — the what and the why.** State what we're trying to do and
   enough scope to act on it. Metadata is **not** a description.
   - ❌ `Child of KC-e01e. Class: functional. Reference CFP (display-only): 4.` —
     pure metadata; tells the developer nothing.
   - ✅ "Add an admin toggle to enable/disable Virtual Agents per account. Enforce
     the two preconditions (…) before it can be enabled; persist the flag and gate
     the VA features on it."

3. **Mockups attached, for any UI ticket.** If the ticket renders a screen, form,
   or component, its design belongs **on the ticket** as an attachment — a
   screenshot or mockup (attachment endpoints are in §12). Before creating a UI
   ticket, ask *"where are the mockups/screenshots?"* and attach them, so nobody
   has to go find the design.

4. **Test cases — authored up front — are the acceptance criteria.** Do **not** add
   a separate "acceptance criteria" or per-ticket "definition of done" field; the
   **test cases *are* the acceptance criteria, just made runnable.** Each test
   case's *expected result* is one acceptance condition. Write them when you write
   the ticket (agent drafts, human edits), not as a QA afterthought:
   - They are the **spec** before build, the **build target** during, and the
     **proof** at QA — one artifact, three jobs.
   - **Ticket-level "done" = all its test cases pass.** The project's enforce-QA
     setting keeps unfilled tickets out of QA, and the PM dashboard's test-case
     summary shows pass / fail / untested. (A *team-wide* definition of done —
     reviewed, deployed, docs — is a separate project convention, not a per-ticket
     field.) Test case endpoints are in §12.

**In short: title (verb) + description (what/why) + mockups (if UI) + test cases
(acceptance criteria) = a ticket that's self-sufficient.**

---

## 2. Structure: features (parents) and work items (children)

Model real deliverables as a **parent ticket** with **child tickets** for the
actual work.

- The **parent** is the deliverable/feature. It carries the *size* (`cfp:`) and/or
  the *deliverable type* (`type:`). Do **not** log hours against it, and give it
  no `estimate_hours`.
- Each **child** carries `parent:<PARENT_ID>`, does one slice of the work, gets
  exactly one **class** tag, and is where **hours are logged**. Wrap children
  also carry `estimate_hours`.

```
AMPL-7k3x  "Onboarding revamp"        tags: cfp:42           ← parent (size lives here)
  AMPL-9f2a  "Build the new flow UI"  tags: parent:AMPL-7k3x, functional
  AMPL-2b8c  "Build retrieval API"    tags: parent:AMPL-7k3x, functional
  AMPL-5h1q  "Provision Bedrock KB"   tags: parent:AMPL-7k3x, config     estimate_hours: 2
```

A small feature can be **self-contained**: one ticket with no `parent:` tag that
carries both `cfp:<N>` and the `functional` class. It is sized by its own CFP.

---

## 3. Tag conventions (the contract)

| Tag / field | Goes on | Meaning |
|---|---|---|
| `parent:<TICKET_ID>` | child | Links a work item to its parent deliverable. |
| `cfp:<N>` | parent (or self-contained feature) | COSMIC functional size of the feature. Integer. **Never on a child.** |
| `functional` | child | **Code**: the human test/debug/review loop on agent-generated code. Generation itself is ~0. Sized by the parent's CFP; no `estimate_hours`. |
| `config` | child | **Config**: console / manual / platform setup (incl. IaC). 0 CFP. Needs `estimate_hours`. |
| `nonfunc` | child | **Non-functional**: deploy, run, validate, troubleshoot, perf, security, hardening. 0 CFP. Needs `estimate_hours`. |
| `estimate_hours` (field) | config / nonfunc child | Hours estimate for the wrap ticket (0.25 steps; picker 0.25/0.5/1/2/4/8). |
| `type:<name>` | parent or wrap child | Deliverable type for cost rollup, e.g. `type:kb-article`, `type:deploy` (§6). |
| `calibration-slice` | parent | Marks a feature as part of the calibration slice (§11). |
| `backlog` | any | Out of committed scope — excluded from completion %, schedule, capacity, and the estimate. |
| `qa` | any | Routes the ticket's logged time to the **QA hours** pool (also applied automatically while a ticket is in QA Testing / Rework). See §5. |

`config` and `nonfunc` together are **wrap** tickets. In the UI the classes are
labeled **Code**, **Config**, and **Non-functional**; the tag values stay
`functional` / `config` / `nonfunc`.

### Promote-time validation

When a ticket leaves `not_started` for the first time, HATE checks it. The check
applies only to tickets that have a `parent:<id>` tag and are not `meeting` or
`administration`. The promote is rejected (HTTP 422) when:

- **a)** the ticket has no class tag, or more than one;
- **b)** its class is `config` or `nonfunc` and `estimate_hours` is not set;
- **c)** its class is `functional` and `estimate_hours` is set;
- **d)** it has a `cfp:` tag (CFP belongs on the parent or a self-contained
  feature).

A ticket with no `parent:` tag (a parent, or a self-contained feature) is not
subject to a)–d). Force-close bypasses all of them.

Rules that keep the data clean:

- **CFP lives only on the parent. Never copy `cfp:` to a child.** Children carry
  hours; the parent carries size. Copying size double-counts it.
- **Class every child** `functional` / `config` / `nonfunc`. Unclassed hours make
  the observed rate unreliable (the dashboards flag them).
- **Don't log hours on the parent.** Hours belong on the children (flagged with ⚑
  in the COSMIC tab if you do).
- **One class tag per child.** If a child mixes code and platform work, split it
  into two children.
- **Meetings and administration get no estimate.** Their time burns the
  Admin/meeting pool.

---

## 4. Counting CFP (COSMIC)

CFP measures **functional data movements** across a boundary — Entry, Exit, Read,
Write — for the functional requirements. It does **not** measure effort, compute,
or platform cost. Put the integer count in `cfp:<N>` on the parent.

CFP is the standard, cross-project size for code. That only holds if every
project is counted the same way, so:

- **Count with a fixed, versioned counting prompt.** Use the counting guide below
  as written. Don't improvise rules per project. If the guide changes, it gets a
  new version number.
- **Record the breakdown in the parent's description.** List each functional
  process and its E/X/R/W movements with the data group, so anyone can check or
  re-run the count.
- **Note which guide version counted it** (e.g. "Counted with HATE counting guide
  v1").

The "is your code in the loop?" rule:

- **A human clicks a console / a managed service does the work** → your code isn't
  in the loop → **0 CFP**. (Example: clicking "Create knowledge base" in the AWS
  console. Real effort, zero functional size — track it as a `config` ticket with
  an hours estimate.)
- **Your code triggers it programmatically** → a **few CFP** for the command and
  its response (the data movements your code makes), **never for the compute** the
  service runs. (Example: your handler calls `Retrieve` on a KB — count the query
  going out and the results coming back, not the embedding compute.)

Do **not** invent CFP for managed-service / platform-heavy work. COSMIC will
under-count it by design — that's expected. Capture that cost as wrap hours (§5).

### COUNTING GUIDE v1

Follow these steps for each feature (parent ticket).

1. **Find the functional users.** Who or what sends data to, or receives data
   from, the software being built: people, other systems, devices, external
   services.
2. **Draw the boundary.** Inside is the software this project builds. Outside are
   the functional users. Persistent storage (databases, files, a KB your code
   queries) is on the software's side of the boundary and is reached by Read and
   Write.
3. **List the functional processes.** A functional process is one complete
   response to one event: it starts with a **triggering Entry** (the data a
   functional user sends that starts it) and ends when everything required in
   response has been done. "Submit a search", "save a customer", "nightly sync
   run" are each one process.
4. **For each process, list the data movements.** Each moves one **data group**
   (a set of attributes about one thing of interest, e.g. customer, order,
   search query):
   - **Entry (E):** data moves in from a functional user across the boundary.
   - **Exit (X):** data moves out to a functional user across the boundary.
   - **Read (R):** data is read from persistent storage.
   - **Write (W):** data is written to persistent storage.
5. **Score 1 CFP per data movement per data group per process.** Moving the same
   data group the same way twice in one process counts once. Count all error and
   confirmation messages in a process as one Exit.
6. **Sanity check.** Every process has at least one Entry (its trigger) and at
   least one Write or Exit, so its minimum size is 2 CFP.
7. **Sum the processes** and put the total in `cfp:<N>` on the parent.

Do **not** count:

- computation, transformation, validation, or formatting inside a process (that
  is data manipulation, not movement);
- work the managed service or platform does (embedding, indexing, routing);
- console clicks, configuration, deploys, or anything a human does by hand;
- the agent generating the code;
- navigation or UI changes that move no data group.

Record the count in the parent's description in this form:

```
CFP: 7 (counted with HATE counting guide v1)
FP1 Search documents (trigger: user submits query)
  E  search query
  R  document index
  X  result list
  X  error/confirmation messages
FP2 Save a search (trigger: user clicks save)
  E  saved search
  W  saved search
  X  error/confirmation messages
```

### Calibration discipline

- Keep **functional**, **config**, and **nonfunc** separate. Never blend platform
  hours into the functional h/CFP — that's what makes a rate look fake.
- The reference rates come from your own delivered features (§11), not from a
  borrowed industry band.

---

## 5. Code wrap, platform wrap, and testing

Agent generation of code is about zero. The hours on a project are the human
work **around** it, the **wrap**. There are two kinds, estimated differently:

| | Code wrap | Platform wrap |
|---|---|---|
| What | Testing, debugging, and reviewing generated code | Console setup, deploys, managed services, manual steps |
| Driver | Amount of code | Number of items to set up |
| Tickets | `functional` | `config` / `nonfunc` |
| Estimate | Feature CFP × a rate range from reference features (Monte Carlo, §11) | Sum of `estimate_hours` on the wrap tickets |

Total estimate = code wrap + platform wrap.

Managed-service setup, platform actions, and content have **no functional size**.
Do not force a CFP on them. Make them `config` or `nonfunc` tickets with an
hours estimate. If it's a recurring kind of item you want hours-per-item history
for, give it a `type:` tag (§6).

**wrap % is info-only.** The COSMIC tab can still show
`(config + nonfunc hours) ÷ functional hours` per feature, but it is not used to
estimate anything. Code and platform wrap don't drive each other, and the ratio
breaks down as functional hours get small.

### Where testing lands

- **Debugging while building** → time on the `functional` ticket.
- **Formal QA** → logged while the ticket is in `qa_testing` / `rework` (QA pool).
- **Smoke / integration validation** → a separate `nonfunc` ticket with an hours
  estimate.

### QA effort is its own hours pool

QA/testing effort is **bid and tracked separately** from the build work, in a
dedicated **QA hours** pool (set in Settings alongside Work and Admin/meeting).
Keeping it out of the functional hours keeps the h/CFP rate honest.

- **Time logged while a ticket is in `qa_testing` or `rework` automatically burns
  the QA pool**; time logged during `in_progress` burns Work. So a single dev_task's
  hours split themselves into build vs. QA by *when* they were logged — no tagging
  needed for the common case.
- For a **standalone** QA/test ticket (one that never passes through another
  ticket's QA status), tag it **`qa`** to route its logged time to the QA pool.
- The forcing function already exists: `qa_testing` is a work status, so the
  "log time to promote out of a work status" rule guarantees QA time is logged
  before the ticket can leave QA. The PM dashboard's Hours Budget shows all three
  pools (Work / Admin-meeting / QA), bid vs. actual.

---

## 6. Deliverable types for cost carry-forward (`type:`)

When you want to know "what does one cost" so it carries to the next similar
project, tag the deliverable with a namespaced `type:<name>`. That can be a
**parent** built from several tickets, or a single **wrap child** (to build
hours-per-item history for platform work, e.g. "a deploy averages 0.5h").

```
AMPL-aa11  "KB article: Returns policy"   tags: type:kb-article
  AMPL-aa12  "Collect source docs"        tags: parent:AMPL-aa11, config    estimate_hours: 1
  AMPL-aa13  "Draft + structure article"  tags: parent:AMPL-aa11, nonfunc   estimate_hours: 2
  AMPL-aa14  "Ingest & verify retrieval"  tags: parent:AMPL-aa11, config    estimate_hours: 0.5

AMPL-bb20  "Deploy the retrieval stack"   tags: parent:AMPL-7k3x, nonfunc, type:deploy   estimate_hours: 0.5
```

The PM dashboard's **Project Cost** section rolls these up:
`type · count · total hours · avg per unit`. Each ticket with a `type:` tag is one
unit. Its hours, plus the hours of any children that don't have their own
`type:`, count toward that type. After a few projects, "a kb-article costs ~Xh" is
a good starting point for the next `estimate_hours`. Use a small, stable
vocabulary of type names (`kb-article`, `deploy`, `data-migration`,
`integration`, …) — don't sprawl.

A parent can carry **both** `cfp:` (it has functional size) and `type:` (it's also
a tracked deliverable kind). They answer different questions.

---

## 7. Backlog (out of scope)

Tag a ticket `backlog` if it's in the project but **not committed**. Backlog
tickets are excluded from completion %, the baseline/projected end date,
capacity, and the estimate. Remove the tag to commit a ticket into scope. Do
**not** park backlog by abusing `phase` — use the tag.

---

## 8. Phases (for the rollup)

Set a **`phase`** on every committed ticket. Phase is the unit the PM **phase
rollup** groups by (the **Σ Phases** view and its CSV export) — it's how you hand
a stakeholder "Phase 2 is 40% done" without them ever seeing individual tickets.

- **Use a small, consistent, ordered set of phase names.** Phases are matched by
  exact string, so `Build`, `build`, and `Building` become three separate phases.
  Pick one spelling per phase and reuse it on every ticket in that phase.
- **Number the phases if execution order matters.** The rollup lists phases
  alphabetically by the phase string, so prefix them to force order:
  `01 - Discovery`, `02 - Build`, `03 - QA`. (Otherwise `Build` sorts before
  `Design`.)
- **The rollup is weighted by estimated hours** (% = done estimated hours ÷ total
  estimated hours). Wrap tickets count their `estimate_hours`; code tickets count
  their share of the parent feature's CFP-based estimate (§11). A ticket with no
  estimate is invisible to the percentage, and a phase of entirely unsized
  tickets falls back to a plain ticket count. So phase **and** a complete
  estimate (class + `estimate_hours` on wrap, `cfp:` on the parent) are what make
  the number meaningful.
- Tickets with no phase land in a **`(no phase)`** bucket — fine for stray items,
  but don't leave committed work there.
- Use `phase` only for the work-stage grouping. Don't park out-of-scope work in a
  phase — that's the `backlog` tag (§7). Descoped/force-closed tickets are excluded
  from the rollup automatically.

---

## 9. Worked example

A feature with functional size plus platform work:

```
PROJ-100  "Semantic search over docs"   tags: cfp:18   (E/X/R/W breakdown in the description)
  PROJ-101  "Query API + ranking"        tags: parent:PROJ-100, functional                    (log hours here)
  PROJ-102  "Result rendering"           tags: parent:PROJ-100, functional                    (log hours here)
  PROJ-103  "Stand up Bedrock KB"        tags: parent:PROJ-100, config     estimate_hours: 2   (0 CFP)
  PROJ-104  "Load test @ 100 rps"        tags: parent:PROJ-100, nonfunc    estimate_hours: 4   (0 CFP)
```

- Code rate = (PROJ-101 + PROJ-102 hours) ÷ 18 CFP. Once both are done, this
  feature can serve as a reference feature (§11).
- Platform wrap estimate = 2 + 4 = 6h. Each wrap ticket's actual hours are
  compared against its own estimate (Estimate Variance on the PM dashboard).
- For scheduling, PROJ-101 and PROJ-102 each get 18 × the reference median rate ÷ 2.
- `cfp:18` is on the parent only; nobody logs hours on PROJ-100.

---

## 10. Quick checklist before you finish

- [ ] Each feature is a parent with children for the work (or a self-contained
      feature with `cfp:` and `functional`).
- [ ] `cfp:<N>` is on the parent and nowhere else (integer; only if it has
      functional size), counted with the current counting guide.
- [ ] The parent's description has the E/X/R/W breakdown and the guide version.
- [ ] Every child has `parent:<id>` **and** exactly one of `functional` (Code) /
      `config` / `nonfunc`.
- [ ] Every `config` / `nonfunc` child has `estimate_hours`; no `functional` child
      does. No ticket sends `effort`.
- [ ] Platform/managed-service work is `config` or `nonfunc` with hours, not
      invented CFP.
- [ ] Smoke/integration validation has its own `nonfunc` ticket.
- [ ] No hours logged on parents.
- [ ] Recurring deliverables have a `type:<name>` (on the parent, or on the wrap
      child for per-item history).
- [ ] Every committed ticket has a consistent `phase`.
- [ ] Anything not committed is tagged `backlog`.
- [ ] For a new kind of project, the calibration-slice parents are tagged
      `calibration-slice` (§11).

---

## 11. Estimating

### How each ticket gets its hours

One function supplies "how many hours is this ticket expected to take" to the
capacity-aware schedule, the Load table, the phase rollup, the baseline, and the
Hours Budget checks:

| Ticket | Estimated hours |
|---|---|
| Wrap child (`config` / `nonfunc`) with `estimate_hours` | `estimate_hours` |
| Wrap child with only a legacy `effort` | `effort_to_days[effort]` × 8 ("converted"; computed at read time, never written) |
| Code child (`functional`) | parent CFP × reference median rate ÷ number of functional children of that parent (not cancelled, not backlog). Scheduling only. |
| Self-contained functional feature | its CFP × reference median rate |
| Unclassed ticket with a legacy `effort` | `effort_to_days[effort]` × 8 ("legacy"), so old projects stay schedulable |
| Parent, meeting, administration | 0 |
| Anything else | 0 (unsized) |

The **reference median rate** is the median h/CFP of the project's chosen
reference features (below). If there are fewer than 3 real reference features
and the **manual baseline** is in effect (including by default on a project with
no saved estimate inputs), it is the baseline's **Likely**. Otherwise, if no
reference set is configured or it has fewer than 3 features, HATE uses a default
of **0.25 h/CFP** (the pooled NEI + Tactic median as of 2026-10-02).

Schedules convert hours to days with the assignee's `daily_hours_available` (8
if the assignee is unknown; an assignee matches a resource by email, git user, or
name).

**Capacity and load.** The projected Gantt (before a baseline) is
capacity-aware and hour-granular: each person works their ready open tickets one
at a time — priority, then dependency work order, then ID — at their daily
hours, so small tickets share a day and nothing rounds up to a whole day.
Predecessors are finish-to-start across people; a `planned_start_date` is a
floor. Only people consume capacity. Unassigned work goes to the project's only
resource when there is exactly one, otherwise to an `unassigned` lane at 8 h/day
(a project with no resources gets one lane per assignee). Unsized tickets get a
0.25 h placeholder and are counted. The PM dashboard's **Load** table shows, per
person, remaining estimated hours (tickets not yet dev_complete, at estimate minus
hours logged; old effort sizes count as unestimated), h/day, days of work, and the
free-from date from that schedule; with a **requested end** set it adds the
working days to it and flags anyone "over by N days". Nothing writes ticket
dates.

**Schedule vs request.** With a requested end (and optionally a requested start)
in the project settings, the PM dashboard opens with a card comparing the request
with the forecast: actual start (first move to in_progress or first logged time),
projected finish likely (the capacity schedule from the later of today and the
requested start) and P85 (code tickets at the reference rate × Monte Carlo code
P85 / P50), variances in business days (plus = late), needs vs has (remaining
hours ÷ working days left vs the team's total daily hours; hours to cut or
spare), and a status: ON TRACK (P85 by the requested end), AT RISK (likely on
time, P85 late), LATE (likely late). Each changed forecast is kept (one entry
per day) in `.tkt/pm/forecast_history.json`, committed, and drawn as a trend.

**Strict time enforcement** applies to wrap tickets only: a time log that would
take a wrap ticket past its estimate is blocked. Code tickets are estimated at the
feature level, so there is no per-ticket gate for them (or for unclassed
tickets). On the PM dashboard, **Estimate Variance** compares wrap tickets one by
one and code **per feature** (once all of a feature's functional children are
done: CFP × reference median rate vs. actual functional hours). **Hours at Risk**
covers wrap tickets only.

### The Monte Carlo estimate (COSMIC tab)

The COSMIC tab is a standard tab. Next to the per-feature table it shows a
project estimate as a range: **P50 / P85 / P95** for code wrap, platform wrap,
and the total.

**Reference set.** Pick any combination of:

- **Specific projects**: past projects in the same domain. The normal case.
  Projects are picked by hand; there is no domain field.
- **All past projects**: every other known project. Use it for a project unlike
  anything before. It gives a deliberately wide starting range.
- **This project's own features**: its finished features.
- **Manual baseline**: a typed rate range for a cold start (below).

Pooling across domains only happens when you choose "all past projects".

A feature counts as a **reference feature** when it has functional hours, has at
least the minimum CFP (default 3, so tiny features with extreme rates don't
dominate), and all its functional children are done (`dev_complete` or later:
`qa_testing`, `submitted_for_review`, `approved`, `complete`, `closed`). If there
are fewer than 3 reference features in total (and no manual baseline), the panel
says "need at least 3 reference features" instead of a range.

**Manual baseline.** For a fresh install or a first-of-its-kind project with
nothing to borrow from, type a rate range in h/CFP: **Low / Likely / High**, read
as P10 / P50 / P90 (Likely is the median; 1 feature in 10 should come in under
Low and 1 in 10 over High). Presets:

| Preset | Low | Likely | High | Notes |
|---|---|---|---|---|
| Agentic (Claude-assisted) — **default** | 0.08 | 0.25 | 1.00 | NEI + Tactic, 32 features; High near the worst observed 0.97 because the backtest showed ranges run wide |
| Traditional (hand-coded) | 8 | 12 | 18 | the industry band |
| Custom | — | — | — | any other range (editing a value switches to it) |

The engine fits a log-normal to the three points: mu = ln(Likely), sd = the mean
of the two log-space spreads (ln Likely − ln Low, ln High − ln Likely) ÷ 1.2816.
It is **not widened** (the typed range is already the stated spread). The
baseline counts as **5 features of evidence**: it satisfies the 3-feature minimum
on its own, shares the non-own draws with borrowed features as 5 : borrowed_N,
and hands over to the project's own finished features by the blending rule —
0 own → all baseline (or baseline + borrowed), 5 own → 50% own, 15+ own → the
baseline no longer affects the draw. Must be 0 < Low ≤ Likely ≤ High.

**Defaults for new projects.** A project with no saved estimate inputs (no
reference projects, all/own off, and the manual fields never saved) uses the
**manual baseline (Agentic) + this project's own features**, so a range shows
immediately; the panel says it's using the default baseline. Once any input is
saved the saved values are used exactly, so unticking everything gives "no
reference selected".

**Blending.** Own features, borrowed features and the manual baseline are kept
as separate pools. Each draw picks the own pool with probability
own_N ÷ (own_N + 5), and always once the project has 15 or more finished
features of its own. Otherwise it picks borrowed vs manual baseline weighted
borrowed_N : 5. The panel shows the current mix (e.g. "Draws: 40% manual
baseline, 60% own features"; non-zero parts only). If only one pool has
features, it uses that one.

**What it computes.**

- Each pool's rates are fitted as a log-normal distribution of h/CFP.
- The feature pools' spread is **widened 1.5×**, keeping the mean rate the same
  (the manual baseline is not widened). In the
  backtest the raw ranges were too narrow (a raw "P85" behaved like a P70-P75);
  the panel says the range is widened.
- Each run: for every feature in this project (not cancelled or backlog), code
  hours = CFP × a rate drawn from the chosen pool. An optional **counting
  uncertainty** (± %) scales the run's total CFP-driven hours once.
- **Platform wrap** is a fixed sum, so its P50/P85/P95 are equal. Each in-scope
  wrap ticket contributes its logged hours once it's done, otherwise its
  `estimate_hours`. Wrap tickets with only a converted legacy effort, or no
  estimate at all, are left out of the sum; the panel counts them so you can
  set real estimates.
- 10,000 runs with a fixed random seed, so the numbers don't change on reload.
  It recalculates when the tab opens or an input changes.

**Also shown:** the number of own and borrowed reference features, the reference
median rate, the widening factor, actual hours so far, and a **projected
finish**: actual hours on done features plus the P50/P85 of the remaining
features.

**It is display-only.** Nothing is written to the tickets, and there is no
button to set the cap from it. The **max-hours cap stays manual** in Settings.

The inputs are saved per project in `.tkt/config.json` (and committed):
`estimate_ref_projects`, `estimate_ref_all`, `estimate_ref_own`,
`estimate_ref_manual`, `estimate_manual` (`{low, likely, high}`),
`estimate_min_cfp`, `estimate_count_unc_pct`.

### Calibration slice (a new kind of project)

For a project unlike anything delivered before, there's no comparable reference
project. Work it like this:

1. Count CFP for the whole spec with the counting guide (§4).
2. Estimate with **all past projects** as the reference (or the **manual
   baseline** when there are none). That's the wide starting range. Pooled NEI + Tactic as of 2026-10-02 (32 features, 3+ CFP):
   P10 0.07 / P25 0.11 / P50 0.25 / P75 0.29 / P90 0.33 / max 0.97 h/CFP.
3. Wrap has no transferable defaults (it's platform-specific). Estimate it by
   judgment with the hours picker, and add explicit **discovery** wrap tickets for
   the unknown platform (dev environment, how it deploys, first console setup).
4. Pick 3-5 representative features, including some wrap on the new platform,
   and build them first. Tag their parents **`calibration-slice`**. The COSMIC tab
   marks them and shows "slice complete: N of M".
5. As slice features finish, turn on **this project's own features**. The
   blending rule moves the estimate onto the project's own rates.
6. Re-estimate the rest after the slice. Because CFP is a consistent size, rates
   measured on the slice apply to remaining features of different sizes.

**Bidding:**

- Preferred: a **two-part bid**. A fixed scope for discovery plus the slice, then
  a firm number for the rest from the project's own rates.
- If it has to be one number, bid at **P90-P95** of the wide range, not P85.
- The max-hours cap stays manual. Revisit it after the slice.

### Legacy effort

`effort` (xs..xl) and the project's `effort_to_days` map are kept only to convert
old tickets: a wrap ticket with no `estimate_hours` falls back to its effort
("converted"), and an unclassed ticket with effort stays schedulable ("legacy").
Review converted estimates: they are days-based and usually too big. Set a real
`estimate_hours` to replace them. You can still clear an old effort (PATCH it to
`null`), but you can't set one.

---

## 12. API endpoint reference

The full HTTP API. The server listens on `http://localhost:8000` (see
`main.go`). No authentication. All request/response bodies are JSON unless noted
(`Content-Type: application/json`), except attachment upload (multipart), the
dashboard (HTML), and the Gantt export (draw.io XML). `{projectId}` is the
project's folder name / id; `{ticketId}` is the full ticket id (e.g.
`AMPL-7k3x`).

Conventions:
- Optional `author` (in body or `?author=` query) attributes the action in the
  activity log; it defaults to a system value when omitted.
- Mutations auto-commit the changed files to the project's git repo.
- Errors return `{"detail": "<message>"}` with a 4xx/5xx status.

### App

| Method & path | What it does | Body |
|---|---|---|
| `GET /api/version` | The build version of the running binary (shown in Settings). | — |

### Projects (app-level) — `/api/projects`

| Method & path | What it does | Body |
|---|---|---|
| `GET /api/projects` | List all discovered projects. | — |
| `POST /api/projects` | Create a new project on disk. | `{folder_name, client, project_name, project_id, prefix}` |
| `GET /api/projects/settings` | Global app settings (projects root, tab toggles, scheduler). | — |
| `PUT /api/projects/settings` | Update global settings. `show_cosmic` is legacy: the COSMIC tab is now always shown. | `{projects_root?, scheduler?, show_billing?, show_cosmic?}` |
| `POST /api/projects/open` | Register an existing project folder by path. | `{path}` |
| `GET /api/projects/hidden` | List hidden projects. | — |
| `POST /api/projects/hide` | Hide a project from the sidebar. | `{path}` |
| `POST /api/projects/unhide` | Unhide a project. | `{path}` |

### Project config & lifecycle — `/api/projects/{projectId}`

| Method & path | What it does | Body |
|---|---|---|
| `GET /{projectId}` | Project details (name, prefix, counts, closed state). | — |
| `GET /{projectId}/sync-status` | Git ahead/behind status vs remote. | — |
| `POST /{projectId}/sync` | Pull/push the project repo. | — |
| `GET /{projectId}/git-status` | Working-tree git status. | — |
| `GET /{projectId}/git-identity` · `POST …/git-identity` | Read / set the project's git author identity. | POST: `{name, email}` |
| `GET /{projectId}/resources` | List team resources (assignable people + capacity). | — |
| `POST /{projectId}/resources` | Add a resource. | `{name, email, git_user, role, daily_hours_available?}` |
| `PATCH /{projectId}/resources/{email}` | Update a resource. | resource fields |
| `DELETE /{projectId}/resources/{email}` | Remove a resource. | — |
| `GET /{projectId}/whoami` | Resolve the current user for this project. | — |
| `GET /{projectId}/effort-to-days` | **Legacy.** Effort-size → days map (+ defaults). Used only to convert old effort sizes (§11). | — |
| `PUT /{projectId}/effort-to-days` | **Legacy.** Set the map. All five sizes required, each ≥ 0.25. | `{"effort_to_days": {"xs":1,"s":2,"m":3,"l":5,"xl":8}}` |
| `GET /{projectId}/hour-budget` | The three hour pools: work, admin/meeting, QA (`null` if unset); `work_hours` migrates a legacy `max_hours`. | — |
| `PUT /{projectId}/hour-budget` | Set/clear the pools. Positive sets; `null` clears; ≤ 0 → 400. | `{"work_hours": 400, "admin_hours": 100, "qa_hours": 80}` |
| `GET /{projectId}/strict-time` · `PUT …/strict-time` | Read / set strict time enforcement (gate time logs that go past a wrap ticket's estimate). | PUT: `{"strict_time_enforcement": true}` |
| `GET /{projectId}/enforce-qa` · `PUT …/enforce-qa` | Read / set enforce-QA (keep tickets without test cases out of QA). | PUT: `{"enforce_qa": true}` |
| `GET /{projectId}/overview` · `PUT …/overview` | Read / replace the Project Overview content (contacts, links, instructions). | PUT: `{contacts:[…], links:[…], instructions:[…]}` |
| `POST /{projectId}/close` · `POST …/reopen` | Close / reopen the project (closed rejects ticket writes). | — |
| `PATCH /{projectId}/info` | Edit the project display name (id/prefix are immutable). Max 120 chars, not empty. | `{project_name}` |

### Tickets — `/api/projects/{projectId}/tickets`

| Method & path | What it does | Body |
|---|---|---|
| `GET …/tickets` | List all tickets. | — |
| `POST …/tickets` | Create a ticket (see §1 for the full body). A non-empty `effort` → 400. | see §1 |
| `GET …/tickets/billing` | Billing rollup (logged hours × rates). | — |
| `GET …/tickets/{ticketId}` | Full ticket JSON. | — |
| `PATCH …/tickets/{ticketId}` | Edit one field (title, description, priority, `estimate_hours`, tags, dates, …). Setting `effort` → 400; clearing it to `null` is allowed. | `{field, value, author?}` |

### Ticket actions

| Method & path | What it does | Body / params |
|---|---|---|
| `POST …/tickets/{ticketId}/promote` | Advance status along the type's workflow. Gated on logged time in work statuses. The first promote out of `not_started` runs the class/estimate checks in §3 (422 on failure). | `?author=` optional |
| `POST …/tickets/{ticketId}/demote` | Move status back one step. | `?author=` optional |
| `POST …/tickets/{ticketId}/block` | Set status to `blocked`. | `?author=` optional |
| `POST …/tickets/{ticketId}/force-close` | Skip the workflow to `closed`, recording a reason (marks it descoped/cancelled). | `{reason, author?}` |
| `POST …/tickets/{ticketId}/comment` | Add a comment to the activity log. | `{message, author?}` |
| `POST …/tickets/{ticketId}/time` | Log a time entry (hours round to 0.25). | `{date, hours, description, author?}` |
| `DELETE …/tickets/{ticketId}/time/{entryId}` | Delete a time entry. | — |
| `POST …/tickets/{ticketId}/test-cases` | Add one test case. | `{step, expected, author?}` |
| `POST …/tickets/{ticketId}/test-cases/bulk` | Add several test cases in one write (the easy path for agents). | `{cases:[{step, expected}], author?}` |
| `PATCH …/tickets/{ticketId}/test-cases/{caseId}` | Edit a test case or record its QA result. `status` is `""` (untested), `pass`, or `fail`. | `{step?, expected?, status?, comment?, author?}` |
| `DELETE …/tickets/{ticketId}/test-cases/{caseId}` | Delete a test case. | — |
| `POST …/tickets/{ticketId}/predecessors` | Add a predecessor dependency. | `{predecessor_id, author?}` |
| `DELETE …/tickets/{ticketId}/predecessors/{predecessorId}` | Remove a predecessor. | — |
| `POST …/tickets/{ticketId}/attachments` | Upload a file (multipart/form-data). | multipart |
| `GET …/tickets/{ticketId}/attachments/{attachmentId}` | Download an attachment. | — |
| `DELETE …/tickets/{ticketId}/attachments/{attachmentId}` | Delete an attachment. | — |

### PM: reporting, baseline & scheduling — `/api/projects/{projectId}`

| Method & path | What it does | Body |
|---|---|---|
| `GET /{projectId}/dashboard` | **HTML** PM dashboard (Schedule vs request card, Load table, hours budget vs cap, estimate variance, project cost, status/slip; the capacity-aware projected Gantt before a baseline). | — |
| `GET /{projectId}/gantt.drawio` | Download the Gantt as an editable draw.io file. Uses the baseline if there is one, otherwise the capacity-aware projected schedule from `?start=YYYY-MM-DD` (default today). | `?start=` optional |
| `GET /{projectId}/snapshot` · `POST …/snapshot` | Read latest / create a new slip snapshot. | — |
| `POST /{projectId}/baseline` | Create the immutable schedule baseline from a template. | `{project_name, template_id, start_date, owner_assignments, duration_adjustments, created_by?}` |
| `POST /{projectId}/baseline-now` | Baseline directly from current tickets (no template). | — |
| `GET /{projectId}/slip` | List slip events. | — |
| `PATCH /{projectId}/slip/{slipEventId}` | Resolve a slip event with a reason. | `{reason_category, reason_narrative, acknowledged_by?}` |
| `GET /{projectId}/requested-dates` · `PUT …/requested-dates` | Read / set the optional requested start and end (the dates the client asked for). PUT validates the dates and start ≤ end (400), saves `requested_start` / `requested_end` to `.tkt/config.json` (dropping a legacy `target_date`) and commits when changed; `null` clears a date. GET reads an old `target_date` as the requested end. | PUT: `{"requested_start": "2026-11-02", "requested_end": "2027-01-29"}` |
| `GET /{projectId}/target-date` · `PUT …/target-date` | Alias for the requested end (kept for v1.0.6 clients): `{"target_date": …}` reads / sets `requested_end`, keeping the start. | PUT: `{"target_date": "2026-10-30"}` or `{"target_date": null}` |
| `GET /{projectId}/forecast` | The Schedule vs request card as JSON: `requested_start`, `requested_end`, `actual_start`, `start_variance_days`, `schedule_start`, `likely_finish`, `p85_finish`, `p85_factor`, `finish_variance_days`, `p85_finish_variance_days`, `remaining_hours`, `unsized`, `working_days_left`, `needs_per_day`, `has_per_day`, `hours_to_cut`, `spare_hours`, `status` (`ON TRACK` / `AT RISK` / `LATE`, empty without a requested end), and `history`. Records the forecast history like the dashboard (one entry per day, committed when it changes). | — |
| `GET /{projectId}/phase-rollup` | % complete per phase, weighted by estimated hours. | — |
| `GET /{projectId}/test-summary` | Per-ticket test-case tallies (pass / fail / untested) plus the cases, for the Test cases tab. | — |
| `POST /{projectId}/report` | **Not implemented** (returns 501). | — |

### COSMIC / estimation — `/api/projects/{projectId}`

| Method & path | What it does | Body |
|---|---|---|
| `GET /{projectId}/cosmic` | The COSMIC report: per-feature h/CFP (wrap % info-only, `calibration_slice` flag), aggregates, slice counts (`slice_total`, `slice_done`), plus `monte_carlo` (the result, or an error state such as "need at least 3 reference features"), `estimate_inputs` (effective inputs: `ref_projects`, `ref_all`, `ref_own`, `ref_manual`, `manual` {low, likely, high}, `manual_preset` agentic\|traditional\|custom, `defaults_applied` (true when nothing is saved and the manual-baseline + own defaults are in effect), `min_cfp`, `count_unc_pct`), `manual_presets` ([{id, label, low, likely, high}]), and `available_projects` (id + name of the other known projects, for the picker). `monte_carlo` includes `p_own`, `p_manual` (share of draws from the manual baseline) and `manual_in_use`. | — |
| `PUT /{projectId}/cosmic-estimate` | Set the Monte Carlo inputs. Validates `min_cfp` ≥ 1, `count_unc_pct` 0-100, every project id exists, and `manual` has 0 < low ≤ likely ≤ high (all 400). The saved values are used exactly (an omitted `ref_manual` is false; an omitted `manual` keeps the saved range or the Agentic default). Saves to `.tkt/config.json`, commits it, and returns `estimate_inputs` and the recomputed `monte_carlo`. | `{ref_projects:[ids], ref_all:bool, ref_own:bool, ref_manual:bool, manual:{low,likely,high}, min_cfp:int\|null, count_unc_pct:number\|null}` |
