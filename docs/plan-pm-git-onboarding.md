# Plan: hate for a PM who has never used Git

Status: PLAN ONLY. Nothing here is built yet.

Goal: a project manager with no Git, GitHub or GitLab experience installs hate,
signs in once, joins a project, and works. Pulls and pushes happen on their own.
The only thing Chuck does is grant the PM access to the repo in GitHub/GitLab.


## 0. Decisions so far

- Platforms: Windows AND Mac, both first-class. Test both credential
  stores (Windows Credential Manager, macOS Keychain), the Git install
  check on both, and the Windows .exe end to end.
- The PM is read-mostly. Expected writes: resolving slips (and taking or
  re-taking baselines). Not editing tickets. So the conflict risk is NOT
  ticket edits; it's the files hate writes on its own on every machine
  (slip events, forecast history, snapshots). Section 6 is reworked
  around that.


## 1. Where the credentials live (and where they must NOT)

The request was "under team settings". Team settings live in
.tkt/config.json, which is committed and pushed to every teammate. A token
there would be published to everyone with repo access and kept in git
history forever.

So:
- Credentials are PER USER, PER MACHINE, in the app's own Settings ("Git
  account"), never in a project.
- They are stored in the OS credential store (macOS Keychain, Windows
  Credential Manager). Fallback when that isn't available: a file in the
  app config dir with owner-only permissions, plus a warning.
- Team settings keep what they already have: the person's name, email and
  git_user for assignment. No secrets.


## 2. How the PM signs in

v1: personal access token (PAT), with a guided screen.
- Provider: GitHub / GitLab.com / GitLab self-hosted (base URL).
- The screen links straight to the right token page with the right scopes
  pre-selected where the provider allows it:
  - GitHub: a fine-grained token with "Contents: read and write" on the
    project repos (or a classic token with `repo`).
  - GitLab: a personal access token with `read_repository` and
    `write_repository` (plus `read_api` to list projects).
- Paste the token, click "Test connection". hate calls the provider's
  /user API, shows "Connected as Jane Doe", and uses that name and email as
  the PM's commit identity (no git config needed).

v2 (later): "Sign in with GitHub" using the OAuth device flow (show a code,
approve on github.com). It needs a registered GitHub OAuth app (a client
id embedded in hate, no secret). Same for GitLab where the instance
supports the device grant. Better UX, but more moving parts, so PAT first.


## 3. Joining a project

New "Add project from GitHub/GitLab" next to "Open existing folder":
- Either paste the repo URL, or pick from a list of repos the token can
  see that contain .tkt/config.json.
- hate clones it into the projects root over HTTPS with the stored
  credentials and opens it.

Chuck's side ("grant access"):
- GitHub: add the PM as a collaborator with Write, or add them to a team
  with Write on the repo. For organisation repos, the org may need to
  approve fine-grained tokens.
- GitLab: add the PM as a project member with the Developer role.


## 4. Plumbing: giving git the credentials

hate shells out to the git CLI. It keeps doing that, and supplies the
token without storing it in git's config or the repo:
- hate runs every network git command with GIT_ASKPASS pointing at hate
  itself in a small "askpass" mode that reads the token from the
  credential store. Nothing is written to .git/config and nothing shows in
  the remote URL.
