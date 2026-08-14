# Multi-Master Workspace Sync Protocol

Status: draft protocol for issue #72. This document defines the intended MVP
behavior. The authoritative service implemented on the parent branch is
transitional implementation material, not part of this protocol.

## Goals

The protocol replicates workspace state between equal trusted nodes. A node may
be a workstation, laptop, LXC, or another machine running `ws`. An always-online
LXC improves reachability and bootstrap availability but has no special
authority.

The protocol provides:

- local writes while disconnected;
- automatic background replication with reachable trusted nodes;
- deterministic convergence after writes stop and nodes can communicate;
- independent shared and local-only workspaces;
- bootstrap from any trusted node with sufficient verified state;
- explicit semantic conflicts instead of arrival-order resolution;
- signed, encrypted portable backups and whole-network recovery.

Background replication changes workspace state only. It never clones, fetches,
pulls, pushes, invokes credentials, runs hooks, or changes worktrees.

## Terms

**Node** is one installation with a stable Ed25519 identity. Its node ID is the
SHA-256 hash of its public key. The same identity is used across workspaces in
the MVP.

**Workspace** is an independent replication and authorization domain with a
random 256-bit workspace ID, a recovery epoch, members, revisions, conflicts,
and backup policy. A local display name is not protocol identity.

**Revision** is an immutable content-addressed workspace snapshot with causal
parents. Revisions form a directed acyclic graph.

**Head** is a revision that is not incorporated by another verified revision in
the same workspace and epoch.

**Reconcile revision** is an authorless revision derived deterministically from
all currently known maximal heads.

**Recovery epoch** separates ordinary history from a whole-network recovery.
Old-epoch identities and revisions cannot authorize new-epoch state.

## Consistency Model

The target is strong eventual consistency:

> If writes stop and trusted nodes can communicate, every node eventually
> derives byte-identical workspace state and the same unresolved conflicts.

The protocol has no leader, quorum for ordinary writes, global transaction
order, or wall-clock conflict winner. Temporary divergence during a partition
is valid.

## Canonical Encoding

Protocol objects use RFC 8949 deterministic CBOR. Encoders must use shortest
representations, definite lengths, and bytewise lexicographic map-key ordering.
Decoders must reject duplicate keys, unknown fields, indefinite lengths,
trailing bytes, unsupported tags, non-finite numbers, oversized objects, and
excessive nesting or collection sizes.

A received object is canonical only when strict decoding followed by
deterministic re-encoding produces the exact received bytes.

Every hash and signature has a distinct textual domain prefix. Raw transport
bytes, TLS identity, timestamps, endpoints, and signatures are not included in
a revision ID unless a type below explicitly includes them.

## Workspace Snapshot

A snapshot contains the complete replicated state:

- workspace schema version;
- projects, groups, aliases, and shared explorer preferences;
- branch claims and observations;
- workspace membership and authorization policy;
- unresolved semantic conflicts.

It excludes local paths, workspace roots, node private keys, repository
credentials, peer endpoints, backup destinations, backup keys, and project
checkouts.

`workspace.toml` is not runtime state. It is accepted once as migration input
and may be exported as a portable human-readable snapshot. Importing an export
with `--local` creates a new unrelated local workspace without membership,
credentials, or replicated history.

## Revision DAG

A revision core contains:

```text
protocol_version
workspace_id
recovery_epoch
kind
parents
generation
snapshot_schema
snapshot
conflicts
author
author_sequence
previous_author_revision
```

`parents` is a sorted, duplicate-free list of revision IDs. `generation` is
zero for genesis and otherwise one plus the greatest parent generation. It is
validated metadata, not ordering authority.

The revision ID is:

```text
SHA-256("ws/revision/v1\0" || deterministic_cbor(revision_core))
```

Signatures are detached proofs over:

```text
Ed25519("ws/revision-signature/v1\0" || revision_id)
```

Detached proofs allow independently produced signatures to accumulate without
changing the revision ID.

### Revision Kinds

**Genesis** has no parents and establishes workspace ID, initial epoch,
recovery-key ID, initial members, admin policy, protocol floor, and first
snapshot.

**Write** has exactly one parent, an author, the next author sequence, the
author's previous authored revision, and one valid author signature. A node
must reconcile multiple local heads before creating an ordinary write.

**Resolution** is an authored write that parents every head carrying the
conflict it resolves and identifies the resolved conflict and chosen value.

**Authority** changes membership, roles, invitations, policy, sensitive
fields, or checkpoint authorization. It carries the required admin proofs.

**Reconcile** has at least two sorted parents containing the complete maximal
head set used for reconciliation. It has no author, author sequence, nonce, or
required signature. Its snapshot and conflicts are a pure function of its
parents and protocol version. Any node, including a replica, may derive and
verify it. The same parent set must always produce the same revision ID.

