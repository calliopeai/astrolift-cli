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
  -v "$HOME/.config/astrolift:/home/nonroot/.config/astrolift" \
  calliopeai/astrolift-cli:latest \
  server add prod https://your-astrolift-install.example
docker run --rm -it \
  -v "$HOME/.config/astrolift:/home/nonroot/.config/astrolift" \
  calliopeai/astrolift-cli:latest app list
```

Credentials live at `~/.config/astrolift/credentials/<server>.yaml` (mode `0600`).
`astro auth login` opens a browser device flow; from a container, run
`auth login --no-browser` and open the printed URL yourself, or export
`ASTROLIFT_TOKEN` with an API token.

### Copy the binary into your own image

```dockerfile
FROM calliopeai/astrolift-cli:0.16.0 AS astro-cli
COPY --from=astro-cli /usr/local/bin/astro /usr/local/bin/astro
```

## Native binary install

Release archives (`astro-<os>-<arch>.tar.gz`, `.zip` on Windows) and
`astro-checksums.txt` are public on
[GitHub Releases](https://github.com/calliopeai/astrolift-cli/releases) and
download anonymously. `scripts/install.sh` in the source repository verifies
the checksum and installs the binary and man page; set `ASTRO_INSTALL_TAG` to
pin a release. Homebrew and Scoop are not published, and there is no
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
