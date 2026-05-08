# ws

Workspace manager for tracking, syncing, and developing many git
projects across multiple machines.

One TOML registry, one daemon per machine, per-feature worktrees with
explicit cross-machine metadata. Project pushes are a deliberate user
action; the daemon never pushes branches behind your back.

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

## Quick start

```sh
mkdir ~/dev && cd ~/dev
ws auth login            # GitHub device flow (or `--pat` for a token)
ws setup                 # TUI: pick repos, organize into groups
ws sync                  # clone everything, ff-pull main worktrees
ws daemon register ~/dev # add this workspace to the daemon's registry
ws daemon start          # background reconciler — keeps things fresh
```

For per-feature work:

```sh
ws worktree add myapp feat/auth-refactor   # new worktree on a literal branch
# (edit, commit)
ws worktree push myapp feat/auth-refactor  # explicit publish + metadata stamp
```

For everyday navigation, run bare `ws` in a terminal — it opens a
TUI launcher across every workspace + worktree the daemon knows
about. See [Agent TUI](docs/agent-tui.md).

## What's where

- [Getting started](docs/getting-started.md) — install, first-time
  setup, adding more repos, authentication.
- [Worktrees](docs/worktrees.md) — `ws worktree add/list/push/rm`,
  branch naming, cross-machine handoff, recovering from
  `branch-orphan` and re-registering legacy `wt/<machine>/*`.
- [Daemon and sync](docs/daemon-and-sync.md) — what each reconciler
  tick does, conflict catalog, `ws doctor`, multi-machine flow.
- [Aliases](docs/aliases.md) — short shell aliases for projects and
  groups.
- [Agent TUI](docs/agent-tui.md) — bare `ws` opens a Bubble Tea
  launcher; keys, search, worktree creation, Claude sessions.
- [Architecture](docs/architecture.md) — internals: data model,
  on-disk layout, daemon contract, conflict invariants.
- [Command reference](docs/reference.md) — every command, every
  flag.

## What `ws` deliberately doesn't do

- Auto-push project branches. Pushes are explicit
  (`ws worktree push` or plain `git push`).
- Run `merge`, `rebase`, `reset`, `force`, or `push` inside a
  project repo from the daemon. The worst the daemon does is
  record a conflict and stop.
- Synthesize a `wt/<machine>/<topic>` namespace. Branches use
  repo-native names from the first commit; pre-0.7.0
  `wt/<machine>/*` checkouts continue to function and can be
  re-registered via `ws worktree add`.

## Status

Pre-1.0; breaking changes happen between minor versions when the
design pressure is real. Single-user tool by design — the
multi-machine sync model assumes one human, several machines.
