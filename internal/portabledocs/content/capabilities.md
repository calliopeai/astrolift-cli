# Discover installation capabilities

Start with the installation you will use. Optional modules, cloud drivers, token
scopes, and your organization grants all affect what you can do.

```bash
astro status --json
astro --help
astro project resources catalog --project platform --cluster production --json
astro project resources catalog --project platform --cluster production --available-only
astro agent workloads ls --project platform --json
astro workflow definitions --json
```

`status` returns `version`, `apiVersion`, `capabilities`, and `authMethods`.
A capability says the installation implements a feature; it does not grant
permission or prove that a particular cluster has the required provider driver.
Bounded returns and serial item bodies require
`workflows.bounded_review_loops` and `workflows.serial_collections`, respectively.
See [bounded workflows](bounded-workflows.md) for finite caps, exact body
bindings, recorded rounds and supported source-import limits.
Reviewed starts require `workflows.reviewed_definition_starts`,
`workflows.definition_input_contracts`, `workflows.definition_start_recovery`,
`pipelines.versioned_start_requests` and `pipelines.start_request_recovery` as
appropriate. See [reviewed starts](reviewed-starts.md) for the exact GUID/revision/
schema and recovery contracts. The executing CLI must also contain those commands.
Preview live streams and exports require `previews.reviewed_live_logs` and
`previews.reviewed_log_exports`. See [exact preview targets](preview-targets.md)
for all-or-nothing source proof, idle checks and credential-bound downloads.
CLI preview logs remain historical polling; these API capabilities add no CLI
WebSocket or export command.
Environment log and trace explorers require `observability.exact_environment_logs`
and `observability.scoped_trace_envelopes`. See [logs and traces](logs-traces.md)
for persisted placement, bounded reads and trusted trace attribution.
Model hosting discovery uses `models.admin_hosting`; connection discovery uses
`models.huggingface_connections`, and local import uses `models.local_artifacts`.
Using these flows also requires the installed source schema and independently
configured runtime/storage. Hosting and source/configuration writes require an
active installation Super-admin with freshly admitted credentials. These markers
grant no repository, license or cluster authority. See [model hosting](model-hosting.md) for setup, immutable sources,
separate checks and safe metadata reads through the existing GraphQL CLI.
Per-app model traffic uses `models.authenticated_subscription_metrics` and
current app-metrics authority. The runtime must provide its version 2 credential
mapping and authenticated scrape. Missing data is distinct from measured zero;
token counts and cost remain unsupported. The hosting guide includes a bounded
read-only query for one exact app connection.
`models.runtime_settings` adds typed Super-admin runtime declarations for one
selected cluster/provider and compute mode. Saving a declaration is separate
from image building, hardware inspection and model deployment. Check current
versions and the hosting guide's runtime setup query before editing.
`models.connection_approvals` adds organization defaults, stricter model
restrictions, pending requests and distinct-person review. Approval permits the
current requester to connect; it does not create a subscription. The hosting
guide explains current permissions, idempotency, stale requests and finalization.
For completion notifications, require `agents.completion_callbacks`; replay
also requires `agents.completion_callback_redelivery`. The resource catalogue
distinguishes available entries from planned and
unavailable entries. Inspect its reason before selecting a kind and variant.

`models.bedrock_connections` identifies the prepared native Bedrock discovery
and existing-connection APIs. It does not enable the default-off installation
setting, grant operator/app authority or prove inference. Check
`bedrockModelConnectionSupport` before entering setup and the exact placement
action before discovery/registration. See [native model connections](native-model-connections.md)
for bounded metadata, immutable sources, workload identity and app policy.

`models.native_connection_metadata` adds the typed `nativeConnection` field to
the existing model inventory/detail reads. It distinguishes native family,
source kind, configuration availability and invocation access, with a nullable
typed source. Local hosted records return null. Unavailable native metadata
does not become local runtime metadata. Vertex and Foundry source types do not
enable those providers' connection flows; require their actual operations before
offering setup. This capability grants no authority or inference guarantee.

