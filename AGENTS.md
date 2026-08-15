# AGENTS.md

The single source of project knowledge and agent operating procedure for
this repository. Read it top to bottom before changing anything.

Instructions apply to AI agents and human developers. Sections are layered:
project knowledge, performance protocol, then agent obligations.

## High-Level Goal

`ws` tracks, synchronizes, and operates development projects across the
user's machines. The same project registry, branches, and work-in-progress
should be available everywhere without hidden or destructive repository
operations.

The core invariants are:

1. **SQLite is runtime authority.** `$XDG_STATE_HOME/ws/registry.db` is the
   authoritative runtime registry. `workspace.toml` is import/export and
   migration interchange only; normal commands do not use it as runtime state.
2. **Named local workspaces.** Each SQLite workspace has a unique name and
   canonical root. Commands select the longest containing root unless an exact
   root is supplied. Explorer reads every workspace in the SQLite registry.
3. **Foreground-only synchronization.** `ws sync` performs preflight,
   review, confirmation, execution, and summary. There is no background
   service, watcher, timer, IPC channel, or retry scheduler.
4. **No project branch auto-push to origin.** `ws sync` fetches project
   state and may fast-forward an eligible main worktree, but origin branch
   pushes are explicit through `ws worktree push` or plain `git push`.
5. **No destructive project operations.** Sync never runs project merge,
   rebase, reset, force, branch deletion, or branch push. Unsafe states are
   skipped and may become conflicts.
6. **Worktree-first layout.** Each project uses one bare object store and
   separate main/feature worktrees. Branch names are literal repo-native
   names such as `feat/foo`; legacy `wt/<machine>/*` branches still resolve.

These are deliberate trade-offs. Do not reintroduce background behavior or
hidden branch publication as a convenience.

## Architecture

### Runtime and Interchange State

`$XDG_STATE_HOME/ws/registry.db` stores named workspaces, roots, project
registration, groups, aliases, mirrors, explorer preferences, and branch
metadata. Paths inside a workspace registry are relative to its root.

`workspace.toml` is accepted by `ws workspace import` and emitted by
`ws workspace export`. It is not read or written during normal operation and
is never synchronized by `ws sync`.

`~/.config/ws/config.toml` is machine-local:

```toml
machine_name = "linux"
```

Machine-specific preferences remain in this file. Workspace names, roots, and
registry contents live in SQLite.

### On-Disk Project Layout

After `ws migrate`, or immediately for projects created by `ws add` /
`ws create`, paths are siblings:

```text
personal/
├── myapp/                              main worktree
│   └── .git                            pointer into ../myapp.bare
├── myapp.bare/                         git object store
└── myapp-wt-linux-feat-auth-refactor/  optional feature worktree
```

- `<project>/` keeps the original project path stable.
- `<project>.bare/` owns git objects and refs shared by all worktrees.
- Extra directories use `<project>-wt-<machine>-<branch-slug>`.
- Slashes flatten only in the directory name. The branch remains literal.
- Slug collisions receive deterministic `-<sha8>` suffixes from
  `SHA-1(branch)`.

### Branch Naming and Metadata

`ws worktree add <project> <branch>` accepts the branch verbatim and
validates it with `git check-ref-format --branch`. It does not synthesize a
namespace or enforce a project convention.

Per-branch ownership lives in `[[projects.X.branches]]`:

- `machines` identifies machines with a local worktree.
- `last_active_*` is updated by worktree creation, explicit push, explorer
  launch stamping, and foreground sync observing local-ahead commits.
- `last_pushed_*` is written after explicit publication or attachment to an
  already-published branch. Orphan detection requires this signal.
- `created_*` records explicit branch/worktree creation. Explorer launch
  stamping may create minimal entries without creator fields.

`ws worktree add` claims a branch for this machine; `ws worktree rm`
releases it. Empty `machines` removes the entry on save. Plain `git push`
works but does not update metadata.

Legacy `wt/<machine>/<topic>` branches can be re-registered by calling
`ws worktree add` with the existing branch. Unregistered legacy worktrees
remain usable and are ignored by metadata refresh.

### Foreground Sync Core

`internal/sync` owns synchronization. `internal/cli/sync*.go` owns TTY
detection, TUI/headless presentation, signals, exit codes, and summaries.

The CLI flow is:

```text
fresh config.Load
      |
      v
BuildPlan -> Probe (parallel, noninteractive, mutation-free)
      |                |
      |                +-> verified known-provider SSH candidates
      v
Selection (interactive run-only choices or strict headless all-target run)
      |
      v
Runner.RunContext (frozen plan, sequential execution)
      |
      +-> selected origin conversions
      +-> selected project origin/fetch/mirrors/worktrees/orphans
      +-> metadata save and typed Report/Event stream
```

