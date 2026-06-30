# Agent guide: auto-building tickets & CFP in HATE

Instructions for Claude (or any agent) creating tickets for a project in HATE.
Follow these conventions exactly — the calibration math (h/CFP, wrap factor,
cost-per-deliverable) only works if the tags are applied consistently.

For the data model, statuses, and on-disk layout, see [`../README.md`](../README.md).
This file is about **how to structure and tag** the tickets you create.

---

## 1. Creating a ticket

`POST /api/projects/{projectId}/tickets` with a JSON body:

```json
{
  "type": "dev_task",
  "title": "Wire Bedrock KB retrieval into the chat handler",
  "description": "Markdown is supported and rendered.",
  "priority": "medium",
  "effort": "m",
  "assignee": "dev@example.com",
  "tags": ["parent:AMPL-7k3x", "functional"],
  "phase": "Build",
  "predecessors": ["AMPL-2b8c"],
  "planned_start_date": "2026-07-01",
  "due_date": "2026-07-04",
  "creator": "claude@example.com"
}
```

- `type`: one of `task`, `dev_task`, `design_task`, `meeting`, `administration`.
- `priority`: `critical` | `high` | `medium` | `low` (default `medium`).
- `effort`: `xs` | `s` | `m` | `l` | `xl` (maps to days via project config).
- All conventions below are expressed through the **`tags`** array.

---

## 2. Structure: features (parents) and work items (children)

Model real deliverables as a **parent ticket** with **child tickets** for the
actual work.

- The **parent** is the deliverable/feature. It carries the *size* (`cfp:`) and/or
  the *deliverable type* (`type:`). Do **not** log hours against it.
- Each **child** carries `parent:<PARENT_ID>`, does one slice of the work, gets a
  **class** tag, and is where **hours are logged**.

```
AMPL-7k3x  "Onboarding revamp"        tags: cfp:42          ← parent (size lives here)
  AMPL-9f2a  "Design new flow"        tags: parent:AMPL-7k3x, functional
  AMPL-2b8c  "Build retrieval API"    tags: parent:AMPL-7k3x, functional
  AMPL-5h1q  "Provision Bedrock KB"   tags: parent:AMPL-7k3x, config
```

---

## 3. Tag conventions (the contract)

| Tag | Goes on | Meaning |
|---|---|---|
| `parent:<TICKET_ID>` | child | Links a work item to its parent deliverable. |
| `cfp:<N>` | parent | COSMIC functional size of the feature. Integer. **Parent only.** |
| `functional` | child | Work that delivers functional data movements (counts toward h/CFP). |
| `config` | child | Platform/managed-service setup. 0 CFP. Tracked as hours. |
| `nonfunc` | child | Non-functional work (perf, security, hardening). 0 CFP. Hours. |
| `type:<name>` | parent | Deliverable type for cost rollup, e.g. `type:kb-article`. |
| `wt:<type>` | child (wrap) | Wrap-deliverable archetype from the catalog (§11), e.g. `wt:flow`. |
| `wtn:<N>` | child (wrap) | Unit count on a wrap ticket (batch-and-count; default 1). |
| `backlog` | any | Out of committed scope — excluded from completion %, schedule, capacity. |

Rules that keep the data clean:

- **CFP lives only on the parent. Never copy `cfp:` to a child.** Children carry
  hours; the parent carries size. Copying size double-counts it.
- **Class every child** `functional` / `config` / `nonfunc`. Unclassed hours make
  the observed rate unreliable (the dashboards flag them).
- **Don't log hours on the parent.** Hours belong on the children (flagged with ⚑
  in the COSMIC tab if you do).
- **One class tag per child.** If a child mixes functional and config work, split
  it into two children.

---

## 4. Counting CFP (COSMIC)

CFP measures **functional data movements** across a boundary — Entry, Exit, Read,
Write — for the functional requirements. It does **not** measure effort, compute,
or platform cost. Put the integer count in `cfp:<N>` on the parent.

The "is your code in the loop?" rule:

- **A human clicks a console / a managed service does the work** → your code isn't
  in the loop → **0 CFP**. (Example: clicking "Create knowledge base" in the AWS
  console. Real effort, zero functional size — track it as `config` hours.)
