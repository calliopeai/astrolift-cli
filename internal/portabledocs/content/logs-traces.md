# Read logs and traces from an exact environment

Start with the application and a persisted environment GUID. The `/logs` and
`/traces` dashboard explorers use these selections to read the recorded cluster
and namespace. A cluster-wide service name, a branch name or an arbitrary trace
ID does not establish application ownership.

This guide requires a compatible server advertising
`observability.exact_environment_logs` and
`observability.scoped_trace_envelopes`. These additive contracts are prepared
for the next compatible server release; check your installation's schema and
`astro status --json`. Capabilities do not grant permissions or establish that
a log backend, collector or trace backend is configured.

CLI v0.10.0 can submit these read-only queries with `astro api graphql`. It has
no dedicated trace-search command or new WebSocket/export command. The
`logs-traces` offline topic is added to the CLI documentation snapshot containing
this guide; it is absent from v0.10.0. Existing `astro app logs` and
`astro app previews logs` remain historical GraphQL polling.

## Select the recorded placement

Use the application's environment list to obtain the GUID and name:

```graphql
query LogTraceEnvironments($app: String!, $page: Int! = 1) {
  astroliftEnvironmentsPage(appSlug: $app, page: $page, pageSize: 20) {
    items { id name kind clusterId }
    totalCount page pageSize
  }
}
```

Keep the selected GUID with every read. An optional name must refer to that same
persisted environment. Deleted, inactive or foreign placement is unavailable;
the resolver does not replace it with the app's default cluster.

An organization-owned cluster must belong to the application's organization.
A shared cluster with no organization owner is eligible only through the
application's persisted `AppEnvironment.tenant_cluster` relationship. Shared
placement does not authorize a whole-cluster read: namespace, application and
environment scope still come from that exact relationship. The resolver rechecks
placement after a backend read and discards data if the recorded binding changes.

Actual previews require the additional reviewed preview GUID and version proof
for historical logs. Use [exact preview targets](preview-targets.md), including
its live-stream/export contract, instead of substituting a conventional preview
name. A persisted environment GUID alone does not authorize those preview logs.

## Historical logs

Historical logs require `APP_READ_LOGS`, current organization visibility and the
credential's scope ceiling. Save this query as `environment-logs.graphql`:

```graphql
query EnvironmentLogs(
  $app: String!, $environment: GUID!, $since: DateTime!, $until: DateTime!,
  $cursor: String, $search: String, $level: String
) {
  astroliftAppLogs(
    appSlug: $app, environmentId: $environment, since: $since, until: $until,
    cursor: $cursor, search: $search, level: $level, limit: 100
  ) {
    reason historicalAvailable nextCursor reachedRetention totalCount
    scope { organizationId appId environmentId environmentName clusterId namespace }
    items { podName container timestamp message level stream }
  }
}
```

Supply timezone-aware ISO timestamps for `DateTime`, for example values derived
from the last hour, in a JSON variables file:

```sh
astro api graphql --file environment-logs.graphql --vars-file log-vars.json --json
```

Reuse the same app, environment, time window and filters when following
`nextCursor`. Restart the cursor chain after changing a selection or refreshing.
`totalCount` counts the returned page, not all retained logs. `reachedRetention`
indicates the requested range reached the effective retention boundary; it is
not evidence that the selected environment never logged anything. The server
bounds individual pages and clamps an oversized history window to 31 days.

`historicalAvailable: false` means this response cannot provide historical logs.
A live-log link is a separate choice, not a successful historical result.
Provider errors and unavailable placement must remain distinguishable from a
successful empty query; inspect `reason` and `scope` as well as `items`.

## CloudWatch collector metadata and existing records

CloudWatch scoped history requires a server containing the collector-identity
admission correction. On that server, reads require a JSON record from a
trustworthy Kubernetes collector. The collector metadata must match the requested
environment namespace and app; a workload-filtered request also requires matching
workload metadata.
Application log text, service names or log-stream names do not establish those
bindings. The reader never supplies a missing record namespace from the request.

| Collector field | Required match |
|---|---|
| `kubernetes.namespace_name` | Exact nonempty recorded environment namespace |
| `kubernetes.labels["astrolift.io/app"]` | Exact selected app slug |
| `kubernetes.labels["astrolift.io/workload"]` | Exact selected workload slug when a workload filter is supplied |

Operators must ensure the collector or writer owns this Kubernetes metadata and
stamps it from verified runtime placement. Application-provided fields must not
supply or override that metadata. A matching JSON field is not cryptographic
proof of its producer: the collector and log-writing permissions establish this
trust boundary. Installing a CloudWatch backend alone does not establish it.