#### Plan

`BuildPlan` snapshots each active project's remote, path, status, default
branch, and mirrors. It classifies disk state as `present`, `missing`,
`needs-migration`, or `blocked`.

Targets are every active project origin and configured mirror. Exact URL
duplicates share one
endpoint. Endpoints are grouped by source identity for review. Ordering is
deterministic.

The snapshot is a safety boundary. A project whose sync-relevant SQLite fields
changed after preflight is skipped with `plan-changed`. Active projects
introduced after preflight are also skipped.

#### Probe

`Probe` checks unique endpoints with up to eight workers, noninteractive git,
and a 15-second per-endpoint timeout. It distinguishes success,
authentication/access failure, timeout, unreachable, unsupported, and
canceled. Probe never mutates repositories, config, or conflicts.

For a failed HTTPS origin on a known provider, probe may derive the exact SSH
form and test it. Conversion becomes selectable only after that SSH endpoint
succeeds. Mirrors are not conversion targets.

#### Selection and UI

Accessible targets begin selected. In the TUI, users may exclude sources,
projects or mirrors for this invocation only.
Excluding a project excludes its mirrors. These choices are ephemeral and
must not become persisted sync preferences.

Both stdin and stdout must be terminals for the TUI. Otherwise headless mode
prints deterministic ANSI-free output, requires every endpoint to pass, and
does no mutation if any preflight probe fails. Headless mode does not choose
SSH conversion automatically.

Exit codes are `0` success, `1` preflight/execution/conflict failure, and
`130` cancellation.

#### Runner

`Runner.RunContext` checks live sidecars, applies selected verified project
origin conversions, then processes projects sequentially in sorted order.

Project conversion updates both the local repository origin and SQLite
registry; a failed registry save rolls repository origins back. Conversions
are always explicit interactive choices backed by successful probes.

For each selected project:

- Missing paths clone into the bare+worktree layout.
- Plain checkout state records `needs-migration`.
- Incompatible paths record `path-blocked`.
- Present bare repositories repair the origin fetch refspec when needed and
  run `git fetch --all --prune --tags`.
- Selected mirrors are pushed after origin fetch.
- Clean main worktrees that are behind and not ahead fast-forward with
  `git pull --ff-only`.
- Diverged main worktrees record `main-divergence`; dirty main worktrees and
  index-locked worktrees are left alone.
- Registered sibling worktrees with local-ahead commits refresh activity
  metadata without pushing.
- Previously pushed registered branches missing from origin record
  `branch-orphan`; local-only branches are skipped by orphan detection.

Cancellation stops new work and waits for in-flight work to return. The
report records operation starts, results, conversions, conflicts, skips, and
cancellation for live progress and final summary.

There is no background retry, cooldown, backoff, auto-bootstrap setting, or
per-project `auto_sync` field. Fix failures and invoke `ws sync` again.

### Workspace Registry Storage

SQLite is the runtime authority. Import and export are explicit interchange
operations; project Git sync does not transfer registry state.

### Sidecars

`ws add`, `ws create`, `ws bootstrap`, and `ws migrate` use sidecars at
`~/.local/state/ws/<kind>/<sha>.toml`. Sidecars support crash recovery and
same-workspace command exclusion. Foreground sync skips when any live sidecar
would make execution race an in-progress operation.

`internal/sidecar` centralizes path, lock, pid, load/save/delete, and stale
process behavior. Command packages own command-specific payloads. Stale
bootstrap and migrate sidecars can be reported and removed by
`ws doctor --fix`.

Sidecars do not signal or pause a background process; none exists.

### Migration

`internal/repo/migrate.go` converts a plain checkout to the bare+worktree
layout. It is fail-safe rather than generally reversible. Preflight handles:

- Detached HEAD: abort by default, or preserve unreachable commits on a
  migration branch before checking out the default branch.
- Stash entries: abort by default, or materialize each stash into a migration
  branch and commit it.
- Dirty tree: abort by default, or snapshot it to a migration WIP branch.

Migration preserves all local branches and executable non-sample hooks.
Internal recovery branches use
`wt/<machine>/migration-{detached,stash,wip}-<timestamp>` and become part of
the bare repository.

To attach the existing non-empty project directory safely:

1. Move `.git` aside to a recoverable path.
2. Add a temporary worktree with `--no-checkout`.
3. Move only the pointer file into the existing project directory.
4. Remove the empty temporary directory.
5. Repair worktree metadata for the final path.
6. Verify HEAD did not change.

