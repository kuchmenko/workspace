# ws

Workspace manager for tracking and developing many Git projects across multiple machines.

SQLite is the runtime source of truth for workspace registries. Each mutation creates a signed revision in a local snapshot DAG. Peer transfer is not implemented yet; local workspace management works without enabling sync.

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
just install
```

## Quick start

Create a new local workspace:

```sh
mkdir -p ~/dev
ws workspace create ~/dev \
  --name personal \
  --recovery-key ~/Documents/workspace/personal-recovery.key
cd ~/dev
ws auth login
ws setup
```

Or migrate an existing TOML registry once:

```sh
ws workspace import ./workspace.toml \
  --name personal \
  --root ~/dev \
  --recovery-key ~/Documents/workspace/personal-recovery.key
```

`workspace.toml` is import/export data, not runtime state. Export a portable copy with:

```sh
ws workspace export personal > workspace.toml
```

Project and worktree commands use the selected SQLite workspace normally:

```sh
ws add git@github.com:owner/myapp.git
ws worktree add myapp feat/auth-refactor
ws worktree push myapp feat/auth-refactor
```

Run bare `ws` in a terminal to open the Explorer across all local workspaces.

## Documentation

- [Getting started](docs/getting-started.md)
- [Explorer](docs/explorer.md)
- [Worktrees](docs/worktrees.md)
- [Aliases](docs/aliases.md)
- [Current sync status](docs/sync.md)
- [Multi-master protocol](docs/multi-master-sync-protocol.md)
- [Architecture](docs/architecture.md)
- [Command reference](docs/reference.md)

## Deliberate boundaries

- Project repositories remain ordinary Git repositories; registry sync never transfers repository contents.
- Project branch pushes remain explicit.
- `workspace.toml` is never a runtime fallback.
- Peer enrollment, transfer, reconciliation, background anti-entropy, and backup automation are not implemented yet.

Pre-1.0 and single-user by design: one human, several equal-weight machines.
