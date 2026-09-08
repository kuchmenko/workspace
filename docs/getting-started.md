# Getting started

A workspace is a named local registry in `$XDG_STATE_HOME/ws/registry.db`
with a canonical root directory that holds many Git projects.
`workspace.toml` is import/export interchange only. Project synchronization
is explicit: `ws sync` preflights the current workspace, lets you review the
run in a terminal, and changes remote Git state only after confirmation.

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

The repository is private, so installation requires an authenticated GitHub
CLI session with repository access. `~/.local/bin` should be on your `PATH`.
If not, the installer prints a reminder.

## Bootstrap a workspace

Choose one of three supported starts.

### First machine

```sh
mkdir ~/dev
ws workspace create ~/dev --name personal
cd ~/dev
ws auth login            # GitHub device flow (or `--pat` for a token)
ws add                    # TUI: choose clipboard or GitHub repositories
```

To register without cloning, use `ws add --no-clone <remote-url>`, then run
top-level `ws sync` to materialize the missing repository.

### Trusted new machine

```sh
ws workspace attach personal --root ~/dev
cd ~/dev
ws sync
```

Attach fetches the authorized registry. Top-level `ws sync` then materializes
its missing repositories.

### TOML recovery

```sh
ws workspace import /path/to/workspace.toml --name personal --root ~/dev
cd ~/dev
ws sync
```

Import restores the registry. Top-level `ws sync` then materializes missing
repositories. Normal commands use SQLite afterward and do not modify the
imported file.

That's enough for one machine. For cross-machine workflow see
[Multi-machine sync](sync.md#multi-machine-flow).

## Adding more repos later

Two flows; pick whichever matches what you have:

```sh
# I have a URL or a list:
ws add git@github.com:owner/repo.git
ws add url1 url2 url3
echo url | ws add -                # stdin, one URL per line

# Seed registration without cloning, then materialize through sync:
ws add --no-clone git@github.com:owner/repo.git
ws sync
```

Both flows end at the same place: an entry in the SQLite registry plus a
project laid out as `<name>/` (main worktree) + `<name>.bare/` (bare
repo) under the chosen group/category directory.

## Managing workspaces

The explorer can show multiple named workspaces from the local SQLite registry:

```sh
ws workspace create ~/dev --name personal
ws workspace import ~/work/workspace.toml --name work --root ~/work
ws workspace export personal > workspace.toml
ws workspace list
```

Names are unique and roots are canonical. Commands use an exact `--root` when
provided; otherwise they select the workspace with the longest containing
root. TOML export is explicit and is never a runtime fallback.

## Authentication

`ws auth login` is the GitHub device flow used by interactive add to list
your repos and orgs. Token lives at `~/.config/ws/token`.

```sh
ws auth login          # browser-based device flow
ws auth login --pat    # paste a PAT instead
ws auth status         # show current state
ws auth logout         # remove the token
```

If no saved ws token exists, GitHub discovery can optionally fall back to an
authenticated `gh` installation.

## What to read next

- [Worktrees](worktrees.md) — the per-feature checkout model and
  `ws worktree add/push/list/rm`.
- [Sync](sync.md) — preflight, interactive selection, strict headless
  behavior, conflicts, and cross-machine syncing.
- [Aliases](aliases.md) — short shell aliases for projects and groups.
- [Explorer TUI](explorer.md) — bare `ws` opens a Bubble Tea launcher
  across registered workspaces and worktrees.
- [Architecture](architecture.md) — internals: data model, on-disk
  layout, and foreground sync contract.
- [Command reference](reference.md) — every command, every flag.
