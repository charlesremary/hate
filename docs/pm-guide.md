# hate for project managers: getting started

This page takes you from nothing to a working project in about 15 minutes. You
don't need to know anything about Git or GitHub: hate does the syncing for you.
You'll need:

- the hate program for your computer (the project owner sends it to you):
  - Mac (Apple Silicon: M1 or newer): `hate-darwin-arm64`
  - Windows: `hate-windows-amd64.exe`
- a GitHub account that the project owner has given access to the project
  (see "For the project owner" at the end).

---

## 1. Put hate somewhere sensible

Make a folder for it, for example `Documents/hate`, and move the file you were
sent into it. hate keeps your projects in a `projects` folder in your home
folder (you can change that later in Settings).

## 2. Start hate the first time

Your computer will be cautious the first time, because hate isn't from an app
store. That's expected; you only do this once.

### On a Mac

1. In Finder, **right-click** (or Control-click) `hate-darwin-arm64` and choose
   **Open**.
2. A box says it's from an unidentified developer. Click **Open**.
   - If there's no Open button (newer macOS): click **Done**, then open
     **System Settings → Privacy & Security**, scroll down to the message about
     `hate-darwin-arm64`, click **Open Anyway**, and confirm with your password.
3. A Terminal window opens and shows `hate ... running on http://localhost:8000/`.
   Your web browser opens at hate.

From now on you can double-click it.

### On Windows

1. Double-click `hate-windows-amd64.exe`.
2. A blue box says **"Windows protected your PC"**. Click **More info**, then
   **Run anyway**.
3. A black window opens and shows `hate ... running on http://localhost:8000/`.
   Your web browser opens at hate.

### Both

- **Keep that window open** while you use hate. Closing it stops hate. You can
  minimise it.
- If the browser doesn't open by itself, open it and go to
  `http://localhost:8000`.
- hate only accepts connections from your own computer; nobody else on your
  network can see it.

## 3. Install Git if hate asks

hate uses a free program called Git to share projects. Click the **⚙** (gear)
at the top left to open **Settings**. If the **Git account** box says Git needs
to be installed, follow its steps:

- **Mac:** open the **Terminal** app (Applications → Utilities), type
  `xcode-select --install`, press Return, click **Install** and wait a few
  minutes.
- **Windows:** download **Git for Windows** from
  <https://git-scm.com/download/win>, run it, and click **Next** on every screen
  (the defaults are fine). Then close hate's black window and start hate again.

Click **Re-check** in Settings. When Git is found, the sign-in box appears.

## 4. Sign in to GitHub (once)

In **Settings → Git account**:

1. Click **Open GitHub's new token page**. Sign in to GitHub if it asks.
2. On that page:
   - **Token name:** leave `hate`.
   - **Expiration:** 90 days is fine (hate reminds you before it runs out).
   - **Resource owner:** pick the **organization** that owns your projects
     (ask the project owner which one).
   - **Repository access:** **All repositories** (or select the project ones).
   - **Permissions → Repository permissions → Contents:** **Read and write**.
   - Click **Generate token**. If GitHub says the organization must approve it,
     tell the project owner; it starts working once they approve.
3. **Copy** the token (it starts with `github_pat_`) and **paste** it into hate.
4. Click **Test connection and save**. You should see **Connected as <your
   name>**.

If you see "GitHub didn't accept this token", copy it again (all of it) and
retry, or make a new one.

The token is kept in your computer's password store (Keychain on a Mac,
Credential Manager on Windows). It is never put into a project and never shared
with the team. Your changes are recorded under your GitHub name and email.

**Is the project in someone's personal GitHub account rather than an
organization?** Use the "classic token" link under the steps instead, tick the
**repo** box, and generate.

## 5. Add your project

1. In the left sidebar, click **Add from GitHub…**.
2. Your projects are listed (hate projects are marked **hate project**). Click
   **Add** next to the one you want. Or paste the project's GitHub address (for
   example `https://github.com/acme/tkt-website`) and click **Add**.
3. hate downloads it into your projects folder and opens it.

If it says a folder with that name already exists, the project is probably
already on your computer: pick it in the sidebar instead. hate never overwrites
an existing folder.

