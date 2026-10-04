# Control API reference

Astrolift's typed control-plane API is GraphQL. Focused REST, SSE, and WebSocket
routes exist where device authentication, callbacks, streaming, or CI semantics
do not fit GraphQL well.

## Base URLs

Given an install at `https://astrolift.example.com`:

| Surface | URL |
|---|---|
| GraphQL HTTP/explorer | `https://astrolift.example.com/app/gql/config/` |
| GraphQL subscriptions | `wss://astrolift.example.com/app/gql/config/ws/` |
| CLI device flow | `https://astrolift.example.com/api/cli/v1/auth/...` |
| MCP | `https://astrolift.example.com/api/mcp/v1/` |
| Health | `https://astrolift.example.com/health/` |

The server root returns a link map when requested as JSON:

```bash
curl -sS -H 'Accept: application/json' https://astrolift.example.com/
```

## Authentication and tenancy

User API tokens start with `alft_at_`. App-scoped deploy tokens start with
`alft_dt_` and are accepted only by their intended CI/deploy routes.

```http
Authorization: Bearer alft_at_...
X-Astrolift-Organization: <organization-guid>
Content-Type: application/json
```

The organization header selects one organization when the identity belongs to
more than one. Never use a slug where the API requires a GUID. The token's
scopes narrow the user's RBAC grants; requests must pass both checks.

The reviewed server scope catalogue includes `read:apps`, `write:apps`,
`read:clusters`, `write:clusters`, `manage:clusters`, `manage:auth-users`,
`write:app-access`, `agent-env-spec:write`, `project:write`, `team:write`,
`app:onboard`, `secret:read`, `secret:write`, `workflow:write`,
`workflow:trigger`, `mcp:read`, `mcp:dispatch`, `mcp:write`, and `admin`.
The selected installation's token catalogue is authoritative; a listed scope
is not necessarily part of the default device-flow credential. Use the smallest
set that supports the integration. `astro auth login --scope` selects documented
CLI profiles (currently `clusters`), not an arbitrary comma-separated scope list.
Account grants from `astro whoami --permissions --json` are informational and
cannot establish this credential's ceiling or exact target authority.

## GraphQL request

```bash
curl -sS https://astrolift.example.com/app/gql/config/ \
  -H "Authorization: Bearer $ASTROLIFT_TOKEN" \
  -H "X-Astrolift-Organization: $ASTROLIFT_ORG_ID" \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'JSON'
{
  "query": "query { astroliftServerInfo { version serverTime capabilities } }",
  "variables": {}
}
JSON
```

A successful HTTP response can still contain GraphQL `errors`; check that
array before reading `data`. Mutations generally return an envelope with
`ok`, structured `errors`, and `data`. Do not treat HTTP 200 as mutation
success without checking `ok`.

The installation's explorer and introspection expose the exact schema supported
by that server. The platform repository also publishes `schema.graphql` for
client generation. Prefer generated types or committed operations over building
query strings from user input.

## Generated contracts and drift checks

