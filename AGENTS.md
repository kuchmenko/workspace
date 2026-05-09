# AGENTS.md

Instructions for AI agents (Claude Code, Cursor, OpenAI Codex, etc.) working
on this repository. Human developers can ignore everything except the
"Performance Protocol" section if they choose; agents must read all of it.

This file is the agent-facing complement to `CLAUDE.md`. `CLAUDE.md` is
project knowledge (architecture, invariants, conventions). `AGENTS.md` is
agent operating procedure (what to run, what to gate on, when to escalate).

## Performance Protocol

This project ships a tiered benchmark protocol designed to keep the binary
fast (cold-start) and resource-efficient (memory, allocations) without CI
infrastructure. The agent is the gate.

### Three tiers

- **L1 — microbenchmarks** (`just bench-l1`)
  - Per-package, `go test -bench` on the hot paths.
  - Wall: ~30-60s. Runs on every PR via `just bench-pr-gate`.
  - Detects regressions in pure Go code (allocations, hot loops, escape).

- **L2 — synthetic-workspace macrobenchmarks** (`just bench-l2`)
  - Cross-package flows on a fixture built by `internal/benchfixture`.
    e.g. `reconciler.Tick` over 50 fake projects.
  - Wall: ~3-5min. Manual (`just bench-l2`) or after architectural changes.
  - Bench files are tagged `//go:build bench_l2` so they don't run on
    `go test ./...`.

- **L3 — end-to-end binary scenarios** (`just bench-l3`)
  - `hyperfine` on the actual `ws` binary against an ephemeral workspace.
    Captures cold/warm wall, peak RSS, binary size, init() trace.
  - Wall: ~10-15min. Trend-only — never gates.

### Priority axis: CLI cold-start

This protocol was designed with CLI cold-start as the optimization
priority. Every `ws <cmd>` invocation pays init() + config.Load + cobra
dispatch; that's the most user-visible latency. Bias new bench coverage
toward functions on the cold-start path:

- `internal/config` (TOML load/save/validate)
- `internal/cli` (root command construction, status path)
- `cmd/ws/main.go` init() trace

Allocation rate matters more than raw CPU here — GC pauses inflate p99 of
a 100ms invocation visibly.

### Per-machine baselines

Without CI, each developer machine has its own baseline. Layout:

```text
bench/
  baseline/
    <machine>/        ← from ~/.config/ws/config.toml machine_name
      l1.txt
      l2.txt
      l3.json
  results/<machine>/  ← scratch; written by every run; .gitignored
  thresholds.toml     ← gate thresholds (Δ%, p_max)
  GATE_ACTIVATION     ← timestamp; hard gate engages 14d later
```

### Gate activation lifecycle

```text
day 0:                         soft mode (no gating)
just bench-gate-activate ───→  GATE_ACTIVATION written, soft for 14d
day 14:                        hard mode — gate exits non-zero on regress
```

Soft → hard transition is automatic based on the file timestamp. There is
no "switch to hard" command — only the activation point.

## Agent obligations

Read top to bottom; follow in order.

### Before opening a PR (mandatory)

1. **Run `just bench-pr-gate`.** Always. Even for "trivial" changes — TOML
   tweaks have surprised us before. The gate runs L1, compares against the
   current machine's baseline, writes `bench/results/<machine>/pr-block.md`
   with a Performance section.

2. **Read the gate's exit code:**
   - Exit 0, no regression: paste `pr-block.md` verbatim into PR body
     under `## Performance`. Proceed with `gh pr create`.
   - Exit 0, regression visible (soft mode): same as above. Mention the
     regression explicitly in the PR description's Solution section if
     it's intentional, or in `## Lessons` if a surprise.
   - Exit 1 (hard mode regression): **stop**. Either fix the regression
     or escalate to the human with the gate output. Do not proceed
     with `--bench-skip` unless the human authorized it for this PR.

3. **`--bench-skip` justification (if used):** include in the PR body a
   `## Performance` section with `bench-skip: <reason>` and a follow-up
   issue link or TODO. Skipping silently is a forbidden action.

### After merging a perf-relevant PR (mandatory)

If the PR was specifically about performance — optimization, refactor of
a hot path, dependency bump that touches reconciler or config — refresh
the baseline:

```bash
git checkout main && git pull
just bench-baseline l1            # or `all` for L1+L2
git add bench/baseline/<machine>
git commit -m "chore(bench): refresh baseline after <PR title>"
git push
```

If the PR was not perf-relevant, do nothing — baselines are sticky on
purpose.

### When adding new code on a hot path

If the new code lives in any of these packages, add at least one
microbenchmark in a `*_bench_test.go` file:

- `internal/config` — TOML load/save, validation, branch metadata helpers
- `internal/cli` — command construction, scan walk, status renderers
- `internal/conflict` — conflict store record/match
- `internal/git` — porcelain parsers, fetch logic
- `internal/clone` — bare/worktree materialize path
- `cmd/ws` — anything that affects init()

Bench naming convention: `BenchmarkFooSmall` / `FooMedium` / `FooLarge`
when the input scales meaningfully (e.g. workspace size). Otherwise just
`BenchmarkFoo`.

### When NOT to add a benchmark

- TUI render code (bubbletea Update loops) — human-timescale, irrelevant
- One-shot interactive commands (setup, auth) — not on hot paths
- Test-only helpers — defeats the point

### Threshold violations — diagnostic playbook

When `bench-pr-gate` reports a regression:

1. Re-run with higher count to rule out noise:
   `BENCH_COUNT=20 just bench-pr-gate`
2. Profile the regressed function:
   `go test -bench=BenchmarkX -cpuprofile=cpu.out -memprofile=mem.out -benchtime=10s ./internal/<pkg>`
   `go tool pprof -http=: cpu.out`  → flame graph
3. If alloc-bound: `go tool pprof -alloc_objects -http=: mem.out`
4. Common causes for cold-start regressions:
   - new package imported on init() path — check `GODEBUG=inittrace=1`
   - TOML schema added a field with non-zero default → larger Marshal
   - new validation rule with O(N²) scan instead of map lookup
   - regex compiled inside loop instead of `var pattern = regexp.MustCompile`

### When in doubt

Do not invent. Ask the human:
- "Is this regression intentional?" — when the change is functional and
  the perf hit looks justified.
- "Refresh baseline now?" — when uncertain whether the PR is
  perf-relevant.
- "Activate gate now?" — when the project hasn't activated yet but the
  PR is foundational enough that establishing a baseline matters.

## Other agent conventions

- Use `ws` for workspace operations: `ws status`, `ws sync`,
  `ws sync resolve`, `ws worktree add/list/push/rm`.
- Start non-trivial work with `ws worktree add workspace <type>/<kebab-topic>`.
  Do not branch inside the main worktree.
- Branch names are literal user input, but must follow conventional form:
  `<type>/<kebab-topic>`.
- Allowed branch/commit/PR types: `feat`, `fix`, `chore`, `docs`, `refactor`,
  `test`, `ci`, `style`, `perf`.
- Commit messages and PR titles must be Conventional Commits:
  `<type>(<scope>): <imperative result-oriented description>`.
- Never create new `wt/<machine>/*` branches; they are legacy compatibility only.
- Never use stale `ws worktree new`, `ws worktree promote`, `--auto-push`,
  or `autopush.branches` guidance.
- PRs: open as **draft** by default (`gh pr create --draft`). Only the
  human flips to ready.
- No `Co-Authored-By` footers in commits.
- See `CLAUDE.md` for the full project conventions.
