# Install the cluster keep-alive agent through the server

This guide describes the prepared server-owned installation contract and matching
CLI source. It is not available in released CLI v0.10.0 and does not establish
that your server exposes it. Check the installation's actual GraphQL schema and
the executing CLI's `operator cluster install-agent --help` and
`agent-install-status --help` before using the examples. The CLI requires the
public handshake capability `clusters.reviewed_agent_install` before review or
dispatch. It discovers that metadata at `/app/gql/config/public/` without sending
credentials. The marker describes API availability only; it grants no permission,
provider support, private-network reachability or cluster health. Recovery keeps
the original tuple when that capability is unavailable; it does not fall back to
local installation.

If an upstream login gateway blocks or redirects `/app/gql/config/public/`,
capability discovery is unavailable and installation stops before submission.
Ask the installation operator to expose that public metadata route. The CLI
does not follow login redirects or send bearer/organization credentials to
discovery; authenticated target operations are a separate path. Preserve any
existing original request file while the route is corrected.

The cluster keep-alive agent reports cluster heartbeat; it is separate from an
application agent task dispatched with `astro agent dispatch`. Installing this
agent does not install log collectors, activate CloudWatch history, configure
tracing or verify any application's telemetry.

## Authority and reachability

Installation requires `cluster.manage` at the selected cluster, current
organization membership and the credential's permission ceiling. A shared
platform cluster also requires the platform-operator gate. The server checks
current authority and the reviewed cluster/provider binding before effects and
before credential activation; a new credential does not bypass an original
operation's revoked or expired authority.

Use exactly one of `--cluster-id` or `--slug` for installation. A known canonical
nonzero cluster GUID goes directly to the scoped API without an inventory query.
Slug discovery requires separate `cluster.register` inventory authority; narrow
cluster-scoped credentials should use the known GUID. Knowing the GUID grants no
permission and does not change the credential ceiling.

The control-plane worker performs Kubernetes operations from its own network
using the registered cluster connection. No local kubeconfig or raw agent key is
required or returned to the CLI. The registered connection still needs actual
Kubernetes reachability and the required resource permissions. Accepted API
submission does not prove that the worker can reach a private endpoint.

## Retain the original installation request

Use the matching CLI to review the exact registered cluster and create the
private metadata request file before dispatch:

```sh
astro operator cluster install-agent --cluster-id CLUSTER_GUID \
  --request-file ./agent-install.json --json
```

An optional `--interval-seconds` is recorded with the original request. The
private file contains server, organization, caller, cluster GUID and selected
slug or GUID, reviewed version/source proof, request UUID and interval metadata. It contains no agent
key, kubeconfig or copied provider credential. Files use POSIX `0600` or a
protected Windows current-user/SYSTEM ACL and are exclusively created and flushed before dispatch.

After a lost reply, run the same command with the **same file and original
flags**. Omitting the interval on replay uses its persisted original value;
supplying a different interval is refused. The original source proof is retained
and sent unchanged on recovery; a newer review never replaces it under the same
request UUID. A saved file missing its original source proof is refused; the CLI
does not rereview or replace it automatically. Existing files never mint a new
request UUID or select a replacement cluster by name. Server, organization,
actor or target changes refuse recovery. An existing slug-selected file can
also be recovered with `--cluster-id` equal to its stored cluster GUID, retaining
the original file bytes and tuple without new discovery. Keep the original
authentication context: the server also binds the operation to its original credential ceiling.

Unlike read-only workflow-definition recovery, installation recovery may submit
the same original reviewed tuple to `installClusterAgent`. The server identifies
the original operation by actor, organization and request UUID and refuses a
changed tuple. It can return or resume that operation rather than rotate another
key. An unknown or refused outcome is not a reason to delete the file, choose a
new key or fall back to local installation.

The matching CLI refuses `--kubeconfig` for this server-owned path. An older
server lacking the API is an explicit compatibility failure; it does not trigger
a local raw-key rotation fallback. Older CLI installations retain their separate
legacy implementation and do not implement this recovery contract.

## Read confirmation, not just acceptance

The operation response returns an installation GUID. Read that exact operation:

```sh
astro operator cluster agent-install-status --install-id INSTALL_GUID --cluster-id CLUSTER_GUID --json
```

Optional `--cluster-id` verifies the returned operation belongs to that exact
cluster before any receipt is printed. It performs no inventory discovery.

