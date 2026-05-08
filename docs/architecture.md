# Architecture

This page is the deep dive: data model, on-disk layout, daemon
contract, conflict invariants. Read this when you need to reason
about *why* `ws` does what it does, or before changing internals.

## High-level goal

Same projects + branches + works-in-progress visible on every machine
the user touches, without losing data and without destructive git
operations behind anyone's back. The four invariants:

1. **One registry of projects** that travels via git, so a `ws add`
   on machine A makes the project appear on machine B.
2. **Bidirectional, safe sync of feature work** — branches start on A
   and continue on B without manual `git push` / `pull` gymnastics
   and without merge conflicts in unrelated branches.
3. **No destructive operations in project repos.** The daemon never
   runs `merge`, `rebase`, `reset`, `force`, or `push`. The worst
   it does is decline to act and surface a conflict.
4. **Worktree-first layout.** Each machine works in its own
   per-feature worktree directory; the bare repo is the shared
   git-object store.

## Source of truth

`workspace.toml` at the workspace root is the single registry.
Committed, synced via the workspace's own git remote, and merged
across machines via `merge=union` on `.gitattributes` so concurrent
additions of different projects from different machines merge
cleanly without manual intervention.

Project record:

```toml
[projects.myapp]
remote         = "git@github.com:user/myapp.git"
path           = "personal/myapp"     # main worktree, relative to ws root
status         = "active"             # active | dormant | archived
category       = "personal"           # personal | work
group          = "personal"           # optional grouping; usually GitHub org
default_branch = "main"
auto_sync      = true                 # default true; false = fetch only

# One [[branches]] block per branch this project knows about.
[[projects.myapp.branches]]
  name                = "feat/auth-refactor"
  machines            = ["linux", "archlinux"]   # who currently has a worktree
  last_active_machine = "linux"                  # last to push or commit
  last_active_at      = "2026-05-08T12:00:00Z"
  last_pushed_machine = "linux"                  # last to publish to origin
  last_pushed_at      = "2026-05-07T16:30:00Z"   # absent until first push
  created_by          = "linux"
  created_at          = "2026-04-08T13:59:04Z"
```

Empty `machines` slice triggers entry GC on save — no orphan
tombstones live across save boundaries.

## On-disk layout

After `ws migrate` (or `ws add` / `ws create` for new projects)
every project lives as a sibling triplet under its category /
group directory:

```text
personal/
├── myapp/                       ← main worktree (proj.default_branch)
│   └── .git                     ← file pointing into ../myapp.bare
├── myapp.bare/                  ← bare repo, source of truth for git state
└── myapp-wt-<machine>-<branch-slug>/   ← extra per-feature worktrees
```

- `<project>/` keeps its original path so `cd personal/myapp` still
  drops the user into a working repo. Tooling that doesn't
  understand worktrees generally still works because `.git` is a
  valid pointer file.
- `<project>.bare/` is the only place git objects live. Worktrees
  share it.
- `<project>-wt-<machine>-<branch-slug>/` is the convention for
  extra worktrees created by `ws worktree add`. Slashes in the
  branch flatten to dashes in the directory name. Slug collisions
  get a deterministic `-<sha8>` suffix from `SHA-1(branch)` so two
  machines adding the same branch independently land on the same
  directory.

## Reconciler (the daemon's brain)

`internal/daemon/reconciler.go` is one state-machine. On each tick:

0. **Sidecar pre-check** — see [Daemon and sync](daemon-and-sync.md).
1. **Phase 1: `syncTOML`** — commit-pull-rebase-push of
   `workspace.toml` against the workspace's git remote. Conflicts
   surface as `toml-merge` / `toml-push-failed`.
2. **Phase 2: `reconcileProjects`** — for each active project,
   handle layout (clone / migrate-needed / path-blocked), fetch,
   ff-pull main, refresh metadata, detect orphans. Persists changes
   back to `workspace.toml` so Phase 1 of the next tick propagates
   them.

The reconciler is **idempotent**: missed ticks and duplicate
triggers never break state. Each tick recomputes desired vs actual
from scratch.

## Sidecars

Interactive commands hold sidecar files at
`~/.local/state/ws/<kind>/<sha>.toml` while running. The reconciler
calls `sidecar.AnyActive(wsRoot)` before any work and skips the tick
for any workspace with a live sidecar pid. This is the lock that
keeps interactive commands safe with the daemon up.