Failures before final verification restore the original `.git` and remove
the incomplete bare repository. `ws migrate --check` is read-only;
`ws migrate --all` skips already-migrated or missing projects.

### Conflict Store and Resolution

`internal/conflict` owns `~/.local/state/ws/conflicts.json`. Foreground sync
records and clears observed conditions. Records deduplicate on
`(workspace, project, branch, kind)` and save atomically.

`ws sync resolve` is an interactive prompt. It can open a relevant shell or
editor, retry a mirror push, or apply branch-metadata actions selected by the
user. It never automatically merges or rebases project work.

Conflict kinds currently include `main-divergence`, `needs-migration`,
`needs-bootstrap`, `path-blocked`,
`clone-failed`, `branch-duplicate`, `branch-orphan`, and
`mirror-push-failed`.

## Project Statuses

- `active`: included in sync planning and ordinary active use.
- `dormant`: retained in the registry but excluded from sync planning.
- `archived`: retained but excluded from sync planning.

Status transitions are explicit registry changes; there is no background
activity classifier.

## Categories

- `personal`: the user's own repositories.
- `work`: organization repositories.

## Workspace Fields

The `[agent]` block is stored in each SQLite workspace and represented in TOML exports:

```toml
[agent]
default_view = "favorites" # "all" by default
```

Machine-specific preferences belong in `~/.config/ws/config.toml`, not this
block.

## Per-Project Fields

```toml
[projects.myapp]
remote         = "git@github.com:user/myapp.git"
path           = "personal/myapp"
status         = "active"
category       = "personal"
default_branch = "main"
favorite       = true
group          = "personal"

[projects.myapp.mirrors]
codeberg = "git@codeberg.org:user/myapp.git"

[[projects.myapp.branches]]
name                = "feat/auth-refactor"
machines            = ["linux", "archlinux"]
last_active_machine = "linux"
last_active_at      = "2026-05-08T12:00:00Z"
last_pushed_machine = "linux"
last_pushed_at      = "2026-05-07T16:30:00Z"
created_by          = "linux"
created_at          = "2026-04-08T13:59:04Z"
```

Do not add `auto_sync`; the field no longer exists. Legacy `autopush`
configuration is migrated to branch metadata on load and removed on save.

## Commands

### Project Management

| Command | Purpose |
|---|---|
| `ws setup` | Interactive GitHub repo selection, registry creation, and local workspace-root registration. |
| `ws add [remote-url...]` | Register and clone one or more repositories directly into the bare+worktree layout; supports stdin and interactive/headless modes. |
| `ws create` | Create a GitHub repository through `gh`, then register and clone it. |
| `ws bootstrap [name]` | Clone registered projects missing on this machine; supports interactive and dry-run flows. |
| `ws migrate [name]` | Convert plain checkouts into the bare+worktree layout. |
| `ws sync` | Explicit preflight, optional interactive selection/conversion, sequential synchronization, and summary. |
| `ws sync resolve` | Inspect and manually resolve persisted conflicts. |
| `ws status` | Show project, group, status, branch, last commit, and layout. |
| `ws scan` | Find unregistered repositories while ignoring bare/worktree siblings. |
| `ws path [project]` | Print the workspace or project path for scripts. |
| `ws doctor [name] [--fix] [--json] [--skip-remote]` | Check system and project health; apply only safe fixes. |
| `ws favorite add/rm/list <project>` | Manage explorer favorites stored in SQLite. |

### Workspace Registry

| Command | Purpose |
|---|---|
| `ws workspace create [path] --name <name>` | Create an empty named SQLite workspace; path defaults to cwd. |
| `ws workspace import <workspace.toml> --name <name> --root <path>` | Import TOML interchange data into a named SQLite workspace. |
| `ws workspace export <name>` | Export a workspace as TOML. |
| `ws workspace list` | List named local workspaces and roots. |

These commands do not synchronize anything.

### Worktrees

| Command | Purpose |
|---|---|
| `ws worktree add <project> <branch> [--from <base>]` | Create, attach, or re-register a worktree for the literal branch. |
| `ws worktree list [project]` | Show worktrees, clean/dirty state, ahead/behind state, ownership, and last activity. |
| `ws worktree rm <project> <branch> [--force]` | Remove a non-main worktree and release machine ownership. |
| `ws worktree push <project> <branch> [--force-dirty]` | Explicitly publish the branch and stamp push/activity metadata. |
| `ws wt ...` | Alias for `ws worktree`. |

### Explorer and Organization

