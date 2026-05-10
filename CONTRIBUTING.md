# Contributing to the Astrolift CLI

Thank you for your interest in contributing!

## Getting Started

1. Fork the repository
2. Clone your fork
3. Read [`bootstrap.md`](bootstrap.md) for the CLI's stack, layout, and
   conventions
4. Create a feature branch from `main`

## Prerequisites

- Go 1.23 or newer
- `golangci-lint` (optional but recommended for local lint runs)
- `goreleaser` (only needed to test release builds)

## Development Process

1. Pick an issue from the project board (or open one to discuss your idea
   before writing code)
2. Comment your plan on the issue before starting
3. Create a branch: `feature/issue-number-short-desc` or
   `fix/issue-number-short-desc`
4. Make your changes following `bootstrap.md` conventions
5. Run `make fmt`, `make lint`, and `make test` before committing
6. Run `go build ./...` and `go vet ./...` — both must pass
7. Open a pull request linking the issue

## Build Commands

```bash
make build         # compile the astro binary (with version ldflag)
make test          # go test ./... -v -count=1
make fmt           # gofmt + goimports
make lint          # golangci-lint run ./...
make clean         # remove the binary, clear test cache
```

## Code Style

- `gofmt` + `goimports` formatting (run `make fmt`)
- `go vet ./...` clean
- `golangci-lint run ./...` clean (matches CI)
- Cobra commands: one resource group per file under `cmd/`; helpers in
  `cmd/helpers.go`; command bodies follow the pattern
  *load active client → call API → render via `internal/output`*
- Use `internal/api` for all platform calls; never hand-roll HTTP in
  command bodies
- Errors: wrap with `%w`; surface with `cmd.PrintErrln` or by returning
  from `RunE`. Don't `os.Exit` inside a command — return the error
- Output: data to stdout, errors to stderr; every command supports
  `--json` (use `internal/output`)
- Exit codes: `0` success, `1` operational failure, `2` usage / config
  error (see `cmd/ci.go` `configErr` for the pattern)

## Testing

- `go test ./...` must pass
- New commands need at least one unit test for argument parsing and
  output formatting
- Tests against the API client should use a stubbed `http.Handler` —
  do not hit a live platform from tests

## Commit + PR Conventions

- Conventional Commits style (`feat:`, `fix:`, `chore:`, `docs:`)
- **No co-author / AI attribution trailers** in commit messages
- **No `git rebase`** — new commits only. Squash via the GitHub merge
  UI if needed.
- Keep PRs focused: one issue per PR, no incidental refactors
- Don't update `README.md` command listings without also updating the
  underlying `cmd/*.go` so help text and docs stay in sync
- Never push to `main` directly except via merged PR; never
  `git push --force` to `main`

## Questions?

Open an issue or start a discussion in this repository.
