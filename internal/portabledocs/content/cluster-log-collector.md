# Install the reviewed CloudWatch log collector

This guide describes the prepared server-owned collector installation API and
matching CLI source. It is not part of released CLI v0.10.0 or the separate
v0.10.1 observability documentation patch. Check your CLI's
`operator cluster install-log-collector --help` and the selected installation's
`clusters.reviewed_log_collector_install` capability before using it. The marker
means that the installation exposes this API; it grants no permission, provider
support, node coverage or collector health. The CLI checks the public installation
handshake at `/app/gql/config/public/` without sending credentials, then uses the
current authenticated organization for target review and operations.

If an upstream login gateway blocks or redirects `/app/gql/config/public/`,
capability discovery is unavailable and installation stops before submission.
Ask the installation operator to expose that public metadata route. The CLI
does not follow login redirects or send bearer/organization credentials to
discovery; authenticated target operations are a separate path. Preserve any
existing original request file while the route is corrected.

The server installs only its fixed chart, image and collector profile. It uses
the registered cluster GUID, AWS credentials, endpoint and CA from the control
plane's network; the CLI does not need a local kubeconfig. The profile supports
Linux EC2 EKS nodes. Fargate and Windows collection are unsupported; mixed clusters
report limited coverage. It does not install tracing or application instrumentation.

## Review the exact target and reader grant

```sh
astro operator cluster log-collector-review --slug production \
  --retention-days 30 --json
```

The CLI discovers the visible cluster, then reviews its exact GUID, version and
source. The review returns `supported`, `refusalCode`, policy metadata and an
**unattached candidate** `readerPolicy`. Unsupported provider, missing declared
AWS account, conflicting registered credentials, unavailable transport or an
existing external historical backend refuse preparation. Review performs no
installation, provider grant or reader activation.

Present `cluster.manage`, organization membership and credential ceiling are
required. Shared platform clusters also require the platform-operator gate.
The server repeatedly checks original actor, token expiry/revocation and captured
permission ceiling, reviewed cluster/provider source, workflow run and deadline
before effects, credential refresh and idle polling. A different credential does
not bypass a revoked original operation. An old server without the capability
fails explicitly; there is no local install or broader credential fallback.