The release source publishes the [GraphQL SDL](https://github.com/calliopeai/astrolift-app/blob/main/backend/schema.graphql)
and the [MCP capability superset](https://github.com/calliopeai/astrolift-app/blob/main/backend/contracts/mcp-tools.json).
Replace `main` in those URLs with an immutable backend tag or commit SHA when
pinning a client build.

Platform contributors regenerate both artifacts with `make contracts` and
verify them with `make contracts-check`. CI assembles the full-feature
Strawberry schema, byte-compares the backend/frontend SDL, validates every MCP
input JSON Schema, checks MCP metadata against live handlers, regenerates
frontend GraphQL types, and rejects any diff.

This is not yet a generated catch-all OpenAPI document. Most focused REST
routes still define payloads imperatively, so route introspection would publish
names without truthful request, response, and authorization schemas. REST
OpenAPI will be generated as those endpoints adopt shared typed contracts.

## Native model connection operations

Compatible servers advertising `models.bedrock_connections` expose bounded
`bedrockModelSources` and exact `bedrockModelSource` reads, with separate
installation/operator `bedrockModelConnectionSupport` and exact-placement
`bedrockModelConnectionAction` admission. `registerBedrockModelConnection`,
`updateBedrockModelConnection` and `unregisterBedrockModelConnection` manage a
local existing-source connection record, returning the existing
`ClusterModelDeploymentMutationResult` envelope. They do not allocate or delete
native AWS model resources. Every write requires current hosting authority and
reviewed target versions/source identity; registration has no generic GraphQL
idempotency guarantee. See [native model connections](../guides/native-model-connections.md)
for the read-only CLI documents, binding and reconciliation boundaries.

## Compatibility

- Query server capabilities before assuming an optional module exists.
- Additive GraphQL fields are backward compatible; clients should ignore
  response fields they do not understand.
- Pin a CLI/SDK version in unattended automation.
- Use idempotency keys on deploy routes when retrying after a network failure.
- Expect `401` for invalid/expired credentials and permission errors when RBAC
  or the token scope ceiling denies the action.

There is no single catch-all REST/OpenAPI surface for control-plane CRUD. Use
GraphQL unless a documented workflow explicitly names a REST, SSE, or WebSocket
route. This avoids depending on internal Django paths.

## Browser elevation and bearer admission

A compatible server can require recent authentication for sensitive browser
mutations. Handle `STEP_UP_REQUIRED` using its `supportedMethods` and
`requiresAttestation` fields; a CLI confirmation, account permission list or
successful metadata read is not that proof. The existing browser-recency gate
does not apply to API-token calls, which retain their bearer and scoped-grant
gates. Reviewed workflow reservation, dispatch and recovery additionally refresh
identity, membership, token ceiling, policy and ownership after lock waits. See the
[reviewed-start SSO setup](../guides/reviewed-starts.md#browser-sso-setup-for-sensitive-operations)
for exact callback registration, identity-link and session requirements, time
limits and refusal recovery. No new capability key or CLI elevation verb is
assumed for this admission repair.

## Secrets

Secret-list operations return names, references, and presence metadata. A
caller needs `secret:read` plus `secret.read` RBAC to reveal a value on the few
surfaces that support reveal. `secret:write` permits write-through operations,
not readback. MCP intentionally never exposes secret values.

## Reviewed team membership

The prepared additive contract advertises
`identity.reviewed_team_membership`. Check the installed server's capability
list and SDL before requesting it. See [membership setup](../running-an-org.md#manage-people-and-team-membership)
for the browser flow and remaining-access rules.

| Operation | Purpose |
|---|---|
| `astroliftTeamMembershipTeam(teamId, slug)` | Resolve exactly one live, visible team GUID or route slug. |
| `astroliftTeamMembershipPerson(orgMemberId)` | Read the exact visible organization identity. |
| `astroliftTeamMembershipsPage(teamId, search, page, pageSize)` | Page a team's roster and direct/inherited/provider provenance. |
| `astroliftPersonTeamMembershipsPage(orgMemberId, search, page, pageSize)` | Page the person's currently visible teams. |
| `astroliftTeamMemberCandidatesPage(teamId, search, page, pageSize)` | Select active existing organization members. |
| `astroliftMembershipTeamsPage(search, page, pageSize)` | Page the caller's manageable team targets. |
| `astroliftTeamMembershipRolesPage(teamId, search, page, pageSize)` | Page eligible team roles under the current grant ceiling. |
| `astroliftTeamMembershipReview(teamId, orgMemberId, kind, roleId)` | Review `ADD` or `REMOVE`; return the exact `expectedSource` digest. |
| `changeAstroliftTeamMembership(input)` | Commit or recover one reviewed direct-membership change. |

For example, save this read-only query as `team-roster.graphql` and GUID variables
as `team-roster-vars.json`, then use
`astro api graphql --file team-roster.graphql --vars-file team-roster-vars.json`:

```graphql
query TeamRoster($team: GUID!) {
  astroliftTeamMembershipsPage(teamId: $team, page: 1, pageSize: 25) {
    totalCount page pageSize
    items {
      team { id name slug canManageMembers }
      person { orgMemberId name active }
      teamMemberId lifecycle canRemove
      sources { id source scopeKind roleId roleName expired removable }
    }
  }
}
```

```json
{"team":"00000000-0000-4000-8000-000000000001"}
```

The mutation input carries `requestId`, `kind`, `teamId`, `orgMemberId`,
`expectedSource`, and `roleId` for `ADD`. Obtain the digest from the exact review;
do not compute it from roster text. Preserve the original UUID and reviewed input
when recovering a lost reply. Successful `data` distinguishes `committed` from
`replayed` and returns `changeId`, exact public GUIDs, removed binding GUIDs and
the original remaining sources. Inspect the standard `ok`/`errors` envelope and
refresh current membership afterward.

Reads and writes check their concrete team/person visibility. Writes refresh
credentials, ownership, membership, policy, role ceilings and required browser
authentication after waits. Replay is confined to the original actor,
organization, credential and reviewed identity. Neither metadata reads nor the
advisory `me.teamAccessNavigation`/`team_access` hints establish write authority.
Existing API-token scope definitions are unchanged.

## Agent completion callbacks

`runAstroliftAgent` accepts optional `callbackUrl`, `callbackSecretRef`,
`correlationId` (at most 128 characters), and `callbackMode` (`FULL` or `NOTIFY`).
The organization configures HTTPS destinations with
`configureAgentTaskCallbacks(allowedHosts)` and writes a signing secret with
`setAgentTaskCallbackSecret(name, value)`. Query `agentTaskCallbackPolicy` for
the configured host allow-list. Never send the signing key in dispatch input.

`agentTask` returns `callbackStatus`, `callbackAttempts`, and
`callbackLastError`. `redeliverAgentTaskCallback(taskId)` replays the final event
without rerunning the agent. Read the [completion callback guide](../guides/agent-completion-callbacks.md)
for the exact signed wire contract, durable 24-hour backoff, authorization,
retention, rotation, and Python verification snippet.

## Bounded workflow authoring and execution metadata

Installations advertising `workflows.bounded_review_loops` and
`workflows.serial_collections` expose stage `maxAttempts`, `backEdge` and
`iteration` on reads and stage create/update inputs. The server validates the
combined finite execution budget before dispatch; a capability is not an
authorization grant. See [bounded workflows](../guides/bounded-workflows.md)
for the native object contracts and supported source-import subset.

Stage agent and nested-workflow string references also accept
`guid:<canonical-lowercase-UUID>`. Authoring round-trips preserve explicit GUIDs;
unavailable or inaccessible targets are refused without substituting a slug.
Literal slugs keep their existing resolution behavior. See the
[exact-reference rules](../guides/bounded-workflows.md#preserve-an-explicitly-selected-target)
for binding overrides and visibility checks.

`workflowExecutionStages(executionId, limit, after)` reads pages belonging to
one exact execution GUID or mirror ID. Preserve the returned execution GUID,
organization and recorded Temporal workflow/run IDs across pages. Its stage
items expose `roundNumber`, `attemptNumber`, return `causedBy`, timestamps,
`collectionIndex` / `collectionParentExecutionGuid` / `collectionStageId`, and
separate fan-out identities. Item and branch indexes are zero-based. A missing
cause/parent remains unknown. `complete` on a collection means bodies finished
under the authored policy, so inspect per-item outcomes rather than assuming
all agent results succeeded. Run-control acceptance remains separate from
closure and resource cleanup.

## Reviewed workflow and pipeline starts

`workflowDefinitionById(id: GUID!)` reads an exact visible definition, execution
revision and JSON Schema 2020-12 input contract. `startWorkflowDefinition` binds
the exact GUID, expected revision, schema digest and actor-scoped request UUID;
`workflowDefinitionStartRequest(requestId)` recovers only that original start.
Receipts contain execution GUID and recorded Temporal workflow/run IDs. Engine
acceptance is separate from workflow completion. Inputs are encrypted on the
server and are excluded from recovery metadata and error diagnostics.

`startPipelineRun` binds an exact pipeline GUID/version/ref and durable request
UUID. `pipelineStartRequest` recovers the original request, and
`cancelPipelineRun` checks the exact run/version/engine identity. Cancellation
acknowledgement, execution closure and owned-resource cleanup are independent
states. Use bounded run/job/step pages rather than unbounded lists.

See [reviewed starts](../guides/reviewed-starts.md) for native CLI commands,
permissions, input handling, recovery differences and limits. The target server
must expose these fields; there is no legacy-write fallback.

The legacy `runWorkflowDefinition` alias keeps its original output shape and
accepts additive nullable review/confirmation arguments. Missing review proof
returns `PRECONDITION` upgrade guidance with no dispatch. A complete proof uses
the same exact durable-start and current-authority gates; new clients should
use `startWorkflowDefinition` rather than relying on slug selection or the
legacy mirror-ID field. `runAstroliftAgent` and configured-workflow APIs retain
their separate contracts.

## Exact resource, preview and metric context

Use [shared services](../guides/shared-services.md) for bounded project resource
pages, exact managed-service GUID metadata and separately paged visible consumers.
Reviewed writes carry `expectedContextRevision`; an enqueue acknowledgement is
separate from provider completion. [Exact preview targets](../guides/preview-targets.md)
explains persisted environment ownership, versions required on historical and live
logs, and preview-export downloads bound to the original requester and credential.
Live/export selectors require their advertised additive server capabilities;
ordinary legacy exports retain their separate capability-link contract.
[Workload signals](../guides/workload-signals.md) documents each signal's scope,
identity basis, physical container membership and instrumentation prerequisites.
Check the selected installation's SDL before requesting additive fields.


## Environment logs and scoped trace envelopes

[Logs and traces](../guides/logs-traces.md) describes exact environment selection,
shared-cluster placement, paged historical logs and bounded trace/detail reads.
Use `astroliftAppTracePage` and `astroliftTraceSpansResult` to inspect availability
and scope as well as items. Trusted collector attribution is required before
trace exposure; a missing collector is not a zero-traffic or successful empty
response. These read-only queries can be submitted through `astro api graphql`;
their fields require a compatible server and do not add CLI live/export commands.

## Server-owned cluster keep-alive installation

The prepared `astroliftClusterAgentInstallReview(clusterId)`,
`installClusterAgent(input)` and `astroliftClusterAgentInstall(installId)` APIs
review an exact registered cluster and return version/source proof. Installation
requires unchanged `expectedVersion` and `expectedSource`, reserves an original
request UUID and returns metadata-only installation status. They require `cluster.manage`, current
organization authority and the credential ceiling; shared platform clusters
retain their platform-operator gate. No new discovery capability is assumed.
Check the actual installation schema before use.

Read the [server-owned installation guide](../guides/cluster-agent-install.md)
for private original-request recovery and heartbeat confirmation. Only
`SUCCEEDED` with `heartbeatConfirmed: true` establishes installation success;
queue acceptance and resource writes do not. This prepared API does not install
log collectors or activate tracing.

### Prepared reviewed log collector API

Compatible future servers expose `astroliftClusterLogCollectorReview`,
`astroliftInstallClusterLogCollector` and
`astroliftClusterLogCollectorOperation`. Public capability
`clusters.reviewed_log_collector_install` describes API presence, not grants or
health. All target operations require scoped `cluster.manage`. Review returns an
unattached candidate reader policy; original private request recovery and
post-pod-loss activation are described in the
[collector installation guide](../guides/cluster-log-collector.md).

## Admin model hosting and source metadata

[Model hosting](../guides/model-hosting.md) separates catalogue discovery,
organization hosting authority, exact repository access, license review,
operator-certified CPU/GPU runtime admission and recorded readiness. Compatible
servers expose `modelHostingAction`, bounded `huggingFaceConnectionsPage`,
`clusterModelSourceAccess` and independent local-artifact metadata/import APIs.
Connection reads are metadata-only; the UI submits an HF token through a
write-only field and the server stores it encrypted. Local upload authorizations
are private capabilities, distinct from immutable manifest metadata. Hosting
requires an active installation Super-admin and freshly admitted credentials;
organization or team administration alone does not grant hosting authority.
Model source/configuration and cluster runtime writes keep that same operator
gate. App connections use separate scoped app authority and organization policy.
API availability proves neither source access nor a configured object
store/hydrator or successful model launch.

`clusterModelUpdateAdmission` and `updateClusterModel` review the exact stored
source and deployment version before changing hosting settings. Shared models
accept independently admitted app connections; dedicated models restrict them
to the selected app's environments while remaining organization-owned.
`astroliftModelSubscriptionMetrics` reads one authenticated subscription under
current `app.read_metrics` and organization visibility. Its declared placement
and subscription identities must match. It exposes measured traffic, accepted
ASGI response bytes and request duration; token use and cost are unsupported.
Use `models.authenticated_subscription_metrics` for API discovery and the hosting
guide's exact read-only query. A capability marker does not grant app access.

`models.connection_approvals` advertises policy-governed connection intake.
`modelConnectionTargetsPage` and `modelConnectionAction` return the current
`AUTO`, `REQUEST` or `DENY` action. `requestModelConnection` stores an idempotent
reviewed request without connecting the app; reviewers with current `org.update`
and scoped `app.approve_deploy` can approve or reject. The current requester
explicitly invokes `finalizeModelConnectionRequest` after approval. It rechecks
the policy, target versions and distinct live quorum before the subscription is
created. Organization policy writes use `org.update`; model restrictions retain
the installation Super-admin hosting gate and only tighten effective policy.
See the hosting guide for recovery, request versions and stale outcomes.
