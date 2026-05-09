#!/usr/bin/env bash
# Run L3 end-to-end binary scenarios via hyperfine + /usr/bin/time.
#
# This builds an isolated binary (under bench/.bin/ws), constructs a
# disposable workspace, then exercises the actual subprocess for cold/warm
# timings, peak RSS, and binary size. Trend-only (no gating).
#
# Output: bench/results/<machine>/l3.json — hyperfine native format,
# easy to diff with `jq`.

. "$(dirname "$0")/lib.sh"
require_cmd hyperfine "install: cargo install hyperfine  OR  apt install hyperfine"

machine="$(bench_machine)"
out_dir="$RESULTS_DIR/$machine"
mkdir -p "$out_dir"

bin_dir="$BENCH_DIR/.bin"
bin="$bin_dir/ws"
mkdir -p "$bin_dir"

scratch="$(mktemp -d -t ws-bench-l3.XXXXXX)"
trap 'rm -rf "$scratch"' EXIT

echo "→ L3: building isolated binary (no PGO, -trimpath, -s -w)"
GOTOOLCHAIN=auto go build \
    -trimpath \
    -ldflags="-s -w" \
    -o "$bin" \
    ./cmd/ws

# Binary size — JSON for easy delta'ing.
size_bytes=$(stat -c%s "$bin" 2>/dev/null || stat -f%z "$bin")

# Build a synthetic workspace via the harness binary so cold-start has
# realistic config to load. Falls back to "empty workspace" if the
# harness isn't built (first-run case).
WS_DIR="$scratch/ws"
mkdir -p "$WS_DIR"
cat > "$WS_DIR/workspace.toml" <<EOF
# minimal synthetic workspace for L3 cold-start measurement
EOF

export HOME="$scratch/home"
export XDG_STATE_HOME="$scratch/state"
export XDG_CONFIG_HOME="$scratch/config"
mkdir -p "$HOME" "$XDG_STATE_HOME" "$XDG_CONFIG_HOME/ws"
cat > "$XDG_CONFIG_HOME/ws/config.toml" <<EOF
machine_name = "$machine"
EOF

echo "→ L3: running hyperfine (cold-start scenarios)"
# Note: `ws --version` is intentionally not implemented yet (no cobra
# Version field set). We probe the existing public surface that cobra
# guarantees exits 0: --help and the known subcommand `docs --help`.
# Adding a real `ws version` later is fine; the bench just adds a line.
#
# No `|| true` — if hyperfine fails (build broke, command non-zero exit,
# can't open the JSON) we want the script to fail loudly so the
# developer sees it, not silently produce a stale JSON.
hyperfine \
    --warmup 3 \
    --runs 30 \
    --export-json "$out_dir/l3.json" \
    --command-name "ws-help"                "$bin --help" \
    --command-name "ws-status (empty ws)"   "WS_ROOT=$WS_DIR $bin status" \
    --command-name "ws-docs-help"           "$bin docs --help"

# Peak RSS — capability-detect the verbose-time tool. GNU `time -v` lives
# at /usr/bin/time on Linux; macOS ships BSD time without -v but Homebrew
# coreutils provides `gtime`. If neither is available we record a
# sentinel so the file is well-formed and downstream tooling doesn't have
# to special-case absence.
rss_log="$out_dir/l3-rss.txt"
if /usr/bin/time -v true >/dev/null 2>&1; then
    time_cmd=(/usr/bin/time -v)
elif command -v gtime >/dev/null 2>&1 && gtime -v true >/dev/null 2>&1; then
    time_cmd=(gtime -v)
else
    time_cmd=()
    echo "WARN: no GNU time(1) available — RSS metrics will be marked unavailable" >&2
fi

# Single-descriptor write to $rss_log: truncate once up front, then the
# block and the inner `time -v` BOTH append. Otherwise the outer block
# (`> "$rss_log"`) holds an offset-tracking fd that overwrites whatever
# `time -v` appended via its own append-mode fd — corrupting the log.
: > "$rss_log"
{
    echo "# binary_size_bytes=$size_bytes"
    echo "# binary=$bin"
    for cmd in "--help" "status" "docs --help"; do
        echo "## ws $cmd"
        if [ "${#time_cmd[@]}" -gt 0 ]; then
            # shellcheck disable=SC2086
            WS_ROOT="$WS_DIR" "${time_cmd[@]}" "$bin" $cmd >/dev/null 2>>"$rss_log"
        else
            echo "(rss unavailable: no GNU time on this host)"
        fi
        echo
    done
} >> "$rss_log"

# init() trace — cheap signal for cold-start regressions in package init.
# Use `--help` because it's the cheapest non-erroring path that exercises
# every package init (cobra walks the full command tree to render help).
init_log="$out_dir/l3-inittrace.txt"
GODEBUG=inittrace=1 "$bin" --help 2>&1 \
    | grep "^init " \
    | sort -k5 -n -r \
    > "$init_log" || true

echo "✓ L3 complete:"
echo "    timing: $out_dir/l3.json"
echo "    RSS:    $out_dir/l3-rss.txt"
echo "    init:   $out_dir/l3-inittrace.txt"
echo "    size:   $size_bytes bytes"
