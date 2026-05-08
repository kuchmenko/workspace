# Daemon and sync

`ws daemon` runs an idempotent reconciler tick in the background. Each
tick brings on-disk state in line with `workspace.toml` without ever
running `merge`, `rebase`, `reset`, `force`, or `push` inside a
project repo. The worst the daemon does is record a conflict and stop.

## One-time setup

```sh
ws daemon register ~/dev    # adds this workspace to the daemon's registry
ws daemon register ~/work   # multiple registered workspaces are supported;
                            # the daemon reconciles each one in its own goroutine
ws daemon start             # background reconciler
ws daemon install-service   # optional: systemd user unit so it starts on login
```

Other commands:

```sh
ws daemon status            # PID + registered workspaces
ws daemon stop
ws daemon restart
ws daemon unregister ~/dev
ws daemon run               # foreground (used by the systemd unit)
```

## What a tick does

The reconciler runs an immediate tick at startup, then every
`daemon.poll_interval` (default 5 minutes), plus on `config_changed`
IPC notifications from the CLI.

**Phase 0 — sidecar pre-check.** If any `add` / `bootstrap` / `migrate` /
`create` sidecar exists in `~/.local/state/ws/<kind>/<sha>.toml` with
a live pid, the tick is skipped for that workspace. This is what
makes interactive commands safe to run while the daemon is up — they
hold the sidecar; the daemon waits.

**Phase 1 — `workspace.toml` sync.** Commits any local changes to
`workspace.toml` under a `ws: auto-sync workspace.toml from <machine>`
message, fetches, runs the local-dirty / local-ahead / remote-ahead
decision matrix, falls back to `pull --rebase` (safe thanks to
`merge=union` on `.gitattributes`), and records `toml-merge` /
`toml-push-failed` conflicts when even rebase fails.

**Phase 2 — per-project reconcile.** For every active project:

- If both `<path>.bare` and `<path>` are missing, attempt `clone` into
  the bare+worktree layout (controlled by `daemon.auto_bootstrap`,
  default `true`).
- If `<path>.bare` is missing but `<path>` exists, record
  `needs-migration` and stop touching the project.
- `git fetch --all --prune --tags` in the bare. Failure increments a
  per-project exponential backoff (5m base, 1h cap).
- For the **main worktree** on the project's default branch:
  `git pull --ff-only` when clean and only behind. Diverged → record
  `main-divergence` and leave it. Dirty → silently skip.
- For every **registered branch** (`[[projects.X.branches]]`) whose
  worktree on this machine has local-ahead commits: stamp
  `last_active_machine = me`, `last_active_at = now()`. No push.
- For every registered branch whose `last_pushed_at` is set: probe
  `refs/remotes/origin/<name>` post-fetch. Missing → record
  `branch-orphan` (typical: PR merged with auto-delete-branch).
  Re-appearance on the next tick auto-clears the conflict.
- After the loop, if anything in the in-memory workspace changed,
  write `workspace.toml` so Phase 1 of the next tick commits and
  pushes the metadata. The other machine sees the activity trail
  without any project-branch push having happened.
- `auto_sync = false` on a project narrows everything above to a
  fetch-only run.

`ws sync` (CLI) runs exactly one tick in the foreground. Useful when
you want to see the output, or after pulling a fresh `workspace.toml`
manually.

## What the daemon never does

- **Push project branches.** Pushes are user-driven via
  `ws worktree push` or plain `git push`.
- **Merge / rebase / reset / force / checkout** inside a project repo.
- **Delete branches** anywhere (local or remote).

The daemon edits `workspace.toml` (in the workspace's own git repo)
and ff-pulls main worktrees. Everything else surfaces as a conflict.

## Conflicts

Conflicts persist to `~/.local/state/ws/conflicts.json` (XDG-aware).
The reconciler is the only writer; `ws sync resolve` is the only
mutator that clears entries.

The current catalog:

- `toml-merge` — `workspace.toml` rebase failed during Phase 1. Open
  the workspace repo, fix manually, exit shell.
- `toml-push-failed` — push rejected and `pull --rebase` did not help.
  Same shell-resolve flow.
- `main-divergence` — main worktree cannot fast-forward (diverged
  from origin). Pull / rebase / merge yourself in the project.
- `needs-migration` — project's `<path>` exists but `<path>.bare`
  doesn't. Run `ws migrate <name>`.
- `needs-bootstrap` — project is registered but the auto-clone could
  not determine the default branch. Run `ws bootstrap <name>`.
- `path-blocked` — non-repo files at the project path; clean up
  manually then re-run.
- `clone-failed` — `git clone` of a missing project failed
  (network / auth). Backed off with exponential delay.
- `branch-duplicate` — two `[[branches]]` entries with the same
  `name` in the same project. Caused by two machines adding the same
  branch concurrently and union-merge concatenating their writes.
  `ws sync resolve` offers to open `workspace.toml` in `$EDITOR`.
- `branch-orphan` — a registered, previously-pushed branch's
  `refs/remotes/origin/<name>` disappeared. Resolve options:
  - **Drop** — clear the `[[branches]]` entry. Works with or without
    a local worktree on the branch (drops the entry directly when no
    worktree exists; instructs you to run `ws worktree rm` first when
    one does).
  - **Keep local** — clear `last_pushed_*` so the orphan check stops
    firing on this branch. A future `ws worktree push` reinstates it.

```sh
ws sync resolve              # interactive prompt
```

## Health check: `ws doctor`

`ws doctor` runs a unified pass over the whole workspace and per-
project state, surfacing problems and (with `--fix`) applying safe
auto-repairs.

```sh
ws doctor                    # walk every project, print findings
ws doctor <project>          # one project
ws doctor --fix              # auto-fix the safe checks
ws doctor --json             # machine-readable
ws doctor --skip-remote      # skip network-touching checks
```

Exit codes: `0` clean · `1` issues found · `2` `--fix` applied
something.

What it checks:

- **System** — daemon running, stale sidecars, active conflicts,
  `workspace.toml` valid, machine config present.
- **Per-project** — bare+worktree layout, `remote.origin.fetch`
  refspec installed, remote URL reachable, default branch resolves,
  every worktree has a sane upstream, no leftover `index.lock`.

Conflicts and `index.lock`s are intentionally **not** auto-fixed —
both want a human's eyes on them. Everything else (refspec missing,
default branch drift, stale sidecars) is safe under `--fix`.

## Multi-machine flow

Two machines sharing the same workspace:

```sh
# Machine A
ws daemon register ~/dev
ws daemon start

# Machine B
ws daemon register ~/dev
ws daemon start
```

Both machines now reconcile independently:

- **Layer 1 — `workspace.toml`.** Any CLI command that mutates the
  registry — `ws add`, `ws create`, `ws setup`, `ws bootstrap`,
  `ws migrate`, `ws worktree add/rm/push`, `ws alias add/rm`,
  `ws sync resolve` — writes to `workspace.toml`. The daemon commits
  and pushes it. The other machine pulls it, runs auto-clone for any
  newly-registered missing project, and updates its own [[branches]]
  view of who's working on what.
- **Layer 2 — project branches.** Project-branch pushes are explicit
  (`ws worktree push`). The daemon only fetches and ff-pulls main on
  each side. The metadata trail in `workspace.toml` (machines,
  last_active_*, last_pushed_*) is the cross-machine state — no
  daemon-driven branch pushes.

Symlinking `workspace.toml` from a dotfiles repo works: the daemon
resolves the symlink and commits to the actual repo.
