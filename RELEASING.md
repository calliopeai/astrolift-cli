# Releasing the Astrolift CLI

## Cadence policy

Releases are **deliberate, not automatic**. Merging to `main` publishes a
container image (`:main-<sha>`, `:latest`) but does **not** create a release.

Tag-on-merge was considered and rejected (#46): it burns a version number on
every README typo, makes the version space meaningless, and removes the moment
where someone asks "is this actually good to ship?". Instead there is one
button, and someone has to press it.

The failure this policy is guarding against is the opposite one, though — the
CLI sat on v0.1.1 for two months while `main` shipped whole feature areas, so
users hit removed-message errors against a current API. **Cut a release
whenever user-visible surface lands.** If you are unsure, cut one; a patch
release is cheap, a two-month-old binary is not.

## Before you tag

1. `main` is green.
2. Add or promote a `## X.Y.Z` heading in `CHANGELOG.md` and merge it first.
   Include the user-visible changes and compatibility requirements. The release
   workflow refuses a version with no changelog heading.
3. Pick the version by what changed — pre-1.0, breaking CLI surface changes
   bump the minor.

## Cut the release

One command:

```bash
gh workflow run release.yml --repo calliopeai/astrolift-cli -f version=0.4.0
```

Add `-f dry_run=true` to run every check without creating the tag.

That workflow freezes one main commit and requires native Windows ACL and saved-request
recovery tests on that exact commit before tagging. It validates the version, refuses to reuse an existing tag, requires
the changelog entry, verifies the complete pinned prerequisite-chart inventory
and actual Helm renders, runs build/vet/test, and proves the release ldflags still
stamp a real version into the binary — then creates and pushes the annotated
tag. Pushing the tag is what triggers `build-publish.yml`, which runs
GoReleaser after Linux verification and the native Windows private-file gate pass
on the tagged commit: cross-platform archives, `astro-checksums.txt`, the GitHub release,
Docker Hub + GHCR images, and a smoke test that downloads the published assets
back and runs them.

The equivalent by hand, if the workflow is unavailable:

```bash
git checkout main && git pull
git tag -s v0.4.0 -m "astro v0.4.0"
git verify-tag v0.4.0
git push origin v0.4.0
```

> Tags must be pushed with a real user credential or `PAT_CALLIOPE_CI`. GitHub
> does not start workflow runs from events raised by the default
> `GITHUB_TOKEN`, so a tag pushed by a workflow using it would never build.

The current dispatch workflow creates an unsigned annotated tag. When a signed
release tag is required, use the manual path after the same checks pass on the
exact `main` commit. Never move an existing release tag.

## After the tag

```bash
gh run list --repo calliopeai/astrolift-cli --workflow build-publish.yml --limit 3
gh release view v0.4.0 --repo calliopeai/astrolift-cli --json assets --jq '.assets[].name'
```

Expect exactly these assets:

```
astro-checksums.txt
astro-darwin-amd64.tar.gz   astro-darwin-arm64.tar.gz
astro-linux-amd64.tar.gz    astro-linux-arm64.tar.gz
astro-windows-amd64.zip     astro-windows-arm64.zip
```

Then confirm the stamp made it into the artifact:

```bash
docker run --rm calliopeai/astrolift-cli:0.4.0 version
# astro 0.4.0 (commit <sha>, built <timestamp>)
```

If that prints `astro dev`, the ldflags broke again — see the guard tests in
`cmd/release_config_test.go`.

## Rolling back

Releases are immutable; never move or delete a published tag. Fix forward with
the next patch version. If a release is actively harmful, mark it as a
pre-release so `:latest` consumers stop resolving to it, and cut the fix.

## Distribution channels

| Channel | Public? | Notes |
|---|---|---|
| Docker Hub `calliopeai/astrolift-cli` | **Yes** | Multi-arch, no credentials needed |
| GHCR `ghcr.io/calliopeai/astrolift-cli` | **Yes** | Multi-arch mirror of the same build |
| GitHub release archives | **Yes** | Public source repository and public releases |
| Homebrew tap / Scoop bucket | Disabled | No formula or manifest publication is configured |

Release archives and source are publicly readable from
[GitHub Releases](https://github.com/calliopeai/astrolift-cli/releases).
`scripts/install.sh` supports anonymous GitHub API downloads when no credentials
are configured. Mirrors, an authenticated `gh`, and explicit tokens remain
supported. Anonymous metadata and asset requests omit Authorization, and the
archive checksum is verified before installation. Pin `ASTRO_INSTALL_TAG` when
reproducible installs are required. See the
[CLI install reference](https://astrolift.dev/reference/cli/#install).

After publication, download the archives and `astro-checksums.txt` anonymously,
verify each SHA-256 checksum, and run `astro version` from the host archive.
Check that callback commands and offline topics are present and that the archive
contains `share/doc/astrolift` and `share/man/man1`. CI also downloads published
Linux and macOS assets, verifies checksums, and runs the version-stamp smoke test.
Checksums establish archive integrity; no detached asset signatures are currently
configured. A signed Git tag records the reviewed source revision separately.

Homebrew/Scoop automation and a vanity installer URL remain separate distribution
work. The canonical installer currently lives in the source repository; do not
advertise an unverified `astrolift.dev/cli/install.sh` endpoint.
