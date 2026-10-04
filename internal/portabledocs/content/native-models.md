# Connect an existing Bedrock model

Register an existing AWS Bedrock foundation model or inference profile in the
organization's model inventory, then connect eligible app environments. The
model remains in AWS; registration creates an Astrolift connection record and
does not allocate paid throughput, deploy a model server or delete a cloud model.

This guide describes the prepared Bedrock connection contract. The selected
installation's schema and capability metadata are authoritative. Require
`models.bedrock_connections` and the actual operations below before use. A
provider catalogue entry or installed AWS plugin alone does not enable this
flow. For Kubernetes-hosted Hugging Face or local models, use
[model hosting](model-hosting.md).

## Enable the installation and admit a cluster

An installation Super-admin opens **Administration → Features**
(`/administration/features`) and enables the public runtime flag
`models.bedrock_connections_enabled`. Its backing setting,
`BEDROCK_MODEL_CONNECTIONS_ENABLED`, defaults to false. API implementation,
installation enablement, current operator authority and exact cluster authority
are separate checks. Enabling connections does not grant AWS model access.

Metadata discovery, registration and settings edits require an active
installation Super-admin, current `org.update` and the selected cluster's
`cluster.update`. Bearer scope ceilings and current browser/session admission
still apply. A setup-entry hint does not authorize any selected cluster.

Use an enabled AWS provider and an eligible managed cluster with a declared AWS
account and region. Discovery uses that cluster's registered cloud credential,
including its supported assumed-role declaration. It verifies actual STS account,
partition and stable principal before and after each metadata call. Operator
credential material is never returned or copied into an app binding.

Keep the selected organization, cluster/provider GUIDs and current record
versions together. Refresh and review again after placement, credential or
authority changes. A twelve-digit account declaration is not an actual AWS
identity proof until the native check succeeds.

## Select and review the exact native source

Foundation models and inference profiles are separate sources. Select the
native source kind and ID/ARN, then inspect its exact detail. A profile retains
its own ARN and full destination-model ARN set; it is not replaced by a default
foundation model. Only eligible active on-demand sources can be registered.

Discovery is bounded metadata, not the paginated registered-model inventory.
The native foundation-model list has no continuation cursor. Profile
continuation remains internal and bounded. Respect `state`, `partial` and
`truncated`; an incomplete or denied read is not an empty complete catalogue.
Use exact ID/ARN lookup for a known source outside a bounded list.

Review the source fingerprint, selected account/region/partition, destination
ARNs and current placement versions before registration. Metadata and native
ACTIVE status do not establish invocation permission, entitlement, license
acceptance or a successful model response. A changed profile destination set
requires fresh review and a separate registration; it cannot silently widen an
existing connection's grants.

## Manage the registered connection

The `clusterModelDeploymentsPage` API includes native connections in the same
organization inventory as locally hosted deployments. Use
`clusterModelDeployment` to inspect the exact record's `sourceKind` and
`nativeSource` projection, including its immutable source and configuration
state. Browser management also requires a UI version that presents these native
fields; the API marker alone does not prove that UI is installed. Native
connections have no local CPU/GPU, pod, vLLM runtime or model-server readiness claim.
`invokeAccess` remains `unknown` until independently established.

Servers advertising `models.native_connection_metadata` also expose the typed
`nativeConnection` field on inventory and detail records. It reports `family`,
`sourceKind`, `configurationState`, `invokeAccess`, a reason and a typed `source`.
Local hosted records return null for this field. A native record whose metadata
is unavailable keeps its family and reports `UNAVAILABLE` with a null source;
it must not be interpreted as a local hosted model.

The source union includes Bedrock, Vertex Endpoint and Foundry deployment types.
This release populates existing Bedrock connection metadata. Vertex/Foundry
types alone do not enable registration, app connections or inference for those
providers. Check each operation and capability on the selected installation.
`resourceIdentityFingerprint` hashes the recorded native resource identity;
`reviewedSourceFingerprint` covers its reviewed configuration. Neither proves
resource ownership, an immutable cloud incarnation or successful invocation.

If installation enablement or the credential declaration changes, the record
remains identifiable as a native Bedrock connection while `nativeSource` can be
unavailable. Refresh configuration and operator admission; absent metadata does
not authorize another source or a local-runtime fallback.

Super-admin settings edits can change the name, subscription enablement and
shared/dedicated settings using the reviewed connection and placement versions.
The source, account, region, destination set and credential declaration are
immutable. Register a replacement connection for a different source. Shared
connections permit independent eligible app environments; dedicated connections
restrict them to the selected app without allocating dedicated cloud capacity.

Unregistering removes Astrolift's local record after connections and active
intents are resolved. It does not call the paid provisioning/deletion driver,
change the cloud source or delete a model. A lost write response needs exact
record recovery and fresh review; generic GraphQL invocation does not provide
an idempotency guarantee for registration.

After native records exist, rollback disables the feature and retains the
forward database schema. Live and soft-deleted native rows prevent restoring
the previous owner constraint. Do not purge or recast connection records to
force a schema reversal.

## Connect apps under organization policy

