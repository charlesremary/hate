# Wrap taxonomy & catalog data model (HATE-b376)

**Status: settled design — the data contract HATE-k0gf implements.** This turns the
ideation in [`future-wrap-based-estimation.md`](./future-wrap-based-estimation.md)
into a concrete schema, a controlled vocabulary, and on-disk locations. Where this
doc and the ideation disagree, **this doc wins** (the differences are called out in
§7).

Builds on the CFP/COSMIC conventions in
[`ticketing-and-cfp-guide.md`](./ticketing-and-cfp-guide.md).

---

## 1. The wrap activity taxonomy (controlled)

Every unit of work is one of three **activities**:

| Activity | What it is | Cost model | Catalogued? | Priced by |
|---|---|---|---|---|
| **author** | Generate code / IaC (the agent does it) | ~constant h/CFP | No | the code constant (HATE-pz8j) |
| **operate** | Deploy, run, validate, troubleshoot the artifacts | variable | Yes | hours-per-unit |
| **configure** | Irreducible console / manual deliverables (incl. provisioning) | variable | Yes | hours-per-unit |

- **author** is the constant slice. It is **not** in the catalog — it's priced by
  `code_constant × CFP`.
- **operate + configure = "wrap"** — the variable cost the estimator predicts from
  the catalog. This is exactly the existing COSMIC `wrap %` numerator.
- The strawman's 4th category **"Provision"** (instance, number) folds into
  **configure** — it's console click-ops like the rest.

### Mapping from today's COSMIC class tags (no data loss)

The catalog reuses the existing child-class tags — it does **not** introduce a
parallel tagging scheme:

| Existing tag | New activity | Notes |
|---|---|---|
| `functional` | **author** | the in-the-loop code, priced by the constant |
| `config` | **configure** | |
| `nonfunc` | **operate** | hardening/perf/security is variable wrap; retag to a specific operate type (e.g. `smoke-validate`) where it fits |

This preserves the current `wrap % = (config + nonfunc) ÷ functional`: author is the
denominator, configure+operate is the numerator. Nothing the COSMIC tab computes
today changes.

A wrap ticket additionally carries a **deliverable-type tag** `wt:<type>` (HATE-qjz4)
naming its catalog archetype.

---

## 2. Deliverable archetypes (the controlled vocabulary)

Platform-agnostic. ~A dozen entries — **fragmentation is the failure mode**, so the
list is curated, not free text. Each archetype belongs to one activity and has a
countable unit.

| Activity | `type` | unit | seed h | Notes / platform mapping |
|---|---|---|---|---|
| operate | `deploy` | stack | 0.5 | Run IaC, watch rollout, confirm resources |
| operate | `deploy-troubleshoot` | incident | 0.75 | Failed deploys, perms, limits — the variance bucket |
| operate | `smoke-validate` | scenario | 0.5 | Connect: place test calls; web: click the happy path |
| configure | `flow` | flow | 0.75 | Connect contact flow / Genesys Architect flow |
| configure | `bot` | bot | 1.5 | Lex / Dialogflow / Genesys NLU |
| configure | `prompt` | prompt | 0.25 | Voice prompt authoring/upload |
| configure | `queue` | queue | 0.25 | Routing/queue config |
| configure | `integration-wiring` | hookup | 0.25 | Console wiring of a fn/service |
| configure | `knowledge-base` | KB | 0.5 | Stand up + sync + verify |
| configure | `knowledge-article` | article | 0.25 | Author + ingest |
| configure | `instance` | instance | 0.5 | Connect instance / Genesys org (was "Provision") |
| configure | `number` | number | 0.25 | Claim/port telephony (was "Provision") |

This is the **seed** catalog shipped on first run. It's editable (HATE-k0gf manager
UI) — the list above is the default, not a hard-coded constant.

---

## 3. Platform is a calibration dimension, not a vocabulary fork

- A ticket carries only the **abstract type** (`wt:flow`). The **project supplies
  the platform**.
- Rate lookups key on **(platform, type)**. Same archetype, different platform =
  different measured rate, same vocabulary.
- **Platform is project-level** (one platform per project for v1). It lives in
  `.tkt/config.json` as a new `platform` field (free-ish text, lower-cased, e.g.
  `connect`, `genesys`, `web`). Multi-platform projects are **out of scope for v1**
  (open Q4) — split into two projects if needed.

---

## 4. The catalog holds *definitions only* — measured numbers are computed

> **This is the one substantive change from the ideation** (which stored
> `measured_avg + N` inside each catalog entry). Storing measured aggregates in the
> catalog recreates the exact failure hate exists to kill: a second store that
> drifts from the tickets (README: "two systems that never sync"). So:
>
> - **Catalog = curated definitions** (slow-changing, human-edited): type, label,
>   activity, unit, description, `seed_hours`, optional per-platform seed overrides.
> - **Measured `avg_hours + n` = always computed live** from tagged tickets by the
>   aggregation engine (HATE-2b1x), keyed (platform, type).
> - **Profiles cache** a pooled snapshot for estimation (HATE-h4ad) with an explicit
>   recompute action — the *only* place a measured number is persisted, and it's
>   clearly a refreshable cache, not a source of truth.
>
> Tickets stay the single source of truth for hours; the catalog never goes stale.

