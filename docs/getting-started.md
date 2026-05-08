# Getting started

A workspace is a directory that holds many git projects, a single
`workspace.toml` registry, and (optionally) a daemon that keeps those
projects in sync with their remotes and across your machines.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/kuchmenko/workspace/main/install.sh | sh
```

Or build from source:

```sh
git clone git@github.com:kuchmenko/workspace.git
cd workspace
just install            # binary lands at ~/.local/bin/ws
```

`~/.local/bin` should be on your `PATH`. If not, the installer prints a
reminder.

## First-time setup (interactive)

```sh
mkdir ~/dev && cd ~/dev
ws auth login            # GitHub device flow (or `--pat` for a token)
ws setup                 # TUI: pick repos, organize into groups, write workspace.toml
ws sync                  # one reconciler tick: clone everything, ff-pull main worktrees
ws daemon register ~/dev # add this workspace to the daemon's registry
ws daemon start          # background reconciler — keeps workspace.toml + repos fresh
```

`ws daemon start` only launches the daemon process; it reads the
already-saved registry and reconciles whatever is registered.
Without `ws daemon register` first the daemon would come up with
zero watched workspaces and nothing would happen on its ticks.

That's enough for one machine. For cross-machine workflow see
[Multi-machine sync](daemon-and-sync.md).

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

**Step 2 — Confirm.** Review the planned `workspace.toml` shape — groups
(usually GitHub orgs) and per-project category (`personal` / `work` is
auto-detected from org ownership; you can override).

**Step 3 — Write.** `ws setup` writes `workspace.toml` and exits. Run
`ws sync` to clone everything; the result is a directory tree like:

```text
~/dev/
├── workspace.toml              ← source of truth (committed)
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

All three end at the same place: an entry in `workspace.toml` plus a
project laid out as `<name>/` (main worktree) + `<name>.bare/` (bare
repo) under the chosen group/category directory.

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
- [Daemon and sync](daemon-and-sync.md) — what the background daemon
  does and how cross-machine syncing works.
- [Aliases](aliases.md) — short shell aliases for projects and groups.
- [Agent TUI](agent-tui.md) — bare `ws` opens a Bubble Tea TUI launcher
  for Claude Code sessions across worktrees.
- [Architecture](architecture.md) — internals: data model, on-disk
  layout, daemon contract.
- [Command reference](reference.md) — every command, every flag.