| Command | Purpose |
|---|---|
| `ws` / `ws explorer` | Open the shell-oriented multi-workspace explorer in a TTY. |
| `ws alias` | Interactive alias management. |
| `ws alias add/rm/list` | Headless alias management. |
| `ws alias init [zsh]` | Generate shell initialization code. |
| `ws alias install` | Install the generated alias-state hook in zsh. |

### Authentication and Setup

| Command | Purpose |
|---|---|
| `ws auth login/logout/status` | Manage the token used for GitHub discovery. |
| `ws docs --agent` | Emit generated command capability metadata. |

`ws create` uses `gh` and therefore requires separate `gh auth login`.

## Runtime Files

- `$XDG_STATE_HOME/ws/registry.db`: authoritative runtime workspace registry.
- `workspace.toml`: optional import/export and migration interchange data.
- `~/.config/ws/config.toml`: machine-local settings including `machine_name`.
- `~/.config/ws/token`: GitHub discovery token.
- `~/.local/state/ws/conflicts.json`: unresolved sync conflicts.
- `~/.local/state/ws/<kind>/<sha>.toml`: command sidecars for `add`,
  `create`, `bootstrap`, and `migrate`.
- `~/.local/state/ws/aliases.zsh`: generated shell aliases.
- `~/.local/state/ws/metrics.json`: local-only bounded fixed-schema usage
  counters; never contains identifiers, arguments, diagnostics, or history.

There are no service, socket, pid, log, watcher, or IPC runtime files.

## Conventions

- Paths in SQLite registries and exported `workspace.toml` are relative to the workspace root.
- Synchronization and command recovery paths must be safe to re-run.
- No secrets belong in this repository.
- **No comments in production Go by default.** Prefer names, types, control
  flow, and file boundaries. Permitted exceptions are build constraints,
  package docs, approved `DECISION` rationale, and approved
  `TODO`/`FIXME`/`HACK` markers. Tests may explain invariants.
- **TUI consumers import `internal/tui` only.** Direct Charmbracelet imports
  outside `internal/tui` are regressions.
- Normal operation may change `registry.db`, conflict state, command sidecars,
  machine config, and generated alias state according to the invoked command.
  Only explicit import/export commands touch `workspace.toml`. Nothing runs in
  the background.
- `ws sync` never pushes project branches to origin or performs project
  merge, rebase, reset, force, or deletion.
- Do not hand-edit `[[projects.X.branches]]` except to resolve a confirmed
  `branch-duplicate`. Use `ClaimBranch`, `ReleaseBranch`, `TouchActive`,
  `StampActivity`, `MarkPushed`, and `RemoveBranch` through their CLI flows.

## Commits

Use Conventional Commits:

- `feat:` for user-visible capability.
- `fix:` for bug fixes.
- `feat!:` or `fix!:` with a `BREAKING CHANGE:` footer for breaking change.
- `chore:`, `docs:`, `refactor:`, `test:`, `ci:`, `style:`, and `perf:` do
  not trigger a feature/fix release increment.

Scope is optional. Never add `Co-Authored-By` or AI attribution footers.

## Release Process

Release Please owns version selection, changelog updates, tags, and GitHub
release creation from conventional commits on `main`. When it creates a
release, the same workflow calls the reusable release-assets workflow, which
runs the full quality gate, cross-compiles the four supported binaries,
creates checksums, and uploads all assets to that release.

Do not hand-pick versions or create release tags manually. If asset
publication fails, manually dispatch the release-assets workflow with the
existing release tag to retry it.

## Tests

The project uses real git repositories under `t.TempDir()` instead of mocks.
Git-touching changes require real-git tests. Shared helpers live in
`internal/testutil/gitfixture.go`:

- `InitFakeRemote` creates a seeded bare remote.
- `InitFakePlainCheckout` creates a non-bare checkout with local branches.
- `RunGit` / `RunGitTry` run deterministic git commands.
- `AddDirty` / `AddStash` create migration states.

Current coverage locations include:

- `internal/git/*_test.go`: clone, remote parsing/probing, context handling,
  mirrors, and worktrees.
- `internal/repo/migrate_test.go`: migration preservation and rollback.
- `internal/repo/bootstrap_test.go`: bootstrap planning.
- `internal/sidecar/sidecar_test.go`: lifecycle and active/stale behavior.
- `internal/sync/*_test.go`: plans, parallel probes, selections,
  conversions, projects, mirrors, cancellation, and reports.
- `internal/cli/sync_*_test.go`: strict headless output/exit behavior and TUI
  model transitions.
- `internal/config/machine_test.go`: local workspace-root registry and legacy
  root migration.