**Checkpoint** commits a verified complete snapshot, current authority state,
unresolved conflicts, and covered history frontier. It requires admin approval.
The MVP uses checkpoints for bootstrap but does not prune history.

### Author Chains and Equivocation

Each authored revision advances a workspace-and-epoch-specific sequence and
names the author's previous authored revision. Two different revisions with
the same node, sequence, and previous authored revision are equivocation.

Peers retain both revisions as evidence, quarantine that author's history after
the fork, stop automatic reconciliation involving it, and require admin
revocation and explicit salvage. A signature proves attribution, not which fork
is honest.

## Deterministic Reconciliation

After accepting verified revisions, a node removes every head reachable from
another head and sorts the remaining maximal heads by revision ID.

- Zero heads is invalid.
- One head is materialized directly.
- Multiple heads produce one deterministic reconcile revision over all heads
  at once.

Pairwise merging in arrival order is forbidden because it does not guarantee
three-node convergence.

For each stable semantic field or keyed entity, reconciliation considers the
causally maximal values contributed by the parent histories:

- one side changed from the common history: take the change;
- identical concurrent values: coalesce;
- independent fields or keys: merge;
- incompatible concurrent values: preserve a structured conflict;
- delete versus unchanged: delete;
- delete versus concurrent edit: conflict;
- branch machine claims: concurrent add wins over concurrent release; a
  causally later release removes the claim;
- activity observations never use wall-clock order as authority.

For a conflicted field, the materialized snapshot retains the common value, or
absence when there was no common value. The conflict contains every canonical
candidate and its source revision IDs. Unrelated fields continue to converge.

A conflict ID is the SHA-256 hash of a domain prefix, workspace ID, epoch,
semantic path, and sorted canonical candidates. Resolution creates a new
revision; history is never rewound.

## Authorization

Transport authentication and workspace authorization are separate. TLS proves
possession of a node key for one connection. Replicated membership determines
what that node may do in each workspace.

Capabilities are:

- **replica**: verify, store, serve, and forward valid history and derive
  deterministic reconcile revisions;
- **writer**: replica capabilities plus ordinary workspace writes and
  non-sensitive conflict resolutions;
- **admin**: writer capabilities plus sensitive and authority approvals.

The initial shared workspace has two admins: the Arch workstation and MacBook.
The threshold is one-of-two, so either admin may approve a sensitive change.
This preserves availability but means compromise of either admin gives the
attacker sensitive authority. LXC and additional machines are writers or
replicas unless explicitly promoted.

Admin approval is required for:

- project and mirror remote additions, changes, or removal;
- project or workspace deletion;
- invitations, grants, role changes, revocation, and membership removal;
- protocol, schema, merge, authorization, and resource-policy changes;
- checkpoints and recovery preparation;
- conflict resolution that changes a sensitive field.

Writers may change aliases, favorites, groups, display metadata, branch claims
and observations, and non-sensitive conflicts.

An approved remote change only changes registry state. Every machine requires
separate explicit local confirmation before its first Git operation against a
new or changed remote.

## Enrollment

Enrollment invitations are bound to the joining node's public key. The joining
node first presents a join request containing its node ID and public key. An
admin approves an invitation containing:

- workspace ID and recovery epoch;
- invite ID and random nonce;
- exact invitee public key;
- granted role;
- expiry;
- bootstrap checkpoint or head set;
- inviter identity and policy version.

The invitation is workspace-scoped, epoch-scoped, role-scoped, expiring, and
single-use. A bearer secret prevents unsolicited redemption but does not grant
authority to another key.

The joining node pins the inviter identity, downloads genesis, authority
history, checkpoint, and successors into inactive storage, verifies its grant
and all hashes and signatures, reconciles the received heads, and activates the
workspace atomically. Failed bootstrap leaves no active partial workspace.

## Revocation

Revocation contains an admin-approved cutoff frontier representing history
accepted from the target node.

Target-authored revisions are valid only when they are in the causal past of
the cutoff. Concurrent or later target revisions are quarantined and never
merged or forwarded as valid history. They remain available for explicit
manual salvage. This intentionally prefers containment over preserving unseen
offline work from a compromised key.

Revocation cannot become immediate across a partition. Nodes that learn it
later deterministically quarantine revisions outside the cutoff.

Promotion authorizes only causally later revisions. Demotion applies the cutoff
rule to removed capabilities. Rejoining normally requires a fresh node key and
grant.

## Peer Synchronization

mDNS advertises node endpoint, protocol versions, and node identity only. It
does not reveal workspace names or membership and never establishes trust.
Remembered endpoints allow synchronization where multicast discovery is
unavailable.

After mutually authenticated TLS, peers determine mutually shared workspace
IDs and epochs, then synchronize each workspace independently:

