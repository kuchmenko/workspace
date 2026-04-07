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

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/):
- `feat:` — new feature (bumps minor pre-1.0, would be minor post-1.0)
- `fix:` — bug fix (bumps patch)
- `feat!:` or `fix!:` with `BREAKING CHANGE:` footer — breaking change (bumps minor pre-1.0, major post-1.0)
- `chore:`, `docs:`, `refactor:`, `test:`, `ci:`, `style:`, `perf:` — no release

Scope is optional: `feat(alias): ...`, `fix(sync): ...`.

Never add `Co-Authored-By` or attribution footers.

## Release process

Automated via [release-please](https://github.com/googleapis/release-please).

**Flow:** conventional commits land on `main` → release-please opens/updates a Release PR with bumped version + CHANGELOG → merge the PR → tag `vX.Y.Z` is pushed → existing `release.yml` builds binaries and publishes the GitHub Release.

**Do NOT** manually edit `CHANGELOG.md`, bump versions, or create `vX.Y.Z` tags by hand — release-please owns all of it.
