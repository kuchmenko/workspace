# ws

Workspace manager — track, sync, and manage development projects across machines.

Single TOML registry, hierarchical groups, interactive TUI setup, multi-machine sync.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/kuchmenko/workspace/main/install.sh | sh
```

Or build from source:

```sh
git clone git@github.com:kuchmenko/workspace.git
cd workspace
just install
```

## Quick start

```sh
mkdir ~/dev && cd ~/dev
ws auth login
ws setup
ws sync
```

## Setup

`ws setup` launches an interactive TUI that walks you through workspace creation:

### Step 1 — Select repos

Fetches all repos you have access to on GitHub, sorted by your activity (last 90 days).
Filter by org, search by name, multi-select.

```
 ws setup   Select repos

  Search: _                            sort: activity (ctrl+s)
   all   acme-corp  personal                          (tab)

> ● acme-corp/api-gateway        3d ago  ●●●●●
  ● acme-corp/web-dashboard      5d ago  ●●●●
  ○ acme-corp/legacy-service    45d ago  ●○○○○
  ● personal/dotfiles            1d ago  ●●●●●
  ● personal/cli-tools           8d ago  ●●●
  ○ personal/old-experiment    120d ago  ○○○○○
  ○ other-org/shared-lib        30d ago  ●○○○○

  ↓ 42 more

  Selected: 4 / 49
  ↑↓ navigate  space select  ctrl+a toggle all  enter next  esc quit
```

### Step 2 — Assign groups

Repos are auto-grouped by GitHub org. Rename, merge, or create new groups.

```
 ws setup   Assign groups
  Auto-grouped by org. Rename, move, or create new groups.

  ┌ acme-corp (2 repos)
  │  api-gateway
  │  web-dashboard
  └
  ┌ personal (2 repos)
  │  dotfiles
  │  cli-tools
  └

  ↑↓ navigate  r rename  m move  n new group  enter finish  esc back
```

### Step 3 — Confirm

```
 ws setup   Confirm

  2 groups, 4 projects

  acme-corp
    api-gateway                work       acme-corp/api-gateway
    web-dashboard              work       acme-corp/web-dashboard

  personal
    dotfiles                   personal   personal/dotfiles
    cli-tools                  personal   personal/cli-tools

  Write workspace.toml? y/n  (esc go back)
```

### Result

```sh
$ ws sync
  clone  api-gateway → acme-corp/api-gateway
  clone  web-dashboard → acme-corp/web-dashboard
  clone  dotfiles → personal/dotfiles
  clone  cli-tools → personal/cli-tools

Done: 4 cloned, 0 pulled, 0 skipped, 0 failed
```

```
~/dev/
├── workspace.toml              ← source of truth (committed)
├── acme-corp/                  ← work group (gitignored)
│   ├── api-gateway/
│   └── web-dashboard/
├── personal/                   ← personal group (gitignored)
│   ├── dotfiles/
│   └── cli-tools/
└── archive/                    ← archived projects (gitignored)
```

## Commands

```
ws setup              Interactive onboarding — select repos, assign groups
ws sync               Clone missing active repos, pull existing ones
ws add <url>          Register and clone a new project
ws archive <name>     Archive (personal→tar, work→remove)
ws restore <name>     Re-clone or untar archived project
ws status             Show all projects with branch, last commit, staleness
ws scan               Find git repos not registered in workspace.toml
ws clean [name|--all] Remove node_modules, target/, .venv, etc.
ws list [--status X]  Filtered project list
ws group add <name>   Create a group
ws group list         List groups with project counts
ws group show <name>  Show projects in a group
```

## How it works

- **workspace.toml** is the only committed file — it tracks repos, groups, and status
- Project directories are gitignored — repos are cloned by `ws sync`
- Groups are directories — fully customizable hierarchy
- Category (`personal`/`work`) is auto-detected from GitHub org ownership

## Multi-machine sync

```sh
# Machine A
ws archive old-project
git add workspace.toml && git commit -m "archive old-project" && git push

# Machine B
git pull && ws sync    # skips archived, clones new active repos
```

## Archival

| Type | What happens |
|------|-------------|
| Personal | Clean deps (node_modules, target/, .venv) → tar.gz to `archive/` → remove |
| Work | Remove local clone, keep registry entry |

```sh
ws archive my-project    # archive
ws restore my-project    # bring it back
```