- **Your code triggers it programmatically** → a **few CFP** for the command and
  its response (the data movements your code makes), **never for the compute** the
  service runs. (Example: your handler calls `Retrieve` on a KB — that's the
  Entry/Read/Exit, not the embedding compute.)

Do **not** invent CFP for managed-service / platform-heavy work. COSMIC will
under-count it by design — that's expected. Capture that cost the other way (§5/§6).

Calibration discipline (so the numbers stay meaningful):

- Keep **functional**, **config**, and **nonfunc** separate. Never blend config
  hours into the functional h/CFP — that's what makes a rate look fake.
- The goal is to replace the borrowed industry band (8 / 12 / 18 h/CFP) and the
  ~60% wrap factor with **measured** numbers from your own delivered work. See the
  experimental **COSMIC** tab.

---

## 5. Non-functional & platform work (0 CFP)

Managed-service setup, platform actions, and content have **no functional size**.
Do not force a CFP on them. Track them as **hours under a typed deliverable** so
they calibrate as "hours per unit" instead.

- Tag the **child** `config` (platform/managed-service) or `nonfunc`.
- If it's a recurring deliverable type you want to estimate later, also give the
  **parent** a `type:` tag (§6).

**What "wrap" is.** The COSMIC view reports a **wrap %** built from these buckets:

```
wrap % = (config hours + nonfunc hours) ÷ functional hours × 100
```

It's the effort spent *around* the functional code — platform/managed-service
setup and non-functional work — as a share of the functional hours. **Wrap = 60%**
means every 10h of functional work carried another 6h of config + non-functional
work. (It's blank for a feature with no functional hours yet — nothing to divide
by.)

The dashboard ships with an *assumed* 60% wrap (and the borrowed 8 / 12 / 18
h-per-CFP band); the whole point of classing hours cleanly is to **replace that
assumption with your project's measured wrap**. Blend config hours into
`functional` and the wrap number becomes meaningless — which is why every child
gets exactly one class and hours never land on the parent (§3).

---

## 6. Deliverable types for cost carry-forward (`type:`)

When a deliverable is built from several tickets and you want to know "what does
one cost" so it carries to the next similar project, tag the **parent** with a
namespaced `type:<name>`.

```
AMPL-aa11  "KB article: Returns policy"   tags: type:kb-article
  AMPL-aa12  "Collect source docs"        tags: parent:AMPL-aa11, config
  AMPL-aa13  "Draft + structure article"  tags: parent:AMPL-aa11, nonfunc
  AMPL-aa14  "Ingest & verify retrieval"  tags: parent:AMPL-aa11, config
```

The PM dashboard's **Project Cost** section rolls these up:
`type · count · total hours · avg per unit`. After a few projects, "a kb-article
costs ~Xh" is your reusable estimate. Use a small, stable vocabulary of type
names (`kb-article`, `data-migration`, `integration`, …) — don't sprawl.

A parent can carry **both** `cfp:` (it has functional size) and `type:` (it's also
a tracked deliverable kind). They answer different questions.

---

## 7. Backlog (out of scope)

Tag a ticket `backlog` if it's in the project but **not committed**. Backlog
tickets are excluded from completion %, the baseline/projected end date, and
capacity. Remove the tag to commit a ticket into scope. Do **not** park backlog by
abusing `phase` — use the tag.

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
- **Give every ticket an `effort` size too.** The rollup is *effort-weighted*
  (% = done effort-days ÷ total effort-days). A ticket with no effort is invisible
  to the percentage, and a phase of entirely unsized tickets falls back to a plain
  ticket count. Phase **and** effort together are what make the number meaningful.
- Tickets with no phase land in a **`(no phase)`** bucket — fine for stray items,
  but don't leave committed work there.
- Use `phase` only for the work-stage grouping. Don't park out-of-scope work in a
  phase — that's the `backlog` tag (§7). Descoped/force-closed tickets are excluded
  from the rollup automatically.

---

## 9. Worked example

A feature with functional size plus a platform dependency:

