# Changelog

## Unreleased

### Added

- Add `astro workflow run-cancel` and `astro workflow run-show`, control and
  stage-level detail for a run that is already in flight. `run-cancel` is
  cooperative by default, so a run that owns external resources tears them
  down; `--terminate --reason <text>` escalates to a hard kill for a wedged
  run. A run that is already terminal is refused before anything is sent, and
  an RBAC denial reads as "not permitted" rather than a transport error.
  `run-show` prints each stage's order, kind, role, status, attempt, and
  timings, and for a `human_gate` its gate state plus the approvers the stage
  declares — "waiting on approval, and on whom" as read-only platform truth.
  Both take the workflow slug and default to the newest run (`--run <guid>`
  selects another): the control plane has no by-guid run resolver. Requires
  the `stageRole` / `stageApprovers` / `humanGateState` / `humanGateNote`
  fields from astrolift-app#1409.

## 0.4.0 — 2026-08-16

### Added

- Add the `astro app previews` command group — `list`, `show`, `logs`, `open`
  and `teardown` — so per-PR preview environments can be driven from the CLI.
  Select one with `--pr <n>`, or `--branch <name>` for a manual preview, which
  carries no PR number. `previews logs` is `app logs` pointed at the
  environment the platform synthesized for the preview, resolved by matching
  the preview hostname rather than guessing an environment name.
- Add `astro agent workloads ls`, which enumerates the org's registered
  `kind=agent` workloads. Each row carries both identifiers the dispatch
  surface needs: the slug `agent dispatch` takes and the GUID
  `workflow create --bind` takes.
- Add `astro agent vnc <task-id>`, resolving a task to an absolute, openable
  console URL. The stored `vnc_url` is a root-relative WebSocket relay path,
  so it could previously only be shown as text.
- Add `astro app pods`, listing the pods `astro exec` picks from, with the pod
  name for `--pod` and container names for `-c`.
- Add `astro workflow run-manifest <file.toml>`, collapsing
  `import` → `create --bind` → `run` into one call and resolving each
  `agent_dispatch` stage against the org's registered agent workloads.
- Add `astro app previews pin` / `unpin`, the operator exemption from preview
  garbage collection. A pin covers both collection rules — TTL expiry and
  max-active eviction — which is what distinguishes it from extending a TTL,
  and it holds until someone unpins. `pin --reason <text>` records why;
  `unpin` clears the whole record and is a no-op on an unpinned preview.
  `previews list` gains a PINNED column and `previews show` reports the pin
  with who set it, when, and why. Requires the `setPreviewPinned` mutation
  from astrolift-app#1407.
- Add `astro agent send <task-id> <input>` to queue a follow-up prompt for a
  running agent task, with `--stdin` for multi-line input and `--json`. The
  message is applied at the agent's next turn boundary, so a send reports
  queued rather than read; `deliveredAt` on the returned record is what
  answers whether the agent has taken it.
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
