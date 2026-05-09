binary := "ws"

# Build the ws binary
build:
    GOTOOLCHAIN=auto go build -o {{binary}} ./cmd/ws

# Build and install to ~/.local/bin. Uses `install` (not `cp`) so it
# atomically unlinks any running binary first — needed when the daemon
# holds ~/.local/bin/ws open and `cp` would fail with "Text file busy".
install: build
    install -m 755 {{binary}} ~/.local/bin/{{binary}}

# Remove built binary
clean:
    rm -f {{binary}}

# Build and run with args
run *args: build
    ./{{binary}} {{args}}

# Run every check the CI quality gate runs (mirrors
# .github/workflows/ci-checks.yml). Useful before pushing a PR so
# you don't ping reviewers on a red CI run.
check: fmt vet tidy lint test vuln

# gofmt -l and fail if any file is non-empty.
fmt:
    #!/usr/bin/env bash
    set -eo pipefail
    out=$(gofmt -l .)
    if [[ -n "$out" ]]; then
      echo "gofmt found unformatted files:" >&2
      echo "$out" | sed 's/^/  /' >&2
      exit 1
    fi

# go vet on every package.
vet:
    GOTOOLCHAIN=auto go vet ./...

# go.mod / go.sum match `go mod tidy` output.
tidy:
    #!/usr/bin/env bash
    set -eo pipefail
    GOTOOLCHAIN=auto go mod tidy
    if ! git diff --exit-code go.mod go.sum; then
      echo "go.mod / go.sum diverged from \`go mod tidy\`. Commit the diff." >&2
      exit 1
    fi

# Tier-1 golangci-lint pass per .golangci.yml.
lint:
    GOTOOLCHAIN=auto golangci-lint run --timeout=5m

# go test -race -coverprofile, mirroring CI.
test:
    GOTOOLCHAIN=auto go test -race -timeout 5m -covermode=atomic -coverprofile=coverage.out ./...
    @echo "→ coverage report: go tool cover -html=coverage.out"

# govulncheck across the whole module.
vuln:
    GOTOOLCHAIN=auto govulncheck ./...

# ─── Performance Benchmark Protocol ─────────────────────────────────────────
# See AGENTS.md "Performance Protocol" for the full contract.

# L1: per-package microbenchmarks (~30s; runs every PR via bench-pr-gate)
bench-l1:
    bench/scripts/run-l1.sh

# L2: synthetic-workspace macrobenchmarks (~3-5min; manual / nightly)
bench-l2:
    bench/scripts/run-l2.sh

# L3: end-to-end binary scenarios via hyperfine (~10-15min; trend-only)
bench-l3:
    bench/scripts/run-l3.sh

# Run L1 + compare vs baseline; emits PR-body block. AGENT MUST run this
# before `gh pr create`. In hard mode (>=14d after activation) exits non-zero
# on regression.
bench-pr-gate:
    bench/scripts/bench-pr-gate.sh

# Refresh per-machine baseline. Run on a clean main branch after merging
# perf-related PR. Layer: l1 (default) | l2 | all
bench-baseline layer="l1":
    bench/scripts/baseline.sh {{layer}}

# Activate the soft→hard gate countdown. Writes current Unix timestamp to
# bench/GATE_ACTIVATION; hard gate engages 14 days later.
bench-gate-activate:
    @date +%s > bench/GATE_ACTIVATION
    @echo "✓ gate activation timestamp: $(cat bench/GATE_ACTIVATION)"
    @echo "  hard gate engages: $(date -d "@$(cat bench/GATE_ACTIVATION)" -d "+14 days" 2>/dev/null || date -r $(($(cat bench/GATE_ACTIVATION) + 14*86400)))"
    @echo "  commit bench/GATE_ACTIVATION to make active project-wide"

# Install pre-push git hook as defense-in-depth backup gate.
bench-install-hook:
    bench/scripts/install-hook.sh

# Compare a layer against current baseline (advisory; gate uses this internally)
bench-compare layer="L1":
    bench/scripts/compare.sh {{layer}}
