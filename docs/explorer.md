# Explorer TUI

`ws` (run with no arguments in a TTY) — or `ws explorer` explicitly —
opens a Bubble Tea TUI explorer across every workspace registered on
this machine. It is the fastest path from "I want to work on something"
to a shell in the right directory.

```sh
ws                          # bare invocation; same as `ws explorer`
ws explorer                 # explicit
```

When stdout is not a TTY, `ws` falls through to `cmd.Help()` so
piping / scripts get help instead of a TUI prompt.

## What you see

The explorer reads `workspace_roots` from `~/.config/ws/config.toml`,
walks each root for projects, groups, and worktrees,
and renders a pinned quick-nav header above a scrollable tree. Manage
the roots with `ws workspace add/rm/list`. The current workspace is a
fallback when no roots are registered.

```text
*1.myapp 2m  2.api 1h    3.docs 3h  4.experiments 1d  5.utils 2d
6.proj-a 5m  7.proj-b 1h  8.proj-c 4h  9.proj-d 1d

~/dev — workspace
    personal
         dotfiles
         workspace
              main
              feat/foo                (mine, ↑2)
              feat/auth-refactor      (shared with archlinux)
    work
         api-gateway
```

### Pinned chip header

Up to nine numbered chips, sorted favorites-first then
recently-touched. The leading `*` marks favorited projects. Each chip
shows `N.name age` — press the digit `1`-`9` to launch the matching
project immediately (a shell in its directory). The chip row stays
pinned above the tree while you scroll, so the shortcuts never
disappear off the top.

A project icon is rendered per ecosystem (Go, Rust, Python, Node, TS,
Java, Ruby, C#, Shell, Docker) based on marker files (`go.mod`,
`Cargo.toml`, `pyproject.toml`, etc.) in the project directory.

### Views

`v` cycles Recent, Projects, and Language. Recent is the default and
orders projects by the newest registry branch activity or worktree HEAD
commit; `o` reverses it. Language groups are inferred locally and never
modify canonical workspace groups. These preferences are machine-local.

## Keys

Navigation:

- `j` / `↓`, `k` / `↑` — move selection
- `g` / `Home`, `G` / `End` — jump to the first or last row
- `ctrl+d` / `ctrl+u` — move half a page
- `ctrl+f` / `ctrl+b`, `PageDown` / `PageUp` — move a full page
- `tab` — toggle expand/collapse for groups
- `v` — cycle home views; `o` — reverse Recent order
- `h` / `←` — collapse to the parent heading on home, or close a sheet
- `l` / `→` — open the selected projection, group, project, or worktree
- `1`-`9` — open a shell for the matching chip
- `q` — quit

Per-row actions:

- `enter` / `l` / `→` — open the selected group or project panel. In a project
  panel, a worktree row opens a shell in that worktree.
- `ctrl+s` — open a shell anywhere from anywhere.
- `w` — on a project row, open the worktree-creation form (single
  "Branch name" input → confirm).
- `e` — on a project row, edit the project's group / category.
- `a` — archive a project, canonical group, or worktree. Project archive
  leaves files untouched; worktree archive removes the checkout but preserves
  its local and remote branches. A dirty single worktree shows a data-loss
  warning and can be force-archived after confirmation.
- `v` — in a project panel, begin or end visual worktree selection. Extend the
  range with Vim motions, then press `a` or `d` for one reviewed bulk action.
- `d` — destructively delete selected non-main worktrees after an `enter` / `y`
  confirmation; `n` cancels. Dirty worktrees show a data-loss warning and are
  force-removed after confirmation. Local-only and ahead branches are also
  deleted; remote deletion failures are reported without preventing local
  cleanup.
- `A` — archive projects or preview/archive old safe worktrees in the current
  project or group when invoked there, or globally when invoked from home.
- `f` — on a project row, toggle favorite. Equivalent to
  `ws favorite add` / `ws favorite rm` from the CLI. The new flag is
  persisted to `workspace.toml` and reaches other machines on the next
  explicit `ws sync` on each side.

Search:

- `s` — flash search inside the current view (jump labels per match).
- `S` — filtered global search across all projects and local worktrees,
  independent of expansion and viewport.

Help:

- `?` or `space` — which-key panel of available actions from home.

Group and project panels use the same full-screen frame as home: pinned chips,
breadcrumb header, an available-height list, optional status, and a persistent
two-row keybar. The first keybar row always exposes the actions available in
the current scope. On a non-main worktree this includes `a:archive` and
`d:delete`; `A:maintenance` remains visible for project/group bulk operations.
The second row contains the shared Vim navigation keys. Project management and
search are keyboard actions rather than synthetic rows in the list. Project
worktrees show separate status and last-activity columns; activity uses the
newer registry branch timestamp or HEAD commit time and displays `—` when no
timestamp is available.

Lifecycle planning, archive, delete, and post-operation refresh run in the
background. The lifecycle panel shows the current target and completed/total
progress. Confirming an operation returns immediately to the originating
Explorer panel; press `A` to reopen progress or results. Debug logs are appended to
`$XDG_STATE_HOME/ws/explorer.log` (default `~/.local/state/ws/explorer.log`).

## Worktree creation from the TUI

Press `w` on a project row → "Branch name" input → confirm. The
explorer runs the same path as `ws worktree add <project> <branch>`:

- Auto-detects an existing remote ref and checks it out.
- Auto-detects an existing local-only ref and attaches.
- Auto-detects an existing on-disk worktree on the branch
  (legacy `wt/<machine>/*` re-registration) and writes metadata
  without creating a duplicate.
- Otherwise creates a fresh branch from the project's default branch.

After the form closes, the explorer invalidates its worktree cache
and re-renders so the new entry appears immediately.

## Project edit

Press `e` on a project row → group / category form. Edits update
`workspace.toml` directly. The next `ws sync` commits and pushes the
change. Useful when reorganizing the layout without leaving the
explorer.

## Why a TUI

Three reasons it earns its keep:

- **One key per pinned project.** Number hotkeys 1-9 beat
  remembering aliases for branches that come and go.
- **Cross-workspace.** Roots registered with `ws workspace add` all show
  up in one list without scheduling background work.
- **Directory-aware shells.** Every launch opens the user's shell with the selected project, group, or worktree as its `cwd`.
