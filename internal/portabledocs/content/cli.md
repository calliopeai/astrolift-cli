# `astro` CLI reference

!!! note "Agent notes"
    The binary is the live truth: `astro --help`, `astro <command> --help`,
    `astro docs search '<terms>'` (offline guides) and `astro version-check`
    (server support, no sign-in). No command is marked preview or experimental
    in its help; groups whose help line carries an issue number (`alert`, `org`,
    `project`, `scm`, `status`, `team`) are the thinnest surfaces. Frequent
    mistakes: parsing prose instead of passing `--json`; giving `app logs` or
    `app exec` an app slug when their positional is a workload (the app comes
    from global `--app <slug>` or the local `astrolift.toml`); running before
    `astro server add` and `astro auth login`, which fails with
    `no current server` or ``loading credentials (run `astro auth login`)``.

`astro` is a single Go binary. It is both the interactive developer client and
the non-interactive client used in CI.

## Install

The [CLI releases](https://github.com/calliopeai/astrolift-cli/releases) and
[source](https://github.com/calliopeai/astrolift-cli) are public. The following `curl` path needs no GitHub account.

Find the current immutable tag and download both the archive and its checksum.
For example, on Apple silicon:

```bash
release_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' \
  https://github.com/calliopeai/astrolift-cli/releases/latest)"
tag="${release_url##*/}"
release_base="https://github.com/calliopeai/astrolift-cli/releases/download/${tag}"
asset=astro-darwin-arm64.tar.gz
download_dir="$(mktemp -d)"
curl -fL "$release_base/$asset" -o "$download_dir/$asset"
curl -fL "$release_base/astro-checksums.txt" -o "$download_dir/astro-checksums.txt"

(cd "$download_dir" && \
  shasum -a 256 -c <(grep "  ${asset}$" astro-checksums.txt))
tar -xzf "$download_dir/$asset" -C "$download_dir"
mkdir -p "$HOME/.local/bin"
install -m 0755 "$download_dir/astro" "$HOME/.local/bin/astro"
astro version
```

`astro version` prints `astro X.Y.Z (commit <sha>, built <timestamp>)` for a
release build. A failed checksum prints `WARNING: 1 computed checksum did NOT
match` and nothing is installed; download the archive and `astro-checksums.txt`
again from the same `tag`. Add `~/.local/bin` to `PATH` when it is not already
there. Use `sha256sum -c` instead of `shasum -a 256 -c` on Linux. Available
archives are:

| Platform | Release asset |
|---|---|
| macOS, Apple silicon | `astro-darwin-arm64.tar.gz` |
| macOS, Intel | `astro-darwin-amd64.tar.gz` |
| Linux, ARM64 | `astro-linux-arm64.tar.gz` |
| Linux, x86-64 | `astro-linux-amd64.tar.gz` |
| Windows, ARM64 | `astro-windows-arm64.zip` |
| Windows, x86-64 | `astro-windows-amd64.zip` |

Container images carry the same binary and are publicly pullable from Docker
Hub (`calliopeai/astrolift-cli`) and GHCR (`ghcr.io/calliopeai/astrolift-cli`);
`latest` and `X.Y.Z` are multi-arch, `X.Y.Z-amd64`/`X.Y.Z-arm64` single-arch.
Mount `~/.config/astrolift` at `/home/nonroot/.config/astrolift` for stateful
commands:

```bash
docker run --rm -it \
  -v "${HOME}/.config/astrolift:/home/nonroot/.config/astrolift" \
  calliopeai/astrolift-cli:latest app list
```

Build from source with `git clone https://github.com/calliopeai/astrolift-cli.git`
followed by `make build` (Go 1.25 or newer). Prefer the checksum-verified release archives for
repeatable installations. Pin `tag=vX.Y.Z` instead of resolving latest in CI.
The current source installer also supports anonymous downloads:

```bash
git clone https://github.com/calliopeai/astrolift-cli.git
cd astrolift-cli
./scripts/install.sh
```

Set `ASTRO_INSTALL_TAG=vX.Y.Z` to an existing immutable release tag to pin the
install. Otherwise the latest release is selected. The installer detects Linux
or macOS and ARM64 or x86-64, verifies `astro-checksums.txt` before installing
the binary, and installs the archive's man page. It requires `curl`, `tar`,
`install`, and either `sha256sum` or `shasum`. `GitHub returned 404: release or
asset not found` means `ASTRO_INSTALL_TAG` names a tag that does not exist; list
tags on the releases page and use one with a `v` prefix.

`ASTRO_INSTALL_BASE_URL` selects a mirror. Otherwise an authenticated `gh` or
an explicit `ASTRO_GITHUB_TOKEN`, `GITHUB_TOKEN`, or `GH_TOKEN` can raise the
GitHub API rate limit; requests are anonymous when none is configured. Do not
paste tokens into command transcripts. Set `ASTRO_INSTALL_DIR` and
`ASTRO_MAN_DIR` to choose installation destinations. Missing release/assets,
rejected credentials and rate limits produce errors before installation;
a checksum failure refuses the archive. Source tags predating the anonymous
installer may still require authentication, so use the archive path above
when using such a checkout.

## Configure and authenticate

```bash
astro server add prod https://astrolift.example.com
astro server use prod
astro auth login
astro auth status
astro version-check
```

Substitute `prod` with any slug you choose for this install and
`https://astrolift.example.com` with the API URL your operator gave you;
`astro server list` shows both afterwards, with `*` on the current one.
Expected output, step by step: `Added server prod -> <url>` and `Set as current
server.` (the first server added becomes current, so `server use` is only
needed to switch); `Logged in to <url>.` after the browser flow; `State:
valid` from `auth status`; `CLI X.Y.Z is compatible with <url>.` from
`version-check`. Failure signatures: ``no current server. run `astro server add <slug>
<api-url>` and `astro auth login` `` means no server is registered;
``loading credentials (run `astro auth login`)`` or ``unauthorized: run `astro
auth login` `` means sign in again; `version-check` exiting 1 names the server
capability this CLI needs, so run `astro update` or ask the operator to upgrade
the server. Without a browser, `astro auth login --no-wait --json` prints
`login_url` and `session_id`; hand the URL to a person and finish with
`astro auth wait --session-id <session_id>`.

Configuration lives under `~/.config/astrolift/` (`$XDG_CONFIG_HOME/astrolift/`
when that variable is set): `config.yaml` holds the registered servers and the
current selection, `credentials/<server-slug>.yaml` holds each server's tokens.
Credential and recovery files must be POSIX regular files with mode `0600`, or
Windows files owned by the current process user with a protected non-inherited
DACL allowing only that user and optionally `LOCAL_SYSTEM`. Windows reads check
the actual handle's owner and ACL and reject broad/inherited permissions and
final reparse-point targets. An explicit login can privately replace a
current-user-owned legacy Windows credential file without reading it; ordinary
reads refuse that file. Recovery files are never automatically replaced.
Flags override `ASTROLIFT_*` environment variables, which override the config
file.

Global flags are `--api-url`, `--server`, `--token`, `--org`, `--team`,
`--project`, `--app`, `--json`, `--no-color`, `--no-prompt`, and `--debug`.
`--server <slug>` addresses one registered server for a single command without
changing the saved selection; an `--api-url` override must match it. Prefer
`--token`, `ASTROLIFT_TOKEN`, or `ASTROLIFT_DEPLOY_TOKEN` only in ephemeral
automation; do not put a token in a repository or command transcript. An
explicit `--token`/`ASTROLIFT_TOKEN` overrides stored credentials.

Environment variables the CLI reads:

| Variable | Read by |
|---|---|
| `ASTROLIFT_API_URL` | every command; overrides the selected server's API URL |
| `ASTROLIFT_TOKEN` | every command; same precedence as `--token` |
| `ASTROLIFT_DEPLOY_TOKEN` | every command; a deploy token that bypasses stored credentials (required by `ci deploy` and `ci render`) |
| `ASTROLIFT_APP_SLUG` | `ci deploy`, `ci render` (required) |
| `ASTROLIFT_IMAGE_TAGS` | `ci deploy` (required); JSON map workload to tag |
| `ASTROLIFT_IMAGE_TAG` | `ci render`, single tag to render against |
| `ASTROLIFT_ENVIRONMENT`, `ASTROLIFT_BRANCH` | `ci deploy`; default `production` and `main` |
| `ASTROLIFT_COMMIT_SHA`, `ASTROLIFT_IDEMPOTENCY_KEY`, `ASTROLIFT_DEPLOY_TIMEOUT` | `ci deploy`; commit falls back to `git rev-parse HEAD`, timeout is a Go duration (default 30m) |
| `ASTROLIFT_NO_UPDATE_CHECK` | set to `1` to silence the once-a-day newer-release hint on stderr |
| `ASTRO_GITHUB_TOKEN`, `GITHUB_TOKEN`, `GH_TOKEN` | `update`/`self-update`; optional, raises the GitHub API rate limit |
| `ASTRO_GITHUB_API_URL` | `update`/`self-update` and `scripts/install.sh`; GitHub API mirror |
| `XDG_CONFIG_HOME` | config directory root |
| `KUBECONFIG` | `cluster bootstrap` |

`ASTROLIFT_TRIGGER_PAYLOAD` is set on an agent task's container by the
platform, not read by the CLI.

## Discover commands

The command's own help is generated from the same Cobra tree that executes it:

```bash
astro --help
astro agent --help
astro agent env-spec upsert --help
astro workflow --help
```

Use `--json` for stable machine-readable data. Normal data is written to
stdout and diagnostics to stderr. Exit `0` means success and `1` means the
command failed, including usage errors; `astro ci deploy` and `astro ci render`
exit `2` for a missing or invalid CI environment variable.

### Command groups

| Group | Purpose |
|---|---|
| `agent` | Dispatch, inspect, log, steer and cancel agent tasks; `env-spec`, `secret`, `callbacks`, `quarantine`, `session attach`, `workloads`, `register-repo`, `run`, `show`, `usage`, `vnc`, `send`, `input-receipt` |
| `alert` | `alert list` (alert rules; `--all` includes inactive) |
| `api` | `api graphql`: run a GraphQL document with stored credentials |
| `app` | App lifecycle (`init`, `register`, `deploy`, `rollback`, `promote`, `list`, `show`, `logs`, `exec`, `pods`, `extend`, `expiry`, `deregister`, `set-build-mode`, `set-build-context`) and sub-resources (`access`, `domains`, `environments`, `events`, `manifest`, `previews`, `secrets`, `services`, `tasks`, `workload`) |
| `apply` | `apply -f <file.toml>`: declarative `[[env_spec]]` create/update |
| `audit` | Zentinelle agent evidence and audit stream (`tail`, `export`) |
| `auth` | Device-flow `login`, `wait`, `logout`, `status`, `refresh` |
| `auth-users` | Users and groups behind a cluster's central auth |
| `box` | Agent-boxes: `ensure`, `ls`, `rm`, `attach`, `images` |
| `brain` | Semantic layers (Mimwell brains): `list`, `show`, `status`, `connect`, `create`, `share`, `unshare`, `host-app`, `rebuild`, `substrate`, `delete` |
| `ci` | Non-interactive `deploy`, `status`, `render`, `setup` |
| `cluster` | `cluster bootstrap`: helm install of the bundled prerequisites chart |
| `completion` | Shell completion scripts |
| `dev` | Local development utilities (`dispatch-stub start`) |
| `docs` | Offline reference: `list`, `show`, `search`, `export`, `man` |
| `exec` | Command or shell in a running container |
| `inbox` | Agents' messages addressed to you: `list`, `show`, `reply`, `follow` |
| `onboard` | Set an AI coding agent up against the install: MCP config, skills, offline docs |
| `operator` | `operator cluster` (`install-agent`, `agent-install-status`, `deploy-agent`, `log-collector-review`, `install-log-collector`, `log-collector-status`) and `operator domains` (`list`, `show`, `check`, `lookup`, `probe`, `create`, `update`, `verify`) |
| `org`, `team`, `project` | Organization selection (`org list`, `org use`, `org current`, `org show`), teams and projects; `project resources` manages shared services |
| `perms` | `perms list` (bundled permission catalogue, offline) and `perms diagnose` |
| `pipeline` | Pipelines: `list`, `run`, `runs`, `show`, `logs`, `cancel`, `reconcile` |
| `plan` | `plan ls`, `plan show`: a brain's committed task DAG |
| `policy` | Zentinelle policies in effect (`ls`, `show`) |
| `role` | Roles and bindings: `list`, `show`, `create`, `update`, `bindings`, `grant`, `revoke` |
| `scm` | `scm list`, `scm disconnect <id>` for source-control connections |
| `server` | Registered servers: `add`, `list`, `use`, `remove` |
| `skill` | Organization skill catalog: `list`, `inspect`, `import`, `repo` |
| `status` | Platform status snapshot from public install discovery |
| `update`, `self-update` | Install the latest release; preview it without installing |
| `version`, `version-check` | CLI version; whether the selected server supports this CLI |
| `whoami` | Verified current user, server and organization |
| `workflow` | Workflow TOML authoring and the definition, run, execution, gate and schedule lifecycle |
| `zentinelle` | Zentinelle connection (`connect`, `disconnect`, `status`, `gateway-url`) and per-cluster gateways (`cluster register|enable|disable|rotate|unregister`) |

`audit`, `policy` and `agent usage` are listed by `--help` only when the
working organization is connected to Zentinelle; they still run, and refuse in
one line, otherwise.

## Common app flow

```bash
astro app init
astro app register --project-id <guid> --source-repo owner/repo
astro app deploy --image-tag sha-abc1234 --wait
astro app show
astro app logs -f
astro app exec -- sh
```

Run these in the repository root. `<guid>` is the project's GUID from
`astro project list --json`; `owner/repo` is the source repository the
platform's source connection can read; `sha-abc1234` is a tag your CI already
pushed (platform-built apps omit `--image-tag`). Expected output: `Created
astrolift.toml`; `Registered app:   <name> (<slug>)` with the ID, project and
namespace; `Deployment started: <id>` followed by status transitions and `Final
status: running`. `Final status: failed` prints a `Reason:` line; read it, then
`astro app logs` for the workload. `astro app init` refuses to overwrite an
existing file (`astrolift.toml already exists`). The app slug is resolved from
`--app` and then the local `astrolift.toml` for later commands; `app logs
[workload]` and `app exec [workload]` take a workload, not the app, as their
positional. The full first-deploy walk-through, build modes and manifest
reference live in [app setup](../guides/app-setup.md) and
[`astrolift.toml`](astrolift-toml.md).

## Reprovision an app whose host does not answer

```bash
astro app show --app <slug>
astro app reprovision --app <slug> --env production --wait
```

`astro app show` and `astro app list` print a reachability next to the status
on servers that report it: `reachable`, `provisioning`, `needs reprovision` or
`no managed domain`. Deployed means the workload rolled out; reachable means
every public host has the Ingress or route, a Ready certificate, a hostname
claim and an environment URL behind it. When an app reads `needs reprovision`,
or its pods run while the public host answers the ingress controller's default
`404`, those platform resources are missing or drifted. `reprovision`
re-renders them from the current manifest and applies them against the running
image: no build, no image change, nothing deleted, and no restart unless the
rendered workload changed. Without `--env` every non-preview environment is
reprovisioned. Expected output is one line per environment with its
reachability and the objects created and updated, or `unchanged` for a healthy
environment. `--wait` polls the app's reachability until it is `reachable`
within `--timeout` (default `5m`) and exits 1 on a timeout, on `needs
reprovision` or on `no managed domain`. It needs the `app.deploy` permission
and no step-up; it is audited as `app.reprovision`.

`no managed domain` is not fixed by reprovisioning: no managed DNS zone
resolves for the organization, so there is no hostname to provision. An
operator restores or creates the platform default zone (`astro operator domains
list`, or Running an organization, Infrastructure), runs the managed-domain
binding backfill from the Domains screen, and then reprovisions the apps that
read `needs reprovision`. The dashboard shows the same states on the app header,
the Apps list filter and the URL card, which carries a Reprovision button.

!!! note "Agent notes"
    Success check: the `--wait` run ends with `reachable` and `curl -sI
    https://<host>/` answers with the app or with a `302` to the install's
    sign-in, not the ingress default `404`. Failure signatures: `needs
    reprovision` after the run means the apply finished but a host still lacks
    an object; read the `reachabilityDetail` with `astro app show --json`.
    `server does not offer reprovision` means an older platform; `astro app
    deploy --image-tag <running tag> --wait` rebuilds the same resources.
    A `PRECONDITION` refusal names the cause: deploys paused, a deployment in
    flight, a native endpoint environment, or an approval-gated environment
    whose manifest changed since the running deployment. Reprovision is not
    Force redeploy, which tears the environment down and recreates it.

## Common agent flow

```bash
astro org list --json
astro org use <organization-slug>
ASTROLIFT_ORG_ID="$(astro org current --json | python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])')"

astro agent register-repo owner/agents --project-id <guid> --ref main

astro agent env-spec upsert triage-prod \
  --agent-type claude \
  --runtime claude-code-vnc \
  --config-repo owner/agents \
  --manifest-path agents/triage/astrolift.toml \
  --secret "ANTHROPIC_API_KEY=secret://agents/${ASTROLIFT_ORG_ID}/anthropic"

printf '%s' "$ANTHROPIC_API_KEY" | \
  astro agent secret set triage-prod ANTHROPIC_API_KEY --stdin

astro agent dispatch triage --env-spec triage-prod \
  --input @input.json --tail
```

The organization `id` returned by `astro org current --json` is its GUID.
Agent secret references must use `secret://agents/<organization-guid>/<name>`
for the same organization selected by the command; a slug or an unscoped
`secret://agents/anthropic` is rejected. The GUID is non-secret metadata.

Packaging, registration and dispatch are explained in
[agent setup](../guides/agent-setup.md); this page only lists the commands.
Secret declarations are references. `secret set` writes the value to the
installation's secret backend and never prints it. `astro agent cancel <task>`
hard-stops the spawned workload as well as transitioning the task.

## Workflow flow

```bash
astro workflow init --pattern chained -o workflows/triage.toml
astro workflow validate workflows/triage.toml
astro workflow validate workflows/triage.toml --server
astro workflow import workflows/triage.toml --preview
astro workflow import workflows/triage.toml
```

Import creates a disabled definition. Review it and run
`astro workflow definition-enable <definition-slug>` before executing a
configured workflow that uses it. For binding and a complete first-run flow,
see [workflow setup](../guides/workflow-setup.md). Agent-repo registration also
reconciles every `workflows/**/*.toml` file.

## Project shared resources

The selected cluster supplies the provider catalogue. Shared project resources
and app-owned services have separate lists. See [shared services](../guides/shared-services.md)
for bounded metadata pages, exact GUID review and permission-checked actions.

```bash
astro project resources catalog --project platform --cluster production
astro project resources add --project platform --cluster production \
  --kind postgres --variant rds --name report-db --size medium \
  --config @database.json --agent report-prod
astro project resources list --project platform --json
```

Since CLI v0.9.0, `list --page --json` returns `{items,totalCount,nextCursor}`
(`--after` continues a page, `--limit` accepts 1 through 200); default JSON
remains an array and refuses an incomplete walk. `show` rereads the exact GUID; `attachments` pages visible
consumer identities. Existing-resource writes carry the
reviewed context revision. `detach <attachment-guid>` resolves its exact visible
owner and rereads that resource with the captured revision; `--resource <GUID>`
can pin the owner explicitly. Missing or inaccessible attachments are refused.
Project writes need both project RBAC and token scope `project:write`.


## CI

```bash
export ASTROLIFT_API_URL=https://astrolift.example.com
export ASTROLIFT_DEPLOY_TOKEN="$ASTRO_TOKEN"
export ASTROLIFT_APP_SLUG=my-app
export ASTROLIFT_IMAGE_TAGS='{"web":"sha-abc1234"}'
astro ci deploy
```

Pin the CLI version. A workflow should use a concurrency group keyed by the app
and environment with `cancel-in-progress: true` when a newer commit makes an
older build/deploy irrelevant.

## Domain checks

`astro operator domains list` discovers visible zones; `show <id>` reads an
exact GUID. `check <id>` reports provider records, public delegation and recorded
app routes. `lookup <id>` (alias `dig`) accepts `--hostname` and `--record-type`;
`probe <id>` accepts `--hostname` and `--tool https|ping|traceroute`. Each check
uses the selected organization/server and a fresh domain version. Observations
come from the server, and unsupported tools or mismatches remain explicit.
No diagnostic changes DNS. See [Domains and DNS](../guides/domains-dns.md) or
`astro docs show domains` for the full contract.

## Temporary apps and the agent inbox

```sh
astro app register --project-id <guid> --source-repo owner/repo --ttl 7d
astro app extend demo --by 2d
astro app extend demo --until 2026-12-01T12:00:00Z
astro app expiry demo --clear --yes
astro app list --temporary
astro inbox list --unread --limit 25
astro inbox show <message-guid> --thread <thread-guid> --mark-read
astro inbox reply <message-guid> --choice continue --request-id <uuid>
astro inbox follow --thread <thread-guid> --json
```

`--ttl` or `--expires-at <RFC3339>` on `app register` and `brain host-app`
creates a temporary app; registration also honors `[app] expires_in` in
`astrolift.toml`. Expiry writes require the server's `apps.temporary_expiry`
capability. The inbox requires `agents.messages` and the person's own
credentials; `reply` prints its request UUID before submission, and the same
`--request-id` recovers a lost response without resending. See
[temporary apps](../guides/temporary-apps.md) and the
[agent inbox](../guides/agent-inbox.md). Both shipped in CLI v0.16.0.

## Offline documentation and man pages

Released binaries embed a release-matched Markdown snapshot:

```bash
astro docs list
astro docs show manifest
astro docs show mcp > mcp.md
astro docs export ./astrolift-docs
astro docs man ./man/man1
```

`docs export` includes the topic guides, a Markdown command reference generated
from the live command tree, and `man/man1/*.1`. It refuses to overwrite an
existing generated file unless `--force` is supplied.

To install generated man pages for one user:

```bash
astro docs man "$HOME/.local/share/man/man1"
man -M "$HOME/.local/share/man" astro
```

Release archives include generated man pages. Packagers can also run `astro
docs export` during packaging without network access.

### Search the offline guides

CLI releases containing `docs search` can find setup and contract guidance
without a network connection, credential or configured server:

```sh
astro docs search 'workflow recovery'
astro docs search '"request file"' --limit 5 --json
astro docs search 'environment exec'
astro docs show reviewed-starts
```

All unquoted terms must occur somewhere in the guide or its topic/title.
Matching is case-insensitive; double quotes group a contiguous phrase with
normalized whitespace. Results follow the guide catalogue order, one match per
guide, and include the source line and a snippet of at most 180 characters.
`--limit` defaults to 10 and accepts 1 through 50. JSON includes `matches`,
`totalMatches` and `truncated`; increase the limit or refine the query when
truncated. Queries accept at most 512 UTF-8 bytes and 32 terms or phrases.
Search covers the public Markdown snapshot embedded in that binary, not live
installation data or the separately generated command reference. Use `--help`
for commands and `docs show <topic>` to read the complete matching guide.
Offline search, reviewed starts and explicit environment controls shipped in
CLI v0.8.0; older releases lack them. Check `astro version` and `astro --help`.

## Completion callbacks and setup guides

```bash
astro agent callbacks configure --allow-host hooks.internal.example.org
astro agent callbacks secret-set completion-key --file /secure/path/completion-key
astro agent dispatch report --callback-url https://hooks.internal.example.org/tasks \
  --callback-secret-ref completion-key --correlation-id request-42 --callback-mode NOTIFY
astro agent inspect <task-guid> --json
astro agent callbacks redeliver <task-guid> --json
astro docs show callbacks
astro docs show capabilities
astro docs show organization
astro docs show domains
astro docs show email-delivery
astro docs show app-setup
astro docs show agent-setup
astro docs show workflow-setup
astro docs show shared-services
astro docs show preview-targets
astro docs show workload-signals
astro docs show logs-traces
```

Callback commands require a compatible server and CLI release. See the
[callback guide](../guides/agent-completion-callbacks.md) for authorization,
signing-key setup, durable backoff, and receiver verification.

## Reviewed starts and exact environments

```sh
astro workflow definition-review <definition-guid> --json
astro workflow definition-start <definition-guid> --inputs-file ./inputs.json \
  --request-file ./definition-request.json --yes --json
astro workflow definition-reconcile --request-file ./definition-request.json --json
astro pipeline run <pipeline-guid> --request-file ./pipeline-request.json --yes --json
astro pipeline reconcile --request-file ./pipeline-request.json --json
astro pipeline show <run-guid> --json
astro pipeline cancel <run-guid> --yes --json
astro docs show reviewed-starts
astro docs show environment-actions
```

These commands shipped in CLI v0.8.0 and need compatible server contracts.
Review the
[start and recovery guide](../guides/reviewed-starts.md) before dispatch: an
existing Definition request file never reads/resubmits inputs, whereas a pipeline
file can resubmit its original metadata key after a successful null recovery,
unchanged version review and confirmation. Both preserve uncertain identities.
Omit `--inputs-file` for a no-input definition. Exact environment controls and
shell admission are documented in the
[environment guide](../guides/environment-actions.md).

`astro agent run` is an alias for this same reviewed Definition service: pass
an exact definition GUID, `--request-file`, `--yes`, and
`--inputs-file` for an input-bearing schema. For a new request, `--input` accepts
only `@file` as a compatibility alias; literal JSON is refused. Existing request
files recover read-only and ignore input flags without reading their files or
parsing their values. `--wait` reads metadata for the
exact execution and pinned engine identity and preserves one JSON receipt.
See the [migration guide](../guides/agent-setup.md#migrate-a-legacy-workflow-definition-run).
Direct agent-task dispatch and app-bound configured workflow runs are separate.


## Read environment telemetry through the API

The [logs and traces guide](../guides/logs-traces.md) contains read-only GraphQL
queries for exact environment history and scoped trace envelopes. Submit one
operation using `astro api graphql --file query.graphql --vars-file vars.json --json`
against a compatible server. Existing CLI app/preview logs remain historical
polling. The `logs-traces` offline topic is embedded since CLI v0.10.1.

## Server-owned cluster keep-alive installation

The CLI implements remote installation with the
[server-owned installation contract](../guides/cluster-agent-install.md)
(since CLI v0.12.0; the server must advertise
`clusters.reviewed_agent_install`):

```sh
astro operator cluster install-agent --cluster-id CLUSTER_GUID --request-file ./agent-install.json --json
astro operator cluster agent-install-status --install-id INSTALL_GUID --cluster-id CLUSTER_GUID --json
astro docs show cluster-agent-install
```

Installation requires exactly one of `--cluster-id` or `--slug`. A known canonical
nonzero GUID skips inventory discovery and still requires scoped `cluster.manage`
authority, current organization membership and the credential ceiling. Shared
platform clusters also require the platform-operator gate. Slug discovery
additionally requires `cluster.register`; use the known GUID with narrow cluster-scoped credentials. Optional `--cluster-id` on status
commands refuses a different returned target before printing a receipt. An
existing slug-selected request file can be recovered using its exact stored GUID
without changing the original file bytes or tuple.

Retain the same private request file and original tuple after a lost reply.
The CLI refuses local kubeconfig fallback; status reads distinguish queued
acceptance from authenticated heartbeat confirmation.

### Reviewed CloudWatch collector commands

These server-owned operations shipped in CLI v0.12.0:

```sh
astro operator cluster log-collector-review --cluster-id CLUSTER_GUID --retention-days 30 --json
astro operator cluster install-log-collector --cluster-id CLUSTER_GUID --request-file ./collector-install.json --json
astro operator cluster log-collector-status --operation-id OPERATION_GUID --cluster-id CLUSTER_GUID --json
astro docs show cluster-log-collector
```

The CLI requires `clusters.reviewed_log_collector_install` from public discovery, then
current authenticated target authority. The same exclusive GUID/slug selection
and optional status target check apply. Preserve the private original actor,
server, organization, cluster GUID/version/source, retention and request UUID on
replay. Only `ACTIVATED` with post-loss timestamps establishes installation; it
is not ongoing health or tracing. See the [collector guide](../guides/cluster-log-collector.md).

## Shared model hosting knowledge

`astro docs show model-hosting` describes admin-gated hosting from Hugging Face
or immutable local artifacts, pinned vLLM 0.15.1 checks, prerequisites and app
subscriptions. This offline topic is embedded since CLI v0.12.0;
`astro docs list` reflects the executing binary. There are no native model
management commands. Submit bounded metadata reads through existing
`astro api graphql --file query.graphql --vars-file vars.json --org YOUR_ORG --json`.
See [model hosting](../guides/model-hosting.md) for tested document shapes and
secret-free selections; do not print connection tokens or upload capabilities.

## Human gate decisions

Use `astro workflow gates --json` for public run/gate GUIDs and the gate's captured
Temporal identity. `astro workflow gate --run <guid> --stage <guid>
--temporal-run <id> --decision approve|reject --yes` submits the user's explicit
choice. `astro workflow gate-status --run <guid> --stage <guid> --json` recovers
read-only. Requested and recorded outcomes are distinct; retain the exact target
from JSON/error output after a lost response. The former slug/newest-gate flow
is replaced. See [workflow setup](../guides/workflow-setup.md).

## Deployment status and release updates

`astro ci deploy --no-wait` prints both the workflow and deployment IDs.
Use the deployment GUID with `astro ci status <deployment-id>`; the workflow ID
is not a status lookup key. Add `--json` for the state object.

`astro self-update` previews the latest GitHub release without replacing the
binary. It is an alias for `astro update --dry-run`; `astro update` installs it.
These commands do not require an Astrolift sign-in.

## Read Zentinelle without an admin credential

```sh
astro auth login --scope zentinelle
astro zentinelle status
astro agent usage
astro policy ls
astro audit tail
```

The browser requests the usual CLI scopes plus `read:zentinelle`. That addition
allows status, policy, token-usage, conformance and audit reads, subject to the
account's existing permissions. It adds no export or write permission. Audit
export and administration still require their own permission and an admin
credential. Default and cluster logins keep their existing scopes.

This login requires a server advertising `identity.zentinelle_read_scope`.
The CLI refuses an older server before opening the login flow rather than
accepting a token without the requested scope. `--scope zentinelle` shipped in
CLI v0.16.0. The other scopes are `--scope clusters`, which adds updating and
operating clusters, and `--scope admin`, full administrative access that only
the platform operator can approve and that lasts 12 hours.

## Inspect a brain plan and dispatch one story

```sh
astro plan ls --brain product-brain --source ./product-brain
astro plan show build/parser --brain product-brain --source ./product-brain --json
astro agent dispatch builder --spec build/parser --brain product-brain \
  --source ./product-brain --issue 123 --dry-run
```

The local checkout's origin must match the registered brain's source repository.
Reads use committed Git objects, so uncommitted edits are not dispatched. The
output records the exact commit. Set `--revision <commit>` to select another
snapshot. Alternatively, `--connection <id> --revision <full-commit-sha>` reads
through an authorized Astrolift source connection without a local checkout.

The plan includes stories only, resolves container dependencies to their stories,
and refuses unresolved references or cycles. Ready means the committed story is
planned, has one target-repository label, and its dependencies are done. Running,
blocked and done are the states declared in that snapshot. This view does not
claim live scheduler state or acquire a concurrency lease.

Spec dispatch compiles the story, acceptance checklist, context recipe, linked
documents, target repository/branch and explicit test commands into the existing
agent input envelope. `--dry-run` prints it without dispatching. Remove that flag
to dispatch through normal agent permissions and the selected environment spec.
Use `--branch`, `--issue` and `--attempt` for the task's review identity. The source
must include its compiled `app/brain.json` and `brain-schema.json`; a missing spec
or a story without an acceptance test command is refused.

The envelope does not authorize a merge, make a story done, or replace the
server-side factory scheduler and its WIP limits. Review the selected snapshot
and keep using the normal workflow and repository gates.
