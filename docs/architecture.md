# Architecture

This page describes the data model, on-disk layout, foreground sync
pipeline, and conflict invariants. Read it before changing sync or
worktree behavior.

## High-Level Goal

The same projects, branches, and works-in-progress should be available on
every machine the user touches without hidden repository mutation. The
core invariants are:

1. `$XDG_STATE_HOME/ws/registry.db` is the runtime authority for named local
   workspace registries.
2. `workspace.toml` is used only for explicit import and export.
3. Synchronization runs only through an explicit foreground `ws sync`.
4. Project branches are never pushed to origin by sync.
5. Project updates are non-destructive: clean main worktrees may
   fast-forward; merge, project rebase, reset, force, and branch deletion
   remain user actions.
6. Projects use a bare repository plus worktrees so checkouts do not
   compete for one directory.

## Configuration Boundaries

`$XDG_STATE_HOME/ws/registry.db` stores named workspaces, canonical roots,
projects, groups, aliases, explorer preferences, mirrors, and branch metadata.
Paths in each registry are relative to that workspace's root. `workspace.toml`
is accepted by `ws workspace import` and emitted by `ws workspace export`;
normal commands do not use it as runtime state, and `ws sync` does not Git-sync
it.

`~/.config/ws/config.toml` is machine-local. It contains the machine name
used for branch attribution:

```toml
machine_name = "linux"
```

`ws workspace create/import/export/list` manages SQLite workspaces. Workspace
names are unique, roots are canonical, and commands select the longest
path-boundary-safe root containing the current directory unless `--root`
selects an exact root. Explorer reads all workspaces from SQLite.

Each workspace also has a content-addressed signed revision DAG and a signed
access policy. Device-network membership alone exposes no workspace. Policy
selects `local`, every active device, or named devices and assigns `admin`,
`writer`, or `replica` authority. Roots are deliberately outside replicated
state. Authenticated TLS peers exchange complete revision bundles manually via
`ws workspace available/attach/sync`; deterministic merges converge independent
changes and preserve same-field conflicts for explicit resolution.

## Project Model

```toml
[projects.myapp]
remote         = "https://github.com/user/myapp.git"
path           = "personal/myapp"
status         = "active"
category       = "personal"
group          = "personal"
default_branch = "main"
favorite       = true

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

Only `active` projects enter a sync plan. There is no per-project sync
toggle or fetch-only mode. Mirrors are optional named destinations pushed
from the project's bare repository after a successful origin fetch.

Empty `machines` causes a branch entry to be removed on save. Legacy
`autopush` fields are migrated into branch metadata on load and omitted on
the next save.

## On-Disk Layout

After `ws migrate`, or immediately after `ws add` / `ws create`, a project
uses sibling paths under its group or category directory:

```text
personal/
├── myapp/                              main worktree
│   └── .git                            pointer into ../myapp.bare
├── myapp.bare/                         shared git object store
└── myapp-wt-linux-feat-auth-refactor/  feature worktree
```

The main worktree keeps the project path stable. Extra worktrees use
`<project>-wt-<machine>-<branch-slug>`. Branch slashes flatten to dashes;
slug collisions get a deterministic `-<sha8>` suffix derived from
`SHA-1(branch)`. The underlying branch remains the literal user input.

## Foreground Sync

`internal/sync` is the synchronization core. The CLI owns orchestration
and chooses interactive or headless presentation.

```text
fresh config.Load
      |
      v
BuildPlan -> Probe (parallel, mutation-free)
      |                |
      |                +-> verified known-provider SSH candidates
      v
Selection (interactive and run-only, or strict all-target headless)
      |
      v
Runner.RunContext (frozen plan, sequential mutation)
      |
      +-> origin conversions
      +-> project origin/fetch/mirrors/worktrees/orphans
      +-> metadata save + Report/Event stream
