# Plan: an MCP server built into the hate binary

Ticket: HATE-1a3s (plan only, no code). Status: decided 2026-11-02 (see section 9).

Goal: let Claude Code (or any MCP client) find projects, read and search
tickets, create and tag them, log time, add test cases and move them through
the workflow with typed tools, instead of hand-written curl calls against the
REST API. Out of scope: remote or multi-user hosting, auth beyond localhost.

Summary of the recommendation:

- Spec target: MCP **2026-07-28** (the current revision).
- Transport: **Streamable HTTP at `/mcp` on the existing chi router**, in the
  running hate process. No stdio in v1 (reason in section 2).
- Library: **github.com/modelcontextprotocol/go-sdk v1.8.0** with
  `StreamableHTTPOptions{Stateless: true, JSONResponse: true}`.
- Prerequisite refactor: a small **`internal/service`** package extracted from
  the ticket handlers, so REST and MCP share one code path for locks, the
  closed-project block, the promote gates, strict time and auto-commit.
- 18 tools, 2 resources, no prompts in v1. 91 routes inventoried.
- 8 implementation tickets plus 1 backlog ticket under one parent (`cfp:21`),
  section 7. CFP counted technology-independently: a process reached through
  a new channel (MCP) adds no size, so only new and changed processes count
  and the MCP tool wrappers are non-functional hours.

---

## 1. Spec target: MCP 2026-07-28

Source: https://modelcontextprotocol.io/specification/versioning says "The
**current** protocol version is **2026-07-28**". Full spec:
https://modelcontextprotocol.io/specification/2026-07-28 . Changes since
2025-11-25: https://modelcontextprotocol.io/specification/2026-07-28/changelog .

This revision is the biggest since launch. What matters for hate:

| Area | What 2026-07-28 requires | Consequence for hate |
|---|---|---|
| Lifecycle | **No `initialize` handshake and no sessions.** Every request carries `_meta`: `io.modelcontextprotocol/protocolVersion` and `.../clientCapabilities` (`.../clientInfo` SHOULD). Servers SHOULD put `.../serverInfo` in each result's `_meta`. Unsupported version -> `UnsupportedProtocolVersionError` (-32022) listing supported versions. | Nothing to keep per connection. Tools must take all context as arguments (project, ticket id, author). |
| Discovery | Servers **MUST** implement `server/discover` (supported versions, capabilities, identity). | Provided by the SDK. serverInfo version = `config.AppVersion`. |
| Capabilities | Server declares `tools`, `resources`, `prompts` (each optional `listChanged`); new `extensions` field. | Declare `tools` and `resources`, `listChanged: false` (the set is static). |
| Tools | `name` (1-128 chars, `[A-Za-z0-9_.-]`), `title`, `description`, `inputSchema` (JSON Schema 2020-12, MUST be an object schema, not null), optional `outputSchema`, `annotations`. With an `outputSchema`, the server **MUST** return `structuredContent` conforming to it and SHOULD also return the JSON as a text block. `tools/list` MUST NOT vary per connection and SHOULD be in deterministic order. | Typed Go In/Out structs; SDK infers both schemas and fills `structuredContent` plus the text block. |
| Tool annotations | `readOnlyHint` (default false), `destructiveHint` (default true, meaningful only when not read-only), `idempotentHint` (default false), `openWorldHint` (default true), `title`. Clients MUST treat them as untrusted hints. | Set all four explicitly on every tool (section 6). `openWorldHint: false` everywhere (closed world: local repos). |
| Results | Every result has `resultType` (`"complete"` or `"input_required"`). List and read results carry `ttlMs` and `cacheScope` (`CacheableResult`). | SDK fills `resultType`. Use `cacheScope: "private"` and a short `ttlMs` on `resources/read`. |
| Errors | Two kinds: **protocol errors** (JSON-RPC: unknown tool or bad params -32602, internal -32603, header mismatch -32020) and **tool execution errors** (`isError: true` in a normal result, with actionable text the model can use to retry). Resource not found is now -32602 (was -32002). | Business-rule failures (promote gate, closed project, strict time) are tool execution errors, not JSON-RPC errors (section 5.4). |
| Resources | `resources/list`, `resources/read`, `resources/templates/list`; subscriptions moved to `subscriptions/listen`. | Two read-only resources, no subscriptions. |
| Prompts | Optional, user-controlled templates. | None in v1 (section 6.3). |
| Server-to-client requests | Replaced by Multi Round-Trip Requests (`InputRequiredResult`). Roots, Sampling, Logging are **deprecated**. | Not used. Log to stderr / the existing `log` output. |
| stdio | Newline-delimited JSON-RPC on stdin/stdout; stdout MUST carry only MCP messages; log to stderr; exit on stdin EOF. | Relevant only if stdio ships later (section 2). |
| Streamable HTTP | One endpoint, **POST only** (GET/DELETE -> 405). Each POST is one request; reply is `application/json` or a per-request SSE stream. Client sends `MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name` headers; server MUST reject header/body mismatch with 400 + -32020. No session ids, no resumability. | `/mcp` route, stateless, JSON responses. |
| Security | Servers **MUST validate `Origin`** on every request; present and invalid -> **403**. When local, SHOULD bind 127.0.0.1. SHOULD authenticate. Tools: servers MUST validate inputs, implement access control, **rate limit**, sanitize outputs. OAuth applies to remote servers. | Origin allowlist + loopback-only peer check + Host check + a small rate limiter (section 2). No OAuth (out of scope). |

---

## 2. Transport and launch

### Recommendation: Streamable HTTP at `/mcp`, in the running app

```
hate -no-browser                 # or the normal app start; /mcp is always mounted
claude mcp add --transport http --scope user hate http://127.0.0.1:8000/mcp
```

Why HTTP in-process and not a stdio subprocess (the deciding fact):
**the project write lock is process-local.** `ticket.LockProject` is a
`sync.Mutex` in a map (internal/ticket/lock.go), and the auto-push hook
(`ticket.AfterCommit = teamsync.Default.Committed`) and the periodic pulls run
only inside the process that called `teamsync.Default.Start()`. A separate
`hate mcp` process would write the same `.tkt/` files and run `git commit`
concurrently with the UI process with no shared lock (read-modify-write races
on ticket JSON, `index.lock` failures in git) and would skip auto-push. Mounting
`/mcp` on the existing router gives MCP calls exactly the lock, sync and commit
path the UI uses, for free.

Cost: the app must be running for the agent to use it. Claude Code shows the
server as failed when it is not; the fix is to start hate. That matches how
agents use hate today (curl against localhost:8000).

stdio is deferred, not rejected. If wanted later, ship `hate mcp` as a thin
**stdio-to-HTTP bridge** that forwards each line to `http://127.0.0.1:<port>/mcp`
(starting `hate -no-browser` in the background if nothing answers), so all
writes still go through the one process. Do not ship a stdio server that
calls the packages directly unless the lock becomes a cross-process file lock.

### Wiring into main.go

- Keep `askpassMode` first. If a `hate mcp` subcommand is ever added, dispatch
  it after `askpassMode` and note the trap: with `HATE_ASKPASS=1` in the
  environment, `hate mcp` (two args) is read as an askpass prompt. Check
  `args[1] == "mcp"` only when the askpass env is unset.
- New flag `-no-mcp` (and `$HATE_NO_MCP=1`) to leave `/mcp` unmounted. Default
  on.
