# Command reference

Every command, every flag. For prose walk-throughs see the topic
docs ([getting-started](getting-started.md), [worktrees](worktrees.md),
[sync](sync.md), [aliases](aliases.md), [explorer](explorer.md)).

Every command supports the global `--root <dir>` flag to override
the workspace-root auto-detection.

## Project management

### `ws sync` / `ws sync resolve`

```sh
ws sync                    # preflight, review, and execute in foreground
ws sync resolve            # interactive prompt for unresolved conflicts
```

`ws sync` first exchanges the selected SQLite workspace registry with reachable
trusted peers, then builds a fresh plan and probes unique project and mirror
endpoints before project mutation. With terminal stdin and stdout it
opens an interactive source/project/mirror review, supports run-only
exclusions and verified known-provider HTTPS-to-SSH origin conversion,
then asks for confirmation. The frozen selection executes sequentially:
project clone/fetch, selected mirror pushes, safe main
worktree fast-forwards, branch metadata refresh, and orphan detection. A final
registry exchange publishes resulting metadata to online peers.

With redirected stdin or stdout it emits ANSI-free text and requires every Git
endpoint to pass preflight. Any failed probe exits before project mutation.

Exit codes: `0` success, `1` failed preflight/execution or conflict, `130`
canceled.

`ws sync resolve` walks `~/.local/state/ws/conflicts.json` one entry
at a time. See [Sync: Conflicts](sync.md#conflicts) for the catalog.

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

Holds an `add/<sha>.toml` sidecar for crash recovery and same-workspace
operation exclusion.

### `ws status`

Table view: project / group / status / branch / last commit / layout.
The LAYOUT column reads `plain`, `worktree`, `worktree+N` (where N
counts extra worktrees), or `missing`.

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
  doesn't exist on disk (hint: `ws sync`).
- `2` — project name not in the selected registry. Lists registered names
  if there are < 5; otherwise just the error.
- `64` — usage error (more than one positional arg).

## Worktrees

### `ws worktree add <project> <branch>` (alias `ws wt add`)

```sh
ws worktree add <project> <branch>
   --from <ref>               # base ref (otherwise default_branch is required).
                              # Ignored when the branch already exists on
                              # origin or locally.
```

Three cases, picked automatically:

1. Branch exists in an existing worktree on disk → re-register
   metadata against that path (no new worktree). Covers legacy
   `wt/<machine>/*` re-registration and retries after a previous
   failed `saveWorkspace`.
2. Branch exists on origin (or locally as a ref) → attach.
3. Otherwise → create from `--from` or the required `proj.default_branch`;
   error when neither is set.

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
`last_active_*` in SQLite. Refuses branches missing from
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

## Local Metrics

`ws` keeps bounded usage counters in `$XDG_STATE_HOME/ws/metrics.json`
(default `~/.local/state/ws/metrics.json`). This machine-local file is never
written to the workspace registry or synchronized. Its fixed schema contains only
command-family, terminal-mode, outcome, duration-bucket, and fixed workflow
counters. It does not contain names, paths, URLs, branches, arguments,
searches, diagnostics, credentials, machine identity, timestamps, history, or
dynamic keys. Recording is best effort; a busy lock or storage error drops the
increment rather than delaying or failing the command. Generated aliases do
not record metrics when invoked.

## Workspace Registry

```sh
ws workspace create [path] --name <name>                     # path defaults to cwd
ws workspace import <workspace.toml> --name <name> --root <path>
ws workspace export <name>                                   # TOML on stdout
ws workspace list                                            # names and roots
ws workspace set-root <workspace> <path>                     # rebind locally; does not move files
ws workspace share <name> --with all --role writer
ws workspace share <name> --with <device>[,<device>...] --role <role>
ws workspace access <name>
ws workspace access set <name> <device> <admin|writer|replica>
ws workspace access remove <name> <device>
ws workspace available [--json]
ws workspace attach <workspace> --root <path> [--name <local-name>]
ws workspace sync [workspace] [--json]
ws workspace conflicts <workspace> [--json]
ws workspace resolve <workspace> <path> --take <base|left|right>
ws workspace resolve <workspace> <path> --value <json>
```

Create, import, and set-root write `$XDG_STATE_HOME/ws/registry.db`; export is
the only command that emits TOML. Set-root changes only the machine-local root
mapping and does not move files or create a synchronized revision. Workspace
sharing and synchronization exchange signed registry revisions with
authenticated online peers. They do not transfer repositories. `ws workspace
sync` performs only that exchange; top-level `ws sync` wraps registry exchange
around project Git synchronization.

## Authentication

```sh
ws auth login                 # GitHub device flow
ws auth login --pat           # paste a Personal Access Token
ws auth status
ws auth logout
```

Token stored at `~/.config/ws/token`; GitHub discovery can fall back to gh.

## Explorer TUI

```sh
ws                            # bare invocation, in a TTY → explorer TUI
ws explorer                   # explicit
```

See [Explorer TUI](explorer.md) for keys and behavior.

## Docs / completion (developer-facing)

```sh
ws docs --agent               # JSON contract for the curated agent command inventory
ws completion <shell>         # cobra-generated shell completion
```

## Global flags

- `--root <dir>` — override workspace-root auto-detection. Useful
  in scripts; otherwise `ws` walks up from cwd or honors `WS_ROOT`.
- `-h, --help` — per-command help.
