# Changelog

## Unreleased

- Add per-command `--server` selection for registered endpoints and their
  credentials without changing the saved default. Reject conflicting endpoint
  overrides before using credentials. Box attachment and removal honor the
  selected organization in the API tenant header (#100).

## 0.7.0 — 2026-08-21

### Changed

- `astro app init` scaffolds the web workload with `is_public = true` (plus a
  comment pointing internal services at `false`). A scaffolded hello-world app
  previously registered private — no Service, no Ingress, no route — and the
  platform deployed it unreachable out of the box
  (calliopeai/astrolift-cli#79).

### Fixed

- `astro exec --app <slug>` now names the right verb when the slug turns out to
  be an agent-box: `box-… is an agent-box, not an app. Use: astro box attach
  box-…`. A settled box points at `box ensure` instead, since it needs starting
  rather than attaching.

  A box is not a registered app, so it resolves to no app pods and the old
  message was `no running pods for app` — true about the wrong subject, and
  read by anyone following calliopeai/astrolift#128's own text, which still
  shows the `astro exec --app X -- claude` form. Someone hits that against a
  box that is running fine and concludes it is broken.

  Deliberately a hint on an already-failing path, not a fallback: `exec` is the
  app verb and `box attach` is the box verb, and the split is kept rather than
  papered over. The lookup costs one query, only when the exec was going to
  fail anyway, and stays silent when it cannot be made — an older control plane
  or a missing grant leaves the original error intact rather than replacing it
  with a guess.

## 0.6.1 — 2026-08-18

### Added

- `api.ErrSchemaMismatch`: a sentinel for "the server's schema does not have
  what this query asked for", classified where the GraphQL error array is
  already parsed rather than by re-parsing a formatted message at each call
  site. Callers adapt with `errors.Is`.

  It exists because a GraphQL server rejects the *whole* query on an unknown
  selection instead of returning a partial result, so a client cannot discover
  a field's absence by inspecting the response — recognising the failure is the
  only way, and every caller that wants to adapt would otherwise reimplement
  the same fragile string matching. Having it lets this repo's releases and the
  control plane's move independently, with no cutover to choreograph.

  Deliberately narrow, and tested for the narrowness: a permission denial, a
  not-found, or a transport failure must never wear it, and a batch mixing a
  schema complaint with a real error is not a mismatch either — otherwise a
  caller's fallback path would silently discard the real problem.

### Fixed

- `astro box attach` resolves a box's pod in a way that survives the control
  plane changing underneath it. Three steps, cheapest first: the row's own
  `podName` when it carries one (free, and the normal case on a current
  server); then `agentBoxPods`, gated on `agent_box.attach`, the same grant
  that authorizes the attach; then a blank pod so the existing resolver runs.

  astrolift-app#1482 closes the `astroliftAppPods` door that answered a box
  slug — correctly, since a second door gated on `app.read_logs` was also
  leaking box pods into an unrelated workload breakdown — but 0.6.0 relies on
  it whenever `podName` is blank, which happens before the first sweep after a
  pod comes up and whenever the best-effort stamp swallowed a failure.

  Also passes the resolved container name rather than an empty one.

## 0.6.0 — 2026-08-18

### Added

- Add the `astro box` command group — `ensure`, `ls`, `rm`, `attach` — for
  agent-boxes, the warm containers an interactive agent session attaches to.
  Unlike `agent dispatch`, which starts a batch run that ends, a box holds a
  tmux session open and waits, so the agent survives a dropped connection, an
  IDE restart, or a closed laptop.

  `ensure` is idempotent rather than a create: it is what a button calls, so
  pressing it twice attaches to the box you already have instead of starting a
  rival one on a second node, and a box that was idle-reaped restarts under the
  same slug so a stored address keeps working. Name what to run with `--agent`
  (a registered agent whose run mode is `persistent`) or `--env-spec` (the
  image and the secret packet). `--idle-timeout` accepts `90m`, a number of
  seconds, or `never`; an unset flag sends nothing rather than a zero, because
  zero is the never-reap sentinel and inventing one would quietly pin a node.
  `ls` shows warm boxes only, `--all` includes settled ones.

  `attach` cannot work end to end yet: the control-plane exec relay admits
  registered apps only, and a box is not one, so a healthy box fails as a
  permission error. Tracked in calliopeai/astrolift#129. The failure is left
  raw deliberately — an earlier revision explained the gap and offered a
  `kubectl exec` fallback, which reads correctly today and becomes a lie the
  moment #129 lands, telling someone with a genuine permission denial that the
  platform does not support boxes. Unhelpful beats confidently wrong.

  Requires the `ensureAgentBox` / `destroyAgentBox` / `agentBoxes` / `agentBox`
  surface from astrolift-app#1475.

## 0.5.0 — 2026-08-16

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