| Status | Meaning |
|---|---|
| `QUEUED` | Original operation reserved; installation is not confirmed |
| `INSTALLING` | Server workflow is reconciling recorded resources |
| `AWAITING_HEARTBEAT` | Recorded Secret and Deployment are confirmed; the replacement credential has not yet confirmed a heartbeat |
| `SUCCEEDED` | `heartbeatConfirmed: true`; authenticated replacement heartbeat confirmed activation |
| `REFUSED` | Authority or reviewed source prevented continuation; not successful installation |
| `UNCERTAIN` | Submission or provider outcome is unconfirmed; preserve the original request and recover it |

`secretConfirmed` and `deploymentConfirmed` describe the recorded resources;
they do not independently mean that the agent is healthy. Only `SUCCEEDED` with
`heartbeatConfirmed: true` establishes installation success. The candidate uses
its own `astrolift-agent-<install-guid>` Deployment while the previous agent remains in
place. The current key remains accepted until the replacement's authenticated
heartbeat confirms the recorded Secret/Deployment identity and activation. The
server then persists the newly active Deployment's exact name and UID. Queue
acceptance or a resource-write acknowledgement cannot substitute for that
heartbeat. The receipt records installation confirmation; continued cluster
health is monitored separately.

Retiring the previously observed Deployment is a separate conditional cleanup
step after activation. It checks the recorded old UID and refuses a replacement;
it does not delete whichever object happens to occupy the old name. A
`SUCCEEDED` receipt can carry `errorCode: RETIREMENT_UNCONFIRMED`: the new agent's
heartbeat is confirmed, but previous Deployment cleanup may need review. Read
`errorCode` and `errorMessage` even on success, and retain the same request file.
Recovery of that original installation can retry its guarded retirement without
creating another credential. The CLI preserves those fields in JSON and prints
an installation note in text; this cleanup diagnostic does not undo success.

Before activation, `HEARTBEAT_UNCONFIRMED` means installation proof could not be
confirmed at that time; the operation remains awaiting heartbeat and the current
agent stays active. Withdrawn source, resource identity or original authority
refuses the candidate. Neither outcome establishes candidate activation.

The CLI emits operation metadata as JSON, including pending states. `REFUSED`
and `UNCERTAIN` still return an operational error after printing that metadata.
`retryable` describes the current operation state; it grants neither permission
nor authority to submit a new request. Status reads require present authorization
and the original operation's credential ceiling.

## API contract

Review the exact GUID/version/source proof, persist a nonzero canonical request
UUID, then retain that tuple for replay. API clients must implement private
durable metadata storage before dispatch themselves; submitting these fields does not create a
CLI request file.

```graphql
query ClusterAgentReview($cluster: GUID!) {
  astroliftClusterAgentInstallReview(clusterId: $cluster) { clusterId version source }
}
```

```graphql
mutation InstallClusterAgent($input: InstallClusterAgentInput!) {
  installClusterAgent(input: $input) {
    ok errors { code message field }
    data {
      id clusterId requestId status
      secretConfirmed deploymentConfirmed heartbeatConfirmed retryable
      workflowId errorCode errorMessage createdAt updatedAt
    }
  }
}
```

`InstallClusterAgentInput` contains required `clusterId: GUID!`,
`expectedVersion: Int!`, `expectedSource: String!` and `requestId: String!`, plus
optional `intervalSeconds: Int`. Copy the current review's opaque `source` value
into `expectedSource` alongside its version on the first dispatch. Do not
recompute that proof or infer it from a cluster name. Retain the original source
and requested interval representation on replay. Do not substitute a newly read
version or source proof under an existing UUID. Check both GraphQL errors and
mutation `ok` before treating the returned receipt as accepted.

```graphql
query ClusterAgentStatus($install: GUID!) {
  astroliftClusterAgentInstall(installId: $install) {
    id clusterId requestId status
    secretConfirmed deploymentConfirmed heartbeatConfirmed retryable
    workflowId errorCode errorMessage createdAt updatedAt
  }
}
```

This source contract requires the compatible server workflow and actual worker
configuration. Local database, Kubernetes-HTTP fixture and CLI tests establish
request identity and confirmation behavior; they do not prove a production
private-network install or a live authenticated heartbeat. For the separate
collector/read-policy handoff and post-pod-loss history acceptance, see
[logs and traces](logs-traces.md#cloudwatch-reader-identity-and-collector-handoff).

For the separate reviewed collector workflow, see
[CloudWatch log collector installation](cluster-log-collector.md). Its activation
receipt requires post-pod-loss verification and is separate from agent heartbeat.
