# Astrolift CLI (`astro`)

Command-line interface for **[Astrolift](https://astrolift.ai)** — the BYOC runtime layer for the Calliope AI ecosystem.

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

### Copy the binary into your own image

```dockerfile
FROM calliopeai/astrolift-cli:0.3.0 AS astro-cli
COPY --from=astro-cli /usr/local/bin/astro /usr/local/bin/astro
```

This image is the only install path that needs no credentials — prefer it for
containers and CI.

## Native binary install

Native archives live in a **private** GitHub release repository, so they
cannot be downloaded anonymously; GitHub answers anonymous requests for a
private repo with 404 rather than 401. Authorized users can fetch them with
`gh release download --repo calliopeai/astrolift-cli`, or with a token:

```bash
GITHUB_TOKEN="$(gh auth token)" ./scripts/install.sh
```

Homebrew, Scoop, anonymous `curl`, and direct browser downloads are not
supported until those archives have a public distribution channel. There is no
`curl | sh` installer URL. See the
[CLI install reference](https://astrolift.dev/reference/cli/#install).

## Tags

| Tag | Architecture | Description |
|-----|--------------|-------------|
| `latest` | multi-arch | Latest tagged release |
| `X.Y.Z` | multi-arch | Specific release |
| `X.Y.Z-amd64` / `X.Y.Z-arm64` | single-arch | Per-architecture images |
| `main` / `main-<sha>` | multi-arch | Latest main branch build (unstable) |

## Source

- Repo: [github.com/calliopeai/astrolift-cli](https://github.com/calliopeai/astrolift-cli)
- Project: [astrolift.ai](https://astrolift.ai)
- Docs: [astrolift.dev](https://astrolift.dev)
- License: MIT

Part of the **Calliope AI** platform: [calliope.ai](https://calliope.ai)
