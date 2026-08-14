# `astro` CLI reference

`astro` is a single Go binary. It is both the interactive developer client and
the non-interactive client used in CI.

## Install

The canonical CLI releases currently live in the private
`calliopeai/astrolift-cli` repository. Authenticate GitHub CLI with an account
that can read that repository:

```bash
gh auth login
gh auth status
```

Find the current immutable tag and download both the archive and its checksum.
For example, on Apple silicon:

```bash
tag="$(gh release view --repo calliopeai/astrolift-cli \
  --json tagName --jq .tagName)"
asset=astro-darwin-arm64.tar.gz
download_dir="$(mktemp -d)"
gh release download "$tag" --repo calliopeai/astrolift-cli \
  --pattern "$asset" \
  --pattern 'astro-checksums.txt' \
  --dir "$download_dir"

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

The public Homebrew tap and Scoop bucket cannot authenticate downloads from a
private GitHub release. Likewise, anonymous `curl` and `go install` cannot read
the private source repository. Do not advertise those as install paths until
the release assets are mirrored publicly or the repository visibility changes.
Repository collaborators can build from source after `gh repo clone
calliopeai/astrolift-cli` by running `make build`.

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
stdout and diagnostics to stderr. Exit `0` means success, `2` means invalid
usage/configuration where documented, and `1` means an operational failure.

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
astro agent register-repo owner/agents --project-id <guid> --ref main

astro agent env-spec upsert triage-prod \
  --agent-type claude \
  --runtime claude-code-vnc \
  --config-repo owner/agents \
  --manifest-path agents/triage/astrolift.toml \
  --secret ANTHROPIC_API_KEY=secret://agents/anthropic

printf '%s' "$ANTHROPIC_API_KEY" | \
  astro agent secret set triage-prod ANTHROPIC_API_KEY --stdin

astro agent dispatch triage --env-spec triage-prod \
  --input @input.json --tail
```

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

Agent-repo registration also reconciles every `workflows/**/*.toml` file.

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
