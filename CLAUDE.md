# Workspace

Personal workspace manager for tracking, syncing and operating on development
projects across multiple machines. The end goal is that the same set of
projects, branches, and even works-in-progress is available on every machine
the user sits down at, without losing data and without making destructive
operations behind the user's back.

## High-level goal

The user works on the same projects from multiple machines (e.g. an Asahi
laptop and a desktop). They want:

1. **One registry of projects** that travels between machines via git, so
   adding a project on one machine makes it appear on the other.
2. **Bidirectional, safe sync of feature work** so a branch started on
   machine A can be picked up on machine B without manual `git push`/`pull`
   gymnastics and without merge conflicts in unrelated branches.
3. **No destructive operations** in project repos. The daemon never runs
   `merge`, `rebase`, `reset`, or `force` inside a project. The worst it
   can do is decline to act and surface a conflict.
4. **Worktree-first layout** so two machines never fight over the same
   checked-out branch — each machine works in its own per-branch worktree
   under a `wt/<machine>/<topic>` namespace.

If you are an agent picking this up: read this whole file before changing
anything. Many design decisions were deliberate trade-offs and are non-obvious.

## Architecture

### Source of truth

- **`workspace.toml`** at the workspace root is the single source of truth
  for project registration. It lists projects, their remotes, status,
  category, default branch, and per-project sync flags. It is committed to
  git and synced between machines via the workspace's own git repo.
- The reconciler ensures `workspace.toml` is mergeable across machines by
  installing a `merge=union` driver in `.gitattributes`. Concurrent
  additions of different projects from different machines merge cleanly
  without manual intervention.

### On-disk layout (per project)

After `ws migrate`, every project lives as a sibling triplet under its
category directory:

```
personal/
├── myapp/                       ← main worktree (project.default_branch)
│   └── .git                     ← file pointing into ../myapp.bare
├── myapp.bare/                  ← bare repo, source of truth for git state
└── myapp-wt-<machine>-<topic>/  ← extra per-feature worktrees, optional
```

- `<project>/` keeps its original path so `cd personal/myapp` still drops
  the user into a working repo. Tooling that doesn't understand worktrees
  generally still works because `.git` is a valid pointer file.
- `<project>.bare/` is the only place git objects live. Worktrees share it.
- `<project>-wt-<machine>-<topic>/` is the convention for extra worktrees
  created by `ws worktree new`. The directory name has dashes only (no
  slashes), but the underlying branch name preserves slashes:
  `wt/<machine>/<topic>`.

### Branch naming convention

- `wt/<machine>/<topic>` — feature/WIP branches owned by one machine.
  These are the **only** branches the reconciler will push automatically.
  Any other local branch is the user's private business and is never
  touched by the daemon.
- The `<machine>` segment is taken from `~/.config/ws/config.toml` field
  `machine_name`. The user is prompted to set it on first use; the default
  is a sanitized version of `os.Hostname()`.
- `<topic>` may contain slashes. Slashes are preserved in the branch name
  but flattened to dashes in directory names.

### Reconciler (the daemon's brain)

`internal/daemon/reconciler.go` is a single state-machine that replaces the
old split Syncer/Poller pair. On each tick (immediate at startup, then on
the configured interval, plus on `config_changed` IPC notifications) it:

1. **Phase 1 — `syncTOML`.** Commits any local changes to `workspace.toml`
   under a `ws: auto-sync workspace.toml from <machine>` message, fetches,
   handles every combination of `local_dirty`/`local_ahead`/`remote_ahead`
   via a fixed decision matrix, falls back to `pull --rebase` (which is
   safe thanks to union-merge), and records `toml-merge`/`toml-push-failed`
   conflicts when even rebase fails.

2. **Phase 2 — `reconcileProjects`.** For every active project:
   - If `<path>.bare` is missing but `<path>` exists, record a
     `needs-migration` conflict and skip. Plain checkouts are never
     auto-migrated — the user runs `ws migrate` explicitly.
   - `git fetch --all --prune --tags` in the bare. Failure increments a
     per-project exponential backoff (base = poll interval, cap = 1h).
   - For each worktree returned by `git worktree list`:
     - Skip if `index.lock` is present (the user is mid-edit).
     - **Owned branches** (`wt/<this-machine>/*`): push if ahead. Diverged
       → record `branch-divergence` and stop touching it.
     - **Main worktree** (the one at `proj.path`): if clean and only
       behind, `git pull --ff-only`. Diverged → record `main-divergence`
       and leave it. Dirty → silently skip.
     - **Other machines' branches**: no-op. Fetch already updated their
       refs in the bare.
   - `auto_sync = false` on a project limits the work to fetch-only.

3. **Phase 3 — conflict bookkeeping.** New conflicts are persisted to
   `~/.local/state/ws/conflicts.json` (XDG-aware) and surfaced via
   `notify-send` (best-effort, silent fallback). The reconciler also
   clears stale entries on each tick when their underlying condition
   has been resolved.

The reconciler is **idempotent**: missed ticks and duplicate triggers
never break state, because each tick recomputes desired vs actual from
scratch.

