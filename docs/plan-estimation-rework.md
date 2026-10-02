# Plan: estimation rework (CFP + wrap hours + Monte Carlo)

Status: PLAN ONLY. Nothing here is built yet. Review and decide the open
questions (end of file) before any code changes.

Supersedes: docs/future-wrap-based-estimation.md (that doc describes a
catalog/profile design that was built and then removed).


## 1. What we decided

- Keep CFP for sizing code, at the FEATURE level (cfp:N on the parent,
  as today).
- Use a rate RANGE for code, not a constant. Tactic suggests about
  0.25 h/CFP typical, with a wide spread (0.08 to 0.97).
- Use hours estimates for wrap tickets.
- Drop t-shirt sizing (effort xs..xl and the effort_to_days map).
- CFP is the standard, cross-project size for code. Consistency comes
  from a fixed, versioned counting guide (Phase 0.2), not from CFP
  itself.
- The COSMIC tab becomes a standard tab (no longer hidden behind the
  "experimental" setting).
- The max-hours cap stays MANUAL. The estimate is display-only; there is
  no "set cap from estimate" button.


## 2. The model

Agent generation of code is about zero. The hours on code tickets are the
human loop around it: prompt, review, run, debug, re-prompt. That is
"code wrap", and it scales (loosely) with the amount of code.

Two kinds of wrap, estimated differently:

  code wrap       testing/debugging/reviewing generated code
                  driver: amount of code
                  estimate: total CFP x rate range (Monte Carlo)
                  tickets: class "functional"

  platform wrap   console setup, deploys, managed services, manual steps
                  driver: number of items to set up
                  estimate: sum of hours estimates on wrap tickets
                  tickets: class "config" or "nonfunc"

  total = code wrap + platform wrap, shown as P50 / P85 / P95

The tag values (functional / config / nonfunc) stay the same so existing
data keeps working. Only the wording in docs and UI changes:
"functional" is described as "code: the test/debug loop on generated
code", not "writing code".

wrap% = (config + nonfunc) / functional stops being a headline number.
It is a ratio between two things that don't drive each other, and it
breaks as functional hours approach zero.


## 3. Phases

### Phase 0: validate before building (no hate code changes)

Goal: prove the approach works on real data before changing the tool.

0.1 Backtest Monte Carlo on Tactic (26 features with CFP and hours).
    One-off script outside the repo. Repeatedly split the features in
    half: use one half as the reference set, simulate the other half,
    and check how often the actual hours land inside the P10-P90 range.
    Target: about 80% of the time. If it's far off, the method or the
    data needs work before we build anything.

    RESULT (2026-10-02, Tactic, 24 features, 196 CFP, 55.2h functional;
    functional tickets at dev_complete counted as done):

                                  target   12 vs 12   18 ref vs 6
      actual inside P10-P90         80%       55%         64%
      actual at/below P50           50%       45%         50%
      actual at/below P85           85%       70%         74%

    - Center is right (P50 unbiased). Ranges are too narrow: raw "P85"
      behaves like P70-P75.
    - Widening the spread 1.5x (log-normal sd) put P85 at 87-93%.
      -> Phase 3 applies a 1.5x widening and says so on screen.
    - Single-average method: median miss 24%, worst 142%.
    - corr(CFP, hours) = 0.44: size explains ~20% of the variation; the
      rest is feature difficulty.
    - CFP vs counting features (P50 error): 22% vs 25% (12 ref),
      28% vs 33% (18 ref). CFP wins modestly inside one project, where
      feature sizes are similar (3-13 CFP). Its real value is across
      projects, which this test can't show yet.
    - Limits: one project only; 4 features with CFP but 0 functional
      hours were skipped.

0.1b Cross-project backtest. As soon as a second clean project in the
    same domain as Tactic has hours, estimate one project from the
    other. This is the test that shows whether CFP earns its keep.

0.2 Write a fixed, versioned CFP counting guide (prompt) for Claude, then
    test it: count the same spec 3 times in fresh sessions and compare
    totals. Within about 10% is fine. A bigger swing has to go into the
    simulation as extra uncertainty.
    - Claude records each feature's count with its data movements
      (E/X/R/W listed), so a count can be checked and re-run.
    - Record which guide version counted each project.
    - Suggested test spec: KlearCom (widest spread of feature sizes).

0.3 Fix the unclassed hours. Tactic has 34.5h (32%) unclassed; ZapNet
    is 100% unclassed. Decide which projects get classed by hand and
    which are excluded as reference data. (Lesson from the TC incident:
    no bulk retagging on title guesses. Confirm per project.)

Exit criteria: backtest result is acceptable, counting is consistent
enough, and at least one project is clean enough to serve as reference.


### Phase 1: data model

1.1 Add estimate_hours (number, optional) to the Ticket struct.
    Meaning: hours estimate for a WRAP ticket (config / nonfunc).
    Picked from a short list in the UI: 0.25, 0.5, 1, 2, 4, 8.
    The API accepts any value >= 0.25 in quarter-hour steps.

