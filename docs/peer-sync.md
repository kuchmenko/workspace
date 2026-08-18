# Peer workspace sync

Peer sync lets trusted machines exchange workspace registry state directly on
the LAN. It does not copy project directories, Git repositories, worktrees,
credentials, machine configuration, or workspace roots.

## Pair devices

Run `ws network pair` on one device and `ws network join <code>` on the other.
Confirm the same verification number on both sides. Keep `ws network serve`
running on devices that should be reachable. The stable default TCP port is
`17337`.

Pairing establishes device identity and transport trust only. It does not make
any workspace visible.

## Share and attach

On the device that already has the workspace:

```sh
ws workspace share personal --with all --role writer
```

On another online paired device:

```sh
ws workspace available
ws workspace attach personal --root ~/Documents/workspace
```

The attached device chooses its own local name and root. The shared workspace
ID, revision history, policy, and canonical root-independent snapshot are the
same on every attached device.

Use selected sharing when only particular devices should participate:

```sh
ws workspace share personal --with asahi,lxc --role writer
ws workspace access set personal lxc replica
ws workspace access personal
```

Roles:

- `admin` writes registry state and changes workspace access.
- `writer` writes registry state and reconciles concurrent revisions.
- `replica` receives and forwards verified revisions but cannot author changes.

Revocation and role demotion rotate the workspace epoch. Old-epoch revisions
are quarantined rather than merged. A device removed from the trusted network
is denied by shared workspaces before they are offered again.

## Synchronize

Run synchronization explicitly from either attached device:

```sh
ws workspace sync personal
```

The command pushes and pulls signed registry revisions through authenticated
TLS. Repeated transfers are idempotent. Independent changes merge
deterministically. A conflicting scalar retains the common-ancestor value and
is shown by:

```sh
ws workspace conflicts personal
ws workspace resolve personal /aliases/editor --take right
ws workspace sync personal
```

Top-level `ws sync` exchanges SQLite registry history before and after its
foreground project Git operations. Use `ws workspace sync` when only the
registry exchange is wanted.