Kinds: `bootstrap`, `migrate`, `add`, `create`. Each has its own
state package (`internal/<kind>/sidecar.go`) but shares
`internal/sidecar` for file/lock/pid mechanics. Stale sidecars (pid
dead) are ignored on the next tick and surfaced by `ws doctor` as
auto-fixable.

## Branch metadata semantics

`[[branches]]` is the cross-machine view of who's on which branch.
Two timestamp fields with distinct meanings:

- `last_active_*` — bumped by `ws worktree add`, `ws worktree push`,
  and the reconciler when this machine has local-ahead commits.
  "Activity" is broad: anything that touches the branch on the
  machine. Used for the `(last: <machine> <date>)` column in
  `ws worktree list` and the agent TUI ownership tags.
- `last_pushed_*` — written **only** when the branch is observed on
  origin (after `ws worktree push` succeeds, or when
  `ws worktree add` attaches to a branch already on origin). The
  orphan detector keys off this field: a branch is orphan-eligible
  only after at least one machine has published it. Locally-created
  unpushed branches never trip orphan detection.

`Machines` is the list of machines that currently hold a local
worktree. `ws worktree add` appends; `ws worktree rm` removes;
empty list → entry GC'd on save.

## Conflict invariants

- The reconciler is the only writer to `conflicts.json`. The
  resolve CLI is the only mutator. They coordinate via the file
  alone (atomic write via tmp + rename); no IPC.
- Records dedupe on `(workspace, project, branch, kind)` so a
  recurring condition does not produce a new entry every tick.
- `ws sync resolve` never auto-rebases or auto-merges anything.
  Every action that modifies git state is the user's choice via the
  spawned shell or editor.

See [Daemon and sync — conflicts](daemon-and-sync.md#conflicts) for
the full kind catalog.

## What `ws` deliberately doesn't do

- Auto-push project branches. Pushes are explicit
  (`ws worktree push` or plain `git push`).
- Hand-edit `[[branches]]` blocks. The CLI helpers
  (`ClaimBranch` / `ReleaseBranch` / `MarkPushed` /
  `RemoveBranch` / `TouchActive`) are the only sanctioned writers;
  manual edits race with the reconciler's metadata refresh.
- Synthesize a `wt/<machine>/<topic>` namespace. New worktrees
  use repo-native branch names from the first commit. Pre-0.7.0
  `wt/<machine>/*` checkouts continue to function and can be
  re-registered via `ws worktree add <project> <legacy-branch>`.
- Mirror branches across machines. Each machine's checkout is
  independent; the metadata trail in `workspace.toml` is the only
  cross-machine state.

## Branches in practice

The user types whatever the project convention is — `feat/foo`,
`fix/bar`, `chore/baz`. `ws` validates with
`git check-ref-format --branch` and surfaces git's error verbatim
on rejection. There is no per-project pattern enforcement: branch
naming convention is the project's responsibility.

The internal `wt/<machine>/migration-{wip,stash,detached}-<ts>`
namespace used by `ws migrate` is the one exception — those
branches are migration-internal, never pushed by the daemon, and
go straight into the bare during the migration pre-flight.

## Tests

Real git in temp dirs, not mocks. Every package that touches git
spins up its own ephemeral repos under `t.TempDir()` and runs
real `git` commands. This catches bugs that mock-based tests
would miss because the mock has no opinion on what real git
accepts.

`internal/testutil/gitfixture.go` is the shared fixture builder:
`InitFakeRemote`, `InitFakePlainCheckout`, `RunGit`, `AddDirty`,
`AddStash`. Extend that file before inlining `exec.Command` in a
new test.

CI runs `go test -race -timeout 5m ./...` on every push to main
and every PR.

## Files the CLI relies on

- `<wsRoot>/workspace.toml` — project registry.
- `<wsRoot>/.gitattributes` — `workspace.toml merge=union` (created
  by the reconciler on first run).
- `~/.config/ws/config.toml` — `machine_name` for branch
  attribution.
- `~/.config/ws/daemon.{toml,sock,pid,log}` — daemon state.
- `~/.config/ws/token` — GitHub OAuth/PAT for `ws auth`.
- `~/.local/state/ws/conflicts.json` — unresolved sync conflicts
  (XDG-aware; honors `$XDG_STATE_HOME`).
- `~/.local/state/ws/<kind>/<sha>.toml` — interactive-command
  sidecars (kinds: `add`, `bootstrap`, `migrate`, `create`).
- `~/.local/state/ws/aliases.zsh` — generated shell aliases (when
  `ws alias install` is in effect).