- Remotes must be HTTPS for token auth. For repos whose remote is SSH (like
  Chuck's git@github.com-... alias), hate adds a per-command
  `url.<https>.insteadOf <ssh>` rewrite when a token is configured, so
  Chuck can keep SSH and the PM uses HTTPS on the same repo.
- Git not installed: detected at startup and on Settings, with a plain
  "install Git for Windows / Xcode tools" page and a re-check button.
  (Bundling a pure-Go git library instead is possible later; see section
  8.)


## 5. Automatic sync

Today: every change is committed locally; pull/push only happen when
someone clicks Sync. auto_push exists in the config but nothing uses it.

New behavior (on by default when a Git account is configured):
- Pull when a project opens, every 5 minutes while the app is open, and
  before the PM dashboard renders (skipped if a pull ran in the last
  minute).
- Push about 10 seconds after the last local commit (debounced, so a
  burst of edits becomes one push).
- Offline: local commits just wait; the next successful sync sends them.
- A small status indicator in the header: Synced / Syncing / Offline /
  Needs attention, with the last sync time. No git words in the UI.


## 6. Conflicts without a "Git person"

Today a conflict aborts the rebase and says "ask your team's Git person".
A PM can't do that. Because the PM is read-mostly, the realistic
conflicts are in files hate writes automatically on BOTH machines:

- Slip events (.tkt/pm/slip_events.json): the PM's dashboard and Chuck's
  dashboard can each auto-snapshot and detect the same slip, producing
  two events with the same sequential id (SE-CW-001) but different
  content. Fix at the source: make slip event ids deterministic (derived
  from baseline id + ticket + revised due date), so both machines produce
  the same event, and merge the file as a union by id (a resolution wins
  over unresolved; the earliest detection date is kept).
- Forecast history: union by date (latest computation for a date wins).
- Baseline: only one person should baseline. A re-baseline by both at once
  is resolved by keeping the remote one and archiving the local one as an
  extra archived baseline, with a "Needs attention" note.
- Snapshots: already gitignored (never conflict).
- index.json: stop tracking it (derived; conflicts on every concurrent
  write).
- Tickets: the PM rarely edits them, but when both sides do, apply the
  three-way rule (union lists by id; for the same field, newest
  updated_at wins plus an activity note recording both values).
- Anything hate can't merge: keep the remote version, save the local one
  under .tkt/conflicts/, show "Needs attention" in plain words. The repo
  is never left half-merged.


## 7. Must-fix first (prerequisites)

These matter once real people and real tokens are involved:
1. Bind to 127.0.0.1, not every network interface. Today anyone on the
   same Wi-Fi can call the API. With stored credentials, that would let
   them push to the client's repo as the PM.
2. A per-project write lock. Background pulls plus edits plus auto-push
   will race without it.
3. Stop committing index.json (above).
4. Surface GitCommit failures instead of ignoring them.
5. Fix the 18 open ZapNet tickets with retired types (feature/defect),
   which can't be edited today. A PM will hit that on day one if they get
   that project.


## 8. Options considered

- Credentials in team settings: rejected (they'd be pushed to everyone).
- SSH keys: rejected for a non-technical user (key generation, upload,
  agent).
- Pure-Go git (go-git) instead of the git CLI: removes the "install Git"
  step and makes auth trivial, but its rebase/merge support is limited
  and it's a big swap. Revisit if installing Git turns out to be the main
  blocker.
- A hosted hate server for PMs: out of scope; hate stays local-first.


## 9. Phases

  0. Prerequisites (section 7).
  1. Git account in app Settings: provider, token, credential store, test
     connection, commit identity.
  2. Credential plumbing (askpass mode, SSH->HTTPS rewrite, git-installed
     check).
  3. "Add project from GitHub/GitLab" (clone).
  4. Auto sync + status indicator.
  5. Automatic conflict resolution for tickets/config/history.
  6. PM onboarding guide (one page: install, sign in, join, what the
     status light means) and Chuck's "grant access" checklist.
  Later: OAuth device-flow sign-in.


## 10. Open questions

Q1  DECIDED: GitHub only for the first build. The self-hosted GitLab is on
    Chuck's other machine and isn't available here, so GitLab is deferred.
    Keep the provider code behind a small interface (sign-in check, list
    repos, token expiry, clone URL) so GitLab can be added later without
    touching the rest.
Q2  DECIDED: both Windows and Mac.
Q3  DECIDED: auto-sync only when a Git account is configured. Chuck keeps
    SSH and his current flow.
Q4  Mostly moot (the PM rarely edits tickets). Lean: newest wins plus an
    activity note for the rare case.
Q6  DECIDED: no PM view; don't hide editing controls.
Q5  DECIDED: OK to add github.com/zalando/go-keyring for the OS credential
    store.


### Added after the first review

Q7  (Deferred with GitLab.) Self-hosted GitLab reachability: the PM's machine must reach the GitLab
    server over the network (LAN, VPN or public). If it runs on Chuck's
    own machine, it is only reachable while that machine is on and
    exposed. Confirm the URL and how the PM reaches it.
Q8  (Deferred with GitLab.) TLS on the self-hosted GitLab: if it uses a self-signed or private CA
    certificate, git and hate's API calls will reject it. Support a
    per-provider "custom CA certificate" file (passed to git as
    http.sslCAInfo and used by hate's HTTP client). Never offer "skip
    verification".
Q9  Token expiry: GitLab and GitHub fine-grained tokens expire. hate reads
    the expiry when testing the connection (GitLab:
    /api/v4/personal_access_tokens/self; GitHub: the
    github-authentication-token-expiration header) and warns 14 days
    ahead, with a "replace token" button.
Q10 First-run experience for a non-technical PM:
    - Unsigned binaries: macOS Gatekeeper blocks them ("can't be opened")
      and Windows SmartScreen warns. Options: sign and notarize (Apple
      Developer account, Windows code-signing certificate), or a
      one-page "how to open it the first time" guide. Lean: the guide
      now, signing later.
    - hate runs in a terminal window and doesn't open a browser. Open the
      browser automatically on start, and keep the console window
      minimal on Windows.
