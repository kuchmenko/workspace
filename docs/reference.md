# Command reference

Every command, every flag. For prose walk-throughs see the topic
docs ([getting-started](getting-started.md), [worktrees](worktrees.md),
[daemon-and-sync](daemon-and-sync.md), [aliases](aliases.md),
[agent-tui](agent-tui.md)).

Every command supports the global `--root <dir>` flag to override
the workspace-root auto-detection.

## Project management

### `ws setup`

Interactive onboarding TUI. Lists every repo you have access to on
GitHub, lets you pick / group them, writes `workspace.toml`. See
[Getting started](getting-started.md#ws-setup--interactive).

### `ws sync` / `ws sync resolve`

```sh
ws sync                    # one reconciler tick in the foreground
ws sync resolve            # interactive prompt for unresolved conflicts
```

`ws sync` runs the same work as a daemon tick: commit-pull-push of
`workspace.toml`, fetch every bare, ff-pull main worktrees, refresh
`last_active_*` for branches with local-ahead commits, surface
`branch-orphan` for vanished origin refs.

`ws sync resolve` walks `~/.local/state/ws/conflicts.json` one entry
at a time. See [Daemon and sync — conflicts](daemon-and-sync.md#conflicts)
for the catalog.

### `ws add`

```sh
ws add <url>...                   # one or more URLs (sequential)
ws add -                          # read URLs from stdin
ws add                            # interactive TUI

  -c, --category <personal|work>  # default: personal
  -g, --group <name>              # group/directory; usually GitHub org
  -n, --name <name>               # override derived name (single URL only)
      --no-clone                  # register only; defer the clone
      --tui                       # force TUI even with positional args
      --no-tui                    # force headless; error if no URLs given
```

Holds an `add/<sha>.toml` sidecar while running so the daemon pauses
the affected workspace.

### `ws create`

Create a new GitHub repo (in any owner you can push to via `gh`),
then register + clone it.

```sh
ws create                                            # TUI: owner / name / visibility / desc
ws create --owner <user-or-org> --name <repo>        # headless
      [--public]                                     # default: private
      [--description "..."]
```

Repos are created with `--add-readme` so the default branch + first
commit exist before the clone runs (avoids the bootstrap-default-
ambiguous error path).

Requires `gh auth login` (separate from `ws auth login`). Holds a
`create/<sha>.toml` sidecar.

### `ws bootstrap [name]`

Clone projects listed in `workspace.toml` that are missing on this
machine. TUI by default; `--dry-run` shows the plan without cloning.
Holds a `bootstrap/<sha>.toml` sidecar.

### `ws migrate [name]`

Convert plain git checkouts into the bare+worktree layout in place.

```sh
ws migrate <name>             # interactive TUI (default)
ws migrate --all              # walk every active project, skip migrated
ws migrate --check [name...]  # preview without touching anything
ws migrate --wip              # snapshot dirty working tree to a wt/<machine>/migration-wip-<ts> branch
ws migrate --no-tui           # force headless mode
```

Pre-flight handles dirty trees, stash entries, and detached HEADs as
recovery branches. Holds a `migrate/<sha>.toml` sidecar; the
attach-worktree strategy is documented in
[Architecture — On-disk layout](architecture.md#on-disk-layout).

### `ws status`

Table view: project / group / status / branch / last commit / layout.
The LAYOUT column reads `plain`, `worktree`, `worktree+N` (where N
counts extra worktrees), or `missing`.

### `ws scan`

Find git repos under the workspace's category / group directories
that are not registered in `workspace.toml`. Ignores `*.bare/` and
`*-wt-*/` siblings so the worktree layout doesn't show up as
orphans.

### `ws path [project]`

Resolve a project name to its absolute filesystem path on stdout.
The pipe-friendly variant of `ws status`.

```sh
ws path                       # workspace root
ws path workspace             # /home/user/dev/personal/workspace
cd "$(ws path workspace)"
```

Exit codes:

- `0` — success.
- `1` — outside any workspace, or project registered but checkout
  doesn't exist on disk (hint: `ws bootstrap`).
- `2` — project name not in `workspace.toml`. Lists registered names
  if there are < 5; otherwise just the error.
- `64` — usage error (more than one positional arg).

### `ws doctor`

Unified diagnostic + auto-fix pass. See
[Daemon and sync — health check](daemon-and-sync.md#health-check-ws-doctor).

```sh
ws doctor                     # all projects + system, print findings
ws doctor <project>           # one project
ws doctor --fix               # apply safe auto-repairs in batch
ws doctor --json              # machine-readable
ws doctor --skip-remote       # skip network-touching checks
```

## Worktrees

### `ws worktree add <project> <branch>` (alias `ws wt add`)

```sh
ws worktree add <project> <branch>
   --from <ref>               # base ref (default: project default_branch).
                              # Ignored when the branch already exists on
                              # origin or locally.
```

Three cases, picked automatically:

1. Branch exists in an existing worktree on disk → re-register
   metadata against that path (no new worktree). Covers legacy
   `wt/<machine>/*` re-registration and retries after a previous
   failed `saveWorkspace`.
2. Branch exists on origin (or locally as a ref) → attach.
3. Otherwise → create from `--from` (or `proj.default_branch`).

Slug collisions in the directory name get a deterministic
`-<sha8>` suffix from `SHA-1(branch)`.

### `ws worktree list [project]` (alias `ws wt list`)

Table: PROJECT, WORKTREE, BRANCH, STATE. STATE includes
clean/dirty, ↑ahead ↓behind, ownership tag (`main`, `mine`,
`shared with <machines>`, `remote (<machines>)`, `legacy-wt`),
and `(last: <machine> <date>)` from the registry.

### `ws worktree rm <project> <branch>` (alias `ws wt rm`)

```sh
ws worktree rm <project> <branch>
   --force                    # remove even if dirty or has unpushed commits
```

Refuses to remove the project's main worktree by branch (would
leave the project unusable). Releases this machine from
`[[branches]].machines`; empty machines causes the entry to be
GC'd on the next save.

### `ws worktree push <project> <branch>` (alias `ws wt push`)

```sh
ws worktree push <project> <branch>
   --force-dirty              # push even with uncommitted changes
```

Wraps `git push -u origin <branch>` and stamps `last_pushed_*` /
`last_active_*` in `workspace.toml`. Refuses branches missing from
`[[branches]]` — that's a sign of out-of-band creation; user
should re-register via `ws worktree add`.

## Aliases

```sh
ws alias                      # interactive TUI
ws alias list                 # show configured aliases
ws alias add <alias> <target> # target is a project name, group name, or "."
ws alias rm <alias>
ws alias init [zsh]           # print shell snippet to eval
ws alias install              # write a sourcing line into ~/.zshrc (idempotent)
```

Generated aliases land at `$XDG_STATE_HOME/ws/aliases.zsh` (default
`~/.local/state/ws/aliases.zsh`). Currently zsh-only.

## Daemon

```sh
ws daemon register [path]     # add a workspace; defaults to cwd
ws daemon unregister [path]
ws daemon start               # background-spawn the daemon
ws daemon stop
ws daemon restart
ws daemon run                 # foreground (for the systemd unit)
ws daemon status              # PID + registered workspaces
ws daemon install-service     # install systemd user unit
```

## Authentication

```sh
ws auth login                 # GitHub device flow
ws auth login --pat           # paste a Personal Access Token
ws auth status
ws auth logout
```

Token at `~/.config/ws/token`. `ws create` uses `gh repo create`
under the hood and therefore needs `gh auth login` separately —
the two authentications don't share state.

## Agent TUI

```sh
ws                            # bare invocation, in a TTY → agent TUI
ws agent                      # explicit
```

See [Agent TUI](agent-tui.md) for keys and behavior.

## Docs / completion (developer-facing)

```sh
ws docs --agent               # JSON dump of every command's annotations
                              # (used by Claude Code agent integration)
ws completion <shell>         # cobra-generated shell completion
```

## Global flags

- `--root <dir>` — override workspace-root auto-detection. Useful
  in scripts; otherwise `ws` walks up from cwd or honors `WS_ROOT`.
- `-h, --help` — per-command help.
