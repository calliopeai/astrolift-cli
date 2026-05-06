# astrolift-cli -- Bootstrap

## What this is

`astro` is the developer-facing CLI for the Astrolift PaaS. It ships as a single
Go binary with no runtime dependencies. Developers use it for daily operations
(login, deploy, logs, exec); CI runners use it for automated build-and-deploy
pipelines.

The full command tree and behavioral spec live in `specs/10-cli.md` in the
Astrolift metarepo.

## Stack

- **Language:** Go 1.23
- **CLI framework:** Cobra
- **Config management:** Viper (YAML config + env vars + flags)
- **API transport:** GraphQL (primary) + REST (auth, CI deploy, streaming)
- **Build:** Makefile + Goreleaser for release binaries

## Dependencies on other Astrolift components

- **astrolift-api:** The CLI is a pure client of the API. It consumes the
  GraphQL schema exported by astrolift-api. Never re-implements business logic.
- **GraphQL schema:** Generated schema lives in astrolift-api and is the
  contract. CLI queries are written against that schema.

## Directory layout

```
astrolift-cli/
  main.go                    entry point
  cmd/                       cobra command definitions
    root.go                  root command, global flags, config init
  internal/
    api/                     GraphQL + REST client
    auth/                    browser device flow login, token refresh
    config/                  config file + credentials management (~/.config/astrolift/)
    output/                  JSON / table / text output formatting
```

## Build commands

```bash
make build                   # compile the astro binary
make test                    # run all tests
make fmt                     # gofmt + goimports
make lint                    # golangci-lint
make clean                   # remove binary, clear test cache
```

The build injects the version via `-ldflags` from the nearest git tag.

## Config and credentials

Config lives at `~/.config/astrolift/config.yaml`. Credentials are stored
per-server at `~/.config/astrolift/credentials/<server-slug>.yaml` with
mode 0600. The CLI refuses to read credentials files with wider permissions.

## Conventions

- Commands return 0 on success, 2 on usage error, 1 on operational failure.
- Every command supports `--json` for machine-readable output.
- Errors go to stderr; data goes to stdout.
- No AI co-authorship messages in commits or code.
- No rebases. New commits only.
