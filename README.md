# astrolift-cli

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-%3E%3D1.23-00ADD8.svg)](https://go.dev/)

`astro` is the command-line client for the
[Astrolift](https://astrolift.ai) PaaS. App developers use it to register
apps, deploy, manage secrets, and inspect runtime state. Operators use it to
register clusters and configure providers against an Astrolift control plane.

> **Status posture.** The CLI is pre-1.0. Pin a tagged release in CI rather
> than tracking `main`; command and API surfaces may still evolve before 1.0.
> Run `astro <command> --help` or `astro docs show cli` for the exact surface
> shipped by your installed release.

---

## Install

> **The binary is behind a token.** `calliopeai/astrolift-cli` is a private
> repository, so release archives cannot be downloaded anonymously — GitHub
> answers anonymous requests for a private repo with **404**, not 401, so an
> unauthenticated attempt looks like a missing file rather than an auth error.
> Homebrew, Scoop, anonymous `curl`, and `go install` cannot work until a
> public binary channel exists. **The container image is public and needs no
> credentials.** There is no `curl | sh` one-liner; any URL you may have seen
> for one does not resolve.

### Docker (no credentials required)

```bash
docker run --rm calliopeai/astrolift-cli:latest version
```

Both registries carry the same multi-arch image and are publicly pullable:

```bash
docker pull calliopeai/astrolift-cli:latest        # Docker Hub
docker pull ghcr.io/calliopeai/astrolift-cli:latest # GHCR
```

For stateful commands, mount your config directory:

```bash
docker run --rm -it \
    -v "${HOME}/.config/astrolift:/home/nonroot/.config/astrolift" \
    calliopeai/astrolift-cli:latest app list
```

The image is `gcr.io/distroless/static-debian12:nonroot`. To copy the binary
out into another image, pin the version:

```dockerfile
FROM calliopeai/astrolift-cli:0.3.0 AS astro-cli
COPY --from=astro-cli /usr/local/bin/astro /usr/local/bin/astro
```

### Installer script (needs a GitHub credential)

```bash
gh repo clone calliopeai/astrolift-cli
cd astrolift-cli
./scripts/install.sh
```

The installer detects your OS and architecture, downloads the matching
archive, **verifies it against `astro-checksums.txt`**, and installs `astro`
into `/usr/local/bin` or `~/.local/bin`. It authenticates in this order:

1. `ASTRO_INSTALL_BASE_URL` — a mirror you control, or an offline fixture
2. an authenticated `gh` (whatever `gh auth login` is already using)
3. `GITHUB_TOKEN` / `GH_TOKEN` / `ASTRO_GITHUB_TOKEN` — for containers and CI
   images that have no `gh` binary:

```bash
GITHUB_TOKEN="$(gh auth token)" ./scripts/install.sh
```

Set `ASTRO_INSTALL_TAG=vX.Y.Z` to pin a release, `ASTRO_INSTALL_DIR` to choose
the destination.

### Manual release download

```bash
gh auth login
tag="$(gh release view --repo calliopeai/astrolift-cli --json tagName --jq .tagName)"
gh release download "$tag" --repo calliopeai/astrolift-cli \
  --pattern 'astro-darwin-arm64.tar.gz' \
  --pattern 'astro-checksums.txt'
```

Archives are named `astro-<os>-<arch>.tar.gz` (`.zip` on Windows) for
`darwin`/`linux`/`windows` and `amd64`/`arm64`. Verify against
`astro-checksums.txt`, extract `astro`, and put it on `PATH`. See the
[CLI install reference](https://astrolift.dev/reference/cli/#install) for the
full matrix.

### Staying current

`astro version` prints the release, commit, and build date it was built from;
an unstamped source build reports `astro dev`. `astro update` replaces the
running binary with the latest release (it needs the same token as above).
Once a day `astro` will note on stderr if a newer release exists; set
`ASTROLIFT_NO_UPDATE_CHECK=1` to silence it. The hint never appears with
`--json`, in CI, or on `dev` builds.

### From source (repository collaborators)

```bash
gh auth login
gh repo clone calliopeai/astrolift-cli
cd astrolift-cli
make build              # produces ./astro
./astro version
```

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
| `astro auth` | Browser device-flow login, logout, status, refresh, plus `wait` for the relay path. `login --no-wait` starts the flow, reports the session (`--json` makes it an object), and exits instead of blocking for fifteen minutes; `auth wait --session-id <id>` finishes it once a human has approved. That is how a browserless caller hands a login off. `login --no-browser` waits without launching a browser. |
| `astro app` | App lifecycle (`init`, `register`, `deploy`, `list`, `show`, `logs`, `exec`, `pods`, `rollback`, `promote`) plus sub-resources (secrets, services, domains, tokens, members, jobs, events, audit, previews). `deploy` needs `--image-tag`; `--wait` polls to a terminal state. `app exec` wraps `astro exec` scoped to the app. `app pods` lists the pods `exec` picks from — the pod name for `--pod` and container names for `-c`, with `--workload`/`--ready` to narrow. `promote --from <env> --to <env>` moves an env's running deployment (image+config) to another via `promoteDeployment`. `app previews` drives per-PR preview environments (`list`, `show`, `logs`, `open`, `teardown`, `pin`, `unpin`); pick one with `--pr <n>`, or `--branch <name>` for a manual preview, which carries no PR number. `previews logs` is `app logs` pointed at the environment the platform synthesized for the preview, so `--since`/`--tail`/`-f`/`--level`/`--search` behave identically. `previews pin` exempts a preview from garbage collection on both axes — TTL expiry *and* max-active eviction — until someone runs `unpin`, which is what separates it from extending a TTL; `--reason <text>` records why, and `list`/`show` surface the pin. `unpin` clears the whole record (who, when, why), is a no-op on an unpinned preview, and unlike `pin` is allowed on a torn-down one so stale state can always be cleared. |
| `astro exec` | Run a command or interactive shell in a running container (`--app <slug>` [`--workload`/`--pod`/`-c`] `-- <cmd>`). Streams over the exec WebSocket relay; requires `app.exec_pod`; every session is audited. |
| `astro agent` | Agent dispatch (`dispatch`, `run`, `ls`, `logs`, `cancel`, `inspect`, `register-repo`, `workloads`, `vnc`, `send`) plus sub-resources: `env-spec` (dispatch recipe CRUD) and `secret` (write-through VALUE management for an env-spec's secret refs — `set`/`ls`/`rm`; `set` prefers `--stdin`/hidden prompt, never echoes the value). `dispatch <agent-slug>` runs a registered agent `Workload(kind=agent)` once via `runAstroliftAgent` (`--input` JSON/@file → triggerPayload; `--env-spec <slug>` pins the image+secret packet; `--wait`/`--tail`); a bounded backfill is a payload the agent loops on (e.g. `{"mode":"backfill","batches":N,"batch_size":M}`). `run <workflow-slug>` is the distinct WorkflowDefinition seam (`runWorkflowDefinition`). `workloads ls` enumerates the org's registered `kind=agent` workloads, carrying both the slug `dispatch` takes and the GUID `workflow create --bind` takes. `vnc <task-id>` resolves a task to the absolute console URL that serves its live VNC viewer (the stored `vnc_url` is a root-relative WebSocket relay path, not openable). `send <task-id> <input>` queues a follow-up prompt for a task that is already running (`--stdin` for multi-line input; `--json`) — the steering channel: the message is applied at the agent's next turn boundary, so a send means queued, not read, and `deliveredAt` is what answers that. The task must be running, and not every agent runtime can accept a follow-up prompt; one that cannot leaves the message queued rather than dropping it. |
| `astro box` | Agent-boxes: warm containers an interactive agent session attaches to (`ensure`, `ls`, `rm`, `attach`). Unlike `agent dispatch`, which starts a batch run that ends, a box holds a tmux session open and waits, so the agent survives a dropped connection, an IDE restart, or a closed laptop, and more than one person can watch it. `ensure` is idempotent by design — it is what a button calls, so pressing it twice attaches to the box you already have rather than starting a rival one on a second node; a box that was idle-reaped restarts under the same slug, so a stored address keeps working. Name what to run with `--agent` (a registered agent whose run mode is `persistent`) or `--env-spec` (which is where the image and the secret packet come from). `--idle-timeout` takes `90m`, a number of seconds, or `never`, and is measured from the last pane activity rather than the last attach, so an agent working while you are away keeps its box. `ls` shows warm boxes only (`--all` includes the settled ones, which is how you find out why a box went away). `attach` ensures, waits, and joins the session; detaching leaves the agent running. Note: `attach` does not work yet — the exec relay admits registered apps only and a box is not one, so it fails as a permission error; tracked in calliopeai/astrolift#129. `ensure`, `ls` and `rm` are unaffected. |
| `astro workflow` | Author, validate, import, and export workflow TOML; browse/clone definitions; configure, run, watch, control, and delete organization workflows. `validate --server` is authoritative for the selected install. `run-manifest <file.toml>` collapses `import` → `create --bind` → `run` into one call, resolving each `agent_dispatch` stage's declared agent against the org's registered workloads (`--dry-run` shows the resolved bindings; `--no-run` stops after configuring). `run-cancel <slug>` stops an in-flight run — cooperatively by default so a run that owns external resources tears them down, or `--terminate --reason <text>` to hard-kill a wedged one; `--run <guid>` picks a run other than the newest, `--yes` is the confirmation (no prompt), and a run that is already terminal is refused before anything is sent. `run-show <slug>` prints a run stage by stage: order, kind, role, status, attempt, timings, and for a `human_gate` its gate state (pending / approved / rejected / closed) plus the approvers the stage declares, so "waiting on approval, and on whom" is read from the platform. Deciding a gate is not a CLI operation. Repository registration separately reconciles `workflows/**/*.toml`. |
| `astro ci` | CI-mode commands (`deploy`, `status`, `render`) — no interactive prompts; reads token + slug from env. `render` prints the manifests the platform would apply (`astroliftRenderedManifest`) for pre-merge review. |
| `astro org` / `astro team` / `astro project` | Org-scoped resource management. `project resources` discovers the selected cluster's full provider catalogue and manages project-owned shared services and their app/agent attachments. |
| `astro operator` | Operator (admin) cluster, provider, and federation management. |
| `astro cluster bootstrap` | One-shot helm install of the `astrolift-prereqs` chart (cert-manager, ingress, storage, external-dns) against a registered cluster; the bundled chart + per-cloud values are vendored into the binary. |
| `astro scm` / `astro alert` | `scm list` (configured source-control connections), `scm disconnect <id>` (remove a connection by id from `scm list`), and `alert list` (alert rules; `--all` includes inactive). |
| `astro status` | Platform status snapshot (`astroliftServerInfo`: version, install identity, region, server time, capabilities). |
| `astro onboard` | Set an AI coding agent up against the selected install in one call: writes an MCP server entry pointing at the install's gateway, installs the bundled skill catalogue, and drops the offline guides as agent context. Components are selectable with `--only mcp,skills,docs`, the layout with `--target claude\|codex\|both`, and `--dry-run` reports the plan without touching the filesystem. Existing files are reported and left alone unless `--force`. Authentication is reported, never performed. |
| `astro docs` | Open public docs, read embedded release-matched topics, or export portable Markdown and man pages. |
| `astro version-check` / `astro self-update` | Server-aware compatibility check + upgrade pointer. |
| `astro version` | Print the CLI version (set at build time via `-ldflags`). |

Every command supports `--json` for machine-readable output, plus
`--api-url`, `--token`, `--org`, `--team`, `--project`, `--app`,
`--no-color`, `--no-prompt`, and `--debug` as global flags. Errors go
to stderr; data goes to stdout.

Run `astro <command> --help` for the full flag set; the help text is
the source of truth.

### Onboarding an AI agent

`astro onboard` assembles what an agent needs against the selected install.
Every piece already existed -- the MCP gateway, API tokens that authenticate
both it and this CLI, a skill catalogue, offline guides in this binary -- but
nothing put them together, so setup meant reading three references and
hand-editing JSON.

```bash
astro onboard --dry-run                 # what would be written
astro onboard                           # mcp + skills + docs, both layouts
astro onboard --only mcp --target codex # just the MCP entry, Codex layout
astro onboard --json                    # machine-readable, for an agent to run itself
```

It writes `.mcp.json` and/or `.codex/mcp.json`, the catalogue under
`.claude/skills/` and/or `.codex/skills/`, and the guides under
`.astrolift/docs/`. Nothing is overwritten without `--force`, and no network
call is made.

Two ways for an agent to authenticate, both reported by `onboard`:

```bash
export ASTROLIFT_TOKEN=alft_at_...   # unattended: mint an API token under
                                     # Settings > API tokens. The same token
                                     # authenticates the CLI and the MCP
                                     # gateway, so nothing else is needed.
astro auth login                     # browser device flow.
```

Relaying that flow from something with no browser, without holding a process
open for fifteen minutes:

```bash
astro auth login --no-wait --json    # -> {"login_url": ..., "session_id": ...}
                                     #    hand login_url to a human
astro auth wait --session-id <id>    # blocks until they finish, then stores
                                     #    the credentials
```

The MCP config references `${ASTROLIFT_TOKEN}` rather than a resolved bearer,
so the file is safe to commit and works for every agent on the machine.

### Documentation for humans and agents

The binary includes a release-matched, network-free reference for the CLI,
control API, MCP, `astrolift.toml`, agent packages, and workflow TOML:

```bash
astro docs list
astro docs show manifest
astro docs export ./astrolift-docs
astro docs man ./man/man1
```

`docs export` also creates `llms.txt`, generated Markdown for the live command
tree, and section-1 man pages. Release archives include the generated man
pages. The canonical public site is [astrolift.dev](https://astrolift.dev).
CLI main builds and tagged releases byte-compare the embedded guide subset with
canonical docs main. A weekday drift workflow performs the same cross-repo
check even when the CLI has not changed. Refresh intentional docs changes with
`make vendor-docs` from the metarepo, then commit the snapshot through the
normal signed/DCO review flow.

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
| `ASTROLIFT_TOKEN` | explicit user API-token override (same precedence as `--token`) |
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
- **astrolift platform / API** — the control plane the CLI talks to; see
  [astrolift.ai](https://astrolift.ai) and the developer docs at
  [astrolift.dev](https://astrolift.dev)

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
make vendor-docs        # refresh the committed offline docs snapshot
make docs               # build a complete portable docs tree under build/
make clean              # remove ./astro, clear test cache
```

The build injects `Version` via `-ldflags` from the nearest git tag
(`git describe --tags --always --dirty`). For a tagged release build
locally:

```bash
goreleaser release --snapshot --clean
```

CI runs the same on tags, plus publishes authenticated GitHub release archives
and Docker images on Docker Hub and GHCR. Cutting a release is a deliberate,
single-command act — see **[RELEASING.md](RELEASING.md)** for the ship
checklist and the cadence policy.

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
- **[RELEASING.md](RELEASING.md)** — release cadence, ship checklist,
  distribution channels
- **[SECURITY.md](SECURITY.md)** — vulnerability disclosure
- **[CLAUDE.md](CLAUDE.md)** / **[AGENTS.md](AGENTS.md)** /
  **[CODEX.md](CODEX.md)** — agent shims
  (all point at `bootstrap.md`)
- **[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)** — community standards
- **[LICENSE](LICENSE)** — MIT

---

## License

MIT. See [LICENSE](LICENSE).

Copyright (c) 2026 Calliope Labs Inc. Calliope AI is a trademark of Calliope
Labs Inc.

Portions of the framework underlying this repo are derived from **[boilerworks](https://github.com/ConflictHQ/boilerworks)** (Copyright (c) Conflict LLC, MIT-licensed). Tip of the hat 🎩
