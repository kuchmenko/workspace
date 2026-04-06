# Workspace

Personal workspace manager. Tracks all development projects across machines via a TOML registry.

## Architecture

- **workspace.toml** — single source of truth. Lists all projects, their remotes, status, and category.
- **ws** — Go CLI binary. All operations go through it.
- Projects are cloned into `personal/`, `work/`, `playground/`, `researches/` — all gitignored.
- `archive/` holds tar.gz of archived personal projects — also gitignored.
- Two machines sync via git push/pull of this repo + `ws sync`.

## Project statuses

- `active` — cloned locally, actively developed
- `archived` — not needed locally. Personal: tar.gz in archive/. Work: removed.
- `dormant` — still cloned but no recent activity (detected by daemon/pulse)

## Categories

- `personal` — user's own repos
- `work` — organization repos

## Commands

```
ws sync              # clone missing active repos, pull existing
ws add <url>         # register + clone a new project
ws archive <name>    # archive project (personal→tar, work→rm)
ws restore <name>    # re-clone or untar
ws status            # show all projects with state
ws scan              # find unregistered git repos
ws clean [name]      # remove node_modules, target/, .venv, etc.
ws list [--filter]   # filtered project list
```

## Conventions

- All paths in workspace.toml are relative to workspace root
- Scripts must be idempotent — safe to re-run
- No secrets in this repo
- workspace.toml is the only file that changes during normal operation
