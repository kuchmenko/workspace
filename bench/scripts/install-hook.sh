#!/usr/bin/env bash
# Optional: install a git pre-push hook that runs `just bench-pr-gate` as a
# defense-in-depth backup to the agent's AGENTS.md instruction. Idempotent.
#
# Uninstall: rm .git/hooks/pre-push (or restore your previous one).

. "$(dirname "$0")/lib.sh"

hook="$REPO_ROOT/.git/hooks/pre-push"
if [ ! -d "$REPO_ROOT/.git" ]; then
    # Possible we're inside a worktree where .git is a file; resolve.
    git_dir="$(git rev-parse --git-common-dir)"
    hook="$git_dir/hooks/pre-push"
fi

mkdir -p "$(dirname "$hook")"

if [ -e "$hook" ]; then
    if grep -q "bench-pr-gate" "$hook" 2>/dev/null; then
        echo "→ pre-push hook already installed: $hook"
        exit 0
    fi
    backup="$hook.bak.$(date +%s)"
    echo "→ existing hook found; backing up to $backup"
    mv "$hook" "$backup"
fi

cat > "$hook" <<'EOF'
#!/usr/bin/env bash
# ws perf-gate hook (installed by bench/scripts/install-hook.sh).
# Skips on:
#   - $WS_BENCH_SKIP=1   (explicit override, agent uses --bench-skip)
#   - HEAD == push base  (no commits being pushed, e.g. delete-only)
#   - non-feat/fix branch (only gate user-story branches)

if [ "${WS_BENCH_SKIP:-0}" = "1" ]; then
    exit 0
fi

# Only gate on push to a remote tracking branch from a working branch.
branch=$(git symbolic-ref --short HEAD 2>/dev/null) || exit 0
case "$branch" in
    main|master|trunk) exit 0 ;;
esac

if ! command -v just >/dev/null 2>&1; then
    echo "WARN: just not on PATH; skipping perf gate" >&2
    exit 0
fi

echo "→ pre-push: running perf gate (just bench-pr-gate)"
just bench-pr-gate
EOF

chmod +x "$hook"
echo "✓ installed: $hook"
echo "  override per-push with: WS_BENCH_SKIP=1 git push ..."
