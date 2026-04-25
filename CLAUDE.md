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

0. **Sidecar pre-check.** Before any work, the reconciler calls
   `sidecar.AnyActive(wsRoot)`, which checks every known sidecar kind
   (`bootstrap`, `migrate`, `add`) for the workspace at
   `~/.local/state/ws/<kind>/<sha>.toml`. If any sidecar exists and its
   recorded pid is alive, the entire tick is skipped for that workspace —
   both Phase 1 and Phase 2. This prevents the daemon from pushing
   half-completed state upstream and from racing the interactive command
   on git operations. Other registered workspaces (each with their own
   reconciler goroutine) are unaffected. Stale sidecars (pid dead) are
   ignored, and the tick proceeds normally.

1. **Phase 1 — `syncTOML`.** Commits any local changes to `workspace.toml`
   under a `ws: auto-sync workspace.toml from <machine>` message, fetches,
   handles every combination of `local_dirty`/`local_ahead`/`remote_ahead`
   via a fixed decision matrix, falls back to `pull --rebase` (which is
   safe thanks to union-merge), and records `toml-merge`/`toml-push-failed`
   conflicts when even rebase fails.

2. **Phase 2 — `reconcileProjects`.** For every active project:
   - If neither `<path>.bare` nor `<path>` exist (project registered in
     `workspace.toml` but nothing on disk), and `daemon.auto_bootstrap` is
     enabled (default `true`) and `auto_sync != false`, attempt
     `clone.CloneIntoLayout` non-interactively. Sequential by construction:
     one project per tick. Errors map to:
       - `ErrNeedsBootstrap` → conflict `needs-bootstrap` (default branch
         ambiguous, user must run `ws bootstrap <name>`)
       - `ErrPathBlocked` → conflict `path-blocked`
       - network/auth → existing per-project exponential backoff +
         `clone-failed` conflict
     On success, `default_branch` is persisted back into `workspace.toml`
     so other machines pick it up via the next Phase 1 sync.
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

Default UX is the **interactive bubbletea TUI** (`internal/cli/migrate_tui.go`):
scan → plan summary → per-project decision for any project that needs one
(`dirty / stash / detached`) → progress → done. CLI flags (`--all`,
`--check`, `--wip`, `--no-tui`) skip the TUI and run the legacy text flow,
which is also what happens when stdout is not a TTY (pipes, CI).

Pre-flight handling, in order. Each path that doesn't simply abort
creates an extra side branch that becomes part of the bare clone:

- **Detached HEAD.** Default: abort. Interactive `[c]` (or
  non-interactive `Options.CheckoutDefault=true`): if the current commit
  is reachable from any local branch, just `checkout default_branch`. If
  it's not reachable, first preserve it on a fresh
  `wt/<machine>/migration-detached-<ts>` branch so the orphaned commits
  survive into the bare clone.
- **Stash entries.** Default: abort (stash refs are not copied by
  `clone --bare`, so they would silently disappear). Interactive `[b]`
  (or `Options.StashBranch=true`): walk every entry via
  `git stash branch wt/<machine>/migration-stash-<ts>-N`, commit the
  popped state, and return to the original branch. The new branches are
  preserved into the bare like any other local branch.
- **Dirty working tree.** Default: abort. Interactive `[w]` (or
  `--wip` / `Options.WIP=true`): commit the dirty state to
  `wt/<machine>/migration-wip-<ts>`, then check out the original branch
  again so the post-migration main worktree matches the user's
  expectation. The WIP branch is attached as a sibling worktree after
  migration completes.

Other invariants:

- **All local branches are preserved into the bare** via `clone --bare
  --no-local` plus belt-and-suspenders `git fetch <main> <branch>` for
  any branch the clone missed.
- **Hooks are migrated.** Files in `.git/hooks/` that are not `*.sample`
  and have an executable bit get copied to `<bare>/hooks/`.
- **No upstream tracking is restored.** Bare repos clone with the mirror
  refspec `+refs/heads/*:refs/heads/*` and have no `refs/remotes/origin/*`
  refs at all, so `branch --set-upstream-to=origin/X` always fails. The
  worktree layout doesn't need it: the reconciler only auto-pushes
  `wt/<machine>/*` branches, and ordinary `git pull` in a worktree
  resolves its upstream lazily.
