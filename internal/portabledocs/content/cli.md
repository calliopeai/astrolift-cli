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
The legacy `scripts/install.sh` still requires an authenticated GitHub CLI or
token unless `ASTRO_INSTALL_BASE_URL` supplies a mirror; it has no anonymous
GitHub fallback. The archive path above avoids that requirement.

## Configure and authenticate

```bash
astro server add prod https://astrolift.example.com
astro server use prod
astro auth login
astro auth status
```

Configuration lives under `~/.config/astrolift/`. Credentials are stored per
server and must be mode `0600`. Flags override `ASTROLIFT_*` environment
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

Project resources are provider-managed services shared by apps and agents in a
project. The selected cluster supplies the live catalogue; it includes both
provisionable drivers and visible roadmap entries with an explanation and
tracking issue.

```bash
astro project resources catalog --project emr-bug-triage --cluster production

astro project resources add --project emr-bug-triage \
  --cluster production \
  --kind postgres \
  --variant rds \
  --name triage-db \
  --size medium \
  --config @database.json \
  --agent emr-triage-intake

astro project resources list --project emr-bug-triage
astro project resources show triage-db --project emr-bug-triage
astro project resources attach triage-db --project emr-bug-triage \
  --app-env <app-environment-guid>
astro project resources detach <attachment-guid> --project emr-bug-triage
astro project resources update triage-db --project emr-bug-triage \
  --config @database.json
astro project resources reprovision triage-db --project emr-bug-triage
astro project resources remove triage-db --project emr-bug-triage --delete-data
```

`--config` accepts a JSON object or `@file.json`; `--size` is a portable preset
merged into that object. Use `--json` for automation. Planned or unavailable
catalogue entries are deliberately shown but cannot be provisioned. Resource
writes require both the caller's project RBAC grant and a token carrying the
`project:write` scope; a newly approved `astro auth login` session includes the
scope, but an older saved session must be refreshed or re-authenticated.

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
The published v0.7.2 CLI predates `docs search` and reviewed-start commands.

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
