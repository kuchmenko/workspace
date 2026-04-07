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
ws setup                Interactive onboarding — select repos, assign groups
ws sync                 Clone missing active repos, pull existing ones
ws add <url>            Register and clone a new project
ws archive <name>       Archive (personal→tar, work→remove)
ws restore <name>       Re-clone or untar archived project
ws status               Show all projects with branch, last commit, staleness
ws scan                 Find git repos not registered in workspace.toml
ws clean [name|--all]   Remove node_modules, target/, .venv, etc.
ws list [--status X]    Filtered project list
ws group add <name>     Create a group
ws group list           List groups with project counts
ws group show <name>    Show projects in a group
ws auth login [--pat]   Authenticate with GitHub (device flow or PAT)
ws auth logout          Remove stored token
ws auth status          Show authentication state
ws daemon start         Start background daemon
ws daemon stop          Stop daemon
ws daemon status        Show daemon state and registered workspaces
ws daemon register      Register workspace with daemon
ws daemon install-service  Install systemd user service
ws alias                Manage shell aliases (TUI)
ws alias list           Show configured aliases
ws alias add <n> <t>    Add alias for project, group, or "." (workspace root)
ws alias rm <name>      Remove alias
ws alias init [zsh]     Print shell snippet to eval
ws alias install        Install sourcing line in ~/.zshrc (idempotent)
```

## Shell aliases

`ws alias` generates short shell aliases that `cd` into any project, group,
or the workspace root. Aliases live in `workspace.toml` and sync between
machines via git.

```
 ws alias   Manage aliases

  type to search...

> ●  ws              (workspace root)
  ●  acme            ├── acme-corp
  ●  api             │   ├── api-gateway
  ●  web             │   ├── web-dashboard
  ○  (auto)          │   └── legacy-service
  ○  (auto)          ├── other-org
  ○  (auto)          │   └── shared-lib
  ●  prs             └── personal
  ●  dot                 ├── dotfiles
  ●  cli                 ├── cli-tools
  ○  (auto)              └── old-experiment

  ↑↓ navigate  space toggle  e edit alias  enter next  esc cancel
```

Each entry is one of:
- a **project** (cd into the project directory)
- a **group** (cd into the group directory)
- the **workspace root** itself

Auto-generated names follow simple rules — short two-part names join
(`mm-eh` → `mmeh`), longer multi-part names use first letters
(`api-gateway` → `ag`), single words use consonants (`dotfiles` → `dtfls`).
Press `e` to override.

### Install into your shell

One-time setup:

```sh
ws alias install                # adds a sourcing line to ~/.zshrc
exec zsh                        # reload shell
```

After that, every `ws alias` save, `ws alias add`, `ws alias rm`, or project
archive automatically regenerates the aliases file at
`$XDG_STATE_HOME/ws/aliases.zsh` (default `~/.local/state/ws/aliases.zsh`).
Open a new shell or `source` that file to pick up the changes — `.zshrc`
itself is never touched again.

Archived projects have their aliases removed automatically.

Currently only zsh is supported.

## How it works

- **workspace.toml** is the only committed file — it tracks repos, groups, and status
- Project directories are gitignored — repos are cloned by `ws sync`
- Groups are directories — fully customizable hierarchy
- Category (`personal`/`work`) is auto-detected from GitHub org ownership

## Authentication

```sh
ws auth login          # GitHub device flow — opens browser, authorize, done
ws auth login --pat    # paste a Personal Access Token instead
ws auth status         # show current auth state
```

No `gh` CLI required. Token stored at `~/.config/ws/token`.

## Multi-machine sync

The daemon handles sync automatically. No manual git commands needed.

```sh
# Machine A — one-time setup
ws daemon register ~/dev
ws daemon start

# Now any ws add/archive/restore automatically:
# 1. Updates workspace.toml
# 2. Daemon commits + pushes to git

# Machine B — one-time setup
ws daemon register ~/dev
ws daemon start

# Daemon polls git remote, detects changes, pulls + clones missing repos
```

The daemon also watches for new git repos created manually (e.g. `cargo init`)
and logs their discovery.

```sh
ws daemon status              # check daemon health
ws daemon install-service     # auto-start on boot (systemd)
```

workspace.toml can live in your dotfiles repo (symlinked). The daemon resolves
symlinks and commits to the correct repository.

## Archival

| Type | What happens |
|------|-------------|
| Personal | Clean deps (node_modules, target/, .venv) → tar.gz to `archive/` → remove |
| Work | Remove local clone, keep registry entry |

```sh
ws archive my-project    # archive
ws restore my-project    # bring it back
```
