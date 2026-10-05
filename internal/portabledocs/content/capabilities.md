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
For completion notifications, require `agents.completion_callbacks`; replay
also requires `agents.completion_callback_redelivery`. The resource catalogue
distinguishes available entries from planned and
unavailable entries. Inspect its reason before selecting a kind and variant.

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

The server GraphQL explorer at `/app/gql/config/` exposes the exact supported
schema. Download the [published SDL](https://github.com/calliopeai/astrolift-app/blob/main/backend/schema.graphql)
from an immutable backend tag or commit when generating clients. Check both
GraphQL `errors` and mutation `ok`; HTTP 200 alone does not prove success.

## Find the right setup guide

| Goal | Offline topic | Online guide |
|---|---|---|
| First login and deployment | `start` | [Getting started](../getting-started.md) |
| Inspect domain records, public delegation and app routes | `domains` | [Domains and DNS](domains-dns.md) |
| Test an exact applied SES service and read delivery observations | `email-delivery` | [Email delivery](email-delivery.md) |
| Register and operate an application | `app-setup` | [Set up an app](app-setup.md) |
| Package, register, and dispatch an agent | `agent-setup` | [Set up an agent](agent-setup.md) |
| Compose and run stages | `workflow-setup` | [Set up a workflow](workflow-setup.md) |
| Bound revisions, retries and record bodies | `bounded-workflows` | [Bounded workflows](bounded-workflows.md) |
| Review and recover exact starts | `reviewed-starts` | [Reviewed starts](reviewed-starts.md) |
| Act on an exact environment | `environment-actions` | [Environment actions](environment-actions.md) |
| Provision once and attach consumers | `shared-services` | [Shared services](shared-services.md) |
| Review a preview environment by GUID | `preview-targets` | [Exact preview targets](preview-targets.md) |
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