Plaintext records and records without the required metadata are not admitted.
Foreign or malformed required identity metadata does not become an owned log line.
Duplicate JSON
keys anywhere in the record cause the entire record to be excluded, even if
one of the duplicate values would match the requested identity. This is a
migration and compatibility boundary for existing log groups:
older unattributed records cannot gain authority from an environment selection
or a newly configured collector. New trusted records can become readable after
the collector is correctly configured; this contract does not backfill or
retroactively prove older records.

The Loki driver retains its existing namespace/app/workload selector contract.
These CloudWatch requirements do not change Loki's record format or install a
collector for either backend. Local SDK and database tests establish admission
behavior, not production collector trust or successful ingestion.

## CloudWatch reader identity and collector handoff

This handoff describes a prepared App/Opscode source contract. It requires a
compatible released server and operator-reviewed collector installation; it does
not establish that a collector is installed or historical reads are activated.
Use the matching reviewed source release for `aws/modules/observability-fluent-bit`
and `helm/tenant-telemetry/fluent-bit`, rather than mixing newer values with an
older unverified deployment.

### Registered read identity

The CloudWatch reader uses `TenantCluster.provider_config.credential` through the
shared AWS session factory. An explicit `aws_assume_role` declaration carries the
registered `role_arn` and optional `external_id` into role assumption; the group
and region come from `log_config.log_group` and `log_config.region` with
`log_driver: cloudwatch_logs`. The credential declaration contains role/account
pointers, not inline access keys or session secrets.

Without an explicit credential declaration, the reader uses the control plane's
ambient identity. Legacy `log_config.role_arn` is supported only with an ambient
registration. Remove that legacy override before enabling an explicit registered
credential. Conflicting declarations or failed role assumption refuse the read;
they do not retry under ambient authority or select another account. The current
read identity needs `logs:FilterLogEvents` on the exact selected group. A role ARN
or external ID alone does not grant that permission or change the role's trust.

### Collector and reader policy are separate

The Opscode module exports a candidate handoff for the operator and the owner of
the runtime connection:

| Output | Handoff |
|---|---|
| `collector_contract` | Exact namespace, ServiceAccount, OIDC subject, collector IRSA role, region and group |
| `historical_reader_config` | Candidate `log_driver/log_config` with the actual group, region and stream prefix; no implicit reader role |
| `historical_reader_policy_json` | Unattached exact-group `logs:FilterLogEvents` policy for review by the runtime connection owner |
| `optional_retention_metadata_policy_json` | Separate optional group-metadata inspection; not a historical-query prerequisite |

