# ws

Workspace manager — track, sync, and develop projects across multiple machines without losing work.

Single TOML registry · interactive TUI setup · per-branch worktrees with explicit
cross-machine metadata.

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
ws sync                           Run one reconciler tick: clone missing, fetch, ff-pull main,
                                  refresh last_active_*, surface branch-orphan / branch-duplicate
ws sync resolve                   Inspect and act on unresolved sync conflicts
ws add <url>...                   Register and clone projects (bare+worktree layout)
ws add -                          Read URLs from stdin, one per line
ws bootstrap [name]               Clone projects listed in workspace.toml that are missing locally
ws migrate [name]                 Convert plain git checkouts into the bare+worktree layout
ws status                         Table: project / group / status / branch / last commit / layout
ws scan                           Find git repos not registered in workspace.toml
```

### Worktrees

```
ws worktree add <proj> <branch>   Create a worktree on the literal <branch>. Auto-detects existing
                                  remote (fetches and checks out) and existing local-only branches
                                  (attaches; covers legacy wt/<machine>/* re-registration).
   --from <ref>                   Branch from a specific base ref instead of default_branch.
                                  Ignored when the branch already exists on origin or locally.
ws worktree push <proj> <branch>  Push <branch> to origin via `git push -u origin` and stamp
                                  last_active_machine / last_active_at in workspace.toml.
   --force-dirty                  Push even with uncommitted changes
ws worktree list [project]        Table of worktrees: branch, state, ownership, last activity
ws worktree rm <proj> <branch>    Remove the worktree (refuses if dirty / unpushed unless --force)
                                  and release this machine from [[branches]].machines. Empty
                                  machines causes the entry to be GC'd on save.
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
└── myapp-wt-linux-feat-fix-login/    ← extra worktree for branch feat/fix-login
```

Convert any plain checkout once with `ws migrate <name>` (interactive TUI by default,
preserves dirty state, stash entries, and detached HEADs as recovery branches).

`ws add` clones new projects directly into this layout — no follow-up `ws migrate`
required:

```sh
ws add git@github.com:owner/repo.git           # single URL
ws add url1 url2 url3                          # several URLs
echo url | ws add -                            # stdin, one URL per line
ws add --no-clone url                          # register only, defer clone
```

While `ws add` is running it holds an `add/<sha>.toml` sidecar so the daemon
pauses both `workspace.toml` sync and project reconcile for the affected workspace
— your in-progress edits never race the reconciler.

### Starting a feature

```sh
ws worktree add myapp feat/fix-login
#   creates branch feat/fix-login from myapp's default branch
#   checks it out at personal/myapp-wt-linux-feat-fix-login
#   registers [[branches]] entry: machines=[linux], created_by=linux
```

The branch name is taken **verbatim** — no prefix injection, no template, no
validation regex. Use whatever convention the project follows (`feat/...`,
`fix/...`, `chore/...`). If the slug-derived directory name collides with
another branch already in the same project, `ws worktree add` appends a
deterministic `-<sha8>` suffix so the path is unique.

### Cross-machine handoff

The daemon does **not** auto-push project branches. Pushes are explicit.

```sh
# Linux:
ws worktree add myapp feat/fix-login
# (edit, commit)
ws worktree push myapp feat/fix-login        # push + update last_active_*

# Archlinux: workspace.toml already synced via the daemon's Phase 1.
ws worktree add myapp feat/fix-login         # auto-detects existing origin ref,
                                             # checks it out, machines=[linux, archlinux]
# (edit, commit)
ws worktree push myapp feat/fix-login        # push from this machine
```

Each `ws worktree push` stamps `last_active_machine` / `last_active_at` so the
other machine sees who is active on the branch in `ws worktree list`. Plain
`cd <wt> && git push` works too — it just won't update the metadata.

### Re-registering a legacy `wt/<machine>/*` branch

Pre-0.7.0 worktrees on `wt/<machine>/<topic>` keep working but live outside
`[[branches]]`. To bring one under the new metadata model:

```sh
ws worktree add myapp wt/linux/old-topic
# attaches to the existing local branch and creates a fresh [[branches]]
# entry with machines=[linux].
```

### Recovering from a deleted-on-origin branch

When a PR is merged with auto-delete-branch enabled (or `git push origin
--delete <branch>`), the next reconciler tick records a `branch-orphan`
conflict. Resolve via `ws sync resolve` — pick "drop entry + remove worktree"
for the merged-PR case, or "keep local" if you have unmerged work on the
branch.

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
(`co-op` → `coop`), longer multi-part names use first letters
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
  and the per-branch metadata in `[[projects.X.branches]]` blocks. Synced
  between machines via its own git repo with `merge=union` so concurrent edits
  from different machines never conflict at the file level. Duplicate `name`
  entries in the same project (a possible race outcome) surface as
  `branch-duplicate` for `ws sync resolve`.
- Project directories are gitignored — repos are cloned by `ws sync` / `ws bootstrap`.
- Groups are directories — fully customizable hierarchy.
- Category (`personal`/`work`) is auto-detected from GitHub org ownership.
- Repos use a **bare + worktree layout** (`<name>/`, `<name>.bare/`,
  `<name>-wt-<machine>-<branch-slug>/`) so each machine has its own per-feature
  worktree and the bare repo holds shared git objects. Slug collisions get a
  deterministic `-<sha8>` suffix from `SHA-1(branch)`. `ws migrate` converts
  existing plain checkouts in place.
- The **daemon** runs an idempotent reconciler tick: commits & syncs
  `workspace.toml`, fetches every bare, ff-pulls main worktrees when safe,
  refreshes `last_active_*` for branches with local-ahead commits, and detects
  branches deleted on origin (`branch-orphan`). The daemon **never pushes
  project branches** — push is the user's explicit action via
  `ws worktree push` or plain `git push`. It also never runs `merge`,
  `rebase`, `reset`, or `force` inside a project — the worst it does is
  record a conflict and stop.

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

### Layer 2: per-project metadata + read-only sync

For each project, the reconciler tick will:

- `git fetch --all --prune --tags` in the bare repo.
- For the **main worktree** on the project's default branch: `git pull --ff-only`
  when clean and only behind. Diverged or dirty → leave it alone.
- For every **registered branch** (`[[projects.X.branches]]`) whose worktree
  on this machine has local-ahead commits: stamp `last_active_machine = me`
  and `last_active_at = now()` so the cross-machine view stays current.
- For every registered branch whose `last_active_at` is set: check whether
  `refs/remotes/origin/<name>` still exists post-fetch. Missing → record
  `branch-orphan` (typical: PR merged with auto-delete-branch). Re-appearance
  on the next tick auto-clears the conflict.
- After the loop, if anything changed in-memory, `config.Save` writes
  `workspace.toml` so Phase 1 of the next tick commits and pushes the
  metadata to the workspace's git remote. Other machines see the activity
  trail without any project-branch push having happened.

The daemon **never** runs `merge`, `rebase`, `reset`, `force`, or `push`
inside a project repo. Project pushes are user-driven via `ws worktree push`
or plain `git push`. The worst the daemon does is record a conflict in
`~/.local/state/ws/conflicts.json` and let you handle it via `ws sync resolve`.

```sh
ws daemon status              # check daemon health
ws daemon install-service     # auto-start on boot (systemd)
```

workspace.toml can live in your dotfiles repo (symlinked). The daemon resolves
symlinks and commits to the correct repository.
