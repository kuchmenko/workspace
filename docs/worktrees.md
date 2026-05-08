# Worktrees

Every project lives as a **bare repo + per-feature worktree** sibling
triplet, so two machines can work on different branches of the same
project without ever fighting over a checked-out ref.

```text
personal/
├── myapp/                            ← main worktree (default branch)
│   └── .git                          ← pointer file into ../myapp.bare
├── myapp.bare/                       ← bare repo, single source of git state
└── myapp-wt-linux-feat-fix-login/    ← extra worktree for branch feat/fix-login
```

Convert any plain checkout once with `ws migrate <name>`; new projects
created via `ws add` / `ws create` start in this layout directly.

## Branch naming

`ws worktree add <project> <branch>` takes the branch name **verbatim**:
no `wt/<machine>/<topic>` injection, no template, no validation regex.
Whatever convention the project follows is what you type:

```sh
ws worktree add myapp feat/auth-refactor
ws worktree add myapp fix/prod-1234
ws worktree add myapp chore/cleanup
ws worktree add myapp wip/throwaway
```

`ws` validates the name with `git check-ref-format --branch` and
surfaces git's own error on rejection.

The directory name is `<project>-wt-<machine>-<branch-slug>` with
slashes flattened to dashes. If a slug collides with another branch
already in the same project, `ws` appends a deterministic `-<sha8>`
suffix derived from `SHA-1(branch)` so the path is unique. The suffix
is stable across machines.

## Starting a feature

```sh
ws worktree add myapp feat/fix-login
#   creates branch feat/fix-login from myapp's default branch
#   checks it out at personal/myapp-wt-linux-feat-fix-login
#   registers [[branches]] entry: machines=[linux], created_by=linux
```

`--from <ref>` overrides the base ref (default: `proj.default_branch`).
Ignored with a warning when the branch already exists locally or on
origin — those cases attach to the existing branch.

## Cross-machine handoff

The daemon does **not** auto-push project branches. Pushes are
explicit. Each `ws worktree push` updates `last_pushed_*` /
`last_active_*` in `workspace.toml` so the other side sees the activity
trail.

```sh
# On linux:
ws worktree add myapp feat/fix-login
# (edit, commit)
ws worktree push myapp feat/fix-login        # publish + stamp

# On archlinux: workspace.toml has already synced via the daemon's Phase 1.
ws worktree add myapp feat/fix-login         # auto-detects existing origin ref,
                                             # creates local from origin/feat/fix-login,
                                             # machines=[linux, archlinux]
# (edit, commit)
ws worktree push myapp feat/fix-login        # publish from this machine
```

Plain `cd <wt> && git push` works too — it just won't update the
`last_pushed_*` fields. The metadata trail is best-effort visibility,
not load-bearing for git correctness.

## Listing worktrees

```sh
ws worktree list                  # all projects
ws worktree list myapp            # one project
```

Output columns: PROJECT, WORKTREE (path relative to ws root), BRANCH,
STATE. STATE includes clean/dirty, ↑ahead ↓behind, and an ownership
tag — `main`, `mine`, `shared with <machines>`, `remote (<machines>)`,
or `legacy-wt` for pre-0.7.0 `wt/<machine>/*` checkouts. When the
branch is registered in `[[branches]]`, the state also carries
`(last: <machine> <date>)` from `last_active_*`.

## Removing a worktree

```sh
ws worktree rm myapp feat/fix-login          # refuses if dirty or has unpushed commits
ws worktree rm myapp feat/fix-login --force  # force regardless
```

`rm` releases this machine from `[[branches]].machines`. When that
slice becomes empty, the entry is GC'd on the next save (no orphan
tombstones). The remote ref on origin is **not** deleted — that's a
separate `git push origin --delete` if you want it gone.

`ws` refuses to remove the project's main worktree by branch
(`ws worktree rm myapp main`); deleting the primary checkout would
leave the project unusable.

## Re-registering a legacy `wt/<machine>/*` worktree

Pre-0.7.0 worktrees on `wt/<machine>/<topic>` keep working but live
outside `[[branches]]`. To bring one under the new metadata model:

```sh
ws worktree add myapp wt/linux/old-topic
# detects an existing local branch (and any existing checkout on disk),
# attaches without creating a duplicate, writes a fresh [[branches]]
# entry with machines=[linux].
```

The same path also recovers from a previous `ws worktree add` that
created the worktree but failed at `saveWorkspace` — re-running the
command sees the existing worktree, skips git, and writes the missing
metadata.

## Recovering from a deleted-on-origin branch

When a PR is merged with auto-delete-branch enabled (or someone runs
`git push origin --delete <branch>`), the next reconciler tick records
a `branch-orphan` conflict. Resolve via `ws sync resolve`:

- **Drop entry** — for the merged-PR cleanup case. If a local worktree
  still exists, `ws sync resolve` instructs you to run `ws worktree rm`
  first; otherwise the `[[branches]]` entry is dropped directly.
- **Keep local** — clears `last_pushed_*` so the orphan check stops
  firing. The branch stays as a local-only ref. A subsequent
  `ws worktree push` reinstates the field and normal orphan detection
  resumes.

See [Daemon and sync](daemon-and-sync.md#conflicts) for the full
conflict catalog.

## When you need a single-shot push helper

`ws worktree push <project> <branch>` is the canonical path:

- Refuses dirty worktrees unless `--force-dirty`.
- Refuses branches missing from `[[branches]]` (sign of out-of-band
  creation; the user should re-register via `ws worktree add`).
- Wires `-u origin <branch>` on first push so subsequent `git pull`
  works without ceremony.
- Stamps `last_pushed_machine` / `last_pushed_at` (the orphan-detection
  signal) and bumps `last_active_*`.

If you push from inside the worktree with plain `git push`, none of
the metadata fields update. That's fine for one-offs but makes the
cross-machine view stale.