1. Exchange sorted head IDs and checkpoint frontiers.
2. Walk unknown parents until known history or an accepted checkpoint.
3. Request missing immutable objects by ID in bounded batches.
4. Verify canonical bytes, hashes, parent closure, signatures, author chains,
   causal authorization, schema, epoch, and resource limits.
5. Persist accepted objects before acknowledging them.
6. Recompute maximal heads and deterministic reconciliation.
7. Atomically materialize the resulting local state.

Sessions are idempotent and resumable because transferred objects are immutable
and content-addressed. Probabilistic summaries may optimize discovery but never
prove completeness.

A malformed, conflicting, quota-exhausted, or unavailable workspace does not
block synchronization of another workspace.

## Background Runtime

Every networked node runs the same background process. It synchronizes after a
local revision, when a peer appears, and periodically with jitter. Failures use
bounded backoff. The runtime exposes status and logs and persists all state
before acknowledgment.

Linux uses systemd and macOS uses launchd. Process supervision changes
availability only; it grants no protocol authority.

## Multiple Workspaces

Each workspace has independent identity, epoch, history, membership, peers,
conflicts, and backup policy. A node joins only selected workspaces. Local-only
workspaces use the same storage and revision model with no remote members.

Explorer may combine locally joined workspaces, but every project, mutation,
conflict, peer status, and bootstrap action displays its owning workspace.

Joining transfers registry state only. Project repositories are selected and
cloned explicitly with workspace-scoped bootstrap commands.

## Backups and Recovery

A live replica is not a backup because valid accidental or malicious revisions
replicate to it.

A portable backup bundle contains protocol and schema versions, workspace ID
and epoch, complete snapshot, membership and authority state, unresolved
conflicts, enough history to verify and continue, and integrity proofs. It
excludes node private keys, repository credentials, checkouts, peer endpoints,
and machine-local settings.

The bundle is signed by its producing node and encrypted to a dedicated offline
recovery key whose public identity is committed by genesis. Backup schedule,
destination, retention, and private recovery-key location are local settings.

Losing one node is repaired by enrollment and peer bootstrap. Accidental state
is corrected by publishing an old snapshot as a new revision.

Whole-network recovery requires the offline key to sign an epoch transition
binding the old workspace and checkpoint, a new random epoch, the restored
checkpoint, fresh membership, and a nonce. Old-epoch nodes and invitations
cannot affect new state. There is no emergency bypass based only on a peer's
claimed higher epoch.

## History Retention

The MVP retains verified revision history indefinitely. Checkpoints accelerate
bootstrap but do not authorize pruning. Stale-node leases, garbage collection,
and manual salvage after expiry require a later protocol decision based on
measured storage growth.

## Security Boundaries

The protocol cannot prevent an authorized writer from making harmful ordinary
changes or a valid admin from authorizing sensitive changes. It limits
capabilities, attributes every authored revision, retains recoverable history,
and prevents background project operations.

One-of-two administration does not protect against compromise of either admin.
The offline recovery key protects whole-network recovery, not routine admin
approval.

No protocol can provide immediate revocation across a partition, choose the
honest branch after key equivocation, prove a restored snapshot was globally
latest after every live copy is lost, or safely prune history needed by an
indefinitely offline node without an expiry rule.

Implementations must authenticate before workspace negotiation and enforce
per-peer and per-workspace limits for bytes, object count, parent depth,
concurrency, CPU, disk, quarantine evidence, and pairing attempts.

## Migration and Export

Migration imports the current `workspace.toml` once to create genesis. After
the new network is verified, Git and the authoritative LAN service stop writing
registry state and the permanent TOML is removed without dual authority.

Export creates a human-readable TOML snapshot:

```sh
ws workspace export shared > workspace.toml
```

Importing it on a disconnected machine creates a new local workspace:

```sh
ws workspace import workspace.toml --name local-copy --root ~/development --recovery-key /offline/ws-recovery.key
```

It carries no membership, credentials, node identities, conflicts, or history
and never reconnects to the source workspace automatically.

The current implementation provides this local migration slice: node identity,
genesis import, export, SQLite-backed reads, and signed compare-and-swap
revisions for CLI registry mutations, including project and worktree metadata.
Peer discovery, transfer, reconciliation, enrollment, revocation, backup, and
background replication remain future slices.

## MVP Validation

The implementation must prove:

- byte-identical revision IDs and reconcile results for fixed test vectors;
- two-, three-, and four-node convergence under reordered and duplicated
  transfer;
- offline concurrent independent edits and explicit same-field conflicts;
- deterministic delete/edit and branch claim/release behavior;
- restart at every persist, acknowledge, and materialization boundary;
- rejection of malformed, non-canonical, forged, unauthorized, revoked,
  equivocated, stale-epoch, oversized, and downgrade objects;
- key-bound one-use enrollment and atomic bootstrap;
- independent failure and synchronization of multiple workspaces;
- background replication without any project repository operation;
- verified encrypted backup and whole-network epoch recovery.
