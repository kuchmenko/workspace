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

The explorer reads every named workspace from
`$XDG_STATE_HOME/ws/registry.db`, walks each root for projects, groups, and
worktrees, and renders them as a scrollable tree.
Manage them with `ws workspace create/import/export/list`.

```text
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

### Views

`v` toggles Recent and Projects. Recent is the default and
orders projects by the newest registry branch activity or worktree HEAD
commit; `o` reverses it. Projects is an alphabetical workspace → canonical
group → project tree. The workspace level is omitted when only one workspace
is registered; with multiple workspaces, their roots begin expanded. Groups
begin collapsed. These preferences are machine-local.

## Keys

Navigation:

- `j` / `↓`, `k` / `↑` — move selection
- `g` / `Home`, `G` / `End` — jump to the first or last row
- `ctrl+d` / `ctrl+u` — move half a page
- `ctrl+f` / `ctrl+b`, `PageDown` / `PageUp` — move a full page
- `tab` — toggle expand/collapse for workspaces and groups
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
- `a` — from the home tree, create, edit, or remove the selected workspace,
  group, or project shell alias. Existing aliases appear on their rows.
- `a` — inside a group or project panel, archive a project, canonical group, or worktree. Project archive
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
  persisted to the SQLite workspace registry.

Search:

- `s` — flash search inside the current view (jump labels per match).
- `S` — filtered global search across all projects and local worktrees,
  independent of expansion and viewport.
- `R` — open the Amp runner view.

Help:

- `?` or `space` — which-key panel of available actions from home.

Group and project panels use the same full-screen frame as home: breadcrumb
header, an available-height list, optional status, and a persistent
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

## Amp runners

Press `R` to open the machine-local Amp runner view. It manages detached
`amp --no-tui` processes without opening terminal windows. This view is
process-centric: it shows managed and external Amp runners, their directories,
IDs when locally known, and local process status.

Create runners from the existing Explorer hierarchy: use `Ctrl+O` on a group,
project, or worktree row and choose **Start Amp runner**. The first start opens a
small form with a generated, editable Amp runner ID and a remote-terminal
toggle. No full path is entered. Pressing `r` on the selected group, project, or
worktree is the direct shortcut for the same action. Later actions use the saved
definition.

The runner view supports:

- `s` — start the selected stopped runner.
- `e` — edit the ID of a stopped or missing saved runner.
- `r` — restart the selected runner after confirmation.
- `x` — shut down the selected runner after confirmation.
- `X` — force shutdown when graceful shutdown did not complete.
- `d` — remove a stopped runner definition after confirmation.
- `p` — set the machine-local runner ID prefix used for new runners. It defaults
  to the configured machine name.
- `Enter` on an external runner — confirm replacement, gracefully stop that
  exact process, then open the normal attach form. Registered targets retain
  their symbolic workspace identity; other detected directories become
  explicit-path targets without requiring path entry. `X` performs the same
  replacement with force permitted.

The attach form proposes `<prefix>-<target>` and leaves the complete runner ID
editable. The chosen ID is persisted for that group, project, or worktree, so
short names such as `arch-lmts`, `arch-dotfiles`, and `arch-tkach` can coexist.

Runner definitions are stored in `~/.config/ws/config.toml`. Runtime PID and
Linux process-start identity are stored under `$XDG_STATE_HOME/ws/runners/` so
`ws` never signals a reused PID. Per-runner output is written there as well.
Existing `amp --no-tui` processes that were not started by `ws` appear as
unmanaged runners. They remain read-only unless replacement is explicitly
confirmed. Replacement verifies PID, Linux process-start time, cwd, and Amp
command before signaling; it never adopts a terminal-attached process in place.

Status is local process state only. Amp does not expose a supported busy or
drain API, so restart and shutdown may interrupt active work. Graceful shutdown
sends `SIGTERM` and waits ten seconds; forced shutdown then permits `SIGKILL`.
Runner management currently requires Linux `/proc`. There is no crash restart,
login auto-start, or background `ws` supervisor.

## Project edit

Press `e` on a project row → group / category form. Edits update the local
SQLite workspace registry. Useful when reorganizing the layout without
leaving the explorer.

## Alias editing

Press `a` on a workspace, canonical group, or project row to open its alias
editor. Enter saves the alias; saving an empty value removes it. Alias names
must be unique across every workspace loaded by Explorer because shell names
are global. Saving regenerates the zsh alias state from all registered
workspaces, while each alias remains owned by its workspace's SQLite registry.

When only one workspace is registered and its row is omitted, use
`Ctrl+O` → **Edit alias** in the Session section to edit the workspace-root
alias.

## Why a TUI

Two reasons it earns its keep:

- **Cross-workspace.** Named SQLite workspaces all show up in one list
  without scheduling background work.
- **Directory-aware shells.** Every launch opens the user's shell with the selected project, group, or worktree as its `cwd`.
