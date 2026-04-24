# ws

Workspace manager — track, sync, and develop projects across multiple machines without losing work.

Single TOML registry · interactive TUI setup · multi-machine WIP sync via per-branch
worktrees.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/kuchmenko/workspace/main/install.sh | sh
```

Or build from source:

```sh
git clone git@github.com:kuchmenko/workspace.git
cd workspace
just install
```

## Quick start

```sh
mkdir ~/dev && cd ~/dev
ws auth login
ws setup
ws sync
```

## Setup

`ws setup` launches an interactive TUI that walks you through workspace creation:

### Step 1 — Select repos

Fetches all repos you have access to on GitHub, sorted by your activity (last 90 days).
Filter by org, search by name, multi-select.

```
 ws setup   Select repos

  Search: _                            sort: activity (ctrl+s)
   all   acme-corp  personal                          (tab)

> ● acme-corp/api-gateway        3d ago  ●●●●●
  ● acme-corp/web-dashboard      5d ago  ●●●●
  ○ acme-corp/legacy-service    45d ago  ●○○○○
  ● personal/dotfiles            1d ago  ●●●●●
  ● personal/cli-tools           8d ago  ●●●
  ○ personal/old-experiment    120d ago  ○○○○○
  ○ other-org/shared-lib        30d ago  ●○○○○

  ↓ 42 more

  Selected: 4 / 49
  ↑↓ navigate  space select  ctrl+a toggle all  enter next  esc quit
```

### Step 2 — Confirm

```
 ws setup   Confirm

  2 groups, 4 projects

  acme-corp
    api-gateway                work       acme-corp/api-gateway
    web-dashboard              work       acme-corp/web-dashboard

  personal
    dotfiles                   personal   personal/dotfiles
    cli-tools                  personal   personal/cli-tools

  Write workspace.toml? y/n  (esc go back)
```

### Result

```sh
$ ws sync
  clone  api-gateway → acme-corp/api-gateway
  clone  web-dashboard → acme-corp/web-dashboard
  clone  dotfiles → personal/dotfiles
  clone  cli-tools → personal/cli-tools

Done: 4 cloned, 0 pulled, 0 skipped, 0 failed
```

```
~/dev/
├── workspace.toml              ← source of truth (committed)
├── acme-corp/                  ← work group (gitignored)
│   ├── api-gateway/
│   └── web-dashboard/
└── personal/                   ← personal group (gitignored)
    ├── dotfiles/
    └── cli-tools/
```

## Commands

### Project management

```
ws setup                          Interactive onboarding — select repos, assign groups
ws sync                           Run one reconciler tick: clone missing, fetch, ff-pull, push owned
ws sync resolve                   Inspect and act on unresolved sync conflicts
ws add <url>                      Register and clone a new project
ws bootstrap [name]               Clone projects listed in workspace.toml that are missing locally
ws migrate [name]                 Convert plain git checkouts into the bare+worktree layout
ws status                         Table: project / group / status / branch / last commit / layout
ws scan                           Find git repos not registered in workspace.toml
```

### Worktrees

```
ws worktree new <proj> <topic>    Create a wt/<machine>/<topic> worktree, branched from default_branch
   --branch <name>                Use a custom (non-wt/) branch name from the start
   --auto-push                    With --branch, opt the branch into the project's autopush list
   --reclaim                      Take ownership even if another machine already owns the branch
   --from <ref>                   Branch from a specific base ref instead of default_branch
ws worktree promote <proj> <topic>  Rename wt/<machine>/<topic> → final repo-native name + move dir
   --name <branch>                Override project.branch_naming.pattern
   --no-push                      Skip pushing the renamed branch
   --no-remote-delete             Keep the stale wt/<machine>/<topic> ref on origin
   --reclaim                      Override an existing owner on the new name