- `internal/agent/workspaces_test.go`: multi-workspace explorer loading.
- `internal/cli/doctor_*_test.go`: system and project health checks.

Run everything with `go test ./...`. CI runs
`go test -race -timeout 5m ./...` on every push and PR.

## Known Follow-Ups

- `ws worktree gc` for old migration/WIP branches and orphaned worktrees.
- A full TUI for `ws sync resolve` instead of the prompt-based flow.
- Per-machine `default_branch` override for repositories whose local default
  differs between machines.

---

# Performance Protocol

The repository has a three-tier human benchmark workflow. Agents do not run
benchmarks, refresh baselines, or add benchmark requirements unless the user
explicitly asks.

## Tiers

- **L1 (`just bench-l1`)**: package microbenchmarks for pure Go hot paths,
  roughly 30-60 seconds.
- **L2 (`just bench-l2`)**: synthetic-workspace macrobenchmarks under the
  `bench_l2` build tag, roughly 3-5 minutes. This includes foreground sync
  over real fake repositories.
- **L3 (`just bench-l3`)**: end-to-end binary scenarios measured with
  `hyperfine`, roughly 10-15 minutes. Trend-only, never a gate.

## Priority: CLI Cold Start

Every command pays initialization, config load, and cobra dispatch. Bias new
coverage toward `internal/config`, `internal/cli`, and `cmd/ws/main.go`.
Allocation rate matters because GC inflates visible tail latency.

## Per-Machine Baselines

```text
bench/
  baseline/<machine>/{l1.txt,l2.txt,l3.json}
  results/<machine>/
  thresholds.toml
```

`just bench-compare` is advisory. Never modify benchmark baselines as part of
ordinary feature or documentation work.

---

# Agent Obligations

## When in Doubt

Do not invent. Ask the user when a choice changes scope, risk, or architecture.

## Mechanical Rules

### No Big Go Files

- Beyond about 500 lines: extract a cohesive cluster on the next touch.
- Beyond 800 lines: extract before adding more code.
- Tests count, but split production first when they are one unit.

### No Decorative Separators

Never add visual section-divider comments. A chunk that needs one belongs in
its own file or package. Package docs, exported-symbol docs, build constraints,
and license headers are not decorative separators.

### Function Complexity

- Cyclomatic complexity over 15: extract now.
- Complexity over 10: split on the next touch before adding branches.
- Bubble Tea reducers and cobra builders may be naturally branchy, but a
  switch that hides a real sub-machine still needs extraction.

Check with:

```sh
go run github.com/fzipp/gocyclo/cmd/gocyclo@latest -over 10 -ignore '_test\.go' .
```

### Comments Are a Last Resort

Prefer clearer code. A justified comment captures a non-obvious invariant,
external constraint, deliberate inefficiency, or workaround that code cannot
express. Do not narrate obvious operations.

## Architectural Changes Require Approval

Do not decide module boundaries, abstractions, provider contracts, or
cross-cutting behavior without approval. First provide:

1. Proposed shape, file list, dependency direction, blast radius, and what
   remains unchanged.
2. One or two rejected alternatives and why.
3. A wait point for user approval.

Mechanical extraction from an oversized file, deleting dead code, or renaming
a local variable does not need architecture approval.

Changes that do require approval include:

- Changing the plan/probe/selection/runner boundary or execution ordering.
- Persisting interactive sync selections or introducing background sync.
- Changing known-provider remote conversion policy.
- Changing conflict persistence or resolution ownership.
- Adding a sidecar kind, field, or lifecycle.
- Introducing a new interface, abstraction layer, feature flag, or build-time
  toggle.

## Other Agent Conventions

- Use `ws` for workspace operations: `ws status`, `ws sync`,
  `ws sync resolve`, `ws workspace create/import/export/list`, and
  `ws worktree add/list/push/rm`.
- Start non-trivial work with
  `ws worktree add workspace <type>/<kebab-topic>`; do not branch in main.
- Branch names use `<type>/<kebab-topic>` with allowed types `feat`, `fix`,
  `chore`, `docs`, `refactor`, `test`, `ci`, `style`, or `perf`.
- Commit messages and PR titles use
  `<type>(<scope>): <imperative result-oriented description>`.
- Never create new `wt/<machine>/*` branches; that namespace is legacy or
  migration-internal only.
- Never use stale `ws worktree new`, `ws worktree promote`, `--auto-push`,
  `autopush.branches`, daemon commands, or service setup guidance.
- Open PRs as draft by default. Only the user marks them ready.
- Never add attribution footers.