The collector write role and historical reader identity are separate. The server
prepares the fixed owned log group and collector IRSA role; it never attaches an
external reader policy or replaces the registered read identity automatically.
`READER_GRANT_PENDING` means the connection owner must review and grant the exact
candidate read policy, then resume the **same** operation. Policy metadata is not
proof that a grant exists. See the [registered reader and ARN handoff](logs-traces.md#cloudwatch-reader-identity-and-collector-handoff)
for role/external-ID threading and the action-specific `:*` group-policy suffix.

## Dispatch with a private original request

```sh
astro operator cluster install-log-collector --slug production \
  --retention-days 30 --request-file ./collector-install.json --json
```

Before dispatch, the CLI exclusively creates and flushes a private metadata file:
server, organization, actor, cluster GUID/slug, reviewed version/source, retention
and a canonical nonzero request UUID. It contains no provider credential, log body,
chart values or agent key. POSIX files require mode `0600`; Windows files require
a protected current-user/SYSTEM ACL. Preserve the file until the outcome is known.

After a lost reply, run the same command with the same file, original selector and
scope. The CLI submits the original tuple unchanged, allowing the server to return
or resume the original operation. It does not refresh the review, pick a same-name
replacement or mint another UUID. Omitting `--retention-days` on replay uses the
persisted original retention; explicitly changing it is refused. Missing original
source or other required tuple fields, actor/server/org changes and malformed
files fail without replacing the file or dispatching another operation.

Supported retention values are 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365,
400, 545, 731, 1827 and 3653 days. This is the prepared API's bounded selection,
not a claim that every CloudWatch retention option is supported. No chart, image,
log group or role overrides are accepted.

## Read the exact operation

```sh
astro operator cluster log-collector-status --operation-id OPERATION_GUID --json
```

| Status | Meaning |
|---|---|
| `QUEUED`, `PREPARING`, `INSTALLING` | Reserved or effect reconciliation in progress; not activated |
| `READINESS_PENDING` | Recorded collector resources have not established bounded readiness |
| `READER_GRANT_PENDING` | Original read identity lacks the required grant; no automatic policy attachment |
| `INGESTION_PENDING` | Owned synthetic marker is not yet visible through the scoped historical reader |
| `PROBE_DELETION_PENDING` | Original probe UID disappearance is not yet confirmed |
| `POST_LOSS_READ_PENDING` | A fresh historical read after source loss has not verified the same event |
| `UNCERTAIN` | Submission/provider outcome is unconfirmed; preserve and resume the original request |
| `REFUSED` | Original authority, reviewed source or resource identity prevented continuation |
| `ACTIVATED` | Readiness, original probe cleanup and fresh post-loss read verified; candidate reader binding activated |

`ACTIVATED` requires `postLossVerifiedAt`, `activatedAt` and
`activatedClusterVersion` with no pending cleanup. It is an installation receipt,
not current collector health or proof of every application's log collection.
Read `coverage`, `cleanupPending`, `retryable`, `stage` and static error metadata.
`retryable: false` also applies after the original deadline; it does not grant
permission or authorize a replacement request. Pending reader policy remains
candidate-only. Operation `readerPolicy` is empty until actual preparation.

Install emits one JSON operation object when a valid receipt is returned. A
`REFUSED` or `UNCERTAIN` receipt is followed by an operational error. Status is a
read: it can return those same states successfully without claiming installation.
Failed transport, mutation refusal or mismatched identity does not emit a fabricated
receipt or copy provider response bodies into diagnostics.

## What the server verifies

The operation pins the chart digest/profile and prepared AWS account, role and
group. It records immutable RoleId and log-group creation identity, then rechecks
ownership and those identities before historical reads and activation. Kubernetes
resource UID/resourceVersion and recorded intents prevent adopting foreign or
same-name replacement objects. Original reader binding is compared again before
activation; failure preserves the external/current reader.

A server-approved digest-pinned synthetic probe must emit a non-sensitive marker
with trustworthy collector-owned namespace, `astrolift.io/app` and
`astrolift.io/workload` labels, plus recorded pod/container identity. The server
reads it through the registered scoped reader, deletes only the recorded probe,
confirms its UID is absent and performs a separate fresh read of the same retained
event. A lost successful DELETE reply can recover from durable delete intent and
observed disappearance; it does not fabricate a DELETE acknowledgement. Events
not ingested before source loss cannot be promised recoverable.

Existing Terraform collector handoffs remain separate. Their group/IRSA outputs
do not install a DaemonSet or automatically transfer ownership to this operation.
A different existing backend or unproven collector binding is not migrated by
this API. Ongoing health, retention expiry and production trust remain separate
operator checks.

## API documents

Persist the original tuple privately before the mutation. All operations require
current scoped authorization after public capability discovery.

```graphql
query ClusterCollectorReview($cluster: GUID!, $retention: Int!) {
  astroliftClusterLogCollectorReview(clusterId: $cluster, retentionDays: $retention) {
    clusterId version source retentionDays supported refusalCode message
    policy readerPolicy
  }
}
```

```graphql
mutation InstallClusterCollector($input: InstallClusterLogCollectorInput!) {
  astroliftInstallClusterLogCollector(input: $input) {
    ok errors { code message field }
    data {
      id clusterId requestId expectedVersion expectedSource retentionDays
      status stage retryable cleanupPending coverage workflowId deadline
      postLossVerifiedAt activatedAt activatedClusterVersion
      errorCode errorMessage readerPolicy createdAt updatedAt
    }
  }
}
```

`InstallClusterLogCollectorInput` requires `clusterId: GUID!`,
`requestId: String!`, `expectedVersion: Int!` and `expectedSource: String!`;
`retentionDays: Int!` defaults to 30. Copy the exact review source and version on
the first dispatch, then replay unchanged. `requestId` is a canonical nonzero
UUID. Check GraphQL errors, mutation `ok` and returned original identity/tuple.

```graphql
query ClusterCollectorStatus($operation: GUID!) {
  astroliftClusterLogCollectorOperation(operationId: $operation) {
    id clusterId requestId expectedVersion expectedSource retentionDays
    status stage retryable cleanupPending coverage workflowId deadline
    postLossVerifiedAt activatedAt activatedClusterVersion
    errorCode errorMessage readerPolicy createdAt updatedAt
  }
}
```

Native database, AWS/Kubernetes HTTP, Temporal and pinned Helm fixtures establish
source admission, recovery and activation behavior. They do not certify an
installed production DaemonSet, actual IRSA ingestion, private-network reachability
or real post-pod-loss collection. Production use still needs those verified checks
on an authorized fixture. No tracing or Fargate acceptance is inferred.
