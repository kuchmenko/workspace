# Plan 001: Make GitHub canonical again

> **Executor instructions**: Follow this plan in order. Run every verification command. Stop rather than improvising when a STOP condition occurs. Do not push, create releases, change remotes, open pull requests, or alter another worktree.
>
> **Drift check**: `git rev-parse HEAD` must initially return `fec7728faf89452091012d47495670f38438c32e`. The working tree may contain only this `plans/` directory before execution.

## Status

- **Priority**: P1
- **Effort**: L
- **Risk**: HIGH
- **Depends on**: none
- **Category**: migration
- **Planned at**: commit `fec7728`, 2026-07-30
- **Repository visibility**: private

## Why this matters

GitHub `main` is 11 commits behind the current Codeberg/local `main`. The current tree intentionally identifies Codeberg as the Go module, CI host, release host, and installer download source. GitHub must become canonical without losing the current sync implementation, breaking generic Codeberg provider support, touching dirty sibling worktrees, or publishing unsigned new history.

## Current state

- Branch `chore/github-canonical` starts at `fec7728` in an isolated worktree.
- GitHub `main` is `ebd176b0194fda5949b65a7c6e1017f8c9ac8d8a` and is an ancestor of this branch.
- Commits `db77e4b`, `97b2745`, `27b0e39`, and `fec7728` are unsigned. The seven earlier commits absent from GitHub are signed.
- `v0.8.1` points to signed commit `cc63c65`, before the unsigned range, and must not be rewritten.
- `go.mod:1` is `module codeberg.org/kuchmenko/workspace`; repository self-imports use the same prefix.
- `.forgejo/workflows/` owns current CI and two conflicting release mechanisms.
- `install.sh:23-32` discovers and downloads releases from Codeberg.
- Generic Codeberg support in remote parsing, conversion, tests, and mirror examples is intentional and must remain.
- The project uses Conventional Commits, signed commits, real-git tests, and `just check` as the local quality gate.

## Commands

| Purpose | Command | Expected result |
|---|---|---|
| Tests | `GOTOOLCHAIN=auto go test -race -timeout 5m ./...` | exit 0 |
| Vet | `GOTOOLCHAIN=auto go vet ./...` | exit 0 |
| Tidy | `GOTOOLCHAIN=auto go mod tidy && git diff --exit-code go.mod go.sum` | exit 0 |
| Lint | `GOTOOLCHAIN=auto golangci-lint run --timeout=5m` | exit 0 |
| Vulnerabilities | `GOTOOLCHAIN=auto govulncheck ./...` | exit 0 |
| Build | `GOTOOLCHAIN=auto go build ./...` | exit 0 |
| Installer syntax | `sh -n install.sh` | exit 0 |
| Workflows | `GOTOOLCHAIN=auto go run github.com/rhysd/actionlint/cmd/actionlint@latest` | exit 0 |

## Scope

In scope:

- The four unsigned commits after `cc63c65`, rewritten only on `chore/github-canonical`.
- `go.mod` and Go self-imports under `cmd/`, `internal/`, and `bench/`.
- `.github/workflows/ci.yml`, `.github/workflows/ci-checks.yml`, `.github/workflows/release-please.yml`, and `.github/workflows/release-assets.yml`.
- `.github/dependabot.yml`, `release-please-config.json`, and `.release-please-manifest.json`.
- Removal of `.forgejo/workflows/` and `.goreleaser.yaml`.
- `install.sh`, `README.md`, `docs/getting-started.md`, and the release-process section of `AGENTS.md`.
- This `plans/` directory.

Out of scope:

- Benchmark baselines under `bench/baseline/`.
- Generic Codeberg provider support, parsing, tests, and mirror examples.
- Other branches, worktrees, stash, and the untracked `internal/agent/model_test.go` in the main worktree.
- Remote pushes, releases, repository settings, Codeberg visibility, registry edits, and local remote cutover.
- Adding a project-remote migration command.

## Git workflow

- Work only in `chore/github-canonical`.
- Sign every rewritten or new commit.
- Use Conventional Commit messages without attribution footers.
- Do not push or open a PR.

## Steps

### Step 1: Re-sign the unpublished unsigned range

Record the original `fec7728^{tree}`. Rewrite only the four commits after `cc63c656903cfe6b03f82e5e29eb6f9a4de3dbba`, preserving messages and signing each commit. Do not rewrite `cc63c65` or `v0.8.1`.

Verify that the rewritten branch tree equals the recorded original tree, `git diff fec7728..HEAD` is empty, GitHub `main` remains an ancestor, and all 11 commits in `github/main..HEAD` report valid signatures. Stop if signing is unavailable.

