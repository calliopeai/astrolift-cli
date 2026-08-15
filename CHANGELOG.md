# Changelog

## Unreleased

### Added

- Add project shared-resource catalogue, provisioning, inspection, update,
  reprovision, attachment, detachment, and removal commands with JSON output.
- Support `GITHUB_TOKEN`/`GH_TOKEN` in the release installer, so machines
  without the GitHub CLI can install from the private release repository.
- Print a once-daily, non-blocking hint on stderr when a newer release exists.
  Suppressed by `ASTROLIFT_NO_UPDATE_CHECK=1`, `--json`, CI, and `dev` builds.
- Document the release cadence and ship checklist in `RELEASING.md`, with a
  dispatchable `release.yml` workflow that validates and cuts the tag.

### Fixed

- Fix `astro update`, which asked for a release archive that has never
  existed (`astro_<version>_Darwin_x86_64.tar.gz` rather than
  `astro-darwin-arm64.tar.gz`) and downloaded it anonymously from a private
  repository. It now resolves the published asset name and authenticates.
- Replace dead `astrolift.app` documentation links; that domain has no DNS
  delegation.

### Changed

- Clarify that cancelling an agent run hard-stops its Kubernetes workload.
- Add embedded, release-matched platform guides with offline export and
  generated section-1 man pages through `astro docs`.
- Make `astro app init` emit the current server-accepted manifest shape.
- Repair the release installer asset mapping and verify archive checksums before
  installation, and fail with an actionable message when no GitHub credential
  is available instead of dead-ending on a 404.
- Honor `--api-url`/`ASTROLIFT_API_URL` with `--token`/`ASTROLIFT_TOKEN` as
  explicit, config-free connection overrides.
