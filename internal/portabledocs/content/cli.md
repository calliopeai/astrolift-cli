# `astro` CLI reference

`astro` is a single Go binary. It is both the interactive developer client and
the non-interactive client used in CI.

## Install

```bash
# macOS or Linux
brew install calliopeai/tap/astro

# Installer, with OS/architecture detection
curl -fsSL https://raw.githubusercontent.com/calliopeai/astrolift-cli/main/scripts/install.sh | sh

# Source build
go install github.com/calliopeai/astrolift-cli@latest
```

Windows users can install from Scoop:

```powershell
scoop bucket add calliopeai https://github.com/calliopeai/scoop-bucket
scoop install astro
```

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
`--token` or `ASTROLIFT_DEPLOY_TOKEN` only in ephemeral automation; do not put a
token in a repository or command transcript.

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

Homebrew installs release man pages automatically. Packagers can run `astro
docs export` during packaging without network access.
