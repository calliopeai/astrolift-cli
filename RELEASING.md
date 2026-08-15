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
2. Promote the changelog: rename `## Unreleased` in `CHANGELOG.md` to
   `## X.Y.Z` and merge that first. The release workflow refuses to tag a
   version with no changelog heading.
3. Pick the version by what changed — pre-1.0, breaking CLI surface changes
   bump the minor.

## Cut the release

One command:

```bash
gh workflow run release.yml --repo calliopeai/astrolift-cli -f version=0.4.0
```

Add `-f dry_run=true` to run every check without creating the tag.

That workflow validates the version, refuses to reuse an existing tag, requires
the changelog entry, runs build/vet/test, and proves the release ldflags still
stamp a real version into the binary — then creates and pushes the annotated
tag. Pushing the tag is what triggers `build-publish.yml`, which runs
GoReleaser: cross-platform archives, `astro-checksums.txt`, the GitHub release,
Docker Hub + GHCR images, and a smoke test that downloads the published assets
back and runs them.

The equivalent by hand, if the workflow is unavailable:

```bash
git checkout main && git pull
git tag -a v0.4.0 -m "astro v0.4.0"
git push origin v0.4.0
```

> Tags must be pushed with a real user credential or `PAT_CALLIOPE_CI`. GitHub
> does not start workflow runs from events raised by the default
> `GITHUB_TOKEN`, so a tag pushed by a workflow using it would never build.

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
| GitHub release archives | **No** | Private repo; requires a token (#53) |
| Homebrew tap / Scoop bucket | Disabled | Cannot authenticate private downloads |

Because the source repository is private, release archives are readable only
with a GitHub token that can see it. GitHub answers anonymous requests for a
private repo with **404**, not 401, which is why an unauthenticated install
looks like a missing asset rather than an auth failure.

`scripts/install.sh` handles all three credential shapes: an authenticated
`gh`, a `GITHUB_TOKEN`/`GH_TOKEN` environment variable, or an
`ASTRO_INSTALL_BASE_URL` mirror.

## Open decisions for the repository owner

These are deliberately unresolved in code; they need a call from the owner.

1. **Should releases be public?** Today every documented binary install path
   requires a token. Publishing archives to a public mirror (or making the repo
   public) would re-enable Homebrew, Scoop, anonymous `curl`, and `go install`.
   Until then, the token requirement is the documented reality.
2. **Is there a vanity installer URL?** `astrolift.app` has no DNS delegation
   at all, and `https://astrolift.dev/cli/install.sh` currently 404s. Serving
   the installer from `astrolift.dev` would need the file published to that
   GitHub Pages site — and it is only worth doing alongside decision 1, since
   a `curl | sh` one-liner that still demands a token is not much of a
   one-liner.