Don't see your project? The project owner hasn't given you access yet, or your
token doesn't include that project (make a new token with **All
repositories**).

## 6. The status light

With your GitHub account set up, hate keeps the project up to date on its own:
when you open it, every 5 minutes, just before the PM Dashboard shows, and a few
seconds after you change something. You don't need to press anything. The light
next to the project name tells you how it's going:

| Light | Means | What to do |
|---|---|---|
| **Synced** (green) + time | Up to date with your team. | Nothing. |
| **Syncing** (blue, pulsing) | Fetching or sending changes. | Nothing; it takes a few seconds. |
| **Offline** (grey) | GitHub couldn't be reached (no internet, VPN, ...). | Nothing. Your changes are saved on your computer and are sent automatically when you're back online. |
| **Needs attention** (red) | Something needs a person. | Click the light to read what happened. Usually: your token expired or was revoked (replace it in Settings), you lost access (ask the project owner), or two versions of a file couldn't be combined (yours was saved aside; tell the project owner). |

**⇅ Sync** next to the light syncs immediately if you don't want to wait.

When your token is 14 days from expiring, a yellow bar at the top of hate says
so. Click **Replace token**, make a new token the same way as in step 4, and
paste it.

## 7. Resolving slips

On the **PM Dashboard** tab, the **Plan strip** at the top shows how many
**slips** are unresolved. A slip is a task whose due date has moved past its
baselined date with no explanation yet.

1. Click the slip count to open the slip list.
2. For each slip, pick a **reason category** and write a short **narrative**
   (what happened and what's being done), then save.

Your resolution is shared with the team automatically. If you and a teammate
both saw the same slip, hate treats it as one slip; a resolution wins over an
unresolved copy.

**Baselines:** only one person should take or re-baseline a plan. If you're
asked to, use **Baseline now** (first time) or **Re-baseline…** (with a
reason) on the Plan strip.

## 8. Every day after that

1. Start hate (double-click it).
2. Your browser opens; pick your project.
3. Check the light is **Synced**, then work as normal.

---

## For the project owner: grant access checklist

Do this once per PM. The PM never needs SSH keys or Git commands.

1. **Give them the program**: `dist/hate-darwin-arm64` (Apple Silicon Mac) or
   `dist/hate-windows-amd64.exe`, plus a link to this page.
2. **Give them access to the repository** (Write):
   - Organization repo: add them to a **team that has Write** on the repo
     (Organization → Teams → team → Repositories), or add them directly
     (repo → Settings → Collaborators and teams → Add people → **Write**).
   - Personal repo: repo → Settings → Collaborators → Add people (collaborators
     get write access). They'll need a **classic** token with `repo` (step 4,
     last paragraph), because fine-grained tokens are made for repos in an
     organization or their own account.
   - They must accept the invitation email from GitHub.
3. **Fine-grained tokens in an organization**: check Organization → Settings →
   Personal access tokens. If the org **requires approval**, approve their
   token request under *Pending requests* after they create it. If the org
   **restricts** fine-grained tokens, allow them (or have them use a classic
   token, if classic tokens are allowed).
4. **SSO**: if the organization uses SAML single sign-on, they must click
   **Configure SSO → Authorize** next to the token on their GitHub tokens page.
5. **Tell them which organization** to pick as the token's resource owner, and
   which repository is the project.
6. Your own setup doesn't change: you can keep your SSH remote (including host
   aliases like `github.com-work:`). If you also add a Git account in Settings,
   hate uses HTTPS with your token for that repo (per command; your remote URL
   is not changed) and turns on automatic sync for you too.

### What gets shared and what doesn't

- Shared with everyone on the project (in the repository): tickets, the project
  settings, baselines, slips and their resolutions, forecast history.
- Never shared: the PM's GitHub token (their password store only), the
  Settings → Git account box, the daily snapshots (derived, ignored by Git).
- If two people changed the same file at once, hate combines the changes
  itself. Anything it can't combine keeps the shared version and saves the
  other copy under `.tkt/conflicts/` in the project, with a **Needs attention**
  note, so nothing is lost.