1.2 Validation rules (in ValidateTicket or at promote; see open
    question Q2):
    - A child ticket (has parent:) must carry exactly one class tag.
    - config / nonfunc children need estimate_hours.
    - functional children must NOT carry estimate_hours (their estimate
      comes from the parent's CFP).
    - cfp:N only on parents (unchanged).
    - meeting / administration: no estimate (they burn the admin pool).

1.3 Allow type:<name> on wrap CHILD tickets (today it's parent-only), so
    platform wrap can build hours-per-item history ("a deploy averages
    0.5h"). See open question Q4.


### Phase 2: one estimate function replaces t-shirt effort

Today t-shirt effort feeds about a dozen places. Replace all of them with
one function, EstimatedHours(ticket, context), so there is a single
source for "how many hours is this ticket expected to take":

  wrap child (config / nonfunc)   -> estimate_hours
  functional child                -> the parent feature's P50 code hours,
                                     split evenly across its functional
                                     children (used for scheduling only)
  parent, meeting, admin          -> 0

Call sites to switch over (from a code search):

  internal/pm/hoursbudget.go     allotment, estimate variance, hours at risk
  internal/pm/projschedule.go    projected Gantt
  internal/pm/execplan.go        execution plan
  internal/pm/balance.go         resource balancing
  internal/pm/conflicts.go       schedule conflicts (also HoursPerDay = 8)
  internal/pm/rollup.go          phase rollup weighting
  internal/pm/snapshot.go        baseline planned days
  internal/api/tickets.go        strict time enforcement check
  internal/ticket/index.go       index fields
  static/app.js                  HPD = 8, exec plan duplicate, effort UI

Scheduling changes from "days from a t-shirt map" to
"estimated hours / assignee daily_hours_available" (already in config).

Strict time enforcement applies to wrap tickets only. Functional tickets
are estimated at the feature level, so a per-ticket allotment is noise.


### Phase 3: Monte Carlo estimate on the COSMIC tab

Replace the manual initial-estimate block (one h/CFP + one wrap%) with:

  Inputs
    - reference set, built from any combination of three options:
        [ ] specific past projects (same domain; the normal case)
        [ ] all past projects ("unknown domain" mode: a deliberately
            wide starting range for a project unlike anything before;
            the screen labels it as such)
        [ ] this project's own completed features
      Pooling across domains is only allowed through the explicit "all
      past projects" option, never as a silent default.
    - blending rule: once this project has at least N finished features
      (default 5), its own features dominate the draw and borrowed ones
      fade out (for example, the share of draws from own features =
      own_N / (own_N + 5), capped at 100% once own_N >= 15). The panel
      shows the current mix ("70% own features, 30% borrowed").
    - minimum feature size to include as a sample (default 3 CFP),
      so tiny features with extreme rates don't dominate
    - optional: CFP counting uncertainty (+/- %), from Phase 0.2

  Computation (server side, Go, fixed random seed so the numbers don't
  change on every page load). No "run" button: it calculates when the
  tab opens or an input changes (10,000 runs take well under a second).
    - per run: for each feature in this project, draw a random reference
      feature and use its h/CFP -> code wrap hours
    - widen the spread 1.5x (calibration from the Phase 0.1 backtest)
    - platform wrap: sum of estimate_hours on wrap tickets (fixed in v1;
      see Q5 for sampling it later)
    - 10,000 runs, then take percentiles

  Output
    - code wrap   P50 / P85 / P95
    - platform wrap (sum)
    - total       P50 / P85 / P95
    - N (number of reference features) shown next to the range
    - actual hours so far, for comparison
    - a note that the range is widened 1.5x
    - no button to set the cap; the max-hours cap stays manual in
      Settings

Persist the inputs per project in .tkt/config.json, replacing
estimate_h_per_cfp / estimate_wrap_pct.


### Phase 3b: calibration slice (new kinds of project)

For a project unlike anything delivered before, there is no comparable
reference project. The workflow:

  1. Claude counts CFP for the whole spec (consistent size).
  2. Estimate with "all past projects" as the reference. This is the
     wide starting range. Pooled NEI + Tactic as of 2026-10-02
     (32 features, 3+ CFP):
       P10 0.07 / P25 0.11 / P50 0.25 / P75 0.29 / P90 0.33 /
       max 0.97 h/CFP
  3. Wrap: no transferable defaults (wrap is platform-specific).
     Estimate by judgment with the hours picker, and add explicit
     DISCOVERY wrap tickets for the unknown platform (dev environment,
     how it deploys, first console setup).
  4. Pick 3-5 representative features, including some wrap on the new
     platform, and build them first. Tag their parents
     `calibration-slice`.
  5. As slice features finish, turn on "this project's own features".
     The blending rule shifts the estimate onto the project's own rates.
  6. Re-estimate the rest after the slice. Because CFP is a consistent
     size, rates measured on the slice apply to remaining features of
     different sizes.

Bidding guidance (goes in the agent guide and Help, not in code):
  - Preferred: two-part bid. Fixed scope for discovery plus the slice,
    then a firm number for the rest from the project's own rates.
  - If it must be one number: bid at P90-P95 of the wide range, not P85.
  - The max-hours cap stays manual; revisit it after the slice.


### Phase 3c: GUI changes

New Ticket form
  - Remove the Effort dropdown.
  - Add Class: Code / Config / Non-functional.
      Code            -> no hours field; note "estimated from the parent
                         feature's CFP"
      Config/Nonfunc  -> Est. hours picker (0.25/0.5/1/2/4/8) and an
                         optional Type (suggests a default from past
                         averages)
      Parent/feature  -> CFP field (already exists as optional sizing)
  - Missing class/hours warns; blocks at first promote (see Q2).

Ticket detail panel
  - "Effort: m (3d . 24h)" becomes "Class: Config  Estimate: 2h", or
    "Class: Code  Estimate: ~1.4h (from feature, 20 CFP)".
  - Class shown as a field, not only as a tag.
  - Over-allotment warning on time logs for wrap tickets only.

Ticket list
  - Effort column -> "Est h". Badge for missing class/estimate.

Settings
  - Remove the "Effort sizing" box.
  - Remove the "Show COSMIC tab (experimental)" checkbox.
  - Reword strict time enforcement to refer to wrap hour estimates.

COSMIC tab
  - Standard tab, always shown (keeps the name COSMIC).
  - Per-feature table stays; Wrap% column becomes info-only or goes.
  - Manual initial-estimate box replaced by the Monte Carlo panel
    (inputs + P50/P85/P95 table + N + actual so far). Display only.
  - Reference picker with the three options from Phase 3 (specific
    projects / all past projects / this project's features) and the
    own-vs-borrowed mix shown under the result.
  - Features in the calibration slice (Phase 3b) are marked in the
    per-feature table, with a "slice complete: N of M" line.

PM dashboard
  - Hours Budget: unchanged (cap stays manual).
  - Estimate Variance: wrap per ticket; code per feature.
  - Hours at Risk: wrap tickets only.
  - Gantt / Exec Plan / balance: bars from est. hours / daily hours.
  - Sigma Phases: "effort-days" -> "est. hours".

Migration notice
  - One-time banner: "N wrap tickets have converted estimates. Review."
    linking to a filtered ticket list.

Help
  - Rewrite the Effort, sizing, schedule check and COSMIC sections.


### Phase 4: migration

4.1 Wrap tickets that have an effort size: convert to estimate_hours
    using the project's current effort_to_days x 8, then list them for
    review. These numbers will be too big (days-based), so the review
    matters.
4.2 Functional tickets: drop effort; nothing to convert.
4.3 effort stays in the struct as read-only legacy for one release,
    then is removed along with effort_to_days, its settings UI, and its
    API endpoints.
4.4 Bump AppVersion and rebuild dist (per the usual rule).


### Phase 5: docs

- ticketing-and-cfp-guide.md: rewrite sections 3, 5, 8, 10, 11 around the
  new model; replace effort with estimate_hours; add a one-line rule for
  each of the three places testing can land:
    debugging while building    -> time on the functional ticket
    formal QA                   -> qa_testing / rework (QA pool)
    smoke/integration validate  -> a nonfunc ticket
- README: replace effort in the field table and the balancing section.
- Delete future-wrap-based-estimation.md, or mark it superseded by this
  plan.
- In-app Help: update the COSMIC and sizing sections.


## 4. Open questions (decide before Phase 1)

Q1  Naming. Keep the "functional" tag value but relabel it in UI/docs,
    or rename it (for example "code")? Lean: keep the value, relabel.

Q2  Where validation bites. Reject at create time, or at first promote
    (leaving not_started)? Create time is stricter but makes quick
    capture annoying. Lean: at first promote.

Q3  Functional children for scheduling. Even split of the parent's P50
    (lean), or a flat default per ticket, or leave them out of the
    Gantt?

Q4  type: on wrap children. Allow it (lean), or keep platform wrap
    history at the parent level only?

Q5  Platform wrap uncertainty. v1 treats wrap estimates as fixed. Later,
    once estimate_hours has history, sample "actual / estimate" ratios
    from past wrap tickets to get a range there too. OK to defer?

Q6  Reference set. Partly decided: the picker offers specific projects,
    all past projects, and this project's own features, with a blending
    rule (Phase 3). Still open: add a project "domain" field so the
    specific-projects choice can default, or keep picking by hand
    (lean)? Also confirm the blending numbers (N=5, full own-weight at
    15).

Q7  Pre-ticket estimates. This plan assumes Claude builds the ticket
    stack from the spec (parents with cfp, wrap children with hours)
    BEFORE the bid, as on KlearCom. Is that the workflow, or do you need
    an estimate from a spec before any tickets exist?


## 5. Out of scope here

The other findings from the code review (security: bind to localhost and
XSS; bugs: legacy ZapNet ticket types, duplicate time-entry IDs, zero-hour
entries, silently skipped tickets, uncommitted config writes, no write
lock; and the slip/baseline redesign) are a separate plan. The ZapNet
legacy-type migration and the zero-hour fix are small and could go first.
