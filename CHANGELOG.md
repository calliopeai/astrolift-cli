# Changelog

## Unreleased

- Install cluster agents through the server's private network using a durable
  reviewed request file. Recover the original operation after lost replies and
  expose exact installation status with authenticated heartbeat confirmation.
  Preserve the original cluster and provider source proof on every replay;
  refuse a missing source proof without replacing an uncertain request.
  Refuse local kubeconfig/key-rotation fallback on older servers (#1696).

## 0.10.0 — 2026-10-02

### Added

- Author bounded workflow back-edges, review loops, nested workflow and serial
  collection stages, and per-stage attempt caps; inspect recorded rounds, attempts,
  and return causes. Bundle the matching canonical guides and skills for offline CLI
  use (#2156).

## 0.9.0 — 2026-10-02

- Read preview GUIDs and their persisted environment identity directly, with
  explicitly requested pricing and reviewed log routes that never infer a
  replacement from a hostname or environment name (#2206).
- Page project resources, resource consumers and app services. Default lists
  retain JSON arrays; `--page` returns one bounded continuation envelope. Show
  credential-free exact owner/placement metadata, require fresh context on
  existing-resource writes, and preserve attachment-only detach through exact
  visible-owner discovery rather than a catalogue scan (#2207).
- Bundle 18 offline guides, adding exact previews and workload measurement scope.
  Keep canonical documentation and the platform skill byte-identical to their
  reviewed source; document collector, instrumentation and redeploy requirements.

- The public release installer supports anonymous latest or pinned downloads when
  no GitHub credentials are configured. Mirrors and authenticated downloads remain
  available; checksums are verified before installation, and access errors identify
  missing releases, rejected authentication or rate limits. The canonical and offline
  installation guides document the same behavior (#134).

## 0.8.0 — 2026-10-02

### Added

- Review and start exact workflow definition GUIDs with revision/schema proofs,
  typed JSON inputs, secret references and actor-scoped saved request identities.
  Recover lost replies without reopening inputs or submitting replacement work.
- Start pipelines with saved request keys, inspect bounded run pages, reconcile
  original submissions and cancel exact recorded versions and engine IDs.
- Review and control explicitly selected app environments. Selected-environment
  shells require current workload/pod/container admission before sending input.
- Search 16 bundled release-matched guides offline with bounded text snippets,
  JSON output and portable command/man-page exports.

### Changed

- `agent run` now accepts an exact definition GUID, `--request-file` and `--yes`.
  Use `--inputs-file` or legacy `--input @file`; new literal inputs and slug-only
  starts are refused. Existing request files perform read-only recovery.
- `agent run --wait` pins the verified receipt's execution and engine identities;
  JSON emits one metadata object and retains known receipts on uncertain outcomes.
  Engine completion and task cleanup remain separate.
- Protect credential and recovery files with POSIX mode `0600` or an actual
  Windows current-user/SYSTEM protected ACL. Explicit login may securely replace
  an owned legacy Windows credential file; recovery files are never replaced.
- Require native Windows ACL and recovery tests before tagging or publishing,
  in addition to Linux verification and canonical documentation parity.

## 0.7.2 — 2026-10-01

### Added

- Register signed final agent-task notifications with `agent dispatch`
  callback URL, signing-secret reference, correlation ID, and FULL/NOTIFY mode.
- Configure organization callback hosts and signing keys through
  `agent callbacks show|configure|secret-set`, and replay a settled final event
  with `agent callbacks redeliver` without executing the agent again.
- Show callback status, attempts, and safe last error on task reads. Ordinary
  dispatch and read-only additive-field fallback preserve older-server support;
  callback commands require the compatible completion-callback API.
- Bundle callback, capability, app, agent, workflow, and shared-service setup
  guides. Offline export and onboarding retain links between bundled guides,
  with public links for unbundled references.

### Changed

- Accept callback signing keys only from a file or standard input, validate their
  UTF-8 size, and suppress reflected signing-key errors.
- Document organization GUID-scoped agent secrets, native app-secret commands,
  workflow activation, and shared-service project/cluster attachment boundaries.
- Describe public GitHub release archives and checksum verification accurately.

- Reconcile the embedded prerequisite chart with canonical opscode chart 0.1.1,
  retaining cert-manager fixes and adding its existing OpenSearch operator/node
  sysctl support. Refresh requires an exact published source revision; offline CI
  verifies the complete committed source/archive inventory instead of silently
  copying an unrelated sibling checkout.

## 0.7.1 — 2026-09-30

### Added

- `astro onboard` installs the bundled skills and offline guides and writes
  MCP configuration for the selected install; `--dry-run` reports the plan
  without changing files or performing authentication.
- `astro api graphql` runs a document with the selected server credentials
  and explicit or saved organization; unknown organizations fail before the
  request is sent.
- `astro box ensure --image` accepts an explicit image for a one-off box.
  This does not supply the cluster image catalog or separate custom-image
  capability requested by CLI #86; that acceptance remains open.
- `astro apply -f <file.toml>` creates or updates declared agent environment
  specs and supports a write-free `--dry-run`; env-spec upsert also exposes
  `--non-root` without changing omitted settings.
- Exact workflow executions can be inspected, watched, stopped and cleaned
  up, including recorded stage history. Workflow imports support `--replace`,
  new manifest imports are activated before launch, and `workflow gate`/`gates`
  expose pending approval gates.
- Project resources expose cost and provider links.

- Add `astro whoami [--permissions]` with server-verified identity in the
  verified selected organization. The optional account grant summary is
  explicitly informational, not target authorization or credential limits.
- Correct `astro perms diagnose [app-slug]` to the actual account grant and
  role-binding queries; `--permission` optionally retrieves the existing
  account diagnostic trace on an app GUID or explicit scope. Preserve denied
  verdicts and reasons without approving or preflighting another operation
  (app #1867 remains partial).
- Preserve safe typed GraphQL/HTTP errors, including declared version metadata,
  and emit structured retrieval errors on stderr for JSON identity/permission
  inspection. Exclude raw error bodies, arbitrary extensions and GraphQL debug
  response dumps; redact the current bearer value from reflected diagnostics.

- Add `agent send --request-id` and `agent input-receipt` to recover queued
  steering after a lost reply without submitting a duplicate instruction.

- `astro agent env-spec upsert --box-workspace[=false]` explicitly changes box
  setup without overwriting it when omitted. Box wait timeouts name workspace
  setup as a possible cause (#112).

- `astro operator domains list|create|update|verify` audits and manages visible
  organization-owned/platform-shared DNS zones (#92). Creation explicitly
  defaults to `tenant_apps`; sparse updates preserve unrelated config. JSON
  exposes ownership, defaults, provisioning and TXT proof, and verification
  exits nonzero while proof is pending. Shared writes remain server-authorized.

- `astro app register --manifest-raw` sends the local manifest when source
  connections cannot fetch the repository (#90).
- `astro app set-build-mode` changes the mode and strategy together without
  overwriting the saved Dockerfile or build context (#89).

- Stop task cancellation and box removal when confirmation input fails, and
  reject an update whose extracted binary cannot be closed successfully.

- Show agent startup diagnostics during task inspection and box waiting, and
  preserve them in task/box JSON. Reads fall back on older servers without
  hiding permission or transport failures or retrying mutations.

- `astro app access show|allow|deny|clear [app] [--group g] [--user email]`
  restricts who may enter an app behind central auth (astrolift-app#2132). A
  change that would lock someone out names them and asks first (`--yes` to
  skip); an app whose astrolift.toml sets `[ingress.access]` is left to the
  repo.

- `astro auth-users list|create|set-password|reset-password|disable|enable|delete|groups|group-create <cluster> ...`
  manages who can sign in to the apps behind a cluster's central auth
  (astrolift-app#2131). Passwords come from a hidden prompt or
  `--password-stdin`, never a flag; `create` without one has the provider
  email a temporary password. `astro auth login --scope clusters` now also
  asks for `manage:auth-users` and `write:app-access`.

- `astro auth login --scope clusters` asks for the CLI's usual scopes plus
  `write:clusters` and `manage:clusters` (astrolift-app#2120), so an operator
  can set a cluster's config or run its recipe without minting an admin token.
  The approval page lists the scopes asked for. Needs a server with #2120.

- Add per-command `--server` selection for registered endpoints and their
  credentials without changing the saved default. Reject conflicting endpoint
  overrides before using credentials. Box attachment, removal, and exec honor
  the selected organization in HTTP requests and terminal WebSocket handshakes
  (#100).

- Implement `astro app secrets` (`list`/`create`/`delete`, over
  `setAppSecret`/`deleteAppSecret`), `astro app events` (`list`, also runs
  bare), `astro app services list`, and `astro app domains list` against the
  real GraphQL API. All four previously printed the generic sub-resource
  placeholder ("Subcommands typically include...") and did nothing. `secrets
  list` returns metadata only and never a value; `create`/`delete` report a
  queued proposal id instead of an applied write on installs that require
  secret-change approval. `services`/`domains` are read-only for now; managed
  service provisioning remains `astro project resources`
  (calliopeai/astrolift-cli#91, calliopeai/astrolift-cli#99).

### Fixed

- `astro auth logout` attempts to end the selected device-flow session at
  the server before clearing local credentials; unreachable servers produce
  a warning while local logout still succeeds.
- Agent log following survives a rolling tail window; task inspection
  exposes recorded failures and task operations retain organization selection.
- Bootstrap launch fields match the API, failed bootstrap releases roll back,
  and prerequisite chart handling installs the CRDs before dependent issuers.

- `astro app deploy` allows omitting `--image-tag` for platform builds and apps
  using manifest images. The API resolves platform builds to a commit before
  scheduling or approval and uses that SHA as the default image tag. `--ref`
  selects a branch, tag, or commit; the response includes the resolved commit.
  CI-pushed apps still require an explicit image tag (#94).

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
