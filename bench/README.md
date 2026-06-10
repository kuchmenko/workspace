# Performance Benchmark Protocol

Tooling for keeping `ws` fast (cold-start) and resource-efficient
(memory, allocations) without CI infrastructure.

Full protocol: `AGENTS.md` → "Performance Protocol".

## Quick reference

```bash
just bench-l1           # L1 microbenchmarks (~30s)
just bench-l2           # L2 macrobenchmarks (~3-5min)
just bench-l3           # L3 E2E hyperfine (~10-15min, requires hyperfine)
just bench-baseline     # refresh per-machine baseline (after merge to main)
just bench-compare      # compare a layer against the baseline (advisory)
```

## Layout

```text
bench/
  thresholds.toml       comparison thresholds (Δ%, p_max) per layer
  baseline/<machine>/   per-machine canonical numbers (committed)
  results/<machine>/    scratch results (.gitignored)
  scripts/              shell entry points
  README.md             this file
```

## Why local-only

Asahi-class machines can't run a stable shared CI runner without an
ARM64-x86 cross-arch matrix. Per-machine baselines keep numbers stable
within a developer's loop. Comparisons are advisory — a human workflow,
not a gate.

## Adding a new benchmark

L1 (pure Go, no subprocess):
- Drop `*_bench_test.go` into the relevant `internal/<pkg>/`.
- Use `b.ReportAllocs()` + `b.ResetTimer()` after fixture setup.
- Name as `BenchmarkFoo_Small` / `Medium` / `Large` if input scales.

L2 (cross-package on synthetic workspace):
- Drop `*_bench_test.go` into `bench/benchfixture/` with the
  `//go:build bench_l2` tag.
- Use `benchfixture.Build(b, opts)` to spin up a workspace.
- Each iteration must re-use the fixture; setup goes before
  `b.ResetTimer()`.

L3 (E2E):
- Add a hyperfine command-line to `bench/scripts/run-l3.sh`.
- Trend-only — never adds gating.