- Mount after the API routes: `r.Handle("/mcp", mcpserver.Handler(...))`.
  Code lives in a new package `internal/mcpserver`, so main.go gains about
  five lines. The SDK handler is `mcp.NewStreamableHTTPHandler(getServer, opts)`
  with one shared `*mcp.Server`.
- The startup line prints the MCP URL:
  `MCP endpoint: http://127.0.0.1:8000/mcp`.

### Localhost and Origin rules for `/mcp`

Applied by a middleware in front of the SDK handler, in this order:

1. **Loopback peer only.** If the TCP peer (`r.RemoteAddr`) is not a loopback
   address, return 403. This holds even when the user starts hate with
   `-listen 0.0.0.0` for the REST API: MCP never leaves the machine.
2. **Host check (DNS rebinding).** The SDK's default localhost protection stays
   on (`DisableLocalhostProtection: false`): a loopback connection with a
   non-localhost `Host` header gets 403.
3. **Origin check.** No `Origin` header: allow (CLI clients such as Claude Code
   send none). `Origin` present: allow only `http://localhost:<port>`,
   `http://127.0.0.1:<port>`, `http://[::1]:<port>` (the hate UI itself);
   anything else -> 403 with a JSON-RPC error body that has no `id`. Also set
   `CrossOriginProtection: http.NewCrossOriginProtection()` (Go 1.25 stdlib)
   so browser `Sec-Fetch-Site: cross-site` requests are refused too.
   (Since go-sdk v1.6.0 cross-origin protection is off unless this is set.)
4. **Rate limit** (spec MUST): one process-wide token bucket, 20 calls/s,
   burst 40, stdlib only; over the limit -> 429. Generous for one agent, stops
   a runaway loop.
5. GET/DELETE -> 405 (the SDK does this in stateless mode).

No authentication token in v1 (localhost-only, single user, same trust as the
REST API, which has none). Open question 3 offers an optional bearer token.

---

## 3. Go library

| | modelcontextprotocol/go-sdk | mark3labs/mcp-go |
|---|---|---|
| Latest | v1.8.0 (2026-09-04) | v1.5.0 (2026-10-30) |
| 2026-07-28 support | Yes since v1.7.0 (2026-07-28). Also 2025-11-25, 2025-06-18, 2025-03-26, 2024-11-05. Over HTTP, 2026-07-28 is accepted only with `Stateless: true`. | Yes since v1.0.0 (2026-09-02). Also older revisions. |
| Ownership | Official MCP org SDK (co-maintained with the Go team). | Community, widely used. |
| Typed tools | `mcp.AddTool(server, &mcp.Tool{...}, func(ctx, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error))`; input and output schemas inferred from struct tags (`jsonschema:"..."`); arguments validated against the schema before the handler runs. | Builder API (`mcp.NewTool(..., mcp.WithString(...))`); typed handlers exist but are less central. |
| Testing | `mcp.NewInMemoryTransports()` for in-process client/server tests; `mcp.CommandTransport` and a Streamable client transport. | Has in-process clients too. |
| Fit | `go 1.25.0` in its go.mod; hate is on go 1.25.5. Uses `net/http` handlers, mounts on chi directly. | Also fits. |
| Churn | Escape-hatch `MCPGODEBUG` flags from v1.7/v1.8 are removed in v1.9.0; we use none. | Major version only reached 1.0 two months ago. |

**Pick the official go-sdk, pinned at v1.8.0.** It tracks the spec first, the
typed `AddTool` gives input and output schemas from the same structs the
service layer returns, and `Stateless: true` is exactly the 2026-07-28 model.
Older clients that still send `initialize` keep working: stateless mode
answers them with a per-request temporary session (verify in the test ticket,
CHILD-6 TC3).

---

## 4. API inventory (91 routes)