ws worktree list [project]        Table of worktrees with branch, state, and owner tag
ws worktree rm <proj> <topic>     Remove a worktree (refuses if dirty / unpushed unless --force)
ws wt …                           Alias for `ws worktree`
```

### Aliases, daemon, auth

```
ws alias                          Manage shell aliases (TUI)
ws alias add <n> <t>              Add alias for project, group, or "." (workspace root)
ws alias rm <name>                Remove alias
ws alias init [zsh]               Print shell snippet to eval
ws alias install                  Install sourcing line in ~/.zshrc (idempotent)
ws auth login [--pat]             Authenticate with GitHub (device flow or PAT)
ws auth logout                    Remove stored token
ws auth status                    Show authentication state
ws daemon start|stop|restart      Manage the background reconciler daemon
ws daemon status                  Show daemon state and registered workspaces
ws daemon register [path]         Register a workspace with the daemon
ws daemon install-service         Install systemd user unit
```

## Worktree workflow

`ws` lays every project out as a **bare repo + per-feature worktree** sibling triplet,
so two machines can work on different branches of the same project without ever
fighting over a checked-out ref.

```
personal/
├── myapp/                            ← main worktree (default branch)
│   └── .git                          ← pointer file into ../myapp.bare
├── myapp.bare/                       ← bare repo, single source of git state
└── myapp-wt-linux-fix-login/         ← extra worktree for wt/linux/fix-login
```

Convert any plain checkout once with `ws migrate <name>` (interactive TUI by default,
preserves dirty state, stash entries, and detached HEADs as recovery branches).

### Starting a feature

```sh
ws worktree new myapp fix-login
#   creates wt/linux/fix-login on myapp's default branch
#   checks it out at personal/myapp-wt-linux-fix-login
```

The branch is namespaced as `wt/<machine>/<topic>`. The daemon **auto-pushes only
branches matching this prefix** (or branches you explicitly opt into via
`--auto-push` / `promote` — see below). Anything else stays strictly local.

### Cross-machine handoff

While `ws daemon` is running on both machines:

1. Linux: edit, commit on `wt/linux/fix-login`. Daemon pushes within ~1 tick.
2. Asahi: daemon fetches, the branch becomes visible in `git branch -a` and `ws worktree list`.
3. Asahi: pull / inspect / pick up the work. Each machine has its own
   `wt/<machine>/*` namespace, so there's never a conflict on the same checked-out
   branch.

### Promoting to a PR-ready branch

When the work is ready for review, rename the WIP branch into the repo's native
naming convention:

```sh
ws worktree promote myapp fix-login
#   renames wt/linux/fix-login → feat/fix-login (taken from project.branch_naming.pattern)
#   moves the worktree dir to match
#   deletes the stale wt/linux/fix-login ref on origin
#   adds feat/fix-login to project.autopush.owned, scoped to this machine
#   pushes the new branch
```

Per-project naming convention lives in `workspace.toml`:

```toml
[projects.acme-api]
remote   = "git@github.com:acme/api.git"
category = "work"

[projects.acme-api.branch_naming]
pattern  = "feat/{topic}"
validate = "^(feat|fix|chore)/[a-z0-9-]+$"   # optional regex check
```

For one-offs that should bypass the wt/* prefix from the start:

```sh
ws worktree new acme-api fix-login --branch feat/fix-login --auto-push
```

`--auto-push` registers the branch in `project.autopush.owned`, which is the
mechanism that lets the daemon keep pushing **non-wt** branches.

## Shell aliases

`ws alias` generates short shell aliases that `cd` into any project, group,
or the workspace root. Aliases live in `workspace.toml` and sync between
machines via git.

```
 ws alias   Manage aliases

  type to search...

> ●  ws              (workspace root)
  ●  acme            ├── acme-corp
  ●  api             │   ├── api-gateway
  ●  web             │   ├── web-dashboard
  ○  (auto)          │   └── legacy-service
  ○  (auto)          ├── other-org
  ○  (auto)          │   └── shared-lib
  ●  prs             └── personal
  ●  dot                 ├── dotfiles
  ●  cli                 ├── cli-tools
  ○  (auto)              └── old-experiment

  ↑↓ navigate  space toggle  e edit alias  enter next  esc cancel
```

Each entry is one of:
- a **project** (cd into the project directory)
- a **group** (cd into the group directory)
- the **workspace root** itself

Auto-generated names follow simple rules — short two-part names join
(`mm-eh` → `mmeh`), longer multi-part names use first letters
(`api-gateway` → `ag`), single words use consonants (`dotfiles` → `dtfls`).
Press `e` to override.

### Install into your shell

One-time setup:

```sh
ws alias install                # adds a sourcing line to ~/.zshrc
exec zsh                        # reload shell
```

After that, every `ws alias` save, `ws alias add`, or `ws alias rm`
automatically regenerates the aliases file at
`$XDG_STATE_HOME/ws/aliases.zsh` (default `~/.local/state/ws/aliases.zsh`).
Open a new shell or `source` that file to pick up the changes — `.zshrc`
itself is never touched again.

Currently only zsh is supported.

## How it works

- **workspace.toml** is the only committed file — tracks repos, groups, status,
  per-project branch naming convention, and the autopush ownership registry.
  Synced between machines via its own git repo with `merge=union` so concurrent
  edits from different machines never conflict.
- Project directories are gitignored — repos are cloned by `ws sync` / `ws bootstrap`.
- Groups are directories — fully customizable hierarchy.
- Category (`personal`/`work`) is auto-detected from GitHub org ownership.
- Repos use a **bare + worktree layout** (`<name>/`, `<name>.bare/`,
  `<name>-wt-<machine>-<topic>/`) so each machine has its own per-feature worktree
  and the bare repo holds shared git objects. `ws migrate` converts existing
  plain checkouts in place.
- The **daemon** runs an idempotent reconciler tick: commits & syncs `workspace.toml`,
  fetches every bare, ff-pulls main worktrees when safe, and pushes
  `wt/<this-machine>/*` branches plus anything in `project.autopush.owned` that
  this machine owns. It never runs `merge`, `rebase`, `reset`, or `force` inside
  a project — the worst it does is record a conflict and stop.

## Authentication

```sh
ws auth login          # GitHub device flow — opens browser, authorize, done
ws auth login --pat    # paste a Personal Access Token instead
ws auth status         # show current auth state
```

No `gh` CLI required. Token stored at `~/.config/ws/token`.

## Multi-machine sync

The daemon handles two layers of sync, both automatic and both safe-by-default.

### Layer 1: workspace.toml registry

```sh
# Machine A — one-time setup
ws daemon register ~/dev
ws daemon start

# Now any ws add / setup / bootstrap automatically:
# 1. Updates workspace.toml
# 2. Daemon commits + pushes to git

# Machine B — one-time setup
ws daemon register ~/dev
ws daemon start

# Daemon polls git remote, detects changes, pulls, and clones missing repos
```

### Layer 2: per-project WIP via worktree branches

For each project, the reconciler tick will:

- `git fetch --all --prune --tags` in the bare repo.
- For every `wt/<this-machine>/*` worktree on this machine, push it if it's
  ahead of upstream. Diverged → record a conflict and stop touching it.
- For every branch in `project.autopush.owned` whose owner is this machine,
  same push semantics. This is how non-`wt/*` branches (e.g. `feat/login`
  after `ws worktree promote`) get auto-synced.
- For other machines' `wt/<other>/*` branches: nothing — the fetch already
  updated their refs in the bare. Pick them up with `ws worktree list` /
  `git checkout` / `ws worktree new --from`.
- For the main worktree on the project's default branch: `git pull --ff-only`
  when clean and only behind. Diverged or dirty → leave it alone.

The daemon **never** runs `merge`, `rebase`, `reset`, or `force` inside a project.
The worst it does is record a conflict in `~/.local/state/ws/conflicts.json`
and let you handle it via `ws sync resolve`.

```sh
ws daemon status              # check daemon health
ws daemon install-service     # auto-start on boot (systemd)
```

workspace.toml can live in your dotfiles repo (symlinked). The daemon resolves
symlinks and commits to the correct repository.
