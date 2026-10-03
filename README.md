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

Source and [release archives](https://github.com/calliopeai/astrolift-cli/releases)
are public. Manual `curl` downloads need no GitHub account. Homebrew and Scoop
publication remain unconfigured; the legacy installer still requires GitHub
credentials unless a mirror is supplied. The container images are public.

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

### Installer script

```bash
git clone https://github.com/calliopeai/astrolift-cli.git
cd astrolift-cli
./scripts/install.sh
```

The installer detects your OS and architecture, downloads the matching
archive, **verifies it against `astro-checksums.txt`**, and installs `astro`
into `/usr/local/bin` or `~/.local/bin`. GitHub credentials are optional.
The download paths, in order, are:

1. `ASTRO_INSTALL_BASE_URL` — a mirror you control, or an offline fixture
2. an authenticated `gh` (whatever `gh auth login` is already using)
3. `GITHUB_TOKEN` / `GH_TOKEN` / `ASTRO_GITHUB_TOKEN` — for containers and CI
   images that have no `gh` binary
4. anonymous GitHub API requests when no credentials are available

An optional token raises the GitHub API rate limit. The installer requires
`curl`, `tar`, `install`, and either `sha256sum` or `shasum`. Authentication
and missing-release errors identify the HTTP status without printing tokens.

Set `ASTRO_INSTALL_TAG=vX.Y.Z` to pin a release, `ASTRO_INSTALL_DIR` to choose
the destination.

### Manual release download

```bash
release_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' \
  https://github.com/calliopeai/astrolift-cli/releases/latest)"
tag="${release_url##*/}"
release_base="https://github.com/calliopeai/astrolift-cli/releases/download/${tag}"
curl -fL "$release_base/astro-darwin-arm64.tar.gz" -o astro-darwin-arm64.tar.gz
curl -fL "$release_base/astro-checksums.txt" -o astro-checksums.txt
shasum -a 256 -c <(grep '  astro-darwin-arm64.tar.gz$' astro-checksums.txt)
```

Archives are named `astro-<os>-<arch>.tar.gz` (`.zip` on Windows) for
`darwin`/`linux`/`windows` and `amd64`/`arm64`. Verify against
`astro-checksums.txt`, extract `astro`, and put it on `PATH`. See the
[CLI install reference](https://astrolift.dev/reference/cli/#install) for the
full matrix.

### Staying current

`astro version` prints the release, commit, and build date it was built from;
an unstamped source build reports `astro dev`. `astro update` replaces the
running binary with the latest public release; GitHub credentials are optional.
Once a day `astro` will note on stderr if a newer release exists; set
`ASTROLIFT_NO_UPDATE_CHECK=1` to silence it. The hint never appears with
`--json`, in CI, or on `dev` builds.

### From source

```bash
git clone https://github.com/calliopeai/astrolift-cli.git
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

# Platform-built apps resolve the deploy branch and tag the image with its commit.
astro app deploy --wait
astro app deploy --ref release/v2 --wait         # branch, tag, or commit
# Apps with build_mode = "none" use images declared in their saved manifest.

# 4. Watch it run
astro app list                  # apps in the org
astro app show                  # status, source, workloads, environments
astro app logs                  # recent logs (-f to follow, [workload] to narrow)
astro app events                # platform events
astro app exec -- bash          # interactive shell (wraps `astro exec`)
astro exec --app web -- ps aux  # one-off command in a running pod
# astro app rollback            # roll back the running deployment
```

### Registering before a source connection is configured

Pass `--manifest-raw` to send the local manifest with registration, creating its
workloads even when the platform cannot fetch the repository:

```bash
astro app register --project-id <uuid> --source-repo myorg/my-app --manifest-raw
```

Without the flag, registration uses the existing repository-based behavior.

### Changing who builds the image

`--build-mode` on `register` only sets the mode at creation. To change it on an
app that already exists:

```bash
astro app set-build-mode my-service ci_pushed       # your CI builds and pushes
astro app set-build-mode my-service platform_build  # the platform builds in-cluster
astro app set-build-mode my-service none            # no image at all

# Pick a non-default builder while switching
astro app set-build-mode my-service platform_build --build-strategy buildpacks
```

Reach for `ci_pushed` when the platform builder cannot produce the image — a
source repo it cannot clone, or no builder on the cluster — and your CI already
can. Push the image to the registry first; the next `astro app deploy` rolls out
the tag you pass rather than trying to produce one.

The command sets `build_strategy` alongside the mode, because the deploy
pipeline decides whether to build from the strategy, not the mode. Flipping only
the mode would leave an app that reports `ci_pushed` and still runs a platform
build on every deploy. A Dockerfile path or build context saved at registration
is left untouched, so switching back and forth loses nothing.

### Preparing a box workspace

Use `astro agent env-spec upsert <slug> --agent-type codex --box-workspace`
to prepare the spec's configured repositories, dependencies and MCP before its
box session starts. Use `--box-workspace=false` to turn setup off. Omitting the
flag preserves the existing setting and compatibility with older servers. Box
readiness waits for setup; a slow workspace can exceed the attach timeout.

### Deploying a platform-built app

A platform-built app can deploy without an image tag. The platform resolves its
deploy branch and uses that commit for the build. Select another branch, tag or
commit with `--ref`:

```bash
astro app deploy --app my-service
astro app deploy --app my-service --ref release/1.2
```

CI-pushed apps still require `--image-tag`; apps with build mode `none` do not.

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

## Managed DNS zones

Managed zones supply platform hostnames for tenant apps and preview environments.
They are separate from an app's custom domains (`astro app domains list`). Select
an organization with `--org` or `astro org use`; `--server` selects its registered
endpoint and credentials without changing the saved default. Permissions,
canonical zone names, proof of control and provisioning are enforced by the API.

```bash
astro operator domains list --org acme
astro operator domains list --org acme --json | jq '.[] | select(.defaultFor == "none")'
astro operator domains create apps.example.com --dns-driver route53 --org acme
astro operator domains create previews.example.com --dns-driver route53 --default-for preview_envs --org acme
astro operator domains update <domain-id> --default-for both --org acme --json
astro operator domains update <domain-id> --wildcard=false --org acme
```

Creation explicitly sends `defaultFor=tenant_apps`, `organizationScoped=true`
and `isWildcardManaged=false`. `--default-for` accepts `tenant_apps`,
`preview_envs`, `both` or `none`; `none` excludes the zone from automatic default
matching. Existing organization/environment bindings may still use it. Updates
send only supplied settings, so correcting `default_for` preserves DNS config and
wildcard settings. Use the ID returned by `list` for updates; the zone and DNS
driver cannot be changed by the current API.

`--shared` on creation asks for a platform-shared zone; the server requires
platform-operator permission for every shared write. Organization-owned writes
require the server's domain configuration permission. Neither flag bypasses the
server's authorization checks. With the current API, domain writes need an
organization-scoped token with `admin` scope and the owner's actual
`provider_plugin.configure` permission; `astro auth login --scope clusters`
does not grant that permission. Team-scoped tokens cannot act on these
organization resources. Reads use `provider_plugin.read` (`read:clusters`).

`--dns-config <file.json>` accepts a JSON object for provider-specific settings.
It is not printed or read back. On update it replaces the **entire** DNS config;
omit it to preserve the saved config. An existing hosted zone may require proof
of control. The create result and JSON list expose the required TXT record; after
publishing it, run:

```bash
astro operator domains verify apps.example.com --org acme --json
```

Verification returns a nonzero exit status while proof remains pending, even
when the API successfully performed the lookup. JSON includes `zone`, `verified`
and `message`. Creating or verifying a zone may start asynchronous provisioning;
a successful command does not prove delegation, TLS issuance or app readiness.
Use `list` to inspect verification, provisioning, nameservers and validation
records (`--json` exposes the full returned status). The API currently lists at
most 200 visible zones without pagination; a full-sized result emits a warning
on stderr, and cannot establish a complete install-wide audit. Deletion and
provisioning repair commands are not exposed here.

## Inspect identity and account grants

```bash
astro whoami --org platform
astro whoami --permissions --json
astro perms diagnose
astro perms diagnose my-app --permission app.deploy
astro perms diagnose --permission app.deploy --scope-type PROJECT --scope-id <project-guid>
```

`whoami` reads your current profile from the selected server after verifying the
selected organization. `--permissions` adds the account grants held somewhere in
that organization. `perms diagnose` also reports their role-binding sources and,
with an app slug, the account's grants on that visible app. These are
**informational summaries**: they do not establish that a bearer credential can
act on a target. A grant on a sibling project cannot approve an operation here.

`--permission` requests the server's existing account diagnostic, preserving its
verdict and trace. An app slug resolves to its actual app GUID. Explicit
`--scope-type` and `--scope-id` must be supplied together; supported types are
`ORG`, `TEAM`, `PROJECT`, `APP` and the existing `AGENT` scope. With no target, the
server diagnoses the selected organization context. A retrieved denied verdict
is a successful diagnostic and exits zero; failure to retrieve a matching trace
exits nonzero. This diagnostic does not establish credential limits or evaluate
a mutation's environment and approval requirements, and is never used to
authorize or preflight another command.

With `--json`, data goes to stdout and retrieval errors produce a JSON error on
stderr while returning nonzero. Structured GraphQL errors preserve the server's
message, code, reason, path and declared current/requested version fields when
present. HTTP refusals without a GraphQL error report the status. Raw error
bodies and arbitrary extensions are excluded; `--debug` reports GraphQL status
and byte count without dumping the response. The current bearer value is
redacted if reflected in diagnostic text. Credentials are not displayed or
decoded to guess identity or scopes.

The early-refusal acceptance in app #1867 remains partial: the existing account
diagnostic is not an operation-specific authorization contract. Enforcement
continues on the server; this change introduces no generic command preflight.

## Commands

| Group | Purpose |
|---|---|
| `astro server` | Manage Astrolift installs the CLI knows about (add / list / use / remove). One install = one DNS zone + database. |
| `astro auth` | Browser device-flow login, logout, status, refresh, plus `wait` for the relay path. `login --no-wait` starts the flow, reports the session (`--json` makes it an object), and exits instead of blocking for fifteen minutes; `auth wait --session-id <id>` finishes it once a human has approved. That is how a browserless caller hands a login off. `login --no-browser` waits without launching a browser. |
| `astro app` | App lifecycle (`init`, `register`, `deploy`, `list`, `show`, `logs`, `exec`, `pods`, `rollback`, `promote`) plus sub-resources. `app secrets` (`list`/`create`/`delete`) drives `setAppSecret`/`deleteAppSecret`; `list` returns metadata only (key, environment, source, who last touched it) and never a value, `create` is an upsert reading the value from `--value`/`--stdin`/a hidden prompt and never echoing it, and both report a queued proposal id instead of an applied write on installs that require secret-change approval. `app services list` and `app domains list` are read-only views of the managed services and custom domains bound to one app (`astroliftManagedServicesPage` / `astroliftAppDomains`); provisioning a managed service is `astro project resources`, and there is no domain create/remove from the CLI yet. `app events` (also `app events list`) reads `astroliftEventsPage` for the app's deploy/secret/scale/health activity, filterable by `--type`/`--severity`/`--search`. `tokens`, `members`, `jobs`, and `audit` remain unimplemented placeholders. `deploy` requires `--image-tag` for CI-pushed apps; platform builds resolve the deploy branch or `--ref` to a commit, and apps using manifest images can omit it; `--wait` polls to a terminal state. `app exec` wraps `astro exec` scoped to the app. `app pods` lists the pods `exec` picks from — the pod name for `--pod` and container names for `-c`, with `--workload`/`--ready` to narrow. `promote --from <env> --to <env>` moves an env's running deployment (image+config) to another via `promoteDeployment`. `app previews` drives per-PR preview environments (`list`, `show`, `logs`, `open`, `teardown`, `pin`, `unpin`); use `--id <GUID>` for an exact read, including previews older than the recent 200-row catalog. `--pr <n>` and `--branch <name>` discover a GUID in that catalog and then reread it. Basic list/show never request pod usage or pricing; `show --cost` explicitly requests one live snapshot. `show --json` includes the stored environment GUID, name, cluster and versions, or an explicit unavailable/retired binding. `previews logs` carries that exact environment GUID and reviewed versions on every page, refuses stale or retired bindings, and never joins by hostname or environment name; so `--since`/`--tail`/`-f`/`--level`/`--search` behave identically. `previews pin` exempts a preview from garbage collection on both axes — TTL expiry *and* max-active eviction — until someone runs `unpin`, which is what separates it from extending a TTL; `--reason <text>` records why, and `list`/`show` surface the pin. `unpin` clears the whole record (who, when, why), is a no-op on an unpinned preview, and unlike `pin` is allowed on a torn-down one so stale state can always be cleared. |
| `astro exec` | Run a command or interactive shell in a running container (`--app <slug>` [`--workload`/`--pod`/`-c`] `-- <cmd>`). Streams over the exec WebSocket relay; requires `app.exec_pod`; every session is audited. |
| `astro agent` | Agent dispatch (`dispatch`, `run`, `ls`, `logs`, `cancel`, `inspect`, `register-repo`, `workloads`, `vnc`, `send`, `input-receipt`) plus sub-resources: `env-spec` (dispatch recipe CRUD) and `secret` (write-through VALUE management for an env-spec's secret refs — `set`/`ls`/`rm`; `set` prefers `--stdin`/hidden prompt, never echoes the value). `dispatch <agent-slug>` runs a registered agent `Workload(kind=agent)` once via `runAstroliftAgent` (`--input` JSON/@file → triggerPayload; `--env-spec <slug>` pins the image+secret packet; `--wait`/`--tail`); a bounded backfill is a payload the agent loops on (e.g. `{"mode":"backfill","batches":N,"batch_size":M}`). `run <definition-guid> --request-file <file> --yes` is the reviewed WorkflowDefinition alias; its optional `--wait` follows the returned exact engine identity. Slug-only starts and literal `--input` are refused; use `--inputs-file` (or `--input @file`). `workloads ls` enumerates the org's registered `kind=agent` workloads, carrying both the slug `dispatch` takes and the GUID `workflow create --bind` takes. `vnc <task-id>` resolves a task to the absolute console URL that serves its live VNC viewer (the stored `vnc_url` is a root-relative WebSocket relay path, not openable). `send <task-id> <input>` queues a follow-up prompt for a task that is already running (`--stdin` for multi-line input; `--json`) — the steering channel: the message is applied at the agent's next turn boundary, so a send confirms queue admission; `deliveredAt` records a delivery claim, not execution. The task must be running, and not every agent runtime can accept a follow-up prompt; one that cannot leaves the message queued rather than dropping it. |
| `astro apply` | Declarative agent environment specs: `apply -f <file.toml>` reads `[[env_spec]]` tables and makes the platform match. A missing spec is created, a spec that differs is updated field by field with the diff printed, and an unchanged spec is left alone, so a second apply is a no-op. Only keys present in the file are managed. `--dry-run` prints the plan and changes nothing. |
| `astro box` | Agent-boxes: warm containers an interactive agent session attaches to (`ensure`, `ls`, `rm`, `attach`). Unlike `agent dispatch`, which starts a batch run that ends, a box holds a tmux session open and waits, so the agent survives a dropped connection, an IDE restart, or a closed laptop, and more than one person can watch it. `ensure` is idempotent by design — it is what a button calls, so pressing it twice attaches to the box you already have rather than starting a rival one on a second node; a box that was idle-reaped restarts under the same slug, so a stored address keeps working. Name what to run with `--agent` (a registered agent whose run mode is `persistent`) or `--env-spec` (which is where the image and the secret packet come from). `--idle-timeout` takes `90m`, a number of seconds, or `never`, and is measured from the last pane activity rather than the last attach, so an agent working while you are away keeps its box. `ls` shows warm boxes only (`--all` includes the settled ones, which is how you find out why a box went away). `attach` ensures, waits, and joins the session; detaching leaves the agent running. Note: `attach` does not work yet — the exec relay admits registered apps only and a box is not one, so it fails as a permission error; tracked in calliopeai/astrolift#129. `ensure`, `ls` and `rm` are unaffected. |
| `astro workflow` | Author, validate, import, and export workflow TOML; browse/clone definitions; configure, run, watch, control, and delete organization workflows. `validate --server` is authoritative for the selected install. `run-manifest <file.toml>` collapses `import` → `create --bind` → activation → `run` into one call, resolving each `agent_dispatch` stage's declared agent against the org's registered workloads (`--dry-run` shows the resolved bindings; `--no-run` leaves the imported definition disabled for review). `import --replace` upserts the org's own definition sharing the manifest's slug instead of always creating a new one: in place when the stage kinds are unchanged, so configured Workflows, bindings and schedules keep working untouched, otherwise as a new version with every configured Workflow repointed to it, or a clear refusal (nothing changed) when a repoint would break one's bindings. Launching enables only the newly imported definition through the platform update API and requires workflow update permission. Use `definition <slug>` to review an import and `definition-enable <slug>` to enable it explicitly; `workflow run` never enables existing definitions implicitly. `run-cancel <slug>` stops an in-flight run — cooperatively by default so a run that owns external resources tears them down, or `--terminate --reason <text>` to hard-kill a wedged one; `--run <guid>` picks a run other than the newest, `--yes` is the confirmation (no prompt), and a run that is already terminal is refused before anything is sent. `run-show <slug>` prints a run stage by stage: order, kind, role, status, attempt, timings, and for a `human_gate` its gate state (pending / approved / rejected / closed) plus the approvers the stage declares, so "waiting on approval, and on whom" is read from the platform. Deciding a gate is not a CLI operation. Repository registration separately reconciles `workflows/**/*.toml`. |
| `astro ci` | CI-mode commands (`deploy`, `status`, `render`) — no interactive prompts; reads token + slug from env. `render` prints the manifests the platform would apply (`astroliftRenderedManifest`) for pre-merge review. |
| `astro org` / `astro team` / `astro project` | Org-scoped resource management. `project resources` discovers the selected cluster's full provider catalogue and manages project-owned shared services and their app/agent attachments. |
| `astro operator` | Cluster, provider, and federation management, plus `domains list/create/update/verify` for organization-owned and visible platform-shared managed DNS zones. Shared writes require platform-operator permission. |
| `astro cluster bootstrap` | One-shot helm install of the `astrolift-prereqs` chart (cert-manager, ingress, storage, external-dns) against a registered cluster; the bundled chart + per-cloud values are vendored into the binary. |
| `astro operator cluster install-agent` | Install the keep-alive agent through the control plane's private network. Exactly one of `--cluster-id <GUID>` or `--slug` plus `--request-file` is required; a known GUID skips operator inventory discovery, while slug discovery requires `cluster.register`; the private metadata file retains the original cluster/version/provider source proof and request UUID before dispatch. Keep that exact tuple for recovery after lost replies; a missing source proof refuses without replacement. No local kubeconfig or raw agent key is transferred. `agent-install-status --install-id <UUID>` reads the exact operation; optional `--cluster-id` refuses a receipt for another cluster; queued acceptance is distinct from authenticated heartbeat confirmation. Requires the server's `installClusterAgent` API; older servers refuse rather than falling back to local key rotation. |
| `astro scm` / `astro alert` | `scm list` (configured source-control connections), `scm disconnect <id>` (remove a connection by id from `scm list`), and `alert list` (alert rules; `--all` includes inactive). |
| `astro status` | Platform status snapshot (`astroliftServerInfo`: version, install identity, region, server time, capabilities). |
| `astro api graphql` | Run a GraphQL document using stored credentials and the explicit `--org` or saved working organization. Unknown organizations fail before the document is sent. Without either organization selection, account-level queries remain unscoped. |
| `astro onboard` | Set an AI coding agent up against the selected install in one call: writes an MCP server entry pointing at the install's gateway, installs the bundled skill catalogue, and drops the offline guides as agent context. Components are selectable with `--only mcp,skills,docs`, the layout with `--target claude\|codex\|both`, and `--dry-run` reports the plan without touching the filesystem. Existing files are reported and left alone unless `--force`. Authentication is reported, never performed. |
| `astro docs` | Open public docs, read embedded release-matched topics, or export portable Markdown and man pages. |
| `astro version-check` / `astro self-update` | Server-aware compatibility check + upgrade pointer. |
| `astro version` | Print the CLI version (set at build time via `-ldflags`). |

Every command supports `--json` for machine-readable output, plus
`--server`, `--api-url`, `--token`, `--org`, `--team`, `--project`, `--app`,
`--no-color`, `--no-prompt`, and `--debug` as global flags. Errors go
to stderr; data goes to stdout.

### Exact workflow executions

Use the execution GUID returned by `workflow run`, `workflow run-manifest`,
`workflow definition-start`, or reviewed `agent run` to observe the exact run.
`execution` looks up that record even after it leaves the recent-run list:

```bash
astro --server <server> --org <organization> workflow execution <id> --json
astro --server <server> --org <organization> workflow execution <id> --watch
astro --server <server> --org <organization> workflow execution-stop <id> --yes
astro --server <server> --org <organization> workflow execution-cleanup <id> --yes
astro --server <server> --org <organization> workflow execution-stages <id>
```

Keep the original server and organization when revisiting a run. The returned
execution GUID also works as `<id>`; a configured workflow instance ID from
`workflow runs` is a different identifier. `--workflow-id` and `--run-id` can
require the original Temporal workflow and execution IDs. Stop and cleanup
first verify the exact record, then pin both Temporal IDs in their request.
`execution-stop --terminate --reason <text> --yes` requests hard termination.

The JSON record separates `status` / `isTerminal` from `taskCleanup` (status,
remaining tasks, errors, and retryability). A successful control prints
`requested: true`, meaning the request was acknowledged; it does not mean the
execution has closed or its resources are gone. Cleanup is allowed only after
verified closure and retries one owned task per call; the platform's scheduled
reconciler also retries unfinished cleanup. Watch keeps observing until closure
and cleanup are both verified, for up to 30 minutes. With `--json --watch` only
the final record is printed. An unavailable observation retains the last state
with `observationError` and cannot be used to initiate control. These commands
require a server exposing `workflowExecution` and `controlWorkflowExecution`.

Use `astro --server <registered-slug> --org <organization> box ls --json` to
address one install without changing `astro server use` or another client's
selection. The endpoint and stored credentials both come from that server.
Authentication and onboarding accept the same selector. Unknown servers fail;
an API URL override must match the explicitly selected registered server.
Without `--server`, the saved selection and existing override behavior apply.
Box attachment, removal, and `exec` resolve `--org` before addressing a target.
HTTP requests and terminal WebSocket handshakes carry the selected organization.
API tokens remain tied to their issuing organization; use credentials for the
selected organization. Older control planes may silently ignore a conflicting
organization header, so they require a server update to reject that mismatch.

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

### Signed task completion callbacks

With a compatible server, configure organization callback destinations and a
signing secret, then attach a callback to an agent dispatch:

```bash
astro agent callbacks configure --allow-host hooks.internal.example.org
astro agent callbacks secret-set completion-key --file /secure/path/completion-key
astro agent dispatch report --callback-url https://hooks.internal.example.org/tasks \
  --callback-secret-ref completion-key --correlation-id request-42 --callback-mode NOTIFY
astro agent callbacks show --json
astro agent inspect <task-guid> --json
astro agent callbacks redeliver <task-guid> --json
```

`secret-set` accepts `--stdin` or `--file` and never a literal key argument.
`configure` replaces the allow-list; repeat `--allow-host`, or use `--clear`.
Delivery retries for at least 24 hours with backoff and jitter when a receiver
is unavailable. `inspect` shows delivery state, attempt count, and sanitized
last error; replay sends the final event without rerunning the agent. See
`astro docs show callbacks` for permissions, receiver signatures, deduplication,
key rotation, and retention. `--wait` waits for task execution, not webhook
acknowledgement.

### Documentation for humans and agents

The binary includes a release-matched, network-free reference for the CLI,
control API, MCP, `astrolift.toml`, agent packages, workflow TOML, capability
discovery, app/agent/workflow setup, shared services, and signed completion callbacks:

```bash
astro docs list
astro docs show manifest
astro docs show callbacks
astro docs show capabilities
astro docs show shared-services
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
    <server-slug>.yaml         protected per-server tokens
```

Credential files use mode `0600` on Linux/macOS. On Windows they use a protected
ACL owned by the current user, granting access only to that user and SYSTEM.
The CLI checks the actual opened file before reading and refuses permissive,
inherited or unrecognized ACLs and symlink/reparse targets. An explicit new
login can replace a current-user-owned legacy Windows credential file with a
protected file; reads never silently migrate or accept its old permissions.

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

### Agent log following

`astro agent logs <task-id> --follow` and `astro agent dispatch <agent-slug> --tail`
poll recent pod log snapshots. Following continues when the tail reaches its
line limit, matching overlapping lines instead of relying on a growing count.
If snapshots no longer overlap, the available tail is printed again. The API
has no log cursor: lines that expire between polls cannot be recovered, and
identical full snapshots cannot reveal whether more identical lines were written.
Use `agent logs --tail <n>` to request a larger recent window when needed.

Task dispatch, inspection, logs, replies, cancellation and VNC lookup resolve
`--org` (or the saved working organization) before making task requests. An
unknown organization fails before any task operation is sent.

`astro agent inspect <task-id>` also reports the control plane's recorded failure
reason in text and JSON output. This remains available for failures before a
pod starts, when there are no pod logs to inspect.

When supported by the server, task inspection and box waiting also report the
latest startup reason, such as insufficient CPU capacity. Task and box list JSON
includes the optional `startupDiagnostic` observation (phase, reason, message,
pod name and observation time). A pending observation is recoverable; it does
not end the task or box. Older servers continue to return their existing records.

## Recover queued agent input

Save a UUID and the original message before sending, then use
`astro agent send <task-id> --request-id <uuid> --json -- '<message>'`. If its
response is lost, `astro agent input-receipt <task-id> --request-id <uuid> --json`
reads the exact receipt without enqueueing or consuming input. It prints JSON
`null` when no receipt exists; that does not prove an in-flight send failed.

Retry the original message with the same UUID to recover safely. Changing the
message for that UUID is rejected. An accepted receipt remains recoverable after
the task finishes. The server must support `clientRequestId`; a keyed send never
falls back to an unkeyed mutation. Omitting `--request-id` retains legacy behavior.

A receipt confirms queue admission. `deliveredAt` records a control-plane claim
for runner delivery and does not confirm that the harness executed the input.

### Agent session attachment

`astro agent session attach <task-uuid>` joins an existing task, prints its
history, and follows turns, tool calls, and approval requests. `--snapshot`
prints history and detaches; `--json` emits snapshots and authoritative actions
as newline-delimited JSON. Ctrl-C detaches and leaves the task running.

```sh
astro agent session attach <task-uuid> --org <organization>
astro agent session attach <task-uuid> --steer "Also check the migration"
astro agent session attach <task-uuid> --approve <displayed-tool-id>
astro agent session attach <task-uuid> --deny <displayed-tool-id> --reason "Outside the brief"
astro agent session attach <task-uuid> --action-file answer.json
astro agent session attach <box-uuid> --box --interactive
```

Controller actions require the caller's current permissions and wait for the
host's acceptance or rejection. An acknowledgment timeout leaves delivery
uncertain; inspect the session before resending. Use `--client-id` to reuse an
attachment identity; the host supplies the next sequence for that identity.
The selected server credentials and organization apply to every request.

For boxes, `--interactive` connects keyboard input to the existing tmux session;
Ctrl-] detaches. Missing live gVisor, network fence, or gateway controls refuse
attachment and name the missing prerequisite. Keyboard mode requires a local
terminal and text output. `--action-file` can send a terminal input or resize
without keyboard mode. Independent clients have separate PTY output histories.

If the task host explicitly reports that live attachment is disabled, observer
mode follows the durable task event stream instead. It preserves question and
tool identities, drains all pages, and prints the recorded terminal result.
Controller actions require live attachment. This adapter negotiates AHP 1.0.0
and tests its attachment messages with the official Go SDK transport, whose
published module is pinned at v0.9.0.

`astro agent quarantine ls --json` lists active dispatch quarantines visible to
this credential in the selected organization. `astro agent quarantine clear
<quarantine-uuid> --yes` performs the authorized, audited recovery mutation.
Without `--yes` it asks for confirmation; noninteractive calls must supply the
flag. Clearing permits future dispatch and does not restart a stopped task.

### Prerequisite chart source and integrity

`astrolift-opscode/helm/astrolift-prereqs` is the canonical source. The CLI
commits a release-matched snapshot plus all locked subchart archives for offline
bootstrap. `internal/charts/source.json` records the full published source
revision and SHA-256 inventory; `make vendor-charts-check` verifies every file,
including missing/extra files and archives, without network access.

To update the snapshot, publish/review canonical changes first, use a clean
opscode checkout containing that revision, configure the chart repositories
listed in `Chart.yaml`, then run:

```sh
make vendor-charts PREREQS_REPO=../astrolift-opscode PREREQS_REV=<full-published-commit>
make vendor-charts-check
```

Refresh fetches canonical `origin/main`, requires published ancestry and reads
Git blobs from the exact commit. A missing, dirty, older or unpublished sibling
fails before the embedded copy changes. Dependencies build from `Chart.lock`;
the helper never copies arbitrary sibling working files. Commit the snapshot and
inventory together. The source repository is private: CI checks local integrity,
not authenticated remote parity. A maintainer must independently verify the
inventory against that published Git tree before approving a new pin. Offline
builds do not need source-repository credentials.

### Reviewed pipeline starts and recovery

Pipeline controls use the selected organization and verified current user. List
one bounded server page with `astro pipeline list --limit 50 --json`; pass the
returned `nextCursor` as `--after` to continue. Names are resolved across server
pages and ambiguous names require an exact GUID.

```sh
astro pipeline run <pipeline-guid> --branch main --request-file ./pipeline-request.json --yes --json
astro pipeline reconcile --request-file ./pipeline-request.json --json
astro pipeline show <run-guid> --json
astro pipeline runs --pipeline <pipeline-guid> --limit 20 --json
astro pipeline cancel <run-guid> --yes --json
```

A start saves its UUID and reviewed pipeline/version/branch before dispatch in
an exclusively created private metadata file (mode `0600` on Linux/macOS;
protected current-user/SYSTEM ACL on Windows). Retain that file if the reply is
lost. Reusing it checks the original actor, server and organization and reads
the original request before any submission. A read outage does not submit; a
known reservation is inspected without resubmitting. A successful empty lookup
can submit only the original key and branch with `--yes`, while the pipeline
still matches the saved version. Reconciliation never submits. Do not create a
new request file to retry an uncertain start: a new key means a different run.

Cancellation binds the exact run version and recorded Temporal workflow/run IDs.
An acknowledgement is a request accepted by the engine; inspect `status`,
`cancellationStatus` and `cleanupStatus` independently with `pipeline show`.
Unconfirmed submission or cancellation returns an error and preserves recovery
identity. Request files and control output contain metadata, without inputs,
bearer credentials, job results or log bodies.

### Reviewed environment controls

Restart and scale use a saved review bound to the current server, organization
and actor. Explicit-environment exec waits for the authoritative admitted target
before forwarding input. Review resolves the exact app and workload identity,
including workloads beyond the old 200-row inventory limit:

```sh
astro app workload review web --app api --environment <environment-GUID> > review.json
astro app workload scale web 2 --app api --environment <environment-GUID> --review review.json --yes
astro app workload review web --app api --environment <environment-GUID> > review.json
astro app workload restart web --app api --environment <environment-GUID> --review review.json --yes
astro exec --app api --environment <environment-GUID> --workload web -- sh
astro app exec web --app api --environment <environment-GUID> -- sh
astro docs show environment-actions
```

Review again after any successful write. Stale, deleted, denied, mismatched or
unavailable targets fail without primary-environment fallback. Workload receipts
distinguish patch acceptance from rollout completion. Exec disconnect requires an
explicit new attach and does not replay input; a lost exit receipt leaves the
last input's outcome unknown. Pod UID checking is preflight only, and this client
does not claim mobile-audience, atomic binding or action-admission proof support.

### Bounded workflow authoring and recorded rounds

`workflow init --pattern review_loop` creates an explicit rejection return with
a finite `max_rounds` cap. Supported authoring patterns are `single`, `chained`,
`fan_out` and `review_loop`; supervisor/advisor labels do not select an implemented
executor. Local validation preserves per-stage `max_attempts`, `back_edge` /
`back_edge_json`, `iteration` / `iteration_json` and nested `workflow` targets.
It supports serial `collection` and `format_record` stages, and refuses malformed
local caps. The server remains authoritative for total execution budgets,
collection body ranges, imported source semantics and current target bindings.

```sh
astro status --json
astro workflow init --pattern review_loop -o review.toml
astro workflow validate review.toml
astro workflow validate review.toml --server
astro workflow execution-stages <execution-guid> --json
astro docs show bounded-workflows
astro docs search 'serial collection'
```

The exact stage reader includes recorded rounds, attempt numbers, return causes,
serial item/parent identities and separate parallel branch identities. Text
prints item/branch labels starting at one; JSON preserves the recorded zero-based
indexes. Missing metadata stays unavailable. These reads require a matching
server schema and current workflow-read authority; a schema or authorization
failure does not select another execution or substitute inferred history.
The offline bounded-workflow topic and `astrolift-workflows` skill also travel
with `astro onboard`'s docs/skills components, without a network connection.

### Exact workflow definition review and recovery

Review the exact definition GUID and its JSON Schema before dispatch:

```sh
astro workflow definitions --json
astro workflow definition-review <definition-guid> --json
astro workflow definition-start <definition-guid> --expected-revision <revision> --expected-input-schema-digest <digest> --inputs-file ./inputs.json --request-file ./definition-request.json --yes --json
astro workflow definition-reconcile --request-file ./definition-request.json --json
astro docs show reviewed-starts
```

The expected revision and schema digest flags are optional as a pair; without
them, the CLI binds the current exact review immediately before dispatch. Use
one bounded JSON object file (at most 64 KiB) for typed or complex inputs, and
omit it for a no-input definition. Sensitive schema fields accept opaque secret
references, never literal credentials. Schema validation and defaults remain
server-authoritative. Disabled or unsupported definitions cannot start.

Before dispatch, the CLI flushes an exclusively created private recovery file
(POSIX mode `0600`; Windows current-user ownership and a protected user/SYSTEM
ACL), containing only the original UUID and actor/server/org/definition/revision/
schema metadata. Existing files always perform read-only recovery: input files
are never opened or resubmitted, including after an empty lookup. An absent
record leaves the original outcome unknown; retain the file rather than making
a replacement key. Scope changes refuse recovery. Known unconfirmed engine
submission prints its exact identities and returns an error; reconciliation
can display that uncertainty without writing. Engine acceptance is separate
from execution completion. `agent run <definition-guid>` is an alias with the
same review/recovery rules; `--wait --json` emits one metadata object, retaining
a known unconfirmed receipt on failure. Waiting pins the original receipt and
reports cleanup separately from engine completion. Existing app-bound
`workflow run` is unchanged.

### Search release-matched guides offline

```sh
astro docs search 'workflow recovery'
astro docs search '"request file"' --limit 5 --json
astro docs search 'environment exec'
astro docs show reviewed-starts
```

Search reads this binary's embedded public Markdown guides without network,
credentials or a configured server. It matches all case-insensitive terms;
double quotes group a contiguous phrase with normalized whitespace. Results
follow catalogue order with one source line and a snippet of at most 180
characters per matching guide. `--limit` defaults to 10, accepts 1–50 and bounds
output; JSON includes `matches`, `totalMatches` and `truncated`. Queries are
bounded to 512 UTF-8 bytes and 32 terms/phrases. Use `docs show` for a complete
guide and command `--help` for the actual executing command tree. These new
commands require a release containing them; the published v0.7.2 predates them.

### Scoped managed resource reads

`astro project resources list` preserves its JSON array output and now walks
permission-checked server cursors. `--page --json` returns one bounded
`{items,totalCount,nextCursor}` page; use `--after` only with `--page`.
`--limit` accepts 1–200. Search and kind/status/environment/cluster-GUID filters
are sent to the server. A refused continuation or a walk exceeding the safety
bound returns an error without emitting a partial JSON array. App-owned
`astro app services list` has the same additive `--page` mode.

```sh
astro project resources list --project platform --page --limit 50 --json
astro project resources show <resource-guid> --project platform --json
astro project resources attachments <resource-guid> --project platform --limit 50 --json
astro project resources cost <resource-guid> --project platform --expected-context-revision <revision> --json
astro app services show <resource-guid> --app example-api --json
```

Project and app-owned resources remain separate. Basic reads contain metadata,
not configuration values, connection material, provider failure bodies or nested
grants. Names on project show/cost/actions use at most two discovery rows, then
reread the selected exact GUID; deleted GUIDs never follow a same-name replacement.
Cost is explicit and permission checked; missing amount, currency, timestamp or
HTTPS source produces unavailable pricing rather than zero.

Existing project-resource update/reprovision/attach/remove commands always carry
the fresh reviewed `contextRevision` through the write. Supply
`--expected-context-revision` to pin an earlier explicit review. Detach keeps its
existing attachment-GUID invocation: it resolves that exact visible owner and
rereads the owner GUID with the captured revision before the write. Optional
`--resource <GUID>` pins the owner explicitly; missing or inaccessible attachments
are refused without a resource scan or name fallback.
Attachments must share the owning project and cluster. An asynchronous operation
receipt proves acceptance/enqueue only; read its operation identity and status
separately. A lost response is unconfirmed and is never automatically retried.


### Exact environment logs and traces reference

This source snapshot adds an offline `logs-traces` topic for persisted environment
placement, historical log pagination and bounded trace envelopes. It requires the
compatible server capabilities `observability.exact_environment_logs` and
`observability.scoped_trace_envelopes`; released CLI v0.10.0 lacks this new topic.

```sh
astro docs show logs-traces
astro docs search 'collector attribution'
astro api graphql --file environment-logs.graphql --vars-file log-vars.json --json
```

The guide includes read-only query documents and explains trusted trace resource
attribution, shared-cluster placement and explicit unavailable states. Existing
CLI app/preview logs remain historical polling; no live WebSocket or export command
is added. The paired preview guide documents compatible API live/export proof and
original-requester/credential downloads, including limits of old name-only artifacts.

### Server-owned cluster keep-alive installation reference

This source snapshot adds the offline `cluster-agent-install` guide for the
prepared server-owned installation API. Released v0.10.0 lacks this topic and
the matching remote installation commands. Check actual server schema and CLI
help before using them.

```sh
astro docs show cluster-agent-install
astro docs search 'heartbeat confirmation'
```

The guide covers exact cluster review, a private original request file,
credential-bound recovery and metadata-only status. Only `SUCCEEDED` with
`heartbeatConfirmed: true` establishes installation success. The separate
`logs-traces` guide explains registered CloudWatch role/external-ID threading,
collector/read-policy handoff and the future live ingestion/post-pod-loss checks
required before accepting that setup. Neither guide certifies an installed
collector, a production heartbeat or tracing activation.

### Reviewed server-owned log collector installation

This prepared source adds three remote commands and an offline
`cluster-log-collector` guide; released v0.10.0 and the separate v0.10.1
documentation patch do not contain them.

```sh
astro operator cluster log-collector-review --cluster-id CLUSTER_GUID --retention-days 30 --json
astro operator cluster install-log-collector --cluster-id CLUSTER_GUID --request-file collector-install.json --json
astro operator cluster log-collector-status --operation-id OPERATION_GUID --cluster-id CLUSTER_GUID --json
astro docs show cluster-log-collector
```

The CLI requires public handshake `clusters.reviewed_log_collector_install`,
then authenticated exact target authority. Existing server-owned install-agent
now similarly requires `clusters.reviewed_agent_install`. API markers grant no
permission, provider support, node coverage or health. No local kubeconfig or
credential fallback is used.

Review and installation require exactly one of `--cluster-id` or `--slug`. A known
canonical nonzero GUID goes directly to the scoped `cluster.manage` API without
an inventory request. Current membership and credential ceilings still apply;
shared platform clusters also require the platform-operator gate. Slug discovery retains its `cluster.register` requirement;
a narrow cluster-scoped credential should use the known GUID. Knowing a GUID
grants no access. Optional `--cluster-id` on either status command verifies the
returned target before printing a receipt.

The private request file is exclusively created and flushed before dispatch.
After lost replies, the same file preserves original server/org/actor, cluster
GUID/version/source, retention and request UUID. Omitted retention uses the
stored value; explicit changes and missing source refuse without refreshing or
replacing the request. An existing slug-selected file can also be recovered with
its exact stored cluster GUID; its original bytes and tuple remain unchanged.
Files require POSIX `0600` or Windows protected current-user/
SYSTEM ACL.

Only ACTIVATED with post-loss proof timestamps confirms collector installation
and reader activation, not ongoing health or tracing. Reader-policy output is
unattached; external grants require the connection owner's action. The server
supports Linux EC2 EKS nodes, not Fargate/Windows collection. Native fixtures
prove transport/recovery behavior; no production install or ingestion is claimed.

### Shared model hosting knowledge

This source adds the release-matched `model-hosting` offline topic: admin-gated
Hugging Face connections or immutable local sources, separate access/license/
CPU-GPU/vLLM 0.15.1 checks, storage and runtime prerequisites, recorded model-server
readiness and subsequent app subscriptions. Known source IDs grant no authority.
The CLI has no native model-management verbs; use the guide’s bounded metadata
queries with existing `astro api graphql`. Token writes use the write-only UI,
and private upload URLs must remain outside logs and metadata output.

```bash
astro docs show model-hosting
astro docs search '"immutable manifest"' --json
```

The source snapshot now contains 23 topics and 24 canonical mirrored files,
including `llms.txt`. `astro docs list` and generated help reflect the executing
binary; this addition does not change the release version or establish a live
installation’s capability, storage setup or model health.