Use the existing [automatic/approval/denial flow](model-hosting.md#choose-automatic-access-approval-or-denial).
App owners need their current scoped app authority. Hosting authority and app
connection authority are separate. Approval records qualified votes; the current
requester separately confirms Connect. Neither approval nor API acceptance
means the app has a working inference connection.

Reconciliation verifies the exact source and current identity/placement before
applying the named configuration and owned AWS workload-identity policy. Observe
the requested and applied subscription revision and reconciliation outcome.
Connection changes can require an app restart. They do not restart or redeploy
the AWS-hosted model.

If a retained source becomes unavailable during another app's removal, cleanup
can confirm that requested removal while the retained connection stays failed
with its applied revision unchanged. Inspect both outcomes before retrying;
partial cleanup does not confirm that the remaining app can invoke the model.

The named binding uses `MODEL_<ALIAS>_ENDPOINT_URL`, `DEPLOYMENT_NAME`, `REGION`,
`API_STYLE` and `AUTH_MODE`, each under the same alias prefix. For example,
`MODEL_CHAT_DEPLOYMENT_NAME` is the selected native ARN,
`MODEL_CHAT_API_STYLE` is `bedrock` and `MODEL_CHAT_AUTH_MODE` is
`cloud_identity`. No model API key or operator AWS credential is issued.
The app uses an AWS SDK and its workload identity for SigV4 requests. Matching
configuration or an observed IAM document does not prove IAM propagation,
invocation entitlement or success for a particular SDK operation.

Revocation removes the owned named binding and grants no longer needed by the
app's other coherent environment/subscription bindings. Equivalent grants still
needed elsewhere and external IAM policies remain effective. An app-wide IAM
role does not isolate calls between that app's individual subscriptions.
Unverified or conflicting legacy role ownership refuses reconciliation; an
operator must resolve its immutable identity through a reviewed identity change.
Astrolift does not infer ownership from a reused app slug or namespace.

Native traffic, token counts, costs, density and playground invocation remain
unsupported observations in this connection flow. Missing/unsupported data is
not measured zero. Local-hosted models retain their own runtime and metrics.

## Read-only discovery through the CLI

The existing `astro api graphql` command can run these typed reads. The CLI
contains no native `astro model` command. First inspect the selected server's
capabilities and schema; older servers cannot answer these fields.

```graphql
query BedrockSupport($organization: GUID!) {
  astroliftServerInfo {
    apiVersion capabilities
    featureFlags { key enabled }
  }
  bedrockModelConnectionSupport(organizationId: $organization) {
    enabled allowed reason
  }
}
```

Save the next document as `bedrock-sources.graphql`. Use actual selected GUIDs
and reviewed versions in a private `bedrock-sources.json` variable file. Choose
`FOUNDATION_MODEL` or `INFERENCE_PROFILE` for `kind`.

```graphql
query BedrockSources(
  $placement: BedrockModelPlacementInput!, $kind: BedrockModelSourceKind!
) {
  bedrockModelConnectionAction(input: $placement) { enabled allowed reason }
  bedrockModelSources(input: $placement, sourceKind: $kind, limit: 100) {
    state reason partial truncated
    items {
      name registerable reason
      identity {
        protocol sourceKind accountId region partition sourceId sourceArn
        destinationModelArns sourceFingerprint metadataObservedAt
        configurationState invokeAccess
      }
    }
  }
}
```

```json
{
  "placement": {
    "organizationId": "018f42f0-4420-7000-8000-000000000001",
    "clusterId": "018f42f0-4420-7000-8000-000000000002",
    "expectedProviderId": "018f42f0-4420-7000-8000-000000000003",
    "expectedClusterVersion": 1,
    "expectedProviderVersion": 1
  },
  "kind": "FOUNDATION_MODEL"
}
```

```bash
astro api graphql --org YOUR_ORG --file bedrock-sources.graphql \
  --vars-file bedrock-sources.json --json
```

Check GraphQL errors as well as returned state; partial metadata is advisory.
Read the exact source before registration:

```graphql
query BedrockSource($source: BedrockModelSourceInput!) {
  bedrockModelSource(input: $source) {
    name registerable reason
    identity {
      protocol sourceKind sourceId sourceArn destinationModelArns
      sourceFingerprint accountId region partition invokeAccess
    }
  }
}
```

`source` repeats the placement GUIDs/versions and adds `sourceKind` plus
`sourceIdentifier`. A null detail is not confirmed access. These reads register
nothing and perform no paid allocation or IAM changes. See the
[control API contract](../reference/api.md) for credential, tenancy and error
handling, and [capability discovery](capabilities.md) for release matching.

On a server advertising `models.native_connection_metadata`, read the registered
connection's common metadata with its actual organization and record GUIDs:

```graphql
query NativeConnection($organization: GUID!, $model: GUID!) {
  clusterModelDeployment(organizationId: $organization, id: $model) {
    id name
    nativeConnection {
      family sourceKind configurationState invokeAccess reason
      resourceIdentityFingerprint reviewedSourceFingerprint metadataObservedAt
      source {
        __typename
        ... on NativeModelConnectionSource {
          accountId region partition sourceId sourceArn destinationModelArns
        }
      }
    }
  }
}
```

This read uses the existing model permissions and token scope ceilings. A null
record can indicate that it is inaccessible; it does not prove deletion. The
common metadata capability grants no additional read or hosting authority.