Source: `grep -n 'r\.\(Get\|Post\|Put\|Patch\|Delete\|Handle\)'` over
internal/api/*.go and main.go, including the new `GET /api/search`
(internal/api/search.go, HATE-z44n). Paths under `/{projectId}` are
`/api/projects/{projectId}/...`; `.../tickets/...` is
`/api/projects/{projectId}/tickets/...`.

Disposition: **T** = MCP tool (name), **R** = MCP resource, **UI** = stays
UI-only (reason). "P2" marks a phase-2 candidate.

### App and Git (internal/api/gitaccount.go, projects.go)

| # | Method + path | What it does | Disposition |
|---|---|---|---|
| 1 | GET /api/version | Running build version | UI: version is in MCP `serverInfo` |
| 2 | GET /api/git/check | Is git installed | UI: machine setup |
| 3 | GET /api/git-account | GitHub account status | UI: credentials never go through an agent |
| 4 | POST /api/git-account | Test + store a GitHub token | UI: credentials |
| 5 | POST /api/git-account/test | Re-check saved token | UI: credentials |
| 6 | DELETE /api/git-account | Sign out | UI: credentials |
| 7 | GET /api/github/repos | Repos the token can see | UI: onboarding |
| 8 | POST /api/github/clone | Clone a repo as a project | UI: creates folders on disk, human decision |
| 9 | GET /api/search | Cross-project ticket search by ID/title (HATE-z44n) | **T `hate_search_tickets`** |

### Projects, app level (projects.go)

| # | Method + path | What it does | Disposition |
|---|---|---|---|
| 10 | GET /api/projects | List projects with counts and health | **T `hate_list_projects`** |
| 11 | POST /api/projects | Create a project on disk | UI: new repo is a human decision (open question 5) |
| 12 | GET /api/projects/settings | Global app settings | UI: app config |
| 13 | PUT /api/projects/settings | Update global settings | UI: app config |
| 14 | POST /api/projects/open | Register a folder as a project | UI: filesystem path from a human |
| 15 | GET /api/projects/hidden | List hidden projects | UI: sidebar housekeeping |
| 16 | POST /api/projects/hide | Hide a project | UI: sidebar housekeeping |
| 17 | POST /api/projects/unhide | Unhide a project | UI: sidebar housekeeping |

### Project config and lifecycle (projects.go)

| # | Method + path | What it does | Disposition |
|---|---|---|---|
| 18 | GET /{projectId} | Project details | **T `hate_get_project`** |
| 19 | GET /{projectId}/sync-status | Ahead/behind vs remote (fetches) | UI: sync is automatic |
| 20 | POST /{projectId}/sync | Fetch, merge, push now | UI: agents must not drive pushes |
| 21 | GET /{projectId}/autosync | Header status light | UI: display only |
| 22 | POST /{projectId}/autosync/open | Start background sync on open | UI: app lifecycle |
| 23 | GET /{projectId}/git-status | Working-tree status | UI: git internals |
| 24 | GET /{projectId}/git-identity | Project git author | UI: used internally for default author |
| 25 | POST /{projectId}/git-identity | Set git author | UI: identity is the human's |
| 26 | GET /{projectId}/resources | Team members + capacity | **T** folded into `hate_get_project` (assignee emails) |
| 27 | POST /{projectId}/resources | Add a resource | UI: staffing decision |
| 28 | PATCH /{projectId}/resources/{email} | Update a resource | UI: staffing decision |
| 29 | DELETE /{projectId}/resources/{email} | Remove a resource | UI: staffing decision |
| 30 | GET /{projectId}/whoami | Current user for the project | UI: MCP uses `author` / clientInfo |
| 31 | GET /{projectId}/effort-to-days | Legacy effort map | UI: legacy |
| 32 | PUT /{projectId}/effort-to-days | Set legacy map | UI: legacy |
| 33 | GET /{projectId}/hour-budget | Three hour pools | UI: PM budget (P2 read) |
| 34 | PUT /{projectId}/hour-budget | Set pools | UI: PM budget |
| 35 | GET /{projectId}/strict-time | Strict time flag | **T** folded into `hate_get_project` (agent must know the gate) |
| 36 | PUT /{projectId}/strict-time | Set flag | UI: project policy |
| 37 | GET /{projectId}/enforce-qa | Enforce-QA flag | **T** folded into `hate_get_project` |
| 38 | PUT /{projectId}/enforce-qa | Set flag | UI: project policy |
| 39 | GET /{projectId}/overview | Contacts, links, instructions | **R `hate://projects/{project}/overview`** |
| 40 | PUT /{projectId}/overview | Replace overview | UI: human-curated |
| 41 | POST /{projectId}/close | Close project | UI: lifecycle decision |
| 42 | POST /{projectId}/reopen | Reopen project | UI: lifecycle decision |
| 43 | PATCH /{projectId}/info | Rename project | UI: cosmetic |

### PM, baseline, scheduling, COSMIC (pm.go, forecast.go, blocks.go, cosmic.go)

| # | Method + path | What it does | Disposition |
|---|---|---|---|
| 44 | GET /{projectId}/snapshot | Latest slip snapshot | UI: PM ceremony |
| 45 | POST /{projectId}/snapshot | Take a snapshot, commit slips | UI: PM ceremony (P2, guide section 14 boundary step) |
| 46 | GET /{projectId}/dashboard | HTML PM dashboard | UI: HTML |
| 47 | GET /{projectId}/gantt.drawio | draw.io Gantt download | UI: binary download |
| 48 | GET /{projectId}/export.xlsx | Excel export | UI: binary download |
| 49 | POST /{projectId}/baseline | Baseline from template | UI: PM decision |
| 50 | POST /{projectId}/baseline-now | Baseline from current tickets | UI in v1 (P2: block planning in guide section 14) |
| 51 | POST /{projectId}/rebaseline | Replace baseline with reason | UI: PM decision, archives history |
| 52 | GET /{projectId}/baselines | Archived baselines | UI: PM history |
| 53 | POST /{projectId}/report | Not implemented (501) | UI: nothing to expose |
| 54 | GET /{projectId}/slip | Slip events | UI in v1 (P2 read) |
| 55 | PATCH /{projectId}/slip/{slipEventId} | Resolve a slip | UI: accountability is human |
| 56 | GET /{projectId}/requested-dates | Client's requested dates | UI in v1 (P2 read) |
| 57 | PUT /{projectId}/requested-dates | Set requested dates | UI: contract dates |
| 58 | GET /{projectId}/target-date | Alias of requested end | UI: legacy alias |
| 59 | PUT /{projectId}/target-date | Set alias | UI: legacy alias |
| 60 | GET /{projectId}/forecast | Schedule vs request (records history) | UI in v1: this GET writes and commits history (P2 tool, not read-only) |
| 61 | GET /{projectId}/phase-rollup | % complete per phase | UI in v1 (P2 read) |
| 62 | GET /{projectId}/block-weeks | Block length (2/3/null) | **T** folded into `hate_get_project` (decides phase convention, guide section 8) |
| 63 | PUT /{projectId}/block-weeks | Set block length | UI: project policy |
| 64 | GET /{projectId}/blocks | Planning blocks | UI in v1 (P2 tool for block planning) |
| 65 | GET /{projectId}/ready | Work order: ready / next / stages | **T `hate_get_ready`** |
| 66 | GET /{projectId}/test-summary | Test-case tallies | UI: agent reads cases via `hate_get_ticket` |
| 67 | GET /{projectId}/cosmic | COSMIC report + Monte Carlo | UI in v1 (P2 read) |
| 68 | PUT /{projectId}/cosmic-estimate | Monte Carlo inputs | UI: estimation policy |

### Tickets (tickets.go)

| # | Method + path | What it does | Disposition |
|---|---|---|---|
| 69 | GET .../tickets | List with filters, estimates, at_risk | **T `hate_list_tickets`** |
| 70 | POST .../tickets | Create one ticket | **T `hate_create_tickets`** (batch of 1..50) |
| 71 | GET .../tickets/billing | Billing rollup | UI: money view |
| 72 | GET .../tickets/{ticketId} | Full ticket + workflow + estimate | **T `hate_get_ticket`** |
| 73 | PATCH .../tickets/{ticketId} | Edit one field | **T `hate_update_ticket`** (several fields, one commit) |
| 74 | POST .../{ticketId}/promote | Next status (gates) | **T `hate_promote_ticket`** |
| 75 | POST .../{ticketId}/demote | Previous status | **T `hate_demote_ticket`** |
| 76 | POST .../{ticketId}/block | Mark blocked with reason | **T `hate_block_ticket`** |
| 77 | POST .../{ticketId}/force-close | Close skipping workflow | **T `hate_force_close_ticket`** |
| 78 | POST .../{ticketId}/comment | Add a comment | **T `hate_add_comment`** |
| 79 | POST .../{ticketId}/time | Log time | **T `hate_log_time`** |
| 80 | DELETE .../{ticketId}/time/{entryId} | Delete a time entry | UI: correcting billed time is a human call |
| 81 | POST .../{ticketId}/test-cases | Add one test case | **T `hate_add_test_cases`** |
| 82 | POST .../{ticketId}/test-cases/bulk | Add several | **T `hate_add_test_cases`** |
| 83 | PATCH .../{ticketId}/test-cases/{caseId} | Edit / record pass-fail | **T `hate_update_test_case`** |
| 84 | DELETE .../{ticketId}/test-cases/{caseId} | Delete a test case | UI: deleting acceptance criteria is a human call |
| 85 | POST .../{ticketId}/predecessors | Add predecessor | **T `hate_link_tickets`** |
| 86 | DELETE .../{ticketId}/predecessors/{predecessorId} | Remove predecessor | **T `hate_link_tickets`** |
| 87 | POST .../{ticketId}/attachments | Upload (multipart) | UI in v1 (P2: base64 blob argument, size cap) |
| 88 | GET .../{ticketId}/attachments/{attachmentId} | Download | UI in v1 (P2 resource for mockups) |
| 89 | DELETE .../{ticketId}/attachments/{attachmentId} | Delete | UI |

### Static (main.go)

| # | Method + path | What it does | Disposition |
|---|---|---|---|
| 90 | GET / | index.html | UI |
| 91 | GET /static/* | Embedded assets | UI |

Tally: 23 routes map to 17 of the 18 tools (several fold together), 1 route to a
resource, 67 stay UI-only (11 of them flagged P2). The 18th tool,
`hate_edit_comment`, uses the new `PATCH .../comments/{commentId}` (CHILD-8).

---

## 5. Gap analysis

### 5.1 API gaps an agent hits

| # | Gap | Why an agent needs it | Proposed shape |
|---|---|---|---|
| G1 | **Children of a parent.** `GET .../tickets` filters by exact tag, so `?tag=parent:HATE-x` works only if the caller knows the tag syntax. | Every planning and review step asks "what are the children of this feature, and are they all classed and estimated". | `GET .../tickets?parent=HATE-x` (sugar for the tag match). MCP: `parent` argument on `hate_list_tickets`. |
| G2 | **Bulk create with intra-batch links.** One POST per ticket; children need the parent id and predecessors need sibling ids that do not exist yet; test cases are a second call per ticket. A 7-ticket feature is about 20 calls. | Agents create whole features (guide sections 2, 9, 13). | `POST .../tickets/bulk` `{tickets:[{ref:"p", type, title, ..., parent_ref?:"p", predecessor_refs?:["c1"], test_cases?:[{step,expected}]}], author?}`. Refs resolve to real ids in order; all tickets validated before any write; one commit for the batch. Returns `[{ref, id}]` plus the tickets. Max 50. |
| G3 | **Multi-field edit.** `PATCH` takes `{field, value}`: tagging and estimating one child is 3 calls and 3 commits. Tags are replaced whole, so adding one tag means read, modify, write (racy across agents). | Classing and estimating children, setting phase and dates during block planning. | `PATCH .../tickets/{id}` also accepts `{fields:{title?, description?, priority?, estimate_hours?, assignee?, phase?, planned_start_date?, due_date?}, add_tags?:[], remove_tags?:[], author?}`. All-or-nothing in one read-modify-write (new `ticket.EditFields`), one activity entry per field, one commit. Old `{field,value}` body unchanged. |
| G4 | **Project by prefix / ticket by id alone.** `{projectId}` must be the project id or folder name; agents usually know only `HATE-1a3s`. | Every ticket tool. | `config.GetProjectPath` also matches prefix (case-insensitive) when no id matches. MCP tools take `project` optional when `ticket_id` is given: the service resolves it from the prefix and returns an `ambiguous_project` error listing candidates when two projects share a prefix (the two KC projects do). |
| G5 | **The agent guide is not reachable from the binary.** docs/ticketing-and-cfp-guide.md is the contract for tags and estimates, but it is not embedded and has no route. | The agent must follow section 1-3 rules to pass the promote gate. | Embed the file in package main (`//go:embed docs/ticketing-and-cfp-guide.md`), pass it to api and mcpserver. `GET /api/guide` returns `text/markdown`; MCP resource `hate://guide`. Optional `?section=3`. |
| G6 | **Search covers only ID and title** (HATE-z44n, max 50). | Finding "the ticket about X" by description. | Not in v1. Later: `GET /api/search?q=&fields=description,tags`. MCP tool keeps the same arguments so it can grow. |
| G7 | **Business rules live in HTTP code.** The closed-project block is a chi middleware (`blockWritesWhenProjectClosed`), the strict-time gate is inline in `handleAddTimeEntry`, estimates (`ticketEstimates`) and `at_risk` are computed in `handleListTickets`. An MCP handler that calls `internal/ticket` directly would skip all three. | Identical behavior across REST and MCP (ticket requirement). | Extract `internal/service` (5.2). |
| G8 | **Errors are HTTP-shaped.** Promote returns 422 with `needs_time_log` / `estimate_invalid` flags; time log returns 409 with `needs_time_extension`. | MCP needs typed errors to build `isError` results the model can act on. | Service returns typed errors (5.4); each transport maps them. |
| G9 | **Author default.** REST falls back to a system value. | Activity log should say an agent did it. | MCP: optional `author` argument; default `<clientInfo.name>@mcp` (for example `claude-code@mcp`), else `mcp@hate.local`. Open question 4. |

### 5.2 Refactor: `internal/service`

How tickets.go handlers are built today (all 20 write handlers follow it):

```
root := getProjectRoot(w, id)            // config.GetProjectPath, 404
defer ticket.LockProject(root)()         // process-local mutex, taken ONCE here
decode body
[business rule inline: strict time, effort retired, estimate_hours validation]
tk, err := ticket.X(root, ...)           // internal/ticket mutator
ticket.RegenerateIndex(root)             // derived index.json
respondTicket(w, tk, commitTicket(...))  // ticket.CommitFiles -> AfterCommit -> teamsync push
```
plus the `blockWritesWhenProjectClosed` middleware wrapping the whole route
group.

Refactor (behavior-preserving, done first):

- New package `internal/service` with one function per operation, taking plain
  Go structs and returning `(*TicketView, error)`. `TicketView` = ticket +
  `workflow` + `estimated_hours` / `estimate_source` + `commit_warning`.
  Functions: `ResolveProject(ref)`, `ResolveTicket(projectRef, ticketID)`,
  `ListProjects`, `GetProject`, `ListTickets(filter)`, `GetTicket`,
  `CreateTickets(batch)`, `EditTicket(fields, addTags, removeTags)`,
  `Promote`, `Demote`, `Block`, `ForceClose`, `AddComment`, `LogTime`,
  `DeleteTimeEntry`, `AddTestCases`, `UpdateTestCase`, `DeleteTestCase`,
  `AddPredecessor`, `RemovePredecessor`, `Ready`, `Guide`.
- Each mutating service function is the lock's "outermost entry point": it
  checks the closed flag, takes `ticket.LockProject` once, calls the
  `internal/ticket` mutator, regenerates the index and commits. Handlers stop
  locking. Update the rule comment in internal/ticket/lock.go to name
  `internal/service` as the entry point.
- Move into the service: the closed-project check (the middleware becomes a
  thin wrapper or goes away), the strict-time gate, effort-retired and
  estimate_hours validation, `ticketEstimates`, the `at_risk` computation.
- REST handlers become: decode -> service -> map error to status -> JSON.
  Response bodies must not change (the existing internal/api tests are the
  guard; run them unchanged).
- MCP handlers: typed input -> service -> typed output or `isError` result.
- Leave PM, project-config, git and attachment handlers alone in v1; only the
  operations MCP exposes move. Attachments keep their handler code.

### 5.3 Behavior that must stay identical (and is tested on both transports)

Promote gates (`ValidatePromoteEstimate`, `ErrNeedsTimeLog`, enforce-QA test
cases), closed project rejects writes, strict time 409 rule, effort retired,
quarter-hour `estimate_hours`, one auto-commit per mutation with
`commit_warning` on failure, `AfterCommit` push, index regeneration.

### 5.4 Error mapping

| Service error | REST (unchanged) | MCP |
|---|---|---|
| project / ticket not found | 404 `{detail}` | `isError`, text `{"error":"not_found","detail":...}` |
| ambiguous prefix (new) | 409 | `isError`, `{"error":"ambiguous_project","candidates":[...]}` |
| project closed | 423 | `isError`, `{"error":"project_closed"}` |
| `ticket.ErrNeedsTimeLog` | 422 + `needs_time_log` | `isError`, `{"error":"needs_time_log","detail":...,"next":"call hate_log_time, then retry"}` |
| `*ticket.EstimateValidationError` | 422 + `estimate_invalid` | `isError`, `{"error":"estimate_invalid","detail":...}` (detail names the rule a-d) |
| time extension needed | 409 + `needs_time_extension` | `isError`, `{"error":"needs_time_extension","allotted_hours","spent_hours","would_be_hours"}` |
| validation (bad date, retired effort, bad enum) | 400 / 422 | `isError`, `{"error":"invalid_input","detail":...}` |
| commit failed | 200 + `commit_warning` | success, `commit_warning` in output |
| arguments fail the input schema | n/a | JSON-RPC -32602 (SDK) |
| panic / unexpected | 500 | JSON-RPC -32603 |

Error results carry the JSON in a text block and no `structuredContent`, so
`outputSchema` only ever describes success.

---

## 6. Tool catalog

Conventions: names `hate_<verb>_<noun>` (unique when Claude Code aggregates
servers). Every ticket tool takes `ticket_id` and optional `project` (id,
folder or prefix). Every write tool takes optional `author` (default: the project's git identity). Success outputs
are typed structs (SDK emits `outputSchema` + `structuredContent` + text).
`openWorldHint: false` on all. Registration order is fixed (deterministic
`tools/list`).

`TicketSummary` = `{id, project_id, title, type, status, priority, assignee,
phase, tags, parent, class, estimate_hours, estimated_hours, estimate_source,
at_risk}`. `TicketView` = full ticket JSON + `workflow {promote_to, demote_to,
path}` + `estimated_hours` + `estimate_source` + `commit_warning?`.

### 6.1 Read-only tools (`readOnlyHint: true`, `idempotentHint: true`)

| Tool | Input schema (required in bold) | Output |
|---|---|---|
| `hate_list_projects` | `{include_closed?: bool = true}` | `{projects:[{id, name, client, prefix, path, closed, ticket_count, health}]}` |
| `hate_get_project` | `{**project**: string}` | `{id, name, prefix, path, closed, block_weeks, strict_time_enforcement, enforce_qa, ticket_count, resources:[{name, email, role, daily_hours_available}]}` |
| `hate_search_tickets` | `{**q**: string (1-200), limit?: int 1-50 = 20}` | `{results:[{project_id, project_name, prefix, ticket_id, title, status, type, closed_project}]}` (same ranking as GET /api/search) |
| `hate_list_tickets` | `{**project**, status?, type?, assignee?, tag?, phase?, parent?: string, include_backlog?: bool = true, limit?: int = 200}` | `{tickets:[TicketSummary], total}` |
| `hate_get_ticket` | `{**ticket_id**, project?}` | `TicketView` (description, test_cases, time_entries, activity, attachments metadata) |
| `hate_get_ready` | `{**project**}` | `{ready:[...], next:[...], stages:[{stage, hours, tickets}]}` (same as GET /ready) |

### 6.2 Write tools (`readOnlyHint: false`)

| Tool | Input schema (required in bold) | Output | destructive / idempotent |
|---|---|---|---|
| `hate_create_tickets` | `{**project**, **tickets**: [1..50 of {ref?, **type** enum(task, dev_task, design_task, meeting, administration, defect...), **title**, description?, priority?, estimate_hours? (multiple of 0.25, >= 0.25), assignee?, tags?: [string], phase?, planned_start_date?, due_date? (YYYY-MM-DD), parent_ref? \| parent_id?, predecessor_refs?: [ref], predecessors?: [id], test_cases?: [{**step**, **expected**}]}], author?}` | `{created:[{ref, id}], tickets:[TicketSummary], commit_warning?}` | false / false |
| `hate_update_ticket` | `{**ticket_id**, project?, fields?: {title?, description?, priority?, estimate_hours? (number\|null), assignee?, phase?, planned_start_date?, due_date?}, add_tags?: [string], remove_tags?: [string], author?}` (at least one change) | `TicketView` | false / true |
| `hate_promote_ticket` | `{**ticket_id**, project?, author?}` | `TicketView` (new status, next `workflow`) | false / false |
| `hate_demote_ticket` | `{**ticket_id**, project?, author?}` | `TicketView` | false / false |
| `hate_block_ticket` | `{**ticket_id**, project?, **reason**: string, author?}` | `TicketView` | false / true |
| `hate_force_close_ticket` | `{**ticket_id**, project?, **reason**: string (min 5), author?}` | `TicketView` | **true** / true |
| `hate_log_time` | `{**ticket_id**, project?, **hours**: number (0.25 steps, > 0, <= 24), **description**: string (non-empty), date?: YYYY-MM-DD = today, extend_authorized?: bool, extend_reason?: string, author?}` | `TicketView` + `{logged_hours_total, allotted_hours?}` | false / false |
| `hate_add_comment` | `{**ticket_id**, project?, **message**: string, author?}` | `{ticket_id, activity_count, commit_warning?}` | false / false |
| `hate_edit_comment` | `{**ticket_id**, project?, **comment_id**: string, **message**: string (non-empty), author?}` | `{ticket_id, comment:{id, author, message, edited_at}, commit_warning?}` | false / true |
| `hate_add_test_cases` | `{**ticket_id**, project?, **cases**: [1..20 of {**step**, **expected**}], author?}` | `{ticket_id, test_cases:[{id, step, expected, status}], commit_warning?}` | false / false |
| `hate_update_test_case` | `{**ticket_id**, project?, **case_id**, step?, expected?, status?: enum("", pass, fail), comment?, author?}` | `{ticket_id, test_case:{...}, commit_warning?}` | false / true |
| `hate_link_tickets` | `{**ticket_id**, project?, add_predecessors?: [id], remove_predecessors?: [id], author?}` | `TicketView` | false / true |

Tool descriptions carry the rules the model needs at call time, for example
`hate_promote_ticket`: "Leaving a work status needs a described time entry
logged since entering it (call hate_log_time first). The first promote out of
not_started checks class/estimate rules; see resource hate://guide section 3."
`hate_force_close_ticket` says it descopes the ticket and is not the normal
way to finish work.

Not exposed in v1, on purpose: deleting time entries and test cases,
attachments, project create/close, settings, baselines, slips, sync, git
account. Each needs a human or is a PM ceremony (section 4).

### 6.3 Resources and prompts

| URI | MIME | Content |
|---|---|---|
| `hate://guide` (template `hate://guide{?section}`) | text/markdown | The embedded agent guide, whole or one `## N.` section. `cacheScope: private`, `ttlMs: 3600000` (changes only with the binary). |
| `hate://projects/{project}/overview` (template) | application/json | Contacts, links and instructions from GET /overview. `ttlMs: 60000`. |

Prompts: none in v1. A `plan_feature` prompt (spec text in, parent + children
draft out, using guide sections 1-4) is a good phase-2 addition once the tools
are proven; it is user-invoked, so it does not help the autonomous loop.

---

## 7. Ticket breakdown (ready to create in tkt-hate)

Conventions applied (guide sections 1-3): one parent carries `cfp:` and
`type:feature`, no `estimate_hours`; every child carries `parent:<id>` and
exactly one class tag; config/nonfunc children carry `estimate_hours` in
quarter-hour steps; functional children carry none; no child has `cfp:`.
Phase for all: `14 - MCP server`. Creator: `claude@hate.local`. IDs below
(`CHILD-n`) are placeholders; with `hate_create_tickets` (or G2) they become
`ref`s.

### Parent

- **Title:** Let agents work hate tickets through an MCP server built into the binary
- **type:** task. **tags:** `type:feature`, `cfp:21`, `mcp`. **priority:** high.
- **Description:** Context: agents use hate via curl and the agent guide; an
  MCP server in the same binary gives them typed tools. Scope: as specified in
  docs/plan-mcp-server.md (transport `/mcp`, go-sdk v1.8.0, 18 tools, 2
  resources, REST gaps G1-G5 plus comment editing, `internal/service`
  refactor). Out of scope: stdio (backlog bridge ticket), remote hosting, auth
  beyond localhost, PM/baseline tools, attachments.

  CFP: 21, counted technology-independently (decision 1, section 9). A
  functional process reached through a new channel is the same process, so
  the MCP tools that wrap existing REST processes add no size; their work is
  logged on non-functional children (CHILD-1, CHILD-4, CHILD-5). Only new
  processes and the changed movements of existing ones count. Search is
  already sized on HATE-z44n; the overview and ready list already exist.

  ```
  FP1  Create ticket batch (new)      E ticket; E test case; R project; R ticket; W ticket;
                                      W test case; X created list; X errors          = 8
  FP2  Get agent guide (new)          E request (section); R guide; X guide text; X errors = 4
  FP3  Edit comment (new)             E comment edit; R project; R ticket; W comment;
                                      X comment; X errors                            = 6
  FP4  List tickets (changed)         E filter gains parent                          = 1
  FP5  Edit ticket (changed)          E changes: multi-field body + tag add/remove   = 1
  FP6  Resolve project ref (changed)  E project ref accepts a prefix                 = 1
  Total                                                                              = 21
  ```

### Children

**CHILD-1 Extract a ticket service layer that REST and MCP both call**
- type: dev_task. tags: `parent:<P>`, `nonfunc`. estimate_hours: 4. predecessors: HATE-1a3s.
  (Refactor: no new functional size, so it is wrap hours, not CFP.)
- Description: Context: MCP tools must behave exactly like the REST API, but
  the closed-project block (chi middleware `blockWritesWhenProjectClosed`), the
  strict-time gate (inline in `handleAddTimeEntry`) and the estimate /
  `at_risk` computation (in `handleListTickets`, `ticketEstimates`) live in
  HTTP code. Scope: add package `internal/service` with one function per
  ticket operation listed in docs/plan-mcp-server.md section 5.2; each mutator
  checks the closed flag, takes `ticket.LockProject` once, calls the
  `internal/ticket` mutator, regenerates the index and commits, returning the
  ticket view plus `commit_warning`. Return typed errors (`ErrNotFound`,
  `ErrProjectClosed`, `*TimeExtensionError`, plus `ticket.ErrNeedsTimeLog`
  and `*ticket.EstimateValidationError`). Rewrite the ticket handlers in
  internal/api/tickets.go to decode, call the service and map errors to the
  same status codes and bodies as today. Update the lock rule comment in
  internal/ticket/lock.go. Rules: no REST response body or status changes; the
  lock is never taken twice in one call chain. Out of scope: PM, project
  config, git and attachment handlers; new endpoints (CHILD-2).
- Test cases:
  1. Run `go test ./internal/api/ ./internal/ticket/` on the branch with the
     test files unchanged from main. Expected: all tests pass.
  2. In a temp project (setupEstimateProject), call
     `service.Promote(root, "EST-x", "a")` on an in_progress dev_task with no
     time logged. Expected: `errors.Is(err, ticket.ErrNeedsTimeLog)` is true
     and the ticket file still shows `in_progress`.
  3. Close the temp project, then call `service.AddComment` on one of its
     tickets. Expected: `errors.Is(err, service.ErrProjectClosed)` and the
     ticket's activity count is unchanged.
  4. With strict time on and a config ticket with `estimate_hours: 1` and 1h
     logged, call `service.LogTime` with 0.5h and no extend reason. Expected:
     a `*service.TimeExtensionError` with allotted 1, spent 1, would_be 1.5.

**CHILD-2 Add the REST endpoints agents are missing: parent filter, multi-field edit, prefix lookup, bulk create, guide**
- type: dev_task. tags: `parent:<P>`, `functional`. predecessors: CHILD-1.
- Description: Context: agents creating and grooming features need several
  calls per ticket today (gaps G1-G5 in docs/plan-mcp-server.md section 5.1).
  Scope: (1) `GET .../tickets?parent=<id>` returns tickets tagged
  `parent:<id>`. (2) `PATCH .../tickets/{id}` also accepts
  `{fields:{...}, add_tags, remove_tags, author}`, applied all-or-nothing in
  one write via a new `ticket.EditFields`, one activity entry per field, one
  commit; the old `{field,value}` body keeps working. (3)
  `config.GetProjectPath` falls back to a case-insensitive prefix match; two
  projects with the same prefix -> 409 listing both ids. (4)
  `POST .../tickets/bulk` with `{tickets:[...], author}` (max 50) supporting
  `ref`, `parent_ref`, `predecessor_refs` and inline `test_cases`; validate
  every ticket before writing any; one commit; returns `[{ref,id}]` and the
  tickets. (5) Embed docs/ticketing-and-cfp-guide.md in package main and serve
  it at `GET /api/guide` as `text/markdown`, with `?section=N` returning only
  `## N.`. Add each endpoint to the section 12 table of the guide. Rules: all
  go through `internal/service`; the closed-project block applies to bulk
  create. Out of scope: search changes, attachments in bulk create.
- Test cases:
  1. With parent `EST-p` and children `EST-a` (tag `parent:EST-p`) and `EST-b`
     (no parent), `GET /api/projects/est/tickets?parent=EST-p`. Expected: 200
     and exactly one item, id `EST-a`.
  2. `PATCH /api/projects/est/tickets/EST-a`
     `{"fields":{"estimate_hours":0.3},"add_tags":["config"]}`. Expected: 422,
     and a GET shows neither the tag nor the estimate changed.
  3. `POST /api/projects/est/tickets/bulk` with
     `{"tickets":[{"ref":"p","type":"task","title":"Build X","tags":["cfp:4"]},{"ref":"c","type":"dev_task","title":"Code X","parent_ref":"p","tags":["functional"],"test_cases":[{"step":"s","expected":"e"}]}]}`.
     Expected: 200; the child has tag `parent:<id of p>` and one test case;
     `git log --oneline -1` in the project shows a single new commit.
  4. `curl -s http://127.0.0.1:8000/api/guide?section=3`. Expected: 200,
     `Content-Type: text/markdown`, body starts with `## 3. Tag conventions`.

**CHILD-3 Mount the MCP endpoint at /mcp with loopback, Host and Origin protection**
- type: task. tags: `parent:<P>`, `nonfunc`. estimate_hours: 3. predecessors: none.
- Description: Context: the MCP server runs inside the hate process so it
  shares the process-local project lock and the auto-push hook (section 2 of
  docs/plan-mcp-server.md). Scope: add `github.com/modelcontextprotocol/go-sdk`
  v1.8.0; new package `internal/mcpserver` exposing `Handler()` built with
  `mcp.NewStreamableHTTPHandler` and `StreamableHTTPOptions{Stateless: true,
  JSONResponse: true, CrossOriginProtection: http.NewCrossOriginProtection()}`;
  serverInfo name `hate`, version `config.AppVersion`; mount at `/mcp` in
  main.go after the API routes unless `-no-mcp` or `HATE_NO_MCP=1`; print the
  MCP URL at startup. Middleware: non-loopback peer -> 403; `Origin` present
  and not `http://localhost|127.0.0.1|[::1]:<port>` -> 403 with a JSON-RPC
  error body without id; token bucket 20/s burst 40 -> 429. Keep the SDK's
  localhost Host check on. Rules: `askpassMode` stays the first thing in main.
  Out of scope: tools (CHILD-4, CHILD-5), stdio.
- Test cases:
  1. Start `hate -no-browser`, then
     `curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:8000/mcp -H 'Origin: http://evil.example' -H 'Content-Type: application/json' -d '{}'`.
     Expected: `403`.
  2. `curl -s -X POST http://127.0.0.1:8000/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H 'MCP-Protocol-Version: 2026-07-28' -H 'Mcp-Method: server/discover' -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'`.
     Expected: 200 JSON whose supported versions include `2026-07-28` and
     whose server name is `hate`.
  3. Start `hate -no-browser -no-mcp` and POST the request from TC2. Expected:
     HTTP 404 or 405 from the router (no MCP handler).
  4. Start `hate -no-browser -listen 0.0.0.0` and POST the TC2 request from
     another machine to `http://<lan-ip>:8000/mcp`. Expected: `403`.

**CHILD-4 Implement the read-only MCP tools and resources**
- type: dev_task. tags: `parent:<P>`, `nonfunc`. estimate_hours: 3. predecessors: CHILD-1, CHILD-2, CHILD-3, HATE-z44n.
- Description: Context: an agent first needs to find projects and tickets.
  Scope: in `internal/mcpserver`, register with typed In/Out structs (schemas
  from struct tags) `hate_list_projects`, `hate_get_project`,
  `hate_search_tickets`, `hate_list_tickets`, `hate_get_ticket`,
  `hate_get_ready` and the resources `hate://guide{?section}` and
  `hate://projects/{project}/overview`, with the inputs, outputs and
  annotations in docs/plan-mcp-server.md sections 6.1 and 6.3. All call
  `internal/service` (or the existing search and ready functions); none call
  the REST API over HTTP. `project` accepts id, folder or prefix;
  `hate_get_ticket` infers the project from the ticket id prefix. Errors map
  per section 5.4. Rules: annotations `readOnlyHint: true, idempotentHint:
  true, openWorldHint: false`; fixed registration order. Out of scope: write
  tools.
- Test cases:
  1. Over an in-memory client against a temp project with ticket `EST-a`,
     call `hate_get_ticket {"ticket_id":"EST-a"}` with no project. Expected:
     `isError` false and `structuredContent.id == "EST-a"` with a `workflow`
     object.
  2. Call `hate_get_ticket {"ticket_id":"EST-zzzz"}`. Expected: `isError`
     true and the text contains `"error":"not_found"`.
  3. Call `tools/list` twice. Expected: identical name order both times, and
     every tool in this ticket has `annotations.readOnlyHint == true`.
  4. `resources/read` `hate://guide?section=3`. Expected: one text content,
     MIME `text/markdown`, starting `## 3. Tag conventions`.

**CHILD-5 Implement the write MCP tools on the shared service**
- type: dev_task. tags: `parent:<P>`, `nonfunc`. estimate_hours: 4. predecessors: CHILD-1, CHILD-2, CHILD-3, CHILD-8.
- Description: Context: agents create, groom and move tickets. Scope:
  register `hate_create_tickets`, `hate_update_ticket`, `hate_promote_ticket`,
  `hate_demote_ticket`, `hate_block_ticket`, `hate_force_close_ticket`,
  `hate_log_time`, `hate_add_comment`, `hate_edit_comment`, `hate_add_test_cases`,
  `hate_update_test_case`, `hate_link_tickets` with the schemas, outputs and
  annotations in docs/plan-mcp-server.md section 6.2. Each calls one
  `internal/service` function, so locks, gates, strict time, the closed
  block, index regeneration and auto-commit are the REST code path. Business
  failures return `isError` with the JSON in section 5.4 (for example
  `needs_time_log` with a `next` hint). Default author: the `author` argument, else the
  project's git identity (the same one the UI commits as). Rules: tool descriptions
  state the promote and time-log rules; `hate_force_close_ticket` has
  `destructiveHint: true`, all others false. Out of scope: deleting time
  entries or test cases, attachments.
- Test cases:
  1. In a temp project, create an in_progress dev_task `EST-a` with no time
     and call `hate_promote_ticket {"ticket_id":"EST-a"}`. Expected: `isError`
     true, text contains `"error":"needs_time_log"`, and the ticket file still
     says `in_progress`.
  2. Call `hate_log_time {"ticket_id":"EST-a","hours":0.5,"description":"wired handler"}`
     then `hate_promote_ticket` again. Expected: second call `isError` false
     and `structuredContent.status == "dev_complete"`.
  3. Call `hate_create_tickets` with a parent (`ref` p, tag `cfp:4`) and one
     child (`parent_ref` p, tag `functional`, two test cases). Expected:
     `created` has two ids; `git log --oneline -1` in the project shows one
     commit naming both ids or the batch.
  4. Close the project, then call `hate_add_comment` on `EST-a`. Expected:
     `isError` true with `"error":"project_closed"`; activity unchanged.

**CHILD-6 Add an MCP round-trip test suite over in-memory and HTTP transports**
- type: task. tags: `parent:<P>`, `nonfunc`. estimate_hours: 3. predecessors: CHILD-4, CHILD-5.
- Description: Context: the tools must be proven end to end at the protocol
  level, not only through unit tests. Scope: `internal/mcpserver/roundtrip_test.go`
  that (a) connects a go-sdk client to the server with
  `mcp.NewInMemoryTransports()`, (b) mounts the full chi router (REST + `/mcp`)
  on `httptest.NewServer` and connects with the go-sdk Streamable client, and
  runs the agent loop on a temp project (setupEstimateProject pattern, with
  `git init` so commits happen): list projects, create a feature with
  children, add test cases, log time, promote, read it back over REST and
  compare. Include a client pinned to protocol 2025-11-25 (sends
  `initialize`). Rules: no network, runs in `go test ./...`. Out of scope:
  Claude Code (CHILD-7).
- Test cases:
  1. Run `go test ./internal/mcpserver/ -run RoundTrip -v`. Expected: PASS for
     both the in-memory and the HTTP sub-tests.
  2. In the HTTP sub-test, after `hate_update_ticket` sets phase `02 - Build`,
     `GET /api/projects/est/tickets/EST-a`. Expected: the REST body has
     `"phase":"02 - Build"`, the same as the tool's `structuredContent.phase`.
  3. In the 2025-11-25 sub-test, call `tools/list` after `initialize`.
     Expected: the 18 hate tools are listed (the legacy era still works).

**CHILD-7 Validate the MCP server from Claude Code and document the setup**
- type: task. tags: `parent:<P>`, `nonfunc`. estimate_hours: 1.5. predecessors: CHILD-6.
- Description: Context: the real client is Claude Code on Chuck's machine.
  Scope: bump `AppVersion` in internal/config/config.go and build the dist
  binary; run `hate -no-browser`; register with
  `claude mcp add --transport http --scope user hate http://127.0.0.1:8000/mcp`;
  in a Claude Code session on a scratch project, have the agent find a project,
  create a parent with two children, log time and promote one child, and check
  the ticket in the hate UI. Add a "Using hate from an MCP client" section to
  docs/ticketing-and-cfp-guide.md (the command, the tool list, that hate must
  be running) and a line to README.md. Out of scope: other MCP clients.
- Test cases:
  1. With hate running, run `claude mcp list`. Expected: `hate` shows as
     connected with the http transport.
  2. In Claude Code, ask "use hate to list my projects". Expected: the
     transcript shows a `hate_list_projects` call and the reply names the same
     projects as the hate sidebar.
  3. Ask Claude Code to promote an in_progress ticket that has no time logged.
     Expected: the agent receives `needs_time_log`, calls `hate_log_time`,
     retries, and the ticket in the UI shows the next status and the new time
     entry.
  4. Open Settings in the hate UI. Expected: the version shown matches the
     bumped `AppVersion`.

**CHILD-8 Add comment editing (REST, UI) with an edit trail**
- type: dev_task. tags: `parent:<P>`, `functional`. predecessors: CHILD-1.
- Description: Context: comments are append-only today. `POST
  .../tickets/{id}/comment` appends an activity entry with action `comment`,
  and activity entries have no id (internal/ticket/schema.go `Activity`), so a
  typo or a wrong statement in a comment can't be fixed. Scope: (1) add `id`
  (`c1`, `c2`, ... per ticket, like time entries) and `edited_at` to
  `Activity`; `AddComment` assigns the next id; comments without an id are
  given one on the ticket's next write (matched by position; ids are never
  reused). (2) `PATCH .../tickets/{id}/comments/{commentId}` `{message,
  author}` replaces the comment text, sets `edited_at`, and appends a
  `comment_edited` activity entry whose detail holds the comment id and the
  previous text, so history is kept; one commit. (3) In the ticket panel, an
  Edit link on each comment opens the text in place with Save / Cancel. (4)
  Add the endpoint to the guide's section 12 table. Rules: only entries with
  action `comment` can be edited (anything else -> 422 "only comments can be
  edited"); empty message -> 422; unknown comment id -> 404; closed project ->
  the usual closed-project block. Any user may edit any comment (hate has no
  per-user permissions). Out of scope: deleting comments (decided: edit only);
  editing system activity entries.
- Test cases:
  1. In a temp project, `POST /api/projects/est/tickets/EST-a/comment`
     `{"message":"teh fix"}`. Expected: the new activity entry has `id: "c1"`.
  2. `PATCH /api/projects/est/tickets/EST-a/comments/c1` `{"message":"the
     fix","author":"a@x"}`. Expected: 200; the c1 entry's detail is `the fix`
     with `edited_at` set, and a later `comment_edited` entry's detail contains
     `c1` and `teh fix`.
  3. PATCH the id of the ticket's `created` activity entry (give it one by
     editing the file). Expected: 422 with `only comments can be edited`.
  4. `PATCH .../comments/c99` `{"message":"x"}`. Expected: 404.
  5. `PATCH .../comments/c1` `{"message":"  "}`. Expected: 422 and the
     comment text is unchanged.
  6. In the UI, open EST-a, click Edit on the comment, change the text, click
     Save. Expected: the comment shows the new text with an "edited" marker,
     and reopening the panel still shows it.

**CHILD-9 (backlog) Add a "hate mcp" stdio bridge that forwards to the running app**
- type: task. tags: `parent:<P>`, `nonfunc`, `backlog`. estimate_hours: 3. predecessors: CHILD-7.
- Description: Context: v1 is HTTP-only, so hate must be running before an
  MCP client connects (decision 2, section 9). The bridge gives stdio
  convenience without a second writer. Scope: a `hate mcp` subcommand that
  never touches project files. It checks whether a hate app is listening on
  the configured port (`GET /api/version`); if not, it starts one in the
  background with `-no-browser` and waits up to 10 s; then it relays each
  JSON-RPC message from stdin to `http://127.0.0.1:<port>/mcp` and writes the
  replies to stdout. Register with `claude mcp add hate -- hate mcp`. Rules:
  the project lock stays in the one app process; if the app can't be reached
  or started, exit non-zero with a message on stderr. Out of scope: running
  the MCP server inside the stdio process.
- Test cases:
  1. With no hate running, run `claude mcp add hate -- hate mcp`, then
     `claude mcp list`. Expected: `hate` connected, and `lsof -iTCP:8000
     -sTCP:LISTEN` shows a hate process.
  2. With hate already running, start a Claude Code session using the stdio
     entry and list projects. Expected: the same projects as the sidebar, and
     still exactly one hate process listening.

Dependency order: CHILD-1 -> {CHILD-2, CHILD-8} -> {CHILD-4, CHILD-5} ->
CHILD-6 -> CHILD-7 -> CHILD-9 (backlog); CHILD-3 runs in parallel with
CHILD-1/2 and gates CHILD-4/5. Code (CHILD-2, CHILD-8) is sized by `cfp:21`
on the parent. Wrap: CHILD-1 4h, CHILD-3 3h, CHILD-4 3h, CHILD-5 4h,
CHILD-6 3h, CHILD-7 1.5h = 18.5h committed; CHILD-9 3h backlog.

---

## 8. Testing approach

| Layer | What | Where |
|---|---|---|
| Service unit tests | Each gate and rule called directly on a temp project: promote gate, estimate rules a-d, enforce-QA, closed block, strict time, bulk-create all-or-nothing, prefix ambiguity. | internal/service/*_test.go |
| REST regression | Existing internal/api tests unchanged must pass after the refactor; new tests for G1-G5. | internal/api |
| MCP handler tests | Each tool: happy path checks `structuredContent` against the declared output struct; each mapped error gives `isError` and the right `error` code; annotations as specified; `tools/list` order is stable. | internal/mcpserver |
| Round trip | go-sdk client over `NewInMemoryTransports` and over `httptest` Streamable HTTP with the full router; cross-check every write via REST GET; one legacy (2025-11-25) client. | internal/mcpserver/roundtrip_test.go |
| Security | Origin rejected (403), non-loopback peer rejected, Host mismatch rejected, GET -> 405, rate limit -> 429, `-no-mcp` unmounts. | internal/mcpserver middleware tests + CHILD-3 manual curls |
| Concurrency | 20 parallel `hate_log_time` calls on one ticket via HTTP alongside REST writes; all entries present, git log shows 20 commits (mirrors TestConcurrentTimeLogs). | round-trip suite |
| Manual | `claude mcp add --transport http --scope user hate http://127.0.0.1:8000/mcp`, then the CHILD-7 script. | Chuck's machine |

---

## 9. Decisions (Chuck, 2026-11-02)

1. **CFP counting:** technology-independent. An existing process reached
   through a new channel adds no size; MCP tool wrappers are non-functional
   hours. Parent is `cfp:21`. Applies to future integration features too.
2. **Transport:** HTTP only in v1; hate must be running. The `hate mcp` stdio
   bridge is a backlog ticket (CHILD-9).
3. **Bearer token:** none in v1; `/mcp` has the same localhost trust level as
   the REST API.
4. **Author:** optional `author` argument on write tools, defaulting to the
   project's git identity.
5. **Write scope:** force-close stays in v1, marked `destructiveHint: true`.
   Project create, baselines, block planning and attachments stay out.
6. **REST twins:** bulk create and `/api/guide` ship on REST as well as MCP.
7. **Comment editing:** added (CHILD-8): edit only, no delete, with an edit
   trail in the activity log; exposed as `hate_edit_comment`.

---

## Sources

- MCP versioning (current revision): https://modelcontextprotocol.io/specification/versioning
- 2026-07-28 changelog: https://modelcontextprotocol.io/specification/2026-07-28/changelog
- Tools: https://modelcontextprotocol.io/specification/2026-07-28/server/tools
- Streamable HTTP: https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http
- stdio: https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio
- go-sdk README and releases: https://github.com/modelcontextprotocol/go-sdk , https://github.com/modelcontextprotocol/go-sdk/releases
  (versions checked on proxy.golang.org: go-sdk v1.8.0 2026-09-04; mcp-go v1.5.0 2026-10-30)
- mcp-go releases: https://github.com/mark3labs/mcp-go/releases
- go-sdk v1.8.0 source read for `StreamableHTTPOptions` (Stateless, JSONResponse,
  DisableLocalhostProtection, CrossOriginProtection) and `ToolAnnotations`.