```
PROJ-100  "Semantic search over docs"   tags: cfp:18
  PROJ-101  "Query API + ranking"        tags: parent:PROJ-100, functional   (log hours here)
  PROJ-102  "Result rendering"           tags: parent:PROJ-100, functional   (log hours here)
  PROJ-103  "Stand up Bedrock KB"        tags: parent:PROJ-100, config        (0 CFP, hours)
  PROJ-104  "Load test @ 100 rps"        tags: parent:PROJ-100, nonfunc       (0 CFP, hours)
```

- Functional pace = (PROJ-101 + PROJ-102 hours) ÷ 18 CFP.
- Wrap = (PROJ-103 + PROJ-104 hours) ÷ functional hours.
- `cfp:18` is on the parent only; nobody logs hours on PROJ-100.

---

## 10. Quick checklist before you finish

- [ ] Every committed ticket has a consistent `phase` (and an `effort` size) so it
      rolls up cleanly.
- [ ] Each feature is a parent with children for the work.
- [ ] `cfp:<N>` is on the parent and nowhere else (integer; only if it has
      functional size).
- [ ] Every child has `parent:<id>` **and** exactly one of `functional` /
      `config` / `nonfunc`.
- [ ] No hours logged on parents.
- [ ] Platform/managed-service work is `config` (0 CFP), not invented CFP.
- [ ] Recurring deliverables have a `type:<name>` on the parent.
- [ ] Anything not committed is tagged `backlog`.
- [ ] Wrap deliverables (console/operate work) carry a `wt:<type>` from the catalog (§11).

---

## 11. Wrap-based calibration (experimental)

This layers on top of the CFP/COSMIC model above. The thesis (see
[`future-wrap-based-estimation.md`](./future-wrap-based-estimation.md) and the
settled model in [`wrap-catalog-data-model.md`](./wrap-catalog-data-model.md)):
agentic coding **inverts the cost model** — generated code is a near-constant per
CFP, so the variable cost is the **wrap** around it.

### The three activities

Every unit of work is one of:

| Activity | What it is | Cost | Class tag today |
|---|---|---|---|
| **author** | Generate code / IaC — the agent does it | ~constant (priced by CFP) | `functional` |
| **operate** | Deploy, run, validate, troubleshoot | variable (catalogued) | `nonfunc` |
| **configure** | Console / manual click-ops | variable (catalogued) | `config` |

`author` is the constant slice; **operate + configure are "wrap"** — the variable
cost. This is the same split as the COSMIC `wrap %` (numerator = config + nonfunc).

### ⚠ Authored IaC is `author`, NOT `configure`

The single most important rule, and the easiest to get wrong:

- **Writing IaC** (CDK, CloudFormation, Terraform, YAML) is **authored code** →
  tag it `functional` (author). It's cheap and agent-generated.
- **Clicking in a console** (create a Connect instance, build a Lex bot, author a
  contact flow) is **configure** → tag it `config` **and** give it a `wt:<type>`.

A ticket titled `CDK: DynamoDB tables` is authoring, not configure — do **not**
tag it `config`/`wt:`. Conflating the two pollutes both the code constant and the
wrap rates. (This is the conflation HATE-bqh6 tracks.)

### Tagging wrap deliverables

A wrap ticket (a `config`/`nonfunc` child) that maps to a repeatable console/operate
deliverable also gets:

- `wt:<type>` — the **archetype** from the org catalog (the Catalog tab, or
  `GET /api/catalog`): `flow`, `bot`, `prompt`, `queue`, `knowledge-base`,
  `knowledge-article`, `instance`, `number`, `deploy`, `deploy-troubleshoot`,
  `smoke-validate`, … Use the picklist in the ticket panel — don't free-type it.
- `wtn:<N>` — optional unit count when one ticket covers N small units
  (batch-and-count; default 1).

The **platform** is set once per project (`.tkt/config.json` → `platform`, e.g.
`connect`), not per ticket — wrap rates are keyed `(platform, type)`.

### What you get

The Catalog tab's **Measured rates** (and `GET /api/wrap-aggregate`) pool every
`wt:`-tagged ticket across projects into **hours-per-unit by (platform, type)**
with a sample size, compared against the catalog seed. That measured number — not
a borrowed constant — is what the estimator calibrates from. Keep the tagging
clean (one `wt:` per wrap deliverable, IaC stays `author`) or the rates lie.