### Migration (`ws migrate`)

`internal/migrate/migrate.go` converts a plain `git clone` checkout into
the bare+worktree layout in place. It is intentionally **fail-safe rather
than reversible** — there is no `ws unmigrate`, but every step before the
irreversible final swap preserves the original `.git` so the user can
recover by hand.

Key invariants:

- **Stash entries always abort.** Stash is bound to the soon-to-be-deleted
  `.git` and would be lost. The user must pop or drop first.
- **Detached HEAD always aborts.**
- **Dirty working trees abort by default.** With `--wip`, the dirty state
  is committed to a fresh `wt/<machine>/migration-wip-<ts>` branch and
  attached as a sibling worktree. The main worktree is then switched
  back to the original branch so the user lands where they left off.
- **All local branches are preserved into the bare** via `clone --bare
  --no-local` plus belt-and-suspenders `git fetch <main> <branch>` for
  any branch that the clone missed.
- **Hooks are migrated.** Files in `.git/hooks/` that are not `*.sample`
  and have an executable bit get copied to `<bare>/hooks/`.
- **The final swap uses `mv .git .git.migrating-<ts>`** (not `rm`) so the
  original is recoverable until verification passes. Verification checks
  that the new worktree is a valid git repo and that HEAD still points
  at the same commit. Only then is the moved-aside `.git` deleted.

`ws migrate --check` reports state without changing anything. `ws migrate
--all` walks every active project, skipping already-migrated ones.

### Conflict store and `ws sync resolve`

`internal/conflict/conflict.go` owns `~/.local/state/ws/conflicts.json`.
The reconciler is the only writer; `ws sync resolve` is the only reader
that mutates entries. Coordination is via the file alone (atomic write
via tmp+rename); there is no IPC between them. The store deduplicates
on `(workspace, project, branch, kind)` so a recurring condition does
not produce duplicate entries on every tick.

`ws sync resolve` is a prompt-based CLI (intentionally not a TUI in v1).
It lists conflicts, lets the user open a shell in the affected worktree
or workspace repo, shows `git log local..remote` and `remote..local`,
and clears entries when the user confirms a fix. **It never auto-rebases
or auto-merges anything** — every action that modifies git state is
explicitly the user's choice via the spawned shell.

## Project statuses

- `active` — cloned locally, actively developed
- `archived` — not needed locally. Personal: tar.gz in `archive/`.
  Work: removed. **Note:** archive of migrated (bare+worktree) projects
  is not yet supported and refused with a clear message.
- `dormant` — still cloned but no recent activity (detected by daemon)

## Categories

- `personal` — user's own repos
- `work` — organization repos

## Per-project fields (`workspace.toml`)

```toml
[projects.myapp]
remote         = "git@github.com:user/myapp.git"
path           = "personal/myapp"   # main worktree, relative to ws root
status         = "active"
category       = "personal"
default_branch = "main"             # determined at migrate time, prompt fallback
auto_sync      = true               # default true; false = fetch only
group          = "..."              # optional grouping
```

## Commands

### Project management

| Command | Purpose |
|---|---|
| `ws add <remote-url>` | Register and clone a new repo into `workspace.toml`. |
| `ws sync` | Run **one reconciler tick** in the foreground (commit/push/pull `workspace.toml`, fetch every bare, ff-pull main worktrees, push owned `wt/<machine>/*` branches). Same work as a daemon tick. |
| `ws sync resolve` | Inspect and act on unresolved conflicts from `~/.local/state/ws/conflicts.json`. Prompt-based; never auto-merges. |
| `ws status` | Table: PROJECT / GROUP / STATUS / BRANCH / LAST COMMIT / LAYOUT. The LAYOUT column reads `plain`, `worktree`, `worktree+N` (where N is the count of extra worktrees), or `missing`. |
| `ws list [--filter ...]` | Filtered list of projects. |
| `ws scan` | Find git repos under `personal/`, `work/`, `playground/`, `researches/`, `tools/` that are not in `workspace.toml`. **Ignores `*.bare/` and `*-wt-*/` siblings** so the worktree layout doesn't show up as orphans. |
| `ws clean [name] [--all]` | Remove dependency/build caches (`node_modules`, `target/`, `.venv`, `dist/`, `.next/`, `.svelte-kit/`, etc.) from a project's main worktree. |
| `ws archive <name>` | Archive a project. Personal → `tar.gz` into `archive/` then remove. Work → just remove. **Refused for migrated projects in v1** (full worktree-aware archiving is planned). |
| `ws restore <name>` | Restore an archived project (untar or re-clone). |

### Worktree layout

