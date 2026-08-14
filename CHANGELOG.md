# Changelog

## Unreleased

### Added

- Add project shared-resource catalogue, provisioning, inspection, update,
  reprovision, attachment, detachment, and removal commands with JSON output.

### Changed

- Clarify that cancelling an agent run hard-stops its Kubernetes workload.
- Add embedded, release-matched platform guides with offline export and
  generated section-1 man pages through `astro docs`.
- Make `astro app init` emit the current server-accepted manifest shape.
- Repair the release installer asset mapping and verify archive checksums before
  installation.
- Honor `--api-url`/`ASTROLIFT_API_URL` with `--token`/`ASTROLIFT_TOKEN` as
  explicit, config-free connection overrides.
