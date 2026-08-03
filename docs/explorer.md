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

### Tree

Group / project rows expand and collapse with `tab`. Worktrees show
the same ownership tags as `ws worktree list` (`main`, `mine`,
`shared with <machines>`, `legacy-wt`).

## Keys

Navigation:

- `j` / `↓`, `k` / `↑` — move selection
- `tab` — toggle expand/collapse for groups and projects
- `h` / `←` — collapse one level. Smart: from a worktree row it
  closes the parent project; from a project row under a group it
  closes the group.
- `1`-`9` — open a shell for the matching chip
- `q` — quit

Per-row actions:

- `enter` — open the selected sheet action; shell and worktree rows open a shell in their directory.
- `l` / `→` — open a shell directly in the selected row's directory.
- `ctrl+s` — open a shell anywhere from anywhere.
- `w` — on a project row, open the worktree-creation form (single
  "Branch name" input → confirm).
- `e` — on a project row, edit the project's group / category.
- `d` — on a non-main worktree row, prompt for delete (with
  registry release; releases this machine from
  `[[branches]].machines`).
- `f` — on a project row, toggle favorite. Equivalent to
  `ws favorite add` / `ws favorite rm` from the CLI. The new flag is
  persisted to `workspace.toml` and reaches other machines on the next
  explicit `ws sync` on each side.

Search:

- `s` — flash search inside the current view (jump labels per match).
- `S` — global flash search (expands every group temporarily).

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
