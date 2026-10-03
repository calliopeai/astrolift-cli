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

Application-supplied tags alone are insufficient. The search applies all five
attributes and verifies them again on returned spans. Logs use their separately
configured historical log driver and namespace query; this trace attribution
contract does not install a log collector or guarantee historical ingestion.

An operator should review the selected environment's actual collector/backend
configuration before changing it. Local database and backend-HTTP fixtures prove
source and authorization contracts; they do not certify a live Tempo deployment,
collector stamping or production log delivery. For CPU/memory and HTTP metric
scope, see [workload signals](workload-signals.md).
