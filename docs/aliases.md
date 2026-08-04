# Shell aliases

`ws alias` generates short shell aliases that `cd` into any project,
group, or the workspace root. Aliases live in `workspace.toml` and
sync between machines via the workspace's git repo.

## Interactive flow

```sh
ws alias
```

```text
 ws alias   Manage aliases

  type to search...

> ●  ws              (workspace root)
  ●  acme            ├── acme-corp
  ●  api             │   ├── api-gateway
  ●  web             │   ├── web-dashboard
  ○  (auto)          │   └── legacy-service
  ○  (auto)          ├── other-org
  ○  (auto)          │   └── shared-lib
  ●  prs             └── personal
  ●  dot                 ├── dotfiles
  ●  cli                 ├── cli-tools
  ○  (auto)              └── old-experiment

  ↑↓ navigate  space toggle  e edit alias  enter next  esc cancel
```

Each entry is one of:

- a **project** (cd into the project directory),
- a **group** (cd into the group directory),
- the **workspace root** itself.

## Auto-name generator

When you toggle a row on without typing a name, `ws` picks a short
default. Rules:

- Two parts separated by `-` or `_`, each ≤ 4 chars → join: `co-op` →
  `coop`.
- Multi-part separated by `-` or `_` → first letter of each:
  `api-gateway` → `ag`, `my-cool-project` → `mcp`.
- Single word → consonants, max 5 chars: `dotfiles` → `dtfls`.

Press `e` on a row to override with your own name.

Alias names must start with an ASCII letter or underscore. The remaining
characters may be ASCII letters, digits, underscores, or hyphens. Each target
can have one alias; `ws alias add --force` replaces its existing alias.

## Headless API

```sh
ws alias add cli cli-tools             # alias name + project name (or group, or ".")
ws alias add cli .                     # cli → workspace root
ws alias rm cli
ws alias list                          # show configured aliases
ws alias init [zsh]                    # print shell snippet to eval
```

## Install into your shell

One-time setup:

```sh
ws alias install                # adds a sourcing line to ~/.zshrc
exec zsh                        # reload shell
```

After that, every `ws alias` save / `add` / `rm` automatically
regenerates `$XDG_STATE_HOME/ws/aliases.zsh` (default
`~/.local/state/ws/aliases.zsh`). Open a new shell or `source` that
file to pick up the changes — `.zshrc` itself is never touched again.

Currently only zsh is supported.
