#!/usr/bin/env bash
# Run L2 macrobenchmarks against synthetic workspaces.
#
# These exercise cross-package flows (reconciler.Tick, scan, validate) on
# fixtures built by internal/benchfixture. Bench files are tagged with
# `//go:build bench_l2` so they don't run on `go test ./...`.

. "$(dirname "$0")/lib.sh"

COUNT="${BENCH_COUNT:-5}"
TIME="${BENCH_TIME:-3s}"

machine="$(bench_machine)"
out_dir="$RESULTS_DIR/$machine"
mkdir -p "$out_dir"
out="$out_dir/l2.txt"

echo "→ L2: count=$COUNT benchtime=$TIME (synthetic workspace fixtures)"
echo "→ output: $out"

GOTOOLCHAIN=auto go test \
    -tags=bench_l2 \
    -run=^$ \
    -bench=. \
    -benchmem \
    -count="$COUNT" \
    -benchtime="$TIME" \
    -timeout=20m \
    ./internal/benchfixture/... 2>&1 | tee "$out"

echo "✓ L2 complete: $out"
