# Getting started

A workspace is a named local registry in `$XDG_STATE_HOME/ws/registry.db`
with a canonical root directory that holds many Git projects.
`workspace.toml` is import/export interchange only. Project synchronization
is explicit: `ws sync` preflights the current workspace, lets you review the
run in a terminal, and changes remote Git state only after confirmation.

## Install

```sh
gh auth login
gh api repos/kuchmenko/workspace/contents/install.sh \
  -H "Accept: application/vnd.github.raw+json" | sh
```

Or build from source:

```sh
gh repo clone kuchmenko/workspace
cd workspace
just install            # binary lands at ~/.local/bin/ws
```

The repository is private, so installation requires an authenticated GitHub
CLI session with repository access. `~/.local/bin` should be on your `PATH`.
If not, the installer prints a reminder.

## First-time setup (interactive)

```sh
mkdir ~/dev
ws workspace create ~/dev --name personal
cd ~/dev
ws auth login            # GitHub device flow (or `--pat` for a token)
ws setup                 # TUI: pick repos and organize them into groups
ws sync                  # preflight, review, clone/fetch, and ff-pull safely
```

To migrate an existing TOML registry instead, run
`ws workspace import /path/to/workspace.toml --name personal --root ~/dev`.
Normal commands use SQLite afterward and do not modify the imported file.

That's enough for one machine. For cross-machine workflow see
[Multi-machine sync](sync.md#multi-machine-flow).

### `ws setup` — interactive

`ws setup` walks you through three steps.

**Step 1 — Select repos.** Lists every repo you have access to on
GitHub, sorted by your activity (last 90 days). Filter by org, search
by name, multi-select.

```text
 ws setup   Select repos

  Search: _                            sort: activity (ctrl+s)
   all   acme-corp  personal                          (tab)

> ● acme-corp/api-gateway        3d ago  ●●●●●
  ● acme-corp/web-dashboard      5d ago  ●●●●
  ○ acme-corp/legacy-service    45d ago  ●○○○○
  ● personal/dotfiles            1d ago  ●●●●●
  ● personal/cli-tools           8d ago  ●●●

  ↓ 42 more

  Selected: 4 / 49
  ↑↓ navigate  space select  ctrl+a toggle all  enter next  esc quit
```

**Step 2 — Confirm.** Review the planned registry shape — groups
(usually GitHub orgs) and per-project category (`personal` / `work` is
auto-detected from org ownership; you can override).

**Step 3 — Write.** `ws setup` writes the selected SQLite workspace and exits.
Run `ws sync` to clone everything; the result is a directory tree like:

```text
~/dev/
├── acme-corp/                  ← work group (gitignored)
│   ├── api-gateway/
│   └── web-dashboard/
└── personal/                   ← personal group (gitignored)
    ├── dotfiles/
    └── cli-tools/
```

## Adding more repos later

Three flows; pick whichever matches what you have:

```sh
# I have a URL or a list:
ws add git@github.com:owner/repo.git
ws add url1 url2 url3
echo url | ws add -                # stdin, one URL per line

# I want a brand-new repo on GitHub created for me:
ws create                          # TUI: owner / name / visibility
ws create --owner me --name foo

# I have a plain git checkout on disk that should join the registry:
ws migrate <name>                  # converts to bare+worktree layout
```

All three end at the same place: an entry in the SQLite registry plus a
project laid out as `<name>/` (main worktree) + `<name>.bare/` (bare
repo) under the chosen group/category directory.

## Managing workspaces

The explorer can show multiple named workspaces from the local SQLite registry:

```sh
ws workspace create ~/dev --name personal
ws workspace import ~/work/workspace.toml --name work --root ~/work
ws workspace export personal > workspace.toml
ws workspace list
```

Names are unique and roots are canonical. Commands use an exact `--root` when
provided; otherwise they select the workspace with the longest containing
root. TOML export is explicit and is never a runtime fallback.

## Authentication

`ws auth login` is the GitHub device flow used by `ws setup` to list
your repos and orgs. Token lives at `~/.config/ws/token`.

```sh
ws auth login          # browser-based device flow
ws auth login --pat    # paste a PAT instead
ws auth status         # show current state
ws auth logout         # remove the token
```

`ws create` is the one exception: it shells out to `gh repo create` and
therefore needs `gh auth login` to be set up separately. The two
authentications are independent — `ws` doesn't reuse the `gh` token and
vice versa.

## What to read next

- [Worktrees](worktrees.md) — the per-feature checkout model and
  `ws worktree add/push/list/rm`.
- [Sync](sync.md) — preflight, interactive selection, strict headless
  behavior, conflicts, and cross-machine syncing.
- [Aliases](aliases.md) — short shell aliases for projects and groups.
- [Explorer TUI](explorer.md) — bare `ws` opens a Bubble Tea launcher
  across registered workspaces and worktrees.
- [Architecture](architecture.md) — internals: data model, on-disk
  layout, and foreground sync contract.
- [Command reference](reference.md) — every command, every flag.