`domains.cloudflare_connections` adds encrypted organization-owned Cloudflare
OAuth/API-token connections and protected read-only zone binding. Require the
current connection support hint, active platform Super-admin and exact org
permissions. The [Domains guide](domains-dns.md#connect-cloudflare-in-five-steps)
explains five-step setup, operator prerequisites and safe recovery. The marker
does not enable DNS writes, registrar migration or certificate provisioning.

`email.exact_service_delivery_tests`, `email.delivery_test_history` and
`email.signed_delivery_observations` identify exact applied-service SES tests,
content-free history and signed provider feedback. They do not certify sending
configuration or delivery. Test sends require current app/service write authority
and the current bearer ceiling; support metadata is separate from native
preflight. See [email delivery](email-delivery.md) for stable request recovery,
suppression and timed-out observations.

Install alert SMTP has no dedicated advertised capability key in this contract.
Use the actual `installAlertMailSupport` query for current operator, event and
configured-channel support; SES capabilities are separate. See
[install alert SMTP](install-alert-mail.md) for private setup and nonce recovery.

The server GraphQL explorer at `/app/gql/config/` exposes the exact supported
schema. Download the [published SDL](https://github.com/calliopeai/astrolift-app/blob/main/backend/schema.graphql)
from an immutable backend tag or commit when generating clients. Check both
GraphQL `errors` and mutation `ok`; HTTP 200 alone does not prove success.

## Find the right setup guide

| Goal | Offline topic | Online guide |
|---|---|---|
| Test the configured install SMTP channel in your own mailbox | `install-alert-mail` | [Install alert SMTP](install-alert-mail.md) |
| First login and deployment | `start` | [Getting started](../getting-started.md) |
| Manage people, teams and direct membership | `organization` | [Running an organization](../running-an-org.md#manage-people-and-team-membership) |
| Inspect domain records, public delegation and app routes | `domains` | [Domains and DNS](domains-dns.md) |
| Test an exact applied SES service and read delivery observations | `email-delivery` | [Email delivery](email-delivery.md) |
| Register and operate an application | `app-setup` | [Set up an app](app-setup.md) |
| Package, register, and dispatch an agent | `agent-setup` | [Set up an agent](agent-setup.md) |
| Compose and run stages | `workflow-setup` | [Set up a workflow](workflow-setup.md) |
| Bound revisions, retries and record bodies | `bounded-workflows` | [Bounded workflows](bounded-workflows.md) |
| Review and recover exact starts | `reviewed-starts` | [Reviewed starts](reviewed-starts.md) |
| Act on an exact environment | `environment-actions` | [Environment actions](environment-actions.md) |
| Host a shared model and review app subscriptions | `model-hosting` | [Model hosting](model-hosting.md) |
| Discover and connect an existing Bedrock source | `native-models` | [Native model connections](native-model-connections.md) |
| Provision once and attach consumers | `shared-services` | [Shared services](shared-services.md) |
| Review a preview environment by GUID | `preview-targets` | [Exact preview targets](preview-targets.md) |
| Install the cluster keep-alive agent | `cluster-agent-install` | [Server-owned installation](cluster-agent-install.md) |
| Read environment logs and traces | `logs-traces` | [Logs and traces](logs-traces.md) |
| Interpret workload measurements | `workload-signals` | [Workload signals](workload-signals.md) |
| Receive a final task event | `callbacks` | [Agent completion callbacks](agent-completion-callbacks.md) |

```bash
astro docs list --json
astro docs show capabilities
astro docs export ./platform-reference
astro onboard --help
```

CLI v0.8.0 includes offline search. Run
`astro docs search 'workflow recovery' --json` or
`astro docs search '"request file"'`. Search reads only the embedded public
guides and requires neither authentication nor network access. The executing
binary's `docs --help` lists its actual topics and commands.

Offline guides travel with the executing CLI release. The website follows the
current public documentation release. Compare `astro version` and `astro
status --json` before using newly added API fields against an older server.
Refresh authentication after a newly required token scope is added. A token
scope narrows RBAC; it cannot grant access the identity does not already have.

`astro perms list --json` reads the permission catalogue bundled with the
executing CLI, without a server connection or login. Its `source` records the
backend repository, immutable revision, source path and SHA-256; `permissions`
remains the array of names. This snapshot does not enumerate the selected
server's current definitions or prove any account or credential has a grant.
`requiresTargetCheck: true` means the actual target's current permission, token
ceiling and approval requirements still apply before an action.

Server-owned cluster-agent installation requires the actual `installClusterAgent`,
`astroliftClusterAgentInstallReview` and `astroliftClusterAgentInstall` fields plus
the matching CLI. No new capability key is assumed; see
[cluster-agent installation](cluster-agent-install.md) for recovery and the
heartbeat-confirmed success boundary. Registration and API acceptance alone
do not prove an installed healthy agent.

## Reviewed cluster installation APIs

Compatible prepared server releases advertise `clusters.reviewed_agent_install`
and `clusters.reviewed_log_collector_install` through the public installation
handshake. They mean API availability only: target permission, provider support,
node coverage and ongoing health remain independently checked. These new commands
are not part of released CLI v0.10.0 or the separate v0.10.1 documentation patch.

Read `astro docs show cluster-agent-install` for original agent-install request
recovery and heartbeat confirmation, and `astro docs show cluster-log-collector`
for original collector tuple recovery, reader grants and post-loss activation. See
[agent installation](cluster-agent-install.md) and
[collector installation](cluster-log-collector.md).

## Prepare authenticated admission, not just discovery

Before a write, confirm `astro whoami --json`, the selected organization,
active membership and exact target authority. `astro whoami --permissions --json`
is an informational account-grant list, not this credential's limits or an action
matrix. Installation `authMethods` advertises available families; it does not
prove an OIDC callback/client is configured or a session is elevated. See
[reviewed-start admission and SSO setup](reviewed-starts.md#current-authority-and-safe-refusal-recovery)
for fresh post-lock checks, browser setup and keeping original request identity
after a refusal or uncertain reply. These fixes introduce no new capability key.

For offline setup context, use the existing embedded guides directly:

```bash
astro docs show app-setup
astro docs show agent-setup
astro docs show workflow-setup
astro docs show shared-services
astro docs show model-hosting
astro docs search '"Browser SSO setup"' --json
astro onboard --only docs --dry-run --json
astro onboard --only docs
```

`onboard --only docs` installs release-matched Markdown under `.astrolift/docs`;
it neither authenticates nor provisions anything. Dry-run reports proposed files,
and existing files are preserved unless explicitly replaced with `--force`.
Installing knowledge does not establish connectivity, permissions, source access,
worker readiness or a completed runtime acceptance test.
