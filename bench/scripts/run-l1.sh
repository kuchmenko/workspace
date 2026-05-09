#!/usr/bin/env bash
# Run L1 microbenchmarks (per-package go test -bench).
#
# Output: bench/results/<machine>/l1.txt — feed into compare.sh or
# baseline.sh.
#
# Knobs (env):
#   BENCH_COUNT   default 10  — minimum for benchstat p<0.05 stability
#   BENCH_TIME    default 1s  — go test -benchtime
#   BENCH_PKG     default ./internal/... ./cmd/...
#                 — overrideable for targeted runs

. "$(dirname "$0")/lib.sh"

COUNT="${BENCH_COUNT:-10}"
TIME="${BENCH_TIME:-1s}"
PKG="${BENCH_PKG:-./internal/... ./cmd/...}"

machine="$(bench_machine)"
out_dir="$RESULTS_DIR/$machine"
mkdir -p "$out_dir"
out="$out_dir/l1.txt"

echo "→ L1: count=$COUNT benchtime=$TIME pkg=$PKG"
echo "→ output: $out"

# -run=^$ skips Test* funcs entirely (only benchmarks run).
# -benchmem captures allocs/op + B/op (needed for allocs_pct gate).
# CPU pinning isn't reliably available; we accept GitHub-runner-class noise
# and rely on -count + benchstat for statistical separation.
GOTOOLCHAIN=auto go test \
    -run=^$ \
    -bench=. \
    -benchmem \
    -count="$COUNT" \
    -benchtime="$TIME" \
    -timeout=10m \
    $PKG 2>&1 | tee "$out"

echo "✓ L1 complete: $out"
