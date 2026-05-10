# Astrolift CLI (`astro`)

Command-line interface for **[Astrolift](https://astrolift.app)** — the BYOC runtime layer for the Calliope AI ecosystem.

`astro` is the operator and developer entry point for Astrolift installs:
provision a new install, manage tenants and workloads, deploy templates, and
drive the platform from CI.

## Quick Start

```bash
docker pull calliopeai/astrolift-cli:latest
docker run --rm calliopeai/astrolift-cli:latest version
```

### Use against your install

```bash
docker run --rm -it \
  -v $HOME/.config/astrolift:/home/nonroot/.config/astrolift \
  calliopeai/astrolift-cli:latest \
  workloads list --server=https://your-astrolift-install.example
```

Credentials live at `~/.config/astrolift/credentials/<server>.yaml` (mode `0600`).

## Other install methods

| Method | Command |
|--------|---------|
| Homebrew | `brew install calliopeai/tap/astro` |
| Scoop (Windows) | `scoop bucket add calliopeai https://github.com/calliopeai/scoop-bucket && scoop install astro` |
| curl \| sh | `curl -fsSL https://astrolift.app/install.sh \| sh` |
| Direct binary | [GitHub Releases](https://github.com/calliopeai/astrolift-cli/releases) |

## Tags

| Tag | Architecture | Description |
|-----|--------------|-------------|
| `latest` | multi-arch | Latest tagged release |
| `X.Y.Z` | multi-arch | Specific release |
| `X.Y.Z-amd64` / `X.Y.Z-arm64` | single-arch | Per-architecture images |
| `main` / `main-<sha>` | multi-arch | Latest main branch build (unstable) |

## Source

- Repo: [github.com/calliopeai/astrolift-cli](https://github.com/calliopeai/astrolift-cli)
- Project: [astrolift.app](https://astrolift.app)
- License: MIT

Part of the **Calliope AI** platform: [calliope.ai](https://calliope.ai)
