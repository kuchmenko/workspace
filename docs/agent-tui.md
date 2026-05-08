# Agent TUI

`ws` (run with no arguments in a TTY) — or `ws agent` explicitly —
opens a Bubble Tea TUI nested-list launcher across every workspace
the daemon knows about. It is the fastest path from "I want to work
on something" to a shell or a Claude Code session in the right
directory.

```sh
ws                          # bare invocation; same as `ws agent`
ws agent                    # explicit
```

When stdout is not a TTY, `ws` falls through to `cmd.Help()` so
piping / scripts get help instead of a TUI prompt.

## What you see

The agent reads `~/.config/ws/daemon.toml` to find every registered
workspace, walks each one for projects / groups / worktrees / Claude
sessions, and renders a single nested list:

```text
~/dev — workspace
├── personal
│   ├── dotfiles
│   ├── ws (workspace itself)
│   │   ├── main
│   │   ├── feat/foo                 (mine, ↑2)
│   │   └── feat/auth-refactor       (shared with archlinux)
│   └── …
└── work
    └── api-gateway
        └── …
```

Group / project rows expand and collapse. Worktrees show the same
ownership tags as `ws worktree list` (`main`, `mine`,
`shared with <machines>`, `legacy-wt`).

## Keys

Navigation:

- `j` / `↓`, `k` / `↑` — move selection
- `tab` — toggle expand/collapse for groups and projects
- `h` / `←` — collapse one level. Smart: from a worktree row it
  closes the parent project; from a project row under a group it
  closes the group.
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
- `d` — on a non-main worktree row, prompt for delete (with
  registry release; releases this machine from
  `[[branches]].machines`).

Search:

- `s` — flash search inside the current view (jump labels per match).
- `S` — global flash search (expands every group temporarily).

Help:

- `?` or `space` — which-key panel of available actions in context.

## Worktree creation from the TUI

Press `w` on a project row → "Branch name" input → confirm. The TUI
runs the same path as `ws worktree add <project> <branch>`:

- Auto-detects an existing remote ref and checks it out.
- Auto-detects an existing local-only ref and attaches.
- Auto-detects an existing on-disk worktree on the branch
  (legacy `wt/<machine>/*` re-registration) and writes metadata
  without creating a duplicate.
- Otherwise creates a fresh branch from the project's default branch.

After the form closes, the agent invalidates its worktree cache and
re-renders so the new entry appears immediately.

## Project edit

Press `e` on a project row → group / category form. Edits update
`workspace.toml` directly (Phase 1 of the next reconciler tick
commits + pushes the change). Useful when reorganizing the layout
without leaving the launcher.

## Sessions

Claude Code sessions are listed under their owning project. Hitting
`enter` on a session row opens it with `claude --resume <id>` rooted
at the session's recorded `cwd`. The session cache is shared with
`SessionCache` so repeated `ws` invocations stay fast.

## Why a TUI

Three reasons it earns its keep:

- **One key per worktree.** Beats remembering aliases for branches
  that come and go.
- **Cross-workspace.** If you have several `ws daemon register`'d
  directories, they all show up in one list.
- **Claude integration.** The launcher is the primary way to drop
  into a Claude session that already has the right `cwd` and an
  optional resume target.
