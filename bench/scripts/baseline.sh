#!/usr/bin/env bash
# Refresh per-machine baselines after merging perf-related work to main.
#
# Usage: baseline.sh [layer]
#   layer: l1 (default) | l2 | all
#
# Run from a clean main branch. Records numbers under
# bench/baseline/<machine>/.  Commit the result so future PRs compare
# against the new baseline.

. "$(dirname "$0")/lib.sh"

layer="${1:-l1}"
machine="$(bench_machine)"
mkdir -p "$BASELINE_DIR/$machine"

case "$layer" in
    l1|all)
        echo "→ recording L1 baseline for machine=$machine"
        BENCH_COUNT="${BENCH_COUNT:-15}" "$SCRIPTS_DIR/run-l1.sh"
        cp "$RESULTS_DIR/$machine/l1.txt" "$BASELINE_DIR/$machine/l1.txt"
        echo "  wrote $BASELINE_DIR/$machine/l1.txt"
        ;;
esac

case "$layer" in
    l2|all)
        echo "→ recording L2 baseline for machine=$machine"
        BENCH_COUNT="${BENCH_COUNT:-8}" "$SCRIPTS_DIR/run-l2.sh"
        cp "$RESULTS_DIR/$machine/l2.txt" "$BASELINE_DIR/$machine/l2.txt"
        echo "  wrote $BASELINE_DIR/$machine/l2.txt"
        ;;
esac

if [ "$layer" != "l1" ] && [ "$layer" != "l2" ] && [ "$layer" != "all" ]; then
    echo "ERROR: unknown layer '$layer' (use l1, l2, all)" >&2
    exit 2
fi

echo
echo "✓ baseline refreshed. Commit with:"
echo "  git add bench/baseline/$machine"
echo "  git commit -m \"chore(bench): refresh $layer baseline for $machine\""
