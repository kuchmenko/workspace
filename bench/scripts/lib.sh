#!/usr/bin/env bash
# Shared helpers for bench/scripts/*. Source via `. "$(dirname "$0")/lib.sh"`.
#
# Conventions:
#  - All scripts run from repo root (they cd there).
#  - All paths are absolute under $REPO_ROOT after sourcing.
#  - Failures are loud: scripts use `set -euo pipefail`.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

BENCH_DIR="$REPO_ROOT/bench"
BASELINE_DIR="$BENCH_DIR/baseline"
RESULTS_DIR="$BENCH_DIR/results"
SCRIPTS_DIR="$BENCH_DIR/scripts"
THRESHOLDS_FILE="$BENCH_DIR/thresholds.toml"
GATE_FILE="$BENCH_DIR/GATE_ACTIVATION"

mkdir -p "$BASELINE_DIR" "$RESULTS_DIR"

# bench_machine resolves the per-machine identity used to namespace baselines.
# Order: $WS_MACHINE_NAME, ~/.config/ws/config.toml machine_name, hostname -s.
bench_machine() {
    if [ -n "${WS_MACHINE_NAME:-}" ]; then
        echo "$WS_MACHINE_NAME"
        return
    fi
    local cfg="${XDG_CONFIG_HOME:-$HOME/.config}/ws/config.toml"
    if [ -f "$cfg" ]; then
        local name
        name=$(awk -F'=' '/^[[:space:]]*machine_name[[:space:]]*=/ {
            gsub(/[[:space:]"'"'"']/, "", $2); print $2; exit
        }' "$cfg")
        if [ -n "$name" ]; then
            echo "$name"
            return
        fi
    fi
    hostname -s 2>/dev/null || hostname
}

# bench_mode prints "hard" or "soft" depending on GATE_ACTIVATION.
# - file missing or empty: soft (gate not yet activated)
# - file present, < 14 days old: soft
# - file present, >= 14 days old: hard
bench_mode() {
    if [ ! -s "$GATE_FILE" ]; then
        echo "soft"
        return
    fi
    local activated_at now elapsed
    activated_at=$(head -n 1 "$GATE_FILE" | tr -d '[:space:]')
    if ! [[ "$activated_at" =~ ^[0-9]+$ ]]; then
        echo "soft"
        return
    fi
    now=$(date +%s)
    elapsed=$((now - activated_at))
    if [ "$elapsed" -ge $((14*86400)) ]; then
        echo "hard"
    else
        echo "soft"
    fi
}

# ensure_benchstat installs golang.org/x/perf/cmd/benchstat under
# ~/go/bin if not on $PATH. Idempotent.
ensure_benchstat() {
    if command -v benchstat >/dev/null 2>&1; then
        return
    fi
    local gobin
    gobin="$(go env GOBIN)"
    [ -z "$gobin" ] && gobin="$(go env GOPATH)/bin"
    if [ -x "$gobin/benchstat" ]; then
        export PATH="$gobin:$PATH"
        return
    fi
    echo "→ installing benchstat (one-time)..." >&2
    GOFLAGS="-mod=mod" go install golang.org/x/perf/cmd/benchstat@latest >&2
    export PATH="$gobin:$PATH"
}

# require_cmd fails loudly if a binary is not on PATH. Use for tools we
# refuse to bootstrap automatically (hyperfine, perf).
require_cmd() {
    local cmd="$1"
    local hint="${2:-install it via your package manager}"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "ERROR: required command \"$cmd\" not on PATH." >&2
        echo "       $hint" >&2
        exit 127
    fi
}

# threshold_for prints the integer threshold for [LN].KEY in thresholds.toml.
# Returns 0 if the key is missing — caller must treat 0 as "advisory only".
threshold_for() {
    local layer="$1"  # L1, L2, L3
    local key="$2"    # time_pct, allocs_pct, rss_pct, p_max
    awk -v sect="[$layer]" -v k="$key" '
        $0 == sect { in_sect=1; next }
        /^\[/      { in_sect=0 }
        in_sect && $1 == k {
            for (i=1; i<=NF; i++) if ($i == "=") { print $(i+1); exit }
        }
    ' "$THRESHOLDS_FILE" 2>/dev/null || echo 0
}