- **Worktree attach via --no-checkout + pointer swap.** `git worktree
  add --force <existing-non-empty-dir>` does NOT attach to a directory
  that already has files — `--force` only relaxes the
  "branch-already-checked-out" and "registered-but-missing" checks, not
  the path-existence check. Migrate's working strategy:
    1. Move existing `.git` aside to `.git.migrating-<ts>` (recoverable).
    2. `git worktree add --no-checkout <main>.wt-tmp <branch>` — git
       writes the worktree's `.git` pointer file to the tmp dir but no
       working-tree files.
    3. `mv <main>.wt-tmp/.git <main>/.git` — pointer file lands in the
       existing main path, on top of the user's untouched files.
    4. `rm -rf <main>.wt-tmp` (now empty).
    5. `git worktree repair <main>` so the bare's `worktrees/<name>/gitdir`
       points at `<main>` instead of the tmp location.
    6. Verify HEAD didn't shift.
  Any failure between steps 2–5 restores `.git.migrating-<ts>` and tears
  down the bare. Step 6 is the last point a rollback is feasible.

`ws migrate --check` reports state without changing anything. `ws migrate
--all` walks every active project, skipping already-migrated ones and
projects that are not cloned on this machine.

The migration process is coordinated with the daemon via a sidecar at
`~/.local/state/ws/migrate/<sha>.toml`. While migrate is running with a
live pid, the reconciler skips its tick entirely for the affected
workspace — both Phase 1 (workspace.toml git sync) and Phase 2 (project
reconcile) — preventing races on git operations and half-migrated state
being pushed upstream. Stale sidecars (crashed run) trigger a resume
prompt on the next `ws migrate` invocation.

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
| `ws add [remote-url...]` | Register and clone one or more new repos into `workspace.toml`, directly into the bare+worktree layout (no follow-up `ws migrate` needed). Accepts positional URLs, `-` for stdin (one URL per line, `#` comments allowed), and the legacy single-URL invocation. Flags: `-c`/`--category` personal\|work, `-g`/`--group`, `-n`/`--name` (single-URL only), `--no-clone` register-only, `--no-tui` force headless, `--tui` force TUI (Phase 3). Crash-safe via a sidecar at `~/.local/state/ws/add/`; daemon pauses while running. |
| `ws bootstrap [name]` | Interactive TUI: clone projects listed in `workspace.toml` that are missing on this machine, directly into the bare+worktree layout. Crash-safe via a sidecar at `~/.local/state/ws/bootstrap/`. While running, the daemon pauses all sync for this workspace. `--dry-run` shows the plan without cloning. |
| `ws migrate [name]` | Convert plain checkouts into the bare+worktree layout. Default: interactive TUI with per-project decisions for `dirty / stash / detached HEAD`. Pass any flag (`--all`, `--check`, `--wip`, `--no-tui`) or run without a TTY to switch to non-interactive mode. Crash-safe via a sidecar at `~/.local/state/ws/migrate/`; daemon pauses while running. See "Migration" below for the worktree-attach strategy. |
| `ws sync` | Run **one reconciler tick** in the foreground (commit/push/pull `workspace.toml`, fetch every bare, ff-pull main worktrees, push owned `wt/<machine>/*` branches). Same work as a daemon tick. |
| `ws sync resolve` | Inspect and act on unresolved conflicts from `~/.local/state/ws/conflicts.json`. Prompt-based; never auto-merges. |
| `ws status` | Table: PROJECT / GROUP / STATUS / BRANCH / LAST COMMIT / LAYOUT. The LAYOUT column reads `plain`, `worktree`, `worktree+N` (where N is the count of extra worktrees), or `missing`. |
| `ws scan` | Find git repos under `personal/`, `work/`, `playground/`, `researches/`, `tools/` that are not in `workspace.toml`. **Ignores `*.bare/` and `*-wt-*/` siblings** so the worktree layout doesn't show up as orphans. |
| `ws doctor [name] [--fix] [--json] [--skip-remote]` | Run unified health check across system (daemon, stale sidecars, active conflicts, config validity) and per-project state (layout, fetch refspec, remote URL, reachability, default branch, branch upstream, index locks). `--fix` applies all safe auto-fixes in batch; conflicts and index-locks are intentionally never auto-fixed. Exit codes: `0` clean, `1` issues found, `2` --fix applied. |

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