The collector's IRSA role can create streams, write messages and describe stream
metadata in its Terraform-owned group. It cannot read messages, create groups or
change retention. Never substitute that write role for the runtime connection's
reader role. Exporting a policy does not attach it, grant assume-role authority
or update a trust policy. Optional `DescribeLogGroups` metadata inspection uses
account-wide `Resource: "*"`; that separate grant is not needed for ordinary
historical queries. See the [CloudWatch Logs IAM action/resource reference](https://docs.aws.amazon.com/service-authorization/latest/reference/list_logs.html).

Terraform's `enable_fluent_bit` creates the group and write role, not the
DaemonSet. The pinned chart installation is a separate operation requiring
Kubernetes API reachability from its authorized installer. The actual chart
namespace and `serviceAccount.name` must match `collector_contract` and the
IRSA trust subject. Existing defaults remain `kube-system/fluent-bit`; a future
worker-managed `astrolift-system` collector must be explicitly provisioned for
that namespace, rather than moving an existing collector implicitly. This
source handoff does not provide that worker installation operation.

The reviewed AWS profile exports the complete collector JSON record. It keeps
`Merge_Log Off`, `K8S-Logging.Parser Off`, `K8S-Logging.Exclude Off` and `Keep_Log On`
so application JSON and annotations cannot promote fields into Kubernetes
metadata or discard the retained message. The CloudWatch output must not set
`log_key`, which would discard that metadata. Terraform owns group creation and
retention; the collector output uses `auto_create_group Off` and does not set
`log_retention_days`. These settings follow the official
[Kubernetes filter](https://docs.fluentbit.io/manual/data-pipeline/filters/kubernetes)
and [CloudWatch output](https://docs.fluentbit.io/manual/data-pipeline/outputs/cloudwatch)
contracts.

### Verify before activation

Before saving or enabling the candidate reader configuration, the operator must
verify the actual installation, trusted ingestion and current connection:

1. Check the installed DaemonSet's readiness, exact namespace/ServiceAccount and
   IRSA trust subject, plus the runtime connection's exact-group read authority.
2. Ingest a unique non-sensitive marker from an authorized fixture with the
   recorded namespace, app and workload metadata; retrieve it through the scoped
   historical API or existing CLI polling path.
3. After the fixture's source pod is removed, retrieve that already-ingested
   marker from the historical backend. Records not collected before source loss
   cannot be promised recoverable.
4. Verify refusal or exclusion for other namespaces, other apps, missing identity
   and duplicate-key records. Confirm the effective retention boundary separately.

Preserve the current external backend until this acceptance succeeds. Native
Helm renders, Terraform provider fixtures and credential-threading tests establish
source contracts; they do not prove installed IAM admission, private-network
reachability, live ingestion or recovery after actual pod loss. This CloudWatch
log profile does not install tracing, an OTel collector or application
instrumentation, and an X-Ray export role does not establish Tempo attribution.

## Bounded trace search and detail

Traces require `APP_READ` with current app visibility and token scope. Trace
windows are ordered and at most 24 hours; `since` and `until` accept UNIX seconds
as JSON **strings**. Search is bounded to 20 candidate results; the dashboard
requests 10. `truncated: true` means the bounded search omitted candidates, not
that it provides a continuation cursor. Narrow the window, service or status.
The status filter accepts `OK` or `ERROR`.

```graphql
query EnvironmentTraces(
  $app: String!, $environment: GUID!, $since: String!, $until: String!,
  $service: String, $status: String
) {
  astroliftAppTracePage(
    appSlug: $app, environmentId: $environment, since: $since, until: $until,
    service: $service, status: $status, limit: 10
  ) {
    reason truncated limit
    scope { organizationId appId environmentId environmentName clusterId namespace }
    items { traceId rootService rootOperation spanCount durationMs statusCode }
  }
}
```

Request detail with a separate document:

```graphql
query EnvironmentTraceSpans(
  $app: String!, $environment: GUID!, $trace: String!,
  $since: String!, $until: String!
) {
  astroliftTraceSpansResult(
    appSlug: $app, environmentId: $environment, traceId: $trace,
    since: $since, until: $until
  ) {
    reason
    scope { organizationId appId environmentId environmentName clusterId namespace }
    items {
      traceId spanId parentSpanId operation service startTime durationMs statusCode
      attributes resourceAttributes
    }
  }
}
```

Use one operation per file with the existing CLI API command. Retain the same
app/environment and bounded window for detail. A trace ID must contain exactly
32 lowercase hexadecimal characters. Detail first establishes that the ID is
in the selected scope; an arbitrary ID cannot authorize a whole-cluster fetch.
Only spans whose authoritative resource attributes match the selected placement
are returned. Foreign parents are detached from the visible span tree. Trace
summaries describe the visible scoped spans, not a complete cross-service trace.

## Interpret availability and trusted collector attribution

Both log and trace envelopes distinguish placement from backend availability:

| Response | Meaning |
|---|---|
| `NOT_CONFIGURED`, `scope: null` | A current eligible placement could not be established, or changed during the read. |
| `NOT_CONFIGURED`, scope present | Placement is known; the historical log collector, supported trace backend or trusted trace attribution is missing. |
| `NO_DATA_YET`, scope present | A supported scoped query found no owned data in the requested window. |
| `ERROR` | The read failed or trace candidates could not establish owned spans; an empty list is not a healthy zero-traffic claim. |
| `OK`, scope present | The response contains verified scoped data. |

The current trace path supports Tempo with
`trace_config.attribution: collector-resource-v1`. This flag records an
operator-established trust boundary; enabling the flag alone does not make
untrusted telemetry authoritative. The collector must remove caller-provided
values for these resource attributes, then stamp values from verified runtime
placement before sending telemetry to the backend:

| Resource attribute | Authoritative value |
|---|---|
| `astrolift.organization.id` | Owning organization GUID |
| `astrolift.app.id` | Registered app GUID |
| `astrolift.environment.id` | Persisted environment GUID |
| `astrolift.cluster.id` | Recorded cluster GUID |
| `k8s.namespace.name` | Recorded environment namespace |

The server uses quoted resource keys such as `resource."astrolift.app.id"`
and intrinsics such as `trace:id`. See
[Grafana's official TraceQL syntax](https://grafana.com/docs/tempo/latest/traceql/construct-traceql-queries/)
for the distinction between resource attributes and intrinsic fields.

Application-supplied tags alone are insufficient. The search applies all five
attributes and verifies them again on returned spans. Logs use their separately
configured historical log driver and namespace query; this trace attribution
contract does not install a log collector or guarantee historical ingestion.

An operator should review the selected environment's actual collector/backend
configuration before changing it. Local database and backend-HTTP fixtures prove
source and authorization contracts; they do not certify a live Tempo deployment,
collector stamping or production log delivery. For CPU/memory and HTTP metric
scope, see [workload signals](workload-signals.md).