| Command | Purpose |
|---|---|
| `ws migrate <name>` | Convert a plain checkout to the bare+worktree layout in place. Verify-before-delete; preserves all local branches and active hooks. |
| `ws migrate --all` | Migrate every active project. Skips already-migrated. |
| `ws migrate --check [name...]` | Preview without changes. Shows state and any blockers (dirty, stash, detached HEAD, hook count). |
| `ws migrate <name> --wip` | Snapshot dirty working tree to a `wt/<machine>/migration-wip-<ts>` branch and attach as a sibling worktree. |
| `ws worktree new <project> <topic> [--from <base>]` | Create a worktree on branch `wt/<machine>/<topic>`. Base is `proj.default_branch` unless `--from` is given. The branch must not already exist. |
| `ws worktree list [project]` | Table: PROJECT / WORKTREE / BRANCH / STATE. STATE includes clean/dirty, ahead/behind, and an owner tag (`main`, `mine`, `remote`, `shared`). |
| `ws worktree rm <project> <topic> [--force]` | Remove a worktree. Refuses if dirty or has unpushed commits unless `--force`. Does not delete the underlying branch (intentional — prevents accidental loss of unpushed work). |
| `ws wt …` | Alias for `ws worktree`. |

### Groups and aliases

| Command | Purpose |
|---|---|
| `ws group add <name>` | Create a project group. |
| `ws group list` | List groups. |
| `ws group show <name>` | Projects in a group. |
| `ws alias list` | Show configured shell aliases. |
| `ws alias add <alias> <target>` | Add an alias. |
| `ws alias rm <alias>` | Remove an alias. |
| `ws alias init [shell]` | Generate alias init code for the user's shell (zsh). |
| `ws alias install` | Install the hook into `~/.zshrc`. |

### Daemon

| Command | Purpose |
|---|---|
| `ws daemon run` | Foreground (used by `start` after fork). |
| `ws daemon start` | Background-spawn the daemon. |
| `ws daemon stop` | Stop the daemon. |
| `ws daemon restart` | Stop + start. |
| `ws daemon status` | PID + running state. |
| `ws daemon register [path]` | Add a workspace to the daemon's config so it gets reconciled on every tick. |
| `ws daemon unregister [path]` | Remove a workspace from the daemon config. |
| `ws daemon install-service` | Install systemd unit for the daemon. |

### GitHub auth (for repo discovery)

| Command | Purpose |
|---|---|
| `ws auth login` | Device flow or PAT. |
| `ws auth logout` | Remove the stored token. |
| `ws auth status` | Token status. |

### Setup

| Command | Purpose |
|---|---|
| `ws setup` | Interactive bootstrap of a new workspace directory. |

## Files the CLI relies on

- `<wsRoot>/workspace.toml` — project registry, single source of truth.
- `<wsRoot>/.gitattributes` — `workspace.toml merge=union` (created by reconciler).
- `~/.config/ws/config.toml` — `machine_name` for branch namespacing.
- `~/.config/ws/daemon.toml` — list of workspaces watched by the daemon plus socket path.
- `~/.config/ws/daemon.{sock,pid,log}` — daemon runtime files.
- `~/.local/state/ws/conflicts.json` — unresolved sync conflicts. Honors `$XDG_STATE_HOME`.

## Conventions

- All paths in `workspace.toml` are relative to the workspace root.
- Scripts and reconciler logic must be idempotent — safe to re-run.
- No secrets in this repo.
- `workspace.toml` is the only file that changes during normal operation
  (plus `.gitattributes` once, on the reconciler's first run).
- The daemon **never** runs `merge`, `rebase`, `reset`, or `force` inside
  a project repo. The worst it does is record a conflict and stop.
- Branches outside the `wt/<machine>/*` namespace are private to the user
  and are **never** pushed by the reconciler.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` — new feature (bumps minor pre-1.0, would be minor post-1.0)
- `fix:` — bug fix (bumps patch)
- `feat!:` or `fix!:` with `BREAKING CHANGE:` footer — breaking change
  (bumps minor pre-1.0, major post-1.0)
- `chore:`, `docs:`, `refactor:`, `test:`, `ci:`, `style:`, `perf:` — no release

Scope is optional: `feat(alias): ...`, `fix(sync): ...`.

Never add `Co-Authored-By` or attribution footers.

## Release process

Automated via [release-please](https://github.com/googleapis/release-please).

**Flow:** conventional commits land on `main` → release-please opens/updates
a Release PR with bumped version + CHANGELOG → merge the PR → tag `vX.Y.Z`
is pushed → existing `release.yml` builds binaries and publishes the GitHub
Release.

**Do NOT** manually edit `CHANGELOG.md`, bump versions, or create `vX.Y.Z`
tags by hand — release-please owns all of it.

## Known follow-ups (not yet implemented)

These were deliberately deferred during the worktree refactor and are open
for future work:

- **Worktree-aware `ws archive` / `ws restore`.** Currently archive refuses
  for migrated projects. Need to tar bare + main + extra worktrees and run
  `git worktree repair` on restore.
- **`ws add` should clone as bare + worktree** instead of plain. Today it
  still clones plain and `ws migrate` is required afterwards.
- **`ws worktree gc`** to clean up old WIP branches and orphaned worktrees.
- **fsnotify on `workspace.toml`** to remove the dependency on IPC
  notifications from CLI commands.
- **Real TUI for `ws sync resolve`** instead of the prompt-based v1.
- **Per-machine `default_branch` override** for the rare case of different
  default branches across machines for the same project.