### Aliases

| Command | Purpose |
|---|---|
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
- `~/.local/state/ws/bootstrap/<sha>.toml` — per-workspace bootstrap progress sidecar. Created by `ws bootstrap`, deleted on success. While present with a live pid, the daemon skips its tick for that workspace. Honors `$XDG_STATE_HOME`.
- `~/.local/state/ws/migrate/<sha>.toml` — per-workspace migrate progress sidecar. Created by `ws migrate`, deleted on success. Same daemon-skip semantics. Honors `$XDG_STATE_HOME`. All three sidecar kinds (`bootstrap`, `migrate`, `add`) share `internal/sidecar` which centralizes file/lock/pid mechanics; command-specific value types live in their own packages and round-trip through `json.RawMessage`.
- `~/.local/state/ws/add/<sha>.toml` — per-workspace `ws add` session sidecar. Created when `ws add` starts (any mode), deleted on success/error/panic via `defer`. While present with a live pid, the daemon skips its tick for that workspace and a second `ws add` invocation refuses with an "is running" error. Honors `$XDG_STATE_HOME`.

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

## Tests

The project uses **real git in temp dirs** rather than mocks. Every test
spins up its own ephemeral git repos under `t.TempDir()` and runs real
`git` commands. This catches the kinds of bugs (the
`git worktree add --force` regression that motivated the migrate rewrite,
for example) that mock-based tests would happily lie about.

`internal/testutil/gitfixture.go` provides the shared helpers:

- `InitFakeRemote(t, name, defaultBranch) string` — creates a bare repo
  with a seed commit; usable as `proj.Remote` for clone/bootstrap tests.
- `InitFakePlainCheckout(t, parent, name, branches) string` — creates a
  non-bare git repo with N branches, each carrying one unique commit.
  Used as the input for migrate tests.
- `RunGit(t, dir, args...)` / `RunGitTry` — wraps `exec.Command("git", ...)`
  with a deterministic env (no global config, no GPG, fixed identity).
- `AddDirty`, `AddStash` — push the working tree into the dirty/stash
  states needed by migrate's pre-flight tests.

Test files live next to the code they cover, in `_test` packages:

- `internal/clone/clone_test.go` — happy path, ErrAlreadyCloned,
  ErrNeedsMigration, ErrPathBlocked, default_branch resolution.
- `internal/migrate/migrate_test.go` — happy path **(regression test for
  the worktree-attach bug)**, dirty + WIP, stash + branch conversion,
  detached HEAD with and without orphan preservation, ErrAlreadyMigrated.
- `internal/bootstrap/bootstrap_test.go` — `ScanPlan` classification of
  every project state, only-filter restriction.
- `internal/sidecar/sidecar_test.go` — Save/Load round-trip,
  Delete-is-idempotent, IsAlive with self/dead/zero pids, AnyActive
  finds either kind, AnyActive ignores stale entries.
- `internal/doctor/*_test.go` — per-check tests for the `ws doctor`
  catalog: happy-path runner, stale-sidecar auto-fix, conflict scoping,
  config validation, fetch-refspec/remote-URL/default-branch/branch-
  upstream fixes on real bare+worktree fixtures, index-lock detection.

Run everything: `go test ./...`. CI runs `go test -race -timeout 5m ./...`
on every push to main and on every PR via `.github/workflows/test.yml`.

When adding new git-touching code: write a real-git test for it. The
testutil helpers cover ~95% of fixture needs; extend them rather than
inlining `exec.Command` in tests.

## Known follow-ups (not yet implemented)

These were deliberately deferred during the worktree refactor and are open
for future work:

- **`ws worktree gc`** to clean up old WIP branches and orphaned worktrees.
- **fsnotify on `workspace.toml`** to remove the dependency on IPC
  notifications from CLI commands.
- **Real TUI for `ws sync resolve`** instead of the prompt-based v1.
- **Per-machine `default_branch` override** for the rare case of different
  default branches across machines for the same project.
