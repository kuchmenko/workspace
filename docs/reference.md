# Command reference

Use `ws <command> --help` for complete flags and examples.

## Workspace registry

```sh
ws workspace create [path] --name <name> --recovery-key <path>
ws workspace import <workspace.toml> --name <name> --root <path> --recovery-key <path>
ws workspace export <name>
ws workspace list
```

Create and import establish a signed genesis revision in SQLite. Export writes TOML to stdout. TOML is never runtime state.

## Projects

```sh
ws setup
ws add [remote-url...]
ws create
ws scan
ws bootstrap [project]
ws migrate [project...]
ws status [project]
ws path [project]
ws doctor [project]
```

`add`, `create`, `setup`, `bootstrap`, `migrate`, and doctor repairs commit registry changes as signed SQLite revisions.

## Worktrees

```sh
ws worktree add <project> <branch> [--from <ref>]
ws worktree list [project]
ws worktree push <project> <branch>
ws worktree rm <project> <branch> [--force]
```

`ws wt` is an alias for `ws worktree`. Project branch pushes are explicit.

## Aliases and favorites

```sh
ws alias add <alias> <target> [--force]
ws alias rm <alias>
ws alias list
ws alias install

ws favorite add <project>
ws favorite rm <project>
ws favorite list
```

## Explorer

```sh
ws
ws explorer
```

Explorer reads every local SQLite workspace and persists its registry mutations through signed revisions.

## Authentication

```sh
ws auth login
ws auth login --pat
ws auth status
ws auth logout
```

## Sync

```sh
ws sync
```

Peer sync is not implemented yet, so the command fails closed without mutation.

## Local files

- `$XDG_STATE_HOME/ws/node/node.db`: workspace revisions and heads.
- `$XDG_STATE_HOME/ws/node/identity.key`: stable node signing identity.
- `$XDG_STATE_HOME/ws/aliases.zsh`: generated shell aliases.
- `$XDG_CONFIG_HOME/ws/config.toml`: machine-local settings.
- `$XDG_CONFIG_HOME/ws/token`: GitHub token.
