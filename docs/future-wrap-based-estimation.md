# Future working point: wrap-based calibration & estimation

**Status: ideation / not yet built.** This captures a design direction we want to
iterate on. Nothing here is implemented. See [`ticketing-and-cfp-guide.md`](./ticketing-and-cfp-guide.md)
for the current CFP/COSMIC conventions this builds on.

---

## The premise: agentic coding inverts the cost model

Classic COSMIC treats **functional size as the cost driver** and wrap as an
overhead multiplier on top. In an agentic-coding workflow that inverts:

- **Code-like functional work is a near-constant.** Across four projects, anything
  generatable — CDK, Lambda code, CloudFormation, YAML — costs roughly
  **~0.015 h/CFP**, and it doesn't meaningfully vary project to project. It stops
  being a calibration target and becomes a constant — effectively a rounding error.
- **Wrap is the entire variable cost.** The non-code work — console click-ops,
  manual configuration, and the operational cost of deploying and validating — is
  what actually differs between projects.

So CFP stops being the thing you estimate. The estimation problem collapses onto
**wrap**, and the per-project-type profile (e.g. "IVR") is really a *wrap
bill-of-materials*, not an h/CFP number. The code rate is universal; what's
type-specific is which manual deliverables that project type drags along.

> Watch item: the constant holds for clean generation. **Code rework
> (deploy failures, debugging, iteration) is wrap in disguise** — variable, and it
> must be tracked separately so it doesn't quietly contaminate the "rock solid"
> code number.

## Three categories of work

| Category | What it is | Cost | In catalog? |
|---|---|---|---|
| **Author** | Generate code / IaC (the agent does it) | ~0.015 h/CFP constant | No |
| **Operate** | Deploy, run, validate, troubleshoot the generated artifacts | Variable (human time) | Yes |
| **Configure** | Irreducible console / manual deliverables | Variable (human time) | Yes |

Author is the constant. **Operate + Configure are "wrap"** — the variable cost the
estimator actually has to predict. "Operate" was the easy one to miss: running a
`cdk deploy`, watching it, validating resources, and fixing the failures is real,
variable human time even though the code that's being deployed was free to author.

## Platform-agnostic vocabulary, platform as a dimension

The deliverable types are **platform-agnostic archetypes**; the **platform is a
calibration dimension** taken from the project context, not a separate catalog.

- A `flow` is a `flow` whether it's an Amazon Connect contact flow or a Genesys
  Architect flow — the same *kind* of human work. Only the hours-per-unit differ.
- The rate lookup keys on **(platform, deliverable-type)**. A ticket carries only
  the abstract type; the project supplies the platform.
- If you only ever do Connect, you only ever see Connect rates. The first Genesys
  project starts a Genesys rate next to it automatically, and cross-platform
  comparison ("Genesys flows cost 1.4× Connect flows") falls out for free.
- Keeps the vocabulary small (~a dozen archetypes) instead of forking per platform.

## The catalog (controlled vocabulary)

An **org-level** catalog (shared across projects/clients — above
`.tkt/config.json`), each entry:

- `type` — the archetype name (controlled, not free text — fragmentation is the
  failure mode).
- `description`.
- `seed_hours` — a manual estimate used while N=0.
- measured **average hours/unit** + **N** (sample size), which supersede the seed as
  real tickets accrue. N is shown so you know how much to trust it.

### Strawman: Connect IVR (abstract types, Connect-seeded)

| Category | Type | Per | Seed h | Notes / platform mapping |
|---|---|---|---|---|
| Operate | `deploy` | stack | 0.5 | Run IaC, watch rollout, confirm resources |
| Operate | `deploy-troubleshoot` | incident | 0.75 | Failed deploys, perms, limits — the variance bucket |
| Operate | `smoke-validate` | scenario | 0.5 | Connect: place test calls; web: click the happy path |
| Configure | `flow` | flow | 0.75 | Connect contact flow / Genesys Architect flow |
| Configure | `bot` | bot | 1.5 | Lex / Dialogflow / Genesys NLU |
| Configure | `prompt` | prompt | 0.25 | Voice prompt authoring/upload |
| Configure | `queue` | queue | 0.25 | Routing/queue config |
| Configure | `integration-wiring` | hookup | 0.25 | Console wiring of a fn/service (Lambda assoc, connector) |
| Configure | `knowledge-base` | KB | 0.5 | Stand up + sync + verify |
| Configure | `knowledge-article` | article | 0.25 | Author + ingest |
| Provision | `instance` | instance | 0.5 | Connect instance / Genesys org |
| Provision | `number` | number | 0.25 | Claim/port telephony |

## Ticket granularity: purpose bifurcates

The old agile heuristic ("more than a day → split it") was a duration proxy.
Agentic coding collapses duration on the code side, so the proxy breaks. The
ticket's *purpose* now differs by work type:

- **Code tickets are units of *record*** — traceability of what the agent changed.
  Size them as "one coherent, reviewable change." Duration is irrelevant.
- **Wrap tickets are units of *measurement*** — one ticket per repeatable manual
  deliverable, because that's what yields hours-per-unit.

> Caveat: the tool rounds logged time to 0.25h (15 min). Many wrap units are
> <15 min (a single `prompt` ~10 min), so "one ticket per unit" either over-counts
> or can't resolve them. Sub-15-min units may need **batch-and-count** (N units on
> one ticket, count in a tag) instead of one-ticket-each. Open question.

## Calibration profiles

A **profile** (e.g. "IVR") = a name + source projects + the pooled wrap catalog
(per-type avg hours + N) and the code constant. Pool the raw data
(`Σ hours ÷ Σ units`), not an average of per-project rates. Store it cached with a
"recompute" action (stable like a baseline, but refreshable). Git-native:
`profiles/ivr.json`.

## The estimate

For a new project once its tickets exist:

```
code hours = total code-CFP × code_constant        (≈ a rounding error)
wrap hours = Σ over wrap deliverables (count × hours-per-unit from catalog)
total      = code hours + wrap hours                (reported as a low/likely/high range)
```

Then optionally chain hours → days (effort_to_days / resource capacity) → a
projected end date via the existing balance engine.

## Open questions to poke

1. **Sub-15-min rounding** — one-ticket-per-unit vs batch-and-count for tiny units.
2. **`deploy-troubleshoot` as a unit** vs just variance on `deploy` (modeling it
   separately makes the hidden iteration cost visible).
3. **Vocabulary altitude** — how coarse? (We collapsed ~6 Connect specifics into
   `flow`/`queue`/`integration-wiring`.)
4. **Platform as project-dimension vs ticket-level** — does one project ever span
   two platforms?
5. **Profile: frozen snapshot vs live-recompute** (lean: cached + refresh).
6. **Headline: single number vs range** (lean: range, given small N).
7. **Bootstrapping** — retag the existing four projects vs accumulate fresh
   (lean: retag; the hours are already logged).
