# Getting Started

This guide walks you through installing the Astrolift CLI and deploying your first application.

## Install the CLI

The CLI release repository is private. Authenticate GitHub CLI with an account
that can read `calliopeai/astrolift-cli`, then download the release archive and
checksum file for your platform:

```bash
gh auth login
tag="$(gh release view --repo calliopeai/astrolift-cli \
  --json tagName --jq .tagName)"
gh release download "$tag" --repo calliopeai/astrolift-cli \
  --pattern 'astro-darwin-arm64.tar.gz' \
  --pattern 'astro-checksums.txt'
shasum -a 256 -c <(grep '  astro-darwin-arm64.tar.gz$' astro-checksums.txt)
tar -xzf astro-darwin-arm64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 0755 astro "$HOME/.local/bin/astro"
astro version
```

That example is for Apple silicon. The [CLI install
reference](reference/cli.md#install) lists every release asset and the Linux
checksum command. Homebrew, Scoop, anonymous `curl`, and `go install` cannot
fetch this private repository; use the authenticated release path until a
public distribution channel is available.

## Authenticate

```bash
astro server add prod https://astrolift.example.com
astro auth login
```

This opens a browser window to complete authentication.

## Create a manifest

```bash
cd my-first-app
astro app init
```

Review the generated `astrolift.toml`, then commit and push it to the branch
Astrolift will register. Registration fetches the manifest from the source
provider; an unpushed local file is not visible to the control plane. The
[`astrolift.toml` reference](reference/astrolift-toml.md) documents every
field the current control-plane parser consumes.

## Deploy

```bash
astro app register --project-id <project-guid> --source-repo owner/my-first-app
astro app deploy --image-tag sha-$(git rev-parse --short HEAD) --wait
```

The manual command deploys an image tag that already exists in the app's
registry, provisions declared managed services, and rolls out the application.
A managed source workflow normally builds and pushes that image before calling
the same deploy surface. Once rollout completes, the CLI prints the app URL.

## What's next

- Edit `astrolift.toml` to add managed services, configure scaling, or set up preview environments.
- Run `astro app show` to inspect your registration.
- Run `astro app logs -f` to stream application logs.
- Run `astro docs show manifest` for the release-matched offline reference.