```

### Plan

`BuildPlan` snapshots every active project's sync-relevant fields and
classifies local layout as `present`, `missing`, `needs-migration`, or
`blocked`. Targets include project origins and project mirrors. Exact URL
matches are deduplicated into endpoints, then grouped by source identity.

The snapshot is a safety boundary. Execution compares the current SQLite
registry and skips a project with `plan-changed` if its remote, path, status,
default branch, or mirrors differ from preflight. Projects introduced after
preflight are also skipped.

### Probe

`Probe` checks endpoints with bounded parallelism, noninteractive git, and
per-endpoint timeouts. It returns typed endpoint and source results. It
does not mutate repositories or config.

When a failed origin uses HTTPS on a known provider, the probe may derive
and test an SSH URL for the same repository. A conversion enters
`Selection` only after that candidate succeeds. Mirror conversion is not
supported.

### Selection

`Selection` starts with accessible targets enabled. Source, project, and
target toggles affect only the current invocation. Disabling a project
also disables its mirrors. An inaccessible target cannot be re-enabled
unless a verified conversion was selected.

Headless mode does not provide selection or conversion. Every endpoint
must pass preflight before execution starts, guaranteeing that endpoint
failure produces no mutation.

### Runner

`Runner.RunContext` first refuses to race a live command sidecar. It then
applies selected project-origin conversions and processes planned projects in
sorted order. A conversion updates the repository origin and SQLite registry;
failed registry saves roll the repository origin back. Events report operation
starts and outcomes to either the TUI or plain text renderer.

Project processing repairs the fetch refspec when needed, fetches origin,
pushes selected mirrors, examines worktrees, fast-forwards eligible main
worktrees, refreshes activity metadata, and detects origin-deleted
branches. Missing selected projects are cloned in the same foreground
run. There is no timer, service, retry queue, backoff, or cooldown.

Cancellation stops scheduling new work and waits for the current git
processes to return before the CLI exits. Reports retain completed,
failed, skipped, conflict, conversion, and cancellation results.

See [Sync](sync.md) for the user-facing flow and exit codes.

## Registry Storage

SQLite is the only runtime registry authority. `ws workspace import` creates a
named local workspace from TOML interchange data, and `ws workspace export`
emits TOML. Project Git sync neither transfers registry data nor treats TOML as
a fallback.

## Branch Metadata

- `machines` lists machines that currently hold a worktree.
- `last_active_*` is updated by worktree creation, explicit worktree push,
  explorer launch stamping, and foreground sync when a registered sibling
  worktree has local-ahead commits.
- `last_pushed_*` is written after `ws worktree push`, or when adding a
  worktree that already exists on origin. Orphan detection only examines
  branches with this signal.
- `created_*` records explicit worktree creation. Explorer launch stamping
  may create a minimal entry without creator fields.

`ws worktree add` claims a branch for this machine; `ws worktree rm`
releases it. Plain `git push` remains valid but does not update metadata.

## Sidecars

`ws add`, `ws create`, `ws bootstrap`, and `ws migrate` write
`~/.local/state/ws/<kind>/<sha>.toml` while operating. Sidecars provide
crash recovery and prevent another command, including foreground sync,
from racing the same workspace operation. They are not messages to a
background process.

The shared `internal/sidecar` package owns file, lock, pid, and stale
process checks. Command-specific payloads remain with their command
packages. `ws doctor` reports stale bootstrap and migrate sidecars for
manual removal after the user confirms that no matching command is running.

## Conflicts

`internal/conflict` persists records in
`~/.local/state/ws/conflicts.json` using atomic replacement. Records
deduplicate on `(workspace, project, branch, kind)`. Foreground sync writes
and clears observed conditions; `ws sync resolve` performs explicit
user-selected resolution.

`ws sync resolve` never auto-merges or auto-rebases project work. It may
open a shell or editor, retry a mirror push, or update branch metadata only
after the user chooses that action.

See [Sync: Conflicts](sync.md#conflicts) for the catalog.

## Migration

`internal/repo/migrate.go` converts a plain checkout into the
bare+worktree layout. It preserves local branches, executable hooks,
detached commits, stash contents, and optional dirty WIP according to the
chosen migration strategy.

The attach sequence preserves the existing working files:

1. Move `.git` to a recoverable temporary name.
2. Add a temporary worktree with `--no-checkout`.
3. Move only its `.git` pointer into the existing project path.
4. Remove the empty temporary directory.
5. Run `git worktree repair` for the final path.
6. Verify HEAD did not move.

Failures before final verification restore the original `.git` and remove
the incomplete bare repository. Migration-internal recovery branches use
`wt/<machine>/migration-{wip,stash,detached}-<timestamp>`; ordinary new
worktrees never synthesize that namespace.

## Tests

Git behavior is tested with real repositories under `t.TempDir()`, not
mocks. `internal/testutil/gitfixture.go` provides fake remotes, plain
checkouts, deterministic git execution, dirty state, and stash helpers.

Important sync coverage lives in `internal/sync/*_test.go`; CLI preflight,
TUI, and headless behavior lives in `internal/cli/sync_*_test.go`; SQLite
registry and multi-workspace coverage lives in `internal/registry/*_test.go`,
`internal/cli/workspace_*_test.go`, and `internal/agent/*_test.go`. CI runs
`go test -race -timeout 5m ./...`.

## Runtime Files

- `$XDG_STATE_HOME/ws/registry.db`: authoritative runtime workspace registry.
- `workspace.toml`: optional import/export interchange data.
- `~/.config/ws/config.toml`: machine-local settings such as `machine_name`.
- `~/.config/ws/token`: GitHub token used by `ws auth`.
- `~/.local/state/ws/conflicts.json`: unresolved sync conflicts.
- `~/.local/state/ws/<kind>/<sha>.toml`: command sidecars.
- `~/.local/state/ws/aliases.zsh`: generated zsh aliases.

There are no service, socket, pid, log, IPC, or watcher runtime files.
