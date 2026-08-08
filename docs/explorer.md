# Explorer TUI

`ws` (run with no arguments in a TTY) — or `ws explorer` explicitly —
opens a Bubble Tea TUI explorer across every workspace registered on
this machine. It is the fastest path from "I want to work on something"
to a shell or a Claude Code session in the right directory.

```sh
ws                          # bare invocation; same as `ws explorer`
ws explorer                 # explicit
ws agent                    # legacy alias, still works
```

When stdout is not a TTY, `ws` falls through to `cmd.Help()` so
piping / scripts get help instead of a TUI prompt.

## What you see

The explorer reads `workspace_roots` from `~/.config/ws/config.toml`,
walks each root for projects / groups / worktrees / Claude sessions,
and renders a pinned quick-nav header above a scrollable view. Manage
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
project immediately (claude in its directory). The chip row stays
pinned above the tree while you scroll, so the shortcuts never
disappear off the top.

A project icon is rendered per ecosystem (Go, Rust, Python, Node, TS,
Java, Ruby, C#, Shell, Docker) based on marker files (`go.mod`,
`Cargo.toml`, `pyproject.toml`, etc.) in the project directory.

### Views

Press `v` to cycle through:

- **Recent** — the default cross-group view, ordered by the newer of
  branch activity and the worktree HEAD commit time. Press `o` to
  reverse the order. Canonical groups appear as row context without
  changing `workspace.toml`.
- **Projects** — canonical workspace groups and projects.
- **Language** — ephemeral language groups inferred from project files.

The selected view and Recent order are stored in machine-local config.
Group rows expand and collapse with `tab`.

## Keys

Navigation:

- `j` / `↓`, `k` / `↑` — move selection
- `tab` — toggle expand/collapse for groups and projects
- `v` — cycle Recent / Projects / Language views
- `o` — reverse Recent ordering
- `h` / `←` — collapse one level. Smart: from a worktree row it
  closes the parent project; from a project row under a group it
  closes the group.
- `1`-`9` — launch the matching chip (claude in its directory)
- `q` — quit

Per-row actions:

- `enter` — open Claude Code session in the row's directory
  (project / worktree) or `cd` into a group / workspace root.
- `p` — same as `enter` but prompts you for an initial Claude prompt.
- `l` / `→` — open a shell in the row's directory.
- `ctrl+s` — open a shell anywhere from anywhere.
- `w` — on a project row, open the worktree-creation form (single
  "Branch name" input → confirm).
- `e` — on a project row, edit the project's group / category.
- `a` — archive the selected project, canonical project group, or
  non-main worktree. Worktree archive removes its local checkout but
  preserves local and remote branches.
- `d` — destructively delete one non-main worktree after exact branch
  confirmation. This removes its checkout, remote branch, and local
  branch. `main`, `master`, `dev`, and the configured default branch
  are protected.
- `A` — open global lifecycle maintenance. Archive projects or archive
  old worktrees using thresholds such as `72h`, `1w`, or `1month`.
- `f` — on a project row, toggle favorite. Equivalent to
  `ws favorite add` / `ws favorite rm` from the CLI. The new flag is
  persisted to `workspace.toml` and reaches other machines on the next
  explicit `ws sync` on each side.

Search:

- `s` / `/` — flash search inside the current view.
- `S` — global filtered search across projects and worktrees, including
  matches outside the current view and viewport.

Help:

- `?` or `space` — which-key panel of available actions in context.

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

## Archival and deletion

Project archive changes project status to `archived` and leaves files,
repositories, worktrees, and branches untouched. It is available for a
project, canonical group, or all loaded workspaces.

Worktree archive is reversible: it removes a non-main checkout, retains
the branch, and releases this machine's ownership metadata. Restore it
with `ws worktree add <project> <branch>`. Age-based project, group, and
global archival previews eligible and skipped counts before confirmation.
Main, dirty, recent, local-only, and unpushed worktrees are skipped.

Worktree delete is single-item only and intentionally destructive. It
verifies the remote branch, requires the exact branch name, and uses a
leased remote deletion so a concurrently changed branch is not removed.

## Project edit

Press `e` on a project row → group / category form. Edits update
`workspace.toml` directly. The next `ws sync` commits and pushes the
change. Useful when reorganizing the layout without leaving the
explorer.

## Sessions

Claude Code sessions are listed under their owning project. Hitting
`enter` on a session row opens it with `claude --resume <id>` rooted
at the session's recorded `cwd`. The session cache is shared with
`SessionCache` so repeated `ws` invocations stay fast.

## Why a TUI

Three reasons it earns its keep:

- **One key per pinned project.** Number hotkeys 1-9 beat
  remembering aliases for branches that come and go.
- **Cross-workspace.** Roots registered with `ws workspace add` all show
  up in one list without scheduling background work.
- **Claude integration.** The explorer is the primary way to drop
  into a Claude session that already has the right `cwd` and an
  optional resume target.