### Step 2: Restore the GitHub Go module identity

Change `go.mod` to `module github.com/kuchmenko/workspace`. Replace only repository self-import prefix `codeberg.org/kuchmenko/workspace` with `github.com/kuchmenko/workspace` in Go source and tests. Do not replace generic Codeberg URLs or examples. Run `go mod tidy`, formatting, tests, and vet. Create a signed `refactor(module): restore GitHub module path` commit.

### Step 3: Restore GitHub CI

Adapt the prior GitHub workflows from `github/main` to the current Go version and package layout. Keep one reusable check suite containing format, vet, tidy, golangci-lint console checking, race tests with coverage artifact, govulncheck in normal check mode, and Ubuntu/macOS build/install smoke. Code scanning is unavailable for this private repository, so do not add SARIF uploads or `security-events` permissions. Add the small push/PR caller. Restore weekly grouped Dependabot updates. Remove Forgejo CI only after equivalent GitHub checks exist.

### Step 4: Implement integrated Release Please publication

Seed Release Please at `0.8.1`. Add a Release Please workflow on pushes to `main`. It must expose `release_created` and `tag_name`, then call a reusable release-assets workflow in the same run when a release is created. Do not depend on a tag push caused by `GITHUB_TOKEN` starting another workflow.

The reusable release-assets workflow must support both `workflow_call` and manual `workflow_dispatch` with a tag input. It must run the reusable quality gate, build `ws-{linux,darwin}-{amd64,arm64}.tar.gz`, generate `checksums.txt`, and upload those five files to the existing GitHub release. The manual path is the retry mechanism. Remove both Forgejo release workflows and `.goreleaser.yaml`. Create a signed `ci: restore GitHub checks and releases` commit.

### Step 5: Restore GitHub installation and release documentation

Keep the GitHub repository private. Require authenticated `gh` in `install.sh`, discover the latest release through `gh api`, and download its platform asset through `gh release download`. Fail with an actionable message when `gh` is missing, unauthenticated, or lacks repository access. Preserve platform detection and daemon-service retirement. Replace anonymous raw GitHub install commands in `README.md` and `docs/getting-started.md` with authenticated `gh` commands. Update `AGENTS.md` to state that Release Please owns version, changelog, tag, and GitHub release creation, while the reusable asset workflow owns checks and binaries. Create signed Conventional Commits for the installer, documentation, and CI changes.

### Step 6: Run final verification

Run all commands in the Commands table. Confirm `git diff --check` passes. Search for canonical-host residue: `codeberg.org/kuchmenko/workspace`, `code.forgejo.org`, `GITEA_TOKEN`, `.forgejo`, and `gitea_urls` must have no matches outside history and this plan. Remaining plain `codeberg.org` matches must be generic provider behavior, tests, or examples. Confirm every commit in `github/main..HEAD` has a valid signature.

Update the plan row in `plans/README.md` to DONE only after every gate passes.

## Done criteria

- [x] The branch is a fast-forward descendant of GitHub `main`.
- [x] The original `fec7728` tree was preserved across the signature rewrite.
- [x] Every commit in `github/main..HEAD` is signed.
- [x] The module and self-imports use `github.com/kuchmenko/workspace`.
- [x] GitHub CI covers the current local quality gate without SARIF or code-scanning permissions.
- [x] Release Please invokes retryable GitHub asset publication without a PAT.
- [x] `install.sh` uses authenticated `gh` for private GitHub releases and retains daemon cleanup.
- [x] User-facing installation commands work with the private repository through authenticated `gh`.
- [x] Forgejo/Gitea canonical-host automation is removed.
- [x] All local verification commands pass.
- [x] No out-of-scope worktree or benchmark baseline changed.

## STOP conditions

- Signing is unavailable or any rewritten/new commit is not validly signed.
- Rewriting changes the tree represented by original `fec7728`.
- GitHub `main` is no longer an ancestor of the migration branch.
- Implementing release publication requires a PAT, repository secret, or repository-setting mutation.
- A generic Codeberg provider test or supported behavior would need removal.
- Any verification command fails twice after a focused correction.

## Maintenance notes

GitHub must remain private. Private code scanning and branch protection are unavailable on the current account, so CI uses text/check output without SARIF and publication review must enforce green checks manually. Installation requires authenticated `gh` access. Codeberg non-main branches, benchmark baseline refresh, historical `v0.8.1` release migration, and local origin cutover are separate operator-reviewed tasks.
