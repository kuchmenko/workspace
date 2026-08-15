# Getting started

## Install

```sh
gh auth login
gh api repos/kuchmenko/workspace/contents/install.sh \
  -H "Accept: application/vnd.github.raw+json" | sh
```

Or clone the repository and run `just install`.

## Create a workspace

```sh
mkdir -p ~/dev
ws workspace create ~/dev \
  --name personal \
  --recovery-key ~/Documents/workspace/personal-recovery.key
cd ~/dev
```

The command creates an empty signed registry in SQLite. It does not create `workspace.toml` and does not require peer sync.

Keep the recovery key outside the node state directory. Peer recovery is not implemented yet, but the public recovery identity is part of workspace genesis.

## Import an existing workspace

```sh
ws workspace import /path/to/workspace.toml \
  --name personal \
  --root ~/dev \
  --recovery-key ~/Documents/workspace/personal-recovery.key
```

Import is a one-time migration. Normal commands use SQLite afterward and never modify the source TOML. Export when a portable, non-peer copy is useful:

```sh
ws workspace export personal > workspace.toml
```

## Add projects

```sh
ws auth login
ws setup

ws add git@github.com:owner/repo.git
ws add url1 url2 url3
echo url | ws add -

ws create
ws create --owner me --name foo
```

Existing plain checkouts can be converted to the bare-repository plus worktree layout with `ws migrate <name>`.

Every registry mutation creates a signed child revision in the local node database.

## Multiple workspaces

Create or import each workspace under a distinct name and root. `ws workspace list` shows all local workspaces. Commands select the exact `--root` when supplied; otherwise they select the workspace containing the current directory. Explorer shows all local workspaces.

## Authentication

`ws auth login` stores the GitHub token used to discover accessible repositories. `ws create` shells out to `gh repo create`, so it separately requires `gh auth login`.

## Sync status

Local workspace management is complete without sync. Peer transfer is not implemented yet, and `ws sync` fails without mutation. See [Sync](sync.md) and the [protocol design](multi-master-sync-protocol.md).
