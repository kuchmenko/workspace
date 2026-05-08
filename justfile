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
