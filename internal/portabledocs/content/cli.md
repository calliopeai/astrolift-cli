# `astro` CLI reference

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

Add `~/.local/bin` to `PATH` when it is not already there. Use `sha256sum
-c` instead of `shasum -a 256 -c` on Linux. Available archives are:

| Platform | Release asset |
|---|---|
| macOS, Apple silicon | `astro-darwin-arm64.tar.gz` |
| macOS, Intel | `astro-darwin-amd64.tar.gz` |
| Linux, ARM64 | `astro-linux-arm64.tar.gz` |
| Linux, x86-64 | `astro-linux-amd64.tar.gz` |
| Windows, ARM64 | `astro-windows-arm64.zip` |
| Windows, x86-64 | `astro-windows-amd64.zip` |

Build from source with `git clone https://github.com/calliopeai/astrolift-cli.git`
followed by `make build`. Prefer the checksum-verified release archives for
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
`install`, and either `sha256sum` or `shasum`.

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
```

Configuration lives under `~/.config/astrolift/`. Credentials are stored per
server. The next CLI's private-file contract requires POSIX regular files with
mode `0600`, or Windows files owned by the current process user with a protected
non-inherited DACL allowing only that user and optionally `LOCAL_SYSTEM`.
Windows reads check the actual handle's owner and ACL and reject broad/inherited
permissions and final reparse-point targets. An explicit login can privately
replace a current-user-owned legacy Windows credential file without reading it;
ordinary reads refuse that file. Recovery files are never automatically replaced.
Use a CLI release containing these platform-specific protections; this wording
does not certify native Windows execution for an older release.
Flags override `ASTROLIFT_*` environment
variables, which override the config file.

Global flags include `--api-url`, `--token`, `--org`, `--team`, `--project`,
`--app`, `--json`, `--no-color`, `--no-prompt`, and `--debug`. Prefer
`--token`, `ASTROLIFT_TOKEN`, or `ASTROLIFT_DEPLOY_TOKEN` only in ephemeral
automation; do not put a token in a repository or command transcript. An
explicit `--token`/`ASTROLIFT_TOKEN` overrides stored credentials.

## Discover commands

The command's own help is generated from the same Cobra tree that executes it:

```bash
astro --help
astro agent --help
astro agent env-spec upsert --help
astro workflow --help
```

Use `--json` for stable machine-readable data. Normal data is written to
stdout and diagnostics to stderr. Exit `0` means success. The current CLI
returns `1` when a command fails, including usage or configuration errors.

## Common app flow

```bash
astro app init
astro app register --project-id <guid> --source-repo owner/repo
astro app deploy --image-tag sha-abc1234 --wait
astro app show
astro app logs -f
astro app exec -- sh
```

`astro app init` refuses to overwrite an existing file. The app slug is
resolved from `--app` and then the local `astrolift.toml` for later commands.

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

CLI v0.8.0 predates the additive resource-page and reviewed-context flags.
In a release containing those commands, `list --page --json` returns
`{items,totalCount,nextCursor}`; default JSON remains an array and refuses an
incomplete walk. `show` rereads the exact GUID; `attachments` pages visible
consumer identities. Cost is explicit. Existing-resource writes carry the
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
CLI v0.8.0 includes offline search, reviewed starts and explicit environment controls. Older releases may lack these commands; check `astro version` and `astro --help`.

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

These commands require a CLI release containing them and compatible server
contracts. They are not present in every older published release. Review the
[start and recovery guide](../guides/reviewed-starts.md) before dispatch: an
existing Definition request file never reads/resubmits inputs, whereas a pipeline
file can resubmit its original metadata key after a successful null recovery,
unchanged version review and confirmation. Both preserve uncertain identities.
Omit `--inputs-file` for a no-input definition. Exact environment controls and
shell admission are documented in the
[environment guide](../guides/environment-actions.md).

The next CLI adapts legacy `astro agent run` to this same reviewed Definition
service: pass an exact definition GUID, `--request-file`, `--yes`, and
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
polling. The `logs-traces` offline topic requires the CLI documentation snapshot
containing this guide; released v0.10.0 does not contain it.

## Server-owned cluster keep-alive installation

The prepared matching CLI implements remote installation with the
[server-owned installation contract](../guides/cluster-agent-install.md):

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

These semantics and the offline topic are absent from released v0.10.0. Retain
the same private request file and original tuple after a lost reply. The matching
CLI refuses local kubeconfig fallback; status reads distinguish queued acceptance
from authenticated heartbeat confirmation.

### Prepared reviewed CloudWatch collector commands

A matching future CLI/server pair exposes these server-owned operations:

```sh
astro operator cluster log-collector-review --cluster-id CLUSTER_GUID --retention-days 30 --json
astro operator cluster install-log-collector --cluster-id CLUSTER_GUID --request-file ./collector-install.json --json
astro operator cluster log-collector-status --operation-id OPERATION_GUID --cluster-id CLUSTER_GUID --json
astro docs show cluster-log-collector
```

These commands are not in released v0.10.0 or the separate v0.10.1 patch. The CLI
requires `clusters.reviewed_log_collector_install` from public discovery, then
current authenticated target authority. The same exclusive GUID/slug selection
and optional status target check apply. Preserve the private original actor,
server, organization, cluster GUID/version/source, retention and request UUID on
replay. Only `ACTIVATED` with post-loss timestamps establishes installation; it
is not ongoing health or tracing. See the [collector guide](../guides/cluster-log-collector.md).
