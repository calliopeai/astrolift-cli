# Getting Started

This guide walks you through installing the Astrolift CLI and deploying your first application.

## Install the CLI

The CLI and platform source repositories are public. Download the release archive and
checksum file for your platform:

```bash
release_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' \
  https://github.com/calliopeai/astrolift-cli/releases/latest)"
tag="${release_url##*/}"
release_base="https://github.com/calliopeai/astrolift-cli/releases/download/${tag}"
curl -fL "$release_base/astro-darwin-arm64.tar.gz" -o astro-darwin-arm64.tar.gz
curl -fL "$release_base/astro-checksums.txt" -o astro-checksums.txt
shasum -a 256 -c <(grep '  astro-darwin-arm64.tar.gz$' astro-checksums.txt)
tar -xzf astro-darwin-arm64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 0755 astro "$HOME/.local/bin/astro"
astro version
```

That example is for Apple silicon. The [CLI install
reference](reference/cli.md#install) lists every release asset and the Linux
checksum command. This download path requires no GitHub account. The example
resolves the latest published tag once; pin an explicit tag in automation.

Success check: `astro version` prints the release tag you downloaded. If the
shell reports `command not found`, `$HOME/.local/bin` is not on your `PATH`.

## Authenticate

Replace `https://astrolift.example.com` with your install's URL, as given by
your platform operator. `prod` is a local nickname you choose; it is stored in
`~/.config/astrolift/config.yaml`.

```bash
astro server add prod https://astrolift.example.com
astro auth login
```

`astro server add` prints `Added server prod -> https://astrolift.example.com`
and, for the first server, `Set as current server.` `astro auth login` opens a
browser window to approve the CLI credential, prints `Waiting for
authentication...`, and ends with `Logged in to https://astrolift.example.com.`
On a machine without a browser, run `astro auth login --no-browser` and open the
printed URL elsewhere; `astro auth login --help` describes the unattended
`--no-wait` and `astro auth wait` pair.

Then select your organization and verify the current server and actor:

```bash
astro org list --json
astro org use <organization-slug>
astro whoami --json
astro status --json
```

`<organization-slug>` is the `slug` field of one entry in the `astro org list
--json` output. `astro org use` prints `Default organization set to <slug>
(<name>)`; `astro org current` shows the resolved organization afterwards.
`astro whoami` prints the `Server`, `Org`, `User` and `Email` lines for the
credential in use. If a later command fails with `api token scope does not
allow this permission`, the sign-in's scopes are the limit, not your role;
`astro auth login --help` lists the `--scope` options.

Approval does not create missing organization membership or target grants.
CLI device-flow authentication and sensitive-operation browser SSO elevation are
separate. If a browser mutation asks for recent authentication, follow its
supported method; see [SSO setup and refusal recovery](guides/reviewed-starts.md#browser-sso-setup-for-sensitive-operations).
Start a new ceremony after a stale or changed-session refusal rather than
reusing a login link. Keep original workflow/pipeline request files after an
uncertain write; [reviewed starts](guides/reviewed-starts.md) explains recovery.

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

`<project-guid>` is the `id` of a project in `astro project list --json`;
`owner/my-first-app` is the repository path on your source provider.
`astro app register` shows the detected build context and Dockerfile and asks
`Use this? [Y/n/edit]`; pass `--yes` in automation. On success it prints
`Registered app:   <name> (<slug>)`. The refusal `No managed cluster connected.
Visit /clusters and finish bringing a cluster into management before adding
apps.` means your organization has no managed tenant cluster yet; see
[Installing Astrolift](operators/install.md).

The default build mode is `ci_pushed`: your CI builds and pushes the image, and
`astro app deploy --image-tag` rolls out a tag that already exists in the
app's registry, provisions declared managed services, and rolls out the
application. Register with `--build-mode platform_build` to have the platform
build the registered branch in-cluster; then omit `--image-tag`. See
[Set up an app](guides/app-setup.md) for both paths and
[Temporary apps](guides/temporary-apps.md) for `--ttl`.

`astro app deploy` prints `Deployment started: <id>` and, with `--wait`,
each status transition until `Final status: running`. A deployment that ends
in `failed`, `superseded` or `rolled_back` exits 1 with `deployment ended in
"<status>"`; open the deployment in the dashboard for the failure detail.
`astro app show` lists each environment with its URL once the rollout is
running.

## What's next

- Edit `astrolift.toml` to add managed services, configure scaling, or set up preview environments.
- Run `astro app show` to inspect your registration.
- Run `astro app logs -f` to stream application logs.
- Run `astro docs show manifest` for the release-matched offline reference.
