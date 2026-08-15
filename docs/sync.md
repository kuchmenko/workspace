# Sync

Peer sync is not implemented yet. `ws sync` fails without changing local or remote state.

The runtime registry is already prepared for the future peer protocol:

- every local workspace is stored in the node SQLite database;
- every mutation creates an Ed25519-signed snapshot revision;
- revisions form a DAG and carry stable workspace and node identities;
- all nodes have equal protocol weight;
- a permanently online LXC node is optional and has no special authority;
- Git project contents and credentials are outside registry synchronization.

The removed Git-backed registry sync and centralized LAN service are not runtime fallbacks.

See [Multi-master sync protocol](multi-master-sync-protocol.md) for the settled protocol model and the remaining transport, enrollment, reconciliation, revocation, and backup work.
