# ws

Workspace manager for tracking, syncing, and developing many git
projects across multiple machines.

One git-synced TOML registry per workspace, a machine-local list of
workspace roots, and per-feature worktrees with explicit cross-machine
metadata. Synchronization happens only when you run `ws sync`; project
branch pushes remain a deliberate user action.

## Install

```sh
gh auth login
gh api repos/kuchmenko/workspace/contents/install.sh \
  -H "Accept: application/vnd.github.raw+json" | sh
```

Or build from source:

```sh
gh repo clone kuchmenko/workspace
cd workspace
just install            # binary lands at ~/.local/bin/ws
```

## Quick start

```sh
mkdir ~/dev && cd ~/dev
ws auth login            # GitHub device flow (or `--pat` for a token)
ws setup                 # TUI: pick repos, organize into groups
ws sync                  # preflight, review, then synchronize explicitly
```

For per-feature work:

```sh
ws worktree add myapp feat/auth-refactor   # new worktree on a literal branch
# (edit, commit)
ws worktree push myapp feat/auth-refactor  # explicit publish + metadata stamp
```

`ws setup` registers the new root in this machine's
`~/.config/ws/config.toml`. Register additional existing workspaces with
`ws workspace add <path>` and inspect them with `ws workspace list`.

For everyday navigation, run bare `ws` in a terminal. It opens the
[Explorer TUI](docs/explorer.md) across every registered workspace and
worktree.

## What's where

- [Getting started](docs/getting-started.md) — install, first-time
  setup, adding more repos, authentication.
- [Worktrees](docs/worktrees.md) — `ws worktree add/list/push/rm`,
  branch naming, cross-machine handoff, recovering from
  `branch-orphan` and re-registering legacy `wt/<machine>/*`.
- [Sync](docs/sync.md) — preflight, interactive selection, execution,
  conflicts, headless behavior, and multi-machine flow.
- [Aliases](docs/aliases.md) — short shell aliases for projects and
  groups.
- [Explorer TUI](docs/explorer.md) — bare `ws` opens a Bubble Tea
  launcher; keys, search, worktree creation, Claude sessions.
- [Architecture](docs/architecture.md) — internals: data model,
  on-disk layout, foreground sync contract, conflict invariants.
- [Command reference](docs/reference.md) — every command, every
  flag.

## What `ws` deliberately doesn't do

- Auto-push project branches to origin. Origin pushes are explicit
  (`ws worktree push` or plain `git push`).
- Synchronize in the background. There is no service, scheduler, or
  watcher; run `ws sync` when you want remote state changed.
- Run `merge`, `rebase`, `reset`, `force`, or project-branch `push`
  inside a project repo. Unsafe states become skips or conflicts.
- Synthesize a `wt/<machine>/<topic>` namespace. Branches use
  repo-native names from the first commit; pre-0.7.0
  `wt/<machine>/*` checkouts continue to function and can be
  re-registered via `ws worktree add`.

## Status

Pre-1.0; breaking changes happen between minor versions when the
design pressure is real. Single-user tool by design — the
multi-machine sync model assumes one human, several machines.

Current main removes the background daemon and its commands; use
`ws workspace add/rm/list` for local workspace discovery and invoke
`ws sync` explicitly.
