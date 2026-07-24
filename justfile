binary := "ws"

# Build the ws binary
build:
    GOTOOLCHAIN=auto go build -o {{binary}} ./cmd/ws

# Build and install to ~/.local/bin. Uses `install` rather than `cp` so an
# existing executable is replaced atomically.
install: build
    #!/usr/bin/env bash
    set -u
    unit="$HOME/.config/systemd/user/ws-daemon.service"
    if [[ "$(uname -s)" == "Linux" ]] && { [[ -e "$unit" ]] || { command -v systemctl >/dev/null 2>&1 && systemctl --user cat ws-daemon.service >/dev/null 2>&1; }; }; then
      cleanup_failed=0
      if command -v systemctl >/dev/null 2>&1; then
        systemctl --user disable --now ws-daemon.service >/dev/null 2>&1 || cleanup_failed=1
      else
        cleanup_failed=1
      fi
      rm -f "$unit" || cleanup_failed=1
      if command -v systemctl >/dev/null 2>&1; then
        systemctl --user daemon-reload >/dev/null 2>&1 || cleanup_failed=1
      fi
      if (( cleanup_failed )); then
        printf "Warning: could not fully retire ws-daemon.service. Before running 'ws sync', run: systemctl --user disable --now ws-daemon.service; rm -f '%s'; systemctl --user daemon-reload\n" "$unit" >&2
      else
        echo "Removed legacy ws-daemon.service"
      fi
    fi
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

# L1: per-package microbenchmarks (~30s)
bench-l1:
    bench/scripts/run-l1.sh

# L2: synthetic-workspace macrobenchmarks (~3-5min; manual / nightly)
bench-l2:
    bench/scripts/run-l2.sh

# L3: end-to-end binary scenarios via hyperfine (~10-15min; trend-only)
bench-l3:
    bench/scripts/run-l3.sh

# Refresh per-machine baseline. Run on a clean main branch after merging
# perf-related PR. Layer: l1 (default) | l2 | all
bench-baseline layer="l1":
    bench/scripts/baseline.sh {{layer}}

# Compare a layer against current baseline (advisory)
bench-compare layer="L1":
    bench/scripts/compare.sh {{layer}}
