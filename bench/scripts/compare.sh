#!/usr/bin/env bash
# Compare a fresh L1 result against the baseline for the current machine.
# Returns 0 on no-regression, 1 on regression beyond threshold.
#
# Usage: compare.sh <layer> [pr-output-file]
#   layer: L1 | L2 (L3 is advisory; this script doesn't gate L3)
#
# Exit codes (consumed by bench-pr-gate.sh):
#   0  within thresholds — gate clean, comparison happened
#   1  regression beyond threshold (and p < p_max)
#   2  unusable invocation (bad args, PR result file missing/empty)
#   3  no baseline for this machine — advisory, NOTHING was evaluated
#
# Why distinct codes: a missing baseline must NOT look like a clean run
# to the gate. Otherwise on any new machine the PR body would claim
# "✓ within thresholds" while no thresholds were actually checked.

. "$(dirname "$0")/lib.sh"
ensure_benchstat

layer="${1:-L1}"
machine="$(bench_machine)"
baseline="$BASELINE_DIR/$machine/$(echo "$layer" | tr 'A-Z' 'a-z').txt"

case "$layer" in
    L1) pr_file="${2:-$RESULTS_DIR/$machine/l1.txt}" ;;
    L2) pr_file="${2:-$RESULTS_DIR/$machine/l2.txt}" ;;
    *)
        echo "ERROR: compare.sh supports L1 or L2 (got: $layer)" >&2
        exit 2
        ;;
esac

if [ ! -s "$baseline" ]; then
    echo "→ no baseline at $baseline (first run on this machine?)"
    echo "  run: just bench-baseline   to record one"
    exit 3  # distinct from 0 (clean): caller knows nothing was evaluated
fi

if [ ! -s "$pr_file" ]; then
    echo "ERROR: PR result file $pr_file is empty or missing" >&2
    exit 2
fi

time_thresh=$(threshold_for "$layer" time_pct)
allocs_thresh=$(threshold_for "$layer" allocs_pct)
p_max=$(threshold_for "$layer" p_max)

echo "→ comparing $pr_file"
echo "  vs baseline: $baseline"
echo "  thresholds:  time=${time_thresh}%, allocs=${allocs_thresh}%, p<${p_max}"
echo

# benchstat output is a sequence of metric blocks, each with a header
# line containing one of `sec/op`, `B/op`, or `allocs/op`. Within a
# block, data rows carry a signed delta like `+5.04% (p=0.000 n=10)`
# (regression) or `-12.30% (p=0.001 n=10)` (improvement).
#
# Gate semantics:
#   - improvements (negative delta) are NEVER regressions, regardless of magnitude
#   - sec/op rows are gated by time_pct
#   - B/op and allocs/op rows are gated by allocs_pct
#   - p-value must be below p_max for any row to count as significant
diff_out="$(benchstat "$baseline" "$pr_file" 2>&1)"
echo "$diff_out"
echo

# Strict gate: any benchmark whose POSITIVE delta exceeds the
# metric-appropriate threshold AND p < p_max.
violations=$(echo "$diff_out" | awk \
    -v t="$time_thresh" -v a="$allocs_thresh" -v p_max="$p_max" '
    # Detect which metric this block reports. benchstat header lines
    # contain a metric label inside the column-divider art (`│ sec/op │`).
    # B/op and allocs/op share the allocs threshold.
    /sec\/op/    { metric = "time";   next }
    /allocs\/op/ { metric = "allocs"; next }
    /B\/op/      { metric = "allocs"; next }

    # Data rows: signed-percent delta + (p=...).
    match($0, /[+-][0-9]+(\.[0-9]+)?%/) {
        delta_str = substr($0, RSTART, RLENGTH)
        delta_num = delta_str + 0   # awk parses leading sign; sign is preserved
        # Improvements (negative delta) are never regressions.
        if (delta_num <= 0) next

        if (match($0, /p=[0-9.]+/)) {
            p_str = substr($0, RSTART+2, RLENGTH-2)
            p = p_str + 0
        } else {
            p = 1.0
        }

        name = $1
        thresh = (metric == "allocs") ? (a + 0) : (t + 0)
        # No threshold configured (== 0) means "advisory only" for this metric.
        if (thresh <= 0) next

        if (delta_num > thresh && p < p_max+0) {
            print "  ✗ " name "  Δ=" delta_str "  p=" p "  metric=" metric
            count++
        }
    }
    END { exit count > 0 ? 1 : 0 }
') && rc=0 || rc=1

if [ "$rc" -ne 0 ]; then
    echo "REGRESSION: $layer threshold violated"
    echo "$violations"
    exit 1
fi

echo "✓ $layer within thresholds"
