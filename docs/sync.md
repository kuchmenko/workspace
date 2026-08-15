# Sync

`ws sync` is the only synchronization entry point. It is an explicit,
foreground operation: nothing watches the SQLite registry, runs on a timer,
or changes repositories in the background.

The command separates discovery from mutation:

1. Load the selected workspace from `$XDG_STATE_HOME/ws/registry.db` and build a deterministic plan.
2. Probe every unique remote endpoint without changing local state.
3. In a terminal, review and adjust the run-only selection, then confirm.
4. Freeze that selection and execute it sequentially.
5. Show live progress and a final result summary.

## Preflight

The plan contains every active project's origin and every configured project
mirror. Exact duplicate URLs become
one endpoint, and endpoints are grouped by source identity for review.
Project disk state is captured as `present`, `missing`,
`needs-migration`, or `blocked`.

Preflight probes unique endpoints with up to eight workers. Each probe is
noninteractive and has a 15-second timeout, so git cannot stop for a
credential prompt. Results distinguish success, authentication/access
failure, timeout, unreachable endpoint, unsupported URL, and cancellation.
No repository, remote URL, config file, or conflict record is mutated by
preflight.

For a failed HTTPS origin on a known provider, preflight also derives the
provider's SSH form and probes that exact repository. Conversion is
offered only when the SSH probe succeeds. Mirrors are never conversion
targets.

## Interactive Run

When both stdin and stdout are terminals, `ws sync` opens an alt-screen
TUI. The preflight view updates as probes finish. The review view groups
projects and mirrors under their sources.

- `j` / `k` or arrows move through the review.
- `space` toggles a source, project, or mirror.
- `c` selects or removes a verified HTTPS-to-SSH origin conversion.
- `enter` opens confirmation and starts the run after confirmation.
- `esc` / `q` cancels before execution.
- `ctrl+c` cancels probing or execution and waits for in-flight work to
  stop before returning.

Selections are ephemeral. Excluding a source, project, or mirror affects
only this invocation and does not write a preference to SQLite. An
inaccessible target cannot be selected unless it
has a verified SSH conversion. Excluding a project also excludes its
mirrors.

After confirmation, the plan and selection are frozen. The runner compares
the current SQLite registry before mutation. If a project's sync-relevant
fields changed since preflight, that project
is skipped with `plan-changed` rather than executing against assumptions
that are no longer true.

## Execution Order

Execution is sequential and cancellation-aware:

1. Apply selected verified project-origin conversions. Each conversion
   updates the local repository origin and SQLite; failed saves roll the
   repository origin back.
2. Process selected active projects in deterministic name order.
3. For each project, clone a missing checkout or fetch the existing bare
   repository, push selected mirrors, inspect worktrees, fast-forward a
   clean behind-only main worktree, refresh local-ahead branch activity,
   and detect deleted remote branches.
4. Save refreshed project metadata once after project processing.

`ws sync` never pushes project branches to origin. Publish those explicitly
with `ws worktree push <project> <branch>` or plain `git push`.

The execution dashboard shows the current operation, elapsed time,
completed counts, and recent results. The final summary reports successes,
failures, skips, cancellations, conflicts, and applied conversions.

## Headless Mode

If either stdin or stdout is not a terminal, `ws sync` uses deterministic,
ANSI-free text output. Headless mode has no selection prompt and is strict:
every planned endpoint must pass preflight. If one endpoint is
inaccessible, unsupported, or times out, the command reports all probe
results, exits `1`, and makes no changes. It does not automatically choose
an SSH conversion.

Only after the complete preflight succeeds does headless mode execute all
planned targets. Exit codes are:

- `0` for a successful run, including targets explicitly excluded in the
  interactive flow.
- `1` for preflight failure, execution failure, a conflict, or a
  non-selection skip.
- `130` for cancellation.

## Project Operations

For each selected active project:

- Missing main and bare paths are cloned into the bare+worktree layout.
- A plain checkout without its sibling bare repository records
  `needs-migration`; run `ws migrate <name>`.
- A blocked path records `path-blocked` and is left untouched.
- Existing bare repositories must have an origin URL matching the frozen
  plan, then fetch only that explicit origin with pruning and tags.
- Selected mirrors receive an explicit mirror push after fetch.
- A clean main worktree that is behind and not ahead pulls its explicit
  origin branch with `--ff-only`.
- A diverged main worktree records `main-divergence`; dirty main worktrees
  are left alone.
- Registered sibling worktrees with local-ahead commits refresh
  `last_active_machine` and `last_active_at`. They are not pushed.
- Previously pushed registered branches missing from origin record
  `branch-orphan`. Local-only branches without `last_pushed_at` are not
  considered orphaned.

There is no retry scheduler, cooldown, or exponential backoff. A failed
operation is reported in this run; fix the cause and invoke `ws sync`
again.

## Conflicts

Conflicts persist to `~/.local/state/ws/conflicts.json` and deduplicate on
`(workspace, project, branch, kind)`. Foreground sync records and clears
conditions as it observes them. `ws sync resolve` is the interactive
reader and mutator for manual resolution.

Current conflict kinds:

- `main-divergence`: a main worktree cannot fast-forward.
- `needs-migration`: a project is a plain checkout; run
  `ws migrate <name>`.
- `needs-bootstrap`: cloning could not determine a default branch; run
  `ws bootstrap <name>`.
- `path-blocked`: the expected project or bare path is occupied by an
  incompatible path.
- `clone-failed`: cloning a missing selected project failed.
- `branch-duplicate`: two metadata entries use the same branch name.
- `branch-orphan`: a previously published branch disappeared from origin.
- `mirror-push-failed`: pushing one configured mirror failed.

`ws sync resolve` can open the relevant shell or editor and offers
kind-specific actions. It never automatically merges or rebases project
work.

## Sidecars

`ws add`, `ws create`, `ws bootstrap`, and `ws migrate` use per-workspace
sidecars under `~/.local/state/ws/<kind>/<sha>.toml` for crash recovery and
same-command exclusion. A foreground sync checks for a live sidecar before
execution and skips rather than racing an in-progress operation. Sidecars
do not coordinate with a background process because none exists.

## Workspace Registry

Named local workspaces are stored in `$XDG_STATE_HOME/ws/registry.db` and used
by commands and the explorer, not by sync scheduling.

```sh
ws workspace create ~/dev --name personal
ws workspace import ~/work/workspace.toml --name work --root ~/work
ws workspace export personal > workspace.toml
ws workspace list
```

Names are unique and paths are canonical. TOML is explicit import/export
interchange only; no sync run reads or writes it.

## Multi-Machine Flow

Each machine creates or imports its local SQLite workspace and runs project
sync explicitly:

```sh
# Machine A
ws workspace import ./workspace.toml --name personal --root ~/dev
ws sync

# Machine B
ws workspace import ./workspace.toml --name personal --root ~/dev
ws sync
```

Registry data does not travel through `ws sync`. Project branch commits travel
only after an explicit project push. A typical handoff, once each machine has
its local workspace registry, is:

```sh
# Machine A
ws worktree push myapp feat/auth-refactor
ws sync

# Machine B
ws sync
ws worktree add myapp feat/auth-refactor
```

## Health Check

`ws doctor` checks stale bootstrap/migrate sidecars, active conflicts,
registry validity, layout, fetch refspecs, remote reachability, default
branches, worktree upstreams, and index locks.

```sh
ws doctor
ws doctor <project>
ws doctor --fix
ws doctor --json
ws doctor --skip-remote
```

Exit codes are `0` for clean, `1` for issues found, and `2` when `--fix`
applied a repair. Conflicts and index locks are never auto-fixed.
