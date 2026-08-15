# Architecture

## Runtime state

The node database at `$XDG_STATE_HOME/ws/node/node.db` is the only runtime source of workspace registry state. The node's Ed25519 identity is stored beside it in `identity.key`.

A node can hold multiple named workspaces. Each workspace has a local root path, stable workspace ID, recovery epoch, one or more revision heads, and a decoded registry snapshot. Commands select the workspace whose root is the longest path-boundary-safe ancestor of the current path, or the exact root supplied with `--root`.

`workspace.toml` is only a migration and interchange format:

- `ws workspace import` creates a genesis revision;
- `ws workspace export` emits a portable copy;
- normal commands never read, create, or rewrite it.

Machine-local preferences and credentials remain outside the workspace registry.

## Revision model

Registry mutations create deterministic CBOR snapshots and signed revisions. A normal local write points to the expected current head, increments the author sequence, and atomically replaces that head in SQLite. The expected-head check prevents silent lost updates inside one node.

The snapshot contains registry data and protocol membership. Security-sensitive registry changes require an authorized role. Recovery identity and epoch are part of genesis so future peer enrollment and revocation do not depend on GitHub credentials.

The DAG preserves ancestry needed for future reconciliation. It is not blockchain consensus: there is no mining, total global ordering, token, quorum for ordinary writes, or Byzantine agreement.

## Command flow

```diagram
CLI or Explorer
      │
      ▼
select SQLite workspace by root
      │
      ▼
load current signed snapshot
      │
      ▼
perform filesystem/Git work and mutate registry state
      │
      ▼
commit signed child revision with expected head
```

Project repositories remain normal Git repositories. The registry stores project metadata, paths, groups, aliases, favorites, and worktree activity; it does not store Git objects or credentials.

## Explorer

Explorer enumerates all workspaces from SQLite and keeps same-named groups isolated by workspace root. Favorites, project organization, activity stamps, archive state, and worktree ownership are committed through the same signed revision path as CLI mutations.

## Current sync boundary

Local storage and signed revisions are implemented. Peer discovery, authenticated transport, enrollment, revocation, DAG exchange, reconciliation, background anti-entropy, and backup automation remain future work. `ws sync` therefore fails closed. See [Multi-master sync protocol](multi-master-sync-protocol.md).
