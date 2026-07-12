# astrolift-cli

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-%3E%3D1.23-00ADD8.svg)](https://go.dev/)

`astro` is the command-line client for the
[Astrolift](https://astrolift.app) PaaS. App developers use it to register
apps, deploy, manage secrets, and inspect runtime state. Operators use it to
register clusters and configure providers against an Astrolift control plane.

> **Status posture.** This CLI is early — pre-1.0 and only a handful of
> commits in. The command tree is structured (every group has its
> subcommands wired in Cobra) but several command bodies still surface
> `not yet wired to the API` until the matching backend resolvers
> ship. The wired surfaces today are: `astro server *`, `astro auth *`,
> `astro app` (`init`, `register`, `deploy`, `list`, `show`, `logs`,
> `exec`, `rollback`, `promote`), `astro exec`, `astro agent *`,
> `astro workflow` (`init`, `validate`, `pull`), `astro ci
> deploy` / `astro ci status`, `astro version-check` / `astro self-update`,
> `astro version`, `astro docs`, and `astro cluster bootstrap`. The `astro
> app` sub-resource groups (secrets, services, domains, …) are still
> scaffolded. Subcommand surface, flag names, and exit codes may shift
> before 1.0 — pin a tagged release in CI rather than tracking `main`.

---

## Install

### Homebrew (macOS, Linux)

```bash
brew install calliopeai/tap/astro
```

(Tap published by [GoReleaser](.goreleaser.yaml) on each tagged release.)

### Scoop (Windows)

```powershell
scoop bucket add calliopeai https://github.com/calliopeai/scoop-bucket
scoop install astro
```

### `curl | sh` installer

```bash
curl -fsSL https://astrolift.app/cli/install.sh | sh
```

The installer detects your OS / arch, fetches the matching tarball from
[GitHub Releases](https://github.com/calliopeai/astrolift-cli/releases),
and drops `astro` into `/usr/local/bin` (or `~/.local/bin` if the system
dir isn't writable). See [`scripts/install.sh`](scripts/install.sh).

### Direct binary download

Grab the platform-appropriate archive from
[Releases](https://github.com/calliopeai/astrolift-cli/releases), extract
the `astro` binary, and place it on your `PATH`.

### Docker

```bash
docker pull ghcr.io/calliopeai/astro:latest
docker run --rm -v "${HOME}/.config/astrolift:/home/nonroot/.config/astrolift" \
    ghcr.io/calliopeai/astro:latest version
```

The image is `gcr.io/distroless/static-debian12:nonroot`; mount your
config directory for stateful commands.

### From source

```bash
git clone https://github.com/calliopeai/astrolift-cli
cd astrolift-cli
make build              # produces ./astro
./astro version
```

Or, with `go install`:

```bash
go install github.com/calliopeai/astrolift-cli@latest
```

(Note: the Go module path is `github.com/calliopeai/astrolift-cli`; the
GitHub repo is `calliopeai/astrolift-cli`. The two are intentional — the
module path will follow the public repo on the next major.)

---

## Quick start

### As an app developer

```bash
# 1. Tell the CLI where your platform lives
astro server add prod https://api.astrolift.example.com
astro auth login

# 2. Scaffold an app manifest in your project
cd my-service/
astro app init                  # writes astrolift.toml

# 3. Register and deploy
astro app register --project-id <project-uuid> --source-repo myorg/my-service
astro app deploy --image-tag sha-deadbeef --wait   # --env defaults to production

# 4. Watch it run
astro app list                  # apps in the org
astro app show                  # status, source, workloads, environments
astro app logs                  # recent logs (-f to follow, [workload] to narrow)
astro app events                # platform events
astro app exec -- bash          # interactive shell (wraps `astro exec`)
astro exec --app web -- ps aux  # one-off command in a running pod
# astro app rollback            # roll back the running deployment
```

### From CI

```bash
# Required env (deploy tokens issued via `astro app tokens`)
export ASTROLIFT_API_URL="https://api.astrolift.example.com"
export ASTROLIFT_DEPLOY_TOKEN="$ASTRO_TOKEN"
export ASTROLIFT_APP_SLUG="my-service"
export ASTROLIFT_IMAGE_TAGS='{"web":"sha-deadbeef"}'

astro ci deploy                 # enqueues + polls until terminal state
# Exit codes: 0 success, 1 deploy failure, 2 config error
```

`ci deploy` defaults to polling. Pass `--no-wait` for fire-and-forget,
or set `ASTROLIFT_DEPLOY_TIMEOUT` (Go duration) to override the
30-minute polling cap.

### As an operator

Operator commands configure the platform itself rather than tenant
apps — cluster CRUD, provider plugin management, federation:

```bash
astro operator cluster ...
astro operator provider ...
astro operator federation ...   # cross-install federation
```

(Operator subcommands are admin-gated and currently scaffolded — see
the status note above.)

---

## Commands

| Group | Purpose |
|---|---|
| `astro server` | Manage Astrolift installs the CLI knows about (add / list / use / remove). One install = one DNS zone + database. |
| `astro auth` | Browser device-flow login, logout, status, refresh. |
| `astro app` | App lifecycle (`init`, `register`, `deploy`, `list`, `show`, `logs`, `exec`, `rollback`, `promote`) plus sub-resources (secrets, services, domains, tokens, members, jobs, events, audit, previews). `deploy` needs `--image-tag`; `--wait` polls to a terminal state. `app exec` wraps `astro exec` scoped to the app. `promote --from <env> --to <env>` moves an env's running deployment (image+config) to another via `promoteDeployment`. |
| `astro exec` | Run a command or interactive shell in a running container (`--app <slug>` [`--workload`/`--pod`/`-c`] `-- <cmd>`). Streams over the exec WebSocket relay; requires `app.exec_pod`; every session is audited. |
| `astro agent` | Agent dispatch (`run`, `ls`, `logs`, `cancel`, `inspect`). |
| `astro workflow` | Author + round-trip the declarative workflow manifest TOML (spec 40 §5.4). `init [--pattern chained] [-o workflow.toml]` scaffolds a starter manifest (client-side; patterns: chained, single, review_loop, fan_out, supervisor_worker). `validate <file.toml>` checks shape locally; `--server` runs authoritative validation via `previewWorkflowManifest`. `pull <slug> [--org <id>] [-o file]` exports a definition's TOML (org ∪ global catalogue) via `exportWorkflowManifest` — the way to start from a global like `ooda`/`rasd`. All server ops are `WORKFLOW_READ`-gated server-side. `push`/`register` is deferred with repo-registration (§8). |
| `astro ci` | CI-mode commands (`deploy`, `status`, `render`) — no interactive prompts; reads token + slug from env. `render` prints the manifests the platform would apply (`astroliftRenderedManifest`) for pre-merge review. |
| `astro org` / `astro team` / `astro project` | Org-scoped resource management. `org list`/`org show`, `team list`/`team create`, `project list`/`project create` (`project create` needs `--team <slug>`; both creates take `--name`/`--description`). |
| `astro operator` | Operator (admin) cluster, provider, and federation management. |
| `astro cluster bootstrap` | One-shot helm install of the `astrolift-prereqs` chart (cert-manager, ingress, storage, external-dns) against a registered cluster; the bundled chart + per-cloud values are vendored into the binary. |
| `astro scm` / `astro alert` | `scm list` (configured source-control connections) and `alert list` (alert rules; `--all` includes inactive). |
| `astro status` | Platform status snapshot (`astroliftServerInfo`: version, install identity, region, server time, capabilities). |
| `astro docs` | Open the platform docs in your browser. |
| `astro version-check` / `astro self-update` | Server-aware compatibility check + upgrade pointer. |
| `astro version` | Print the CLI version (set at build time via `-ldflags`). |

Every command supports `--json` for machine-readable output, plus
`--api-url`, `--token`, `--org`, `--team`, `--project`, `--app`,
`--no-color`, `--no-prompt`, and `--debug` as global flags. Errors go
to stderr; data goes to stdout.

Run `astro <command> --help` for the full flag set; the help text is
the source of truth.

---

## Configuration + credentials

The CLI stores its state under `~/.config/astrolift/`:

```
~/.config/astrolift/
  config.yaml                  servers, current server, output prefs
  credentials/
    <server-slug>.yaml         per-server tokens, mode 0600
```

The CLI **refuses to read credentials files with permissions wider
than `0600`**. Don't loosen them.

Environment variables take precedence over the config file (CI mode):

| Var | Used by |
|---|---|
| `ASTROLIFT_API_URL` | overrides `current_server`'s API URL |
| `ASTROLIFT_DEPLOY_TOKEN` | bypasses stored credentials (CI tokens) |
| `ASTROLIFT_APP_SLUG` | required by `astro ci deploy` + `astro ci render` |
| `ASTROLIFT_IMAGE_TAGS` | required by `astro ci deploy` (JSON map workload→tag) |
| `ASTROLIFT_IMAGE_TAG` | optional, `astro ci render` (single tag to render against) |
| `ASTROLIFT_ENVIRONMENT` | optional, default `production` |
| `ASTROLIFT_BRANCH` | optional, default `main` |
| `ASTROLIFT_COMMIT_SHA` | optional, falls back to `git rev-parse HEAD` |
| `ASTROLIFT_IDEMPOTENCY_KEY` | optional, suppresses duplicate deploys |
| `ASTROLIFT_DEPLOY_TIMEOUT` | optional, Go duration; default 30m |

---

## How this fits in the Astrolift project

Astrolift is split across a handful of repos. This is the **client
side**:

- **astrolift-cli** (this repo) — `astro` developer + operator CLI
- **[astrolift-opscode](https://github.com/calliopeai/astrolift-opscode)**
  — Terraform + Helm IaC for installing the platform on a cloud you
  control (AWS / GCP / Azure / vanilla k8s)
- **astrolift platform / API** — the control plane the CLI talks to;
  see [astrolift.app](https://astrolift.app) and the public docs (once
  published)

The CLI is a pure client of the platform API — it never re-implements
business logic. Backend policy lives behind the GraphQL + REST surface;
the CLI's job is to pack arguments, call the API, and render results.

---

## Building + testing

```bash
make build              # compile ./astro with version ldflag
make test               # go test ./... -v -count=1
make fmt                # gofmt + goimports
make lint               # golangci-lint run ./...
make clean              # remove ./astro, clear test cache
```

The build injects `Version` via `-ldflags` from the nearest git tag
(`git describe --tags --always --dirty`). For a tagged release build
locally:

```bash
goreleaser release --snapshot --clean
```

CI runs the same on tags, plus produces Homebrew tap + Scoop bucket
updates and a Docker image at `ghcr.io/calliopeai/astro`.

---

## Contributing

PRs welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the dev loop,
style requirements, and PR conventions.

Issues: please file against
[github.com/calliopeai/astrolift-cli/issues](https://github.com/calliopeai/astrolift-cli/issues).
For security reports, see [SECURITY.md](SECURITY.md) — do not open a
public issue for vulnerabilities.

Community standards: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

---

## Documentation

- **[bootstrap.md](bootstrap.md)** — stack, directory layout,
  conventions, build commands, config + credentials model
- **[CONTRIBUTING.md](CONTRIBUTING.md)** — fork + PR flow + style
- **[SECURITY.md](SECURITY.md)** — vulnerability disclosure
- **[CLAUDE.md](CLAUDE.md)** / **[AGENTS.md](AGENTS.md)** /
  **[CODEX.md](CODEX.md)** — agent shims
  (all point at `bootstrap.md`)
- **[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)** — community standards
- **[LICENSE](LICENSE)** — MIT

---

## License

MIT. See [LICENSE](LICENSE).

Copyright (c) 2026 Calliope Labs Inc. All Rights Reserved. Calliope AI is a trademark of Calliope Labs Inc.

Portions of the framework underlying this repo are derived from **[boilerworks](https://github.com/ConflictHQ/boilerworks)** (Copyright (c) Conflict LLC, MIT-licensed). Tip of the hat 🎩