### Catalog entry schema

```jsonc
{
  "type": "flow",                 // archetype id — controlled, kebab-case, unique
  "label": "Contact / Architect flow",
  "activity": "configure",        // "operate" | "configure"  (never "author")
  "unit": "flow",                 // countable unit noun, for "N flows"
  "description": "Author a routing/contact flow in the console.",
  "seed_hours": 0.75,             // manual estimate, used while measured n = 0
  "platform_seed_hours": {        // optional per-platform seed overrides (rare)
    "genesys": 1.0
  }
}
```

### Catalog file schema

```jsonc
{
  "schema_version": "1.0.0",
  "updated_at": "2026-06-30T00:00:00Z",
  "entries": [ /* CatalogEntry, ... */ ]
}
```

### Go types (the contract for HATE-k0gf)

```go
type CatalogEntry struct {
    Type              string             `json:"type"`
    Label             string             `json:"label"`
    Activity          string             `json:"activity"` // "operate" | "configure"
    Unit              string             `json:"unit"`
    Description       string             `json:"description"`
    SeedHours         float64            `json:"seed_hours"`
    PlatformSeedHours map[string]float64 `json:"platform_seed_hours,omitempty"`
}

type Catalog struct {
    SchemaVersion string         `json:"schema_version"`
    UpdatedAt     string         `json:"updated_at"`
    Entries       []CatalogEntry `json:"entries"`
}
```

Validation rules k0gf enforces: `type` unique + kebab-case + non-empty;
`activity ∈ {operate, configure}`; `seed_hours >= 0`.

---

## 5. On-disk location (org-level, git-native)

The catalog is **org-level** — shared across every project under the projects root —
so it lives **beside** the project folders, not inside any one of them:

```
<projects_root>/.pm-catalog/
  catalog.json            # the controlled vocabulary (this doc, §4)
  profiles/
    <name>.json           # cached calibration profiles (HATE-h4ad)
```

- Discoverable from the same `projects_root` that `config.ListProjects()` already
  scans — no new configuration.
- Git-native: `.pm-catalog/` can be its own git repo (or committed wherever the org
  keeps shared config) so teams sync it the same way they sync a project — push/pull.
- Seeded from §2 on first access if the file is absent.
- **Fallback:** if `projects_root` isn't writable, fall back to
  `~/.pm-agent/catalog.json`. (k0gf's call; prefer the projects-root location.)

---

## 6. How the pieces consume this model (forward references)

- **HATE-pz8j** — `code_constant` (the author price). A global, **configurable**
  setting; default seeded from measured data where it exists, **not** a hard-coded
  0.015 (see HATE-xd99: 0.015 is unvalidated; the measured spread is 0.06–0.17 with
  high variance). Show the spread, don't assert a number.
- **HATE-qjz4** — adds the `wt:<type>` tag to wrap tickets via a picklist driven by
  this catalog.
- **HATE-2b1x** — aggregates `Σ hours ÷ Σ units` per (platform, type) with N, from
  `wt:`-tagged tickets. Pool raw data, not an average-of-averages.
- **HATE-h4ad** — a **profile** = name + source projects + `code_constant` + the
  pooled per-(platform, type) `{avg_hours, n}`, cached with a recompute action.
- **HATE-y1wn / 4ze3** — estimate = `code_constant × ΣCFP` (author) +
  `Σ (count × hours-per-unit)` (wrap), reported as a low/likely/high range given
  small N.

---

## 7. What changed from the ideation, and open questions

**Settled here (differs from / firms up the ideation):**

1. Activity taxonomy fixed at **author / operate / configure**; "Provision" folds
   into configure; legacy `functional/config/nonfunc` map cleanly (§1).
2. Measured numbers are **computed from tickets, not stored in the catalog** (§4) —
   the principled fix.
3. Platform is **project-level, single-platform v1** (§3, closes the lean on Q4).
4. Catalog lives at **`<projects_root>/.pm-catalog/`** (§5).
5. Vocabulary altitude settled at the ~dozen archetypes in §2 (closes Q3).

**Still open (owned by later tickets, unaffected by this data model):**

- Sub-15-min rounding → batch-and-count vs one-ticket-per-unit (Q1; affects qjz4
  tagging shape).
- `deploy-troubleshoot` as its own unit vs variance on `deploy` (Q2; kept separate
  here so the hidden iteration cost stays visible).
- Profile frozen-snapshot vs live-recompute (Q5; lean cached+refresh, h4ad's call).
- Estimate headline single-number vs range (Q6; lean range, y1wn's call).
