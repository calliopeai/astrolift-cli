# Host a shared model from Hugging Face or local files

Host a model once in an organization-owned cluster deployment, independently of
an app or project. After the deployment is ready, connect eligible app
environments through independent subscriptions. Existing app/cloud endpoints
remain a separate deployment path.

For existing organization-owned Bedrock connections, use
[native model connections](native-model-connections.md). That flow shares the
inventory and app policy while retaining AWS identity, configuration and
invocation semantics; it does not deploy a local vLLM runtime.

This guide describes the prepared hosting contract. Check the selected
installation's schema and capability metadata before use. Older servers without
the hosting fields cannot run this flow. The offline `model-hosting` topic
requires a CLI release containing this guide; `astro docs list` shows the
executing binary's actual topics. There are no native `astro model` commands.

## Choose the source, then the cluster

The admin hosting wizard shows one active step at a time: choose a model, choose
placement and resources, then review the deployment. Back and Next preserve the
draft; changing reviewed inputs invalidates the old review. Hugging Face and local files have
separate source cards. A catalogue **Host model** action reveals revision
selection; **Host a model** on the shared-model list opens the setup flow.
“Ready for review” permits confirmation, and “Request accepted” means queued
hosting. Neither status establishes a ready model server.

1. Open **Models** and choose **Host a model**, or start from a Hugging Face
   catalogue entry. Catalogue metadata alone proves neither download access nor
   deployment compatibility.
2. Choose a public Hugging Face repository, a private/known repository, or a
   supported local artifact. Hugging Face sources pin a full 40-character commit
   SHA. Local sources pin an artifact GUID, record version and manifest SHA-256;
   a filename, mutable branch or uploaded archive is not that identity.
3. Choose an eligible cluster and explicit CPU or GPU mode, CPU/memory requests,
   GPU count and, for CPU mode, KV-cache allocation. Model ownership remains with
   the organization; an app is not needed to host it.
4. Review repository access, license, runtime admission and hardware separately.
   Review again after the organization, source, connection, cluster or authority
   changes. Never substitute a new connection or revision under the old review.
5. Request hosting. An accepted mutation queues reconciliation; it does not mean
   the model is running. Read the exact deployment's status, operation identity,
   readiness generation and observation time. The deployed model server must pass
   its `/health` probe; an open port alone does not establish readiness.
6. Once ready, review an eligible app environment and a named subscription alias.
   Adding/revoking a subscription restarts the shared model; all consumers may
   temporarily lose access. Wait for the requested subscription revision to be
   applied before treating the access change as complete.

## Find and edit a hosted model

Open **Models** to find organization-hosted deployments, including pending and
failed ones. Search and page through the inventory, then open a deployment to
inspect its immutable source, placement, requested and applied resources,
readiness, operation and app connections. A visible deployment is not proof of
authority to change it or readiness to serve requests.

An installation Super-admin can choose **Edit settings** to review the name,
resource requests and sharing settings. Updates preserve the existing source
identity and private source credential; changing displayed text cannot silently
replace that source. The review binds the exact deployment, cluster, provider
and record version. After an unconfirmed write, refresh that deployment before
submitting again. An accepted change remains pending until reconciliation and
the corresponding ready generation are observed.

Choose **Shared** to permit independent app subscriptions, or **Dedicated** to
restrict subscriptions to the selected app's eligible environments. A dedicated
model still belongs to the organization and cluster; it does not reserve a
separate physical machine. Existing subscriptions from other apps must finish
revocation before the mode can change. Revocation requested or queued is not
enough. Each app connection retains its own alias, credential and lifecycle.

## Choose automatic access, approval or denial

On servers advertising `models.connection_approvals`, the organization can set
`AUTO`, `REQUIRE_APPROVAL` or `DENY` for new app connections. The installation
defaults to automatic access, one reviewer and no self-approval. An organization
administrator with current `org.update` can change its policy; an installation
Super-admin can set a model restriction that only tightens the effective policy.
Model restrictions can increase the quorum, prohibit self-approval or choose a
stricter mode. They cannot bypass organization policy, app permissions, source
access, readiness or shared/dedicated restrictions.

Inspect a destination's current `modelConnectionAction` before acting. `AUTO`
uses the existing direct subscription admission, including `org.read` and the
destination's `app.update`. `REQUEST` permits an app owner with scoped
`app.update` to submit an approval request without a separate organization-read
or app-read grant. `DENY` prevents a new connection. Changes to these policies
do not automatically disconnect existing subscriptions.

The approval flow separates three actions:

1. **Request:** `requestModelConnection` creates a pending request, with no
   subscription, credential reference or reconciliation. Supply the exact
   organization/model/cluster/provider/environment tuple, reviewed model, app
   and environment versions, alias, current policy fingerprint and a fresh
   caller-generated `idempotencyKey` GUID. Retain that key for recovery; retrying
   the same reviewed request returns its identity, while reusing the key for a
   different target is refused.
2. **Review:** each distinct reviewer needs current `org.update` and scoped
   `app.approve_deploy`. `approveModelConnectionRequest` records a vote;
   `rejectModelConnectionRequest` closes the request. The quorum is one through
   sixteen qualified people. Repeated votes never increase the count, and
   self-review is refused unless the effective policy explicitly permits it.
3. **Connect:** an approved request still needs the current requester to call
   `finalizeModelConnectionRequest`. It rechecks the reviewed targets, source,
   policy, requester and currently qualified votes. A successful repeat returns
   the same subscription. Acceptance starts reconciliation; wait for the applied
   subscription revision before treating the connection as ready.

Use `modelConnectionRequestsPage` to recover your own requests, and
`modelConnectionApprovalRequestsPage` for a qualified reviewer's inbox. Read the
current request version before deciding, cancelling or finalizing it. Pending
and approved requests may become `STALE` when reviewed targets, source or policy
change; review again and create a new request. A withdrawn grant, expired token
or invalid session cannot count as a current approval. Use the server's current
`canApprove`, `canReject`, `canCancel` and `canFinalize` hints to present actions;
every mutation independently rechecks authority.

### Connect and review in the dashboard

Open a hosted model, then **Add connection**. Search or page through eligible
app environments, enter an alias and review the exact destination. The current
policy determines whether the action is **Connect**, **Request approval**, or a
disabled action with a reason. An unavailable capability or failed admission
read does not fall back to the legacy direct-write form.

Open `/models/connections` for **My requests** or the permitted **Review inbox**.
Select a request to inspect its current version and review an available action.
Reviewing an approval does not connect the app: after approval, the current
requester separately reviews **Connect**. A recorded subscription still needs
reconciliation; the screen does not treat acceptance as a ready model endpoint.

Organization administrators edit the connection policy at
`/administration/organization`. Installation Super-admins edit a model's tighter
restriction on its detail page. These are separate permissions. An absent model
restriction is a neutral overlay, not a copy of the organization's effective
policy.

The request form retains safe reviewed metadata and the original request UUID
in that browser tab for lost-response recovery. Restore and re-review the same
destination before retrying; a changed policy, version, actor or source requires
fresh admission. Clearing this local reminder does not cancel a server request.
Use My requests to inspect the durable outcome. A write that succeeded remains
accepted if the following read fails; refresh before taking another action.

Read the organization's current policy before a versioned edit:

```graphql
query ModelAccessPolicy($organization: GUID!) {
  organizationModelConnectionPolicy(organizationId: $organization) {
    id version mode requiredApprovals allowSelfApproval
  }
}
```

`updateOrganizationModelConnectionPolicy` accepts that organization GUID,
`ifMatchVersion`, mode, quorum and self-approval choice. Version zero means the
organization is using installation defaults. A Super-admin reads a model's
overlay with `modelConnectionRestriction` before editing it through
`setModelConnectionRestriction`; an absent overlay is version zero and neutral,
not the effective organization policy. Both writes require their reviewed
versions; the model write also requires `ifMatchDeploymentVersion`.

Check an exact app environment's action without creating a request:

```graphql
query ModelAccessAction($target: ModelConnectionTargetInput!) {
  modelConnectionAction(input: $target) {
    action reason policyVersion requiredApprovals allowSelfApproval
  }
}
```

`target` contains `organizationId`, `modelDeploymentId`, `expectedClusterId`,
`expectedProviderId` and `appEnvironmentId`. Obtain GUIDs and reviewed versions
from the installed server's eligible, paginated `modelConnectionTargetsPage`.

## Inspect traffic for one app connection

Select a connection on the hosted model's detail page to inspect its recent
traffic. Refresh the selected connection's 15-minute window explicitly. The
request binds the organization, deployment, cluster, provider and subscription;
switching context clears the old observation. Aggregate model observations are
separate from an individual app's authenticated traffic.

The attributed measures are requests per second, errors per second, accepted
response bytes per second and p95 request duration in seconds. Error rate here
is an absolute count per second, not an error percentage. Bytes cover the ASGI
application's accepted sends, not proof of receipt by the caller. Token counts
and cost are **unsupported** until a real attributable usage source exists.
Missing, unconfigured, unavailable and stale measurements remain distinct from
measured zero.

The runtime's version 2 credential mapping supplies the authenticated
subscription identity; callers cannot choose it through headers. Operator and
unauthorized traffic are excluded. Older version 1 deployments need a completed
reconciliation to install this attribution. The installation must scrape the
model's authenticated ServiceMonitor and retain the canonical service, namespace
and pod labels. No prompt or response body is parsed for these measurements.

## Only installation Super-admins configure and host models

Catalogue browsing requires current organization read access. Adding a model,
changing its hosting configuration, managing model sources and declaring a
cluster runtime require an active installation **Super-admin**. An organization
administrator or team manager cannot obtain that installation authority by
assigning an organization role. A bearer credential must also retain its current
administrative scope, active organization membership and organization/team
ceiling; account authority does not widen a token.

The hosting action read is an eligibility hint, not authorization for an arbitrary
cluster. Request admission checks the actual target and freshly checks the actor
and credential after relevant database waits. Reconciliation checks the live
source, connection version, placement and runtime before applying the deployment.
An accepted request is not a guarantee that later reconciliation will succeed.

App owners can connect eligible app environments using their scoped app authority
and the organization's connection policy. Connecting to a hosted model does not
permit editing its runtime, source or deployment. A known GUID, visible catalogue
entry or successful capability read grants no extra permission. Ask an installation
Super-admin for configured, admitted placement when a runtime is unavailable.

## Connect Hugging Face without exposing the token

Use **Connect Hugging Face** in the hosting source form. Sign in to Hugging Face
and create a read or fine-grained token for the intended repository; use the
write-only UI field to save it for the current organization. The backend verifies
the account, encrypts the token at rest and returns only connection identity,
version, name, account username and verification time. It never returns the token.
See [Hugging Face user access tokens](https://huggingface.co/docs/hub/security-tokens)
for read and fine-grained permissions.

Choose anonymous access only for an actually readable public revision. A private
or gated repository needs an admitted connection and an independent access check
for the pinned revision. Resolve the full commit SHA rather than relying on a
moving branch or tag; Hugging Face's
[download revision contract](https://huggingface.co/docs/huggingface_hub/guides/download)
explains immutable commit selection.

For gated models, open the model's Hugging Face page using the same connected
account and submit its access request. Approval belongs to the model owner and
may require manual review; access is associated with the individual account.
Astro neither accepts those terms nor obtains approval for you. Return to Astro
and recheck the exact revision after approval. See
[Hugging Face gated models](https://huggingface.co/docs/hub/en/models-gated).

Read access is not license acceptance. Review the model card and license for your
intended use, then explicitly acknowledge that review in the hosting form. An
account verification or repository read is not proof that deployment is permitted
by the license.

Connections are version-bound to hosted models. Rotation in place is unsupported;
create a replacement connection and review a new hosting request. Disconnecting
a connection still used by a live model is refused. Disconnecting an unused Astro
connection does not revoke the token on Hugging Face. After an unconfirmed save,
refresh the connection list before another write; the first save may have
committed despite a lost reply.

## Import supported local files as an independent source

Local imports are organization-owned artifacts, not Hugging Face repositories.
Supply a flat manifest of filenames, decimal byte sizes and SHA-256 checksums.
The supported set requires `config.json`, `tokenizer.json` or `tokenizer.model`,
and safetensors weights. Supported supplementary configuration/tokenizer files
and safetensors indexes must remain within the server's allowlist. Archives,
nested paths, duplicate names, pickle weights and arbitrary model code are
refused. Configuration requiring remote code is unsupported.

An import reserves the manifest, authorizes short-lived private uploads, then
finalizes verified object versions. The current single-PUT contract supports at
most 256 files, 5,000,000,000 bytes (5 GB) per file, 64 MiB per JSON metadata file
and 200 GiB total. Multipart imports are unsupported; shard weights before
importing when one file exceeds the limit. These are import bounds, not an
estimate that the model will fit in the selected machine's memory.

Upload URLs and required signed headers are private capabilities. Keep them in
the upload client only; never print them to a terminal, job log or public issue.
Metadata reads return artifact GUID/version/state, manifest digest, file count
and total byte size, not object keys, object versions or upload/download URLs.
Finalization verifies the bytes and immutable storage identity; it does not
prove runtime launch, inference, model fit or health.

Review the license and redistribution rights of the local model files before
acknowledging the local-source review in the hosting form. Verified bytes do not
approve those rights or replace that review.

The import lifecycle is explicit: `beginLocalModelArtifact` reserves the file
manifest, `authorizeLocalModelUploads` issues private upload capabilities, and
`finalizeLocalModelArtifact` verifies stored versions. Upload authorization and
finalization take the exact artifact GUID and expected record version. After a
lost reply, refresh the artifact metadata and review its current state/version
before continuing; do not assume a new write or upload is needed.

### Installation prerequisites

The installation operator must configure the existing object store with
`AWS_STORAGE_BUCKET_NAME`. Region selection uses `AWS_S3_REGION_NAME`, then
`AWS_REGION` or `AWS_DEFAULT_REGION`, otherwise the existing `us-east-1` default.
Verify that the selected region actually matches the store. An optional
`AWS_S3_ENDPOINT_URL` must use HTTPS. The bucket must report versioning `Enabled`
and all four S3 Block Public Access settings enabled; absent/inaccessible checks
or suspended versioning refuse import. The API does not change those settings.

Existing backend credentials need versioning/public-access-block metadata reads,
checksum-enabled object HEAD, upload and version-specific reads on the import
prefix. Configured KMS encryption also needs the relevant checksum/encryption
permissions. Browser uploads require operator-configured CORS for the product
origin, PUT and the returned checksum/encryption headers. Keep public access
blocked; CORS does not require making model files public.
See [S3 uploading objects](https://docs.aws.amazon.com/AmazonS3/latest/userguide/upload-objects.html)
for single-PUT, versioning and checksum behavior. This guide does not provision a
bucket, change IAM or certify an installed storage service.

Local runtime delivery additionally requires the installation's certified,
digest-pinned vLLM 0.15.1 Python hydrator image and supported storage/PVC setup.
The hydrator verifies the complete immutable manifest before publishing the
model directory. Hosting selects `localArtifactId` and `expectedArtifactVersion`
instead of a Hugging Face repository/revision/connection; omit all Hugging Face
source fields for that input. Mixing the two source identities is refused.
Missing storage,
versioning, source identity, hydrator or runtime
configuration must refuse the operation; no local directory or Hugging Face
fallback is implied. Ask the operator to configure and verify delivery, then
review the same intended source and placement again. Issued upload grants expire
according to their original lifetime; this flow does not promise immediate URL
revocation or automatic abandoned-object cleanup.

## Check vLLM and hardware with distinct evidence

The shared flow currently supports the pinned **vLLM 0.15.1 Python generation
runtime**. CPU and GPU modes require separate operator declarations in the
cluster's `vllm_shared_runtimes`: version/image, supported package build,
architecture, hardware certification and nontrivial hardware node selectors.
An architecture label alone does not establish CPU/GPU capability. Unsupported
or unconfigured declarations refuse admission rather than choosing a default
runtime. Arbitrary remote-code execution is not enabled by hosting.

### Configure a cluster runtime

An installation Super-admin can open runtime setup from the wizard's Placement
step. Read the selected cluster/provider and their current versions before
editing. Declare a digest-pinned image, supported package and architecture,
explicit hardware selectors, supported data types, context/concurrency defaults
and ceilings, and CPU/memory/GPU request ceilings. Saving changes only the
selected CPU or GPU declaration and preserves the other mode and unrelated
provider settings. The API never returns the raw provider or authentication
configuration.

Certification requires an explicit operator acknowledgement and a bounded
evidence reference. Inspect the actual selected nodes and smoke-test the image
before certifying them. An uncertified declaration can be saved, but remains
ineligible for hosting. Saving a declaration does not build an image, label a
node, download a model, install a runtime or prove scheduling/inference.

Hosting accepts optional data type, maximum context and concurrent-sequence
requests within the declaration's supported values and ceilings. Context limits
are 256–131072 tokens; concurrency limits are 1–4096 sequences. Omitted controls
use declared defaults for new models and preserve stored values on updates.
The worker checks current declarations again before applying a queued model.
Existing declarations without these controls keep their previous contract and
cannot admit a request claiming the new controls.

The small CPU preset requests 1 CPU, 4 GiB memory, 1 GiB KV cache, float32,
256-token context and one concurrent sequence. These are request values, not
proof that the model fits or that the selected nodes support the image.
Require `models.runtime_settings` and inspect this metadata through the CLI:

```graphql
query RuntimeSetup(
  $organization: GUID!, $cluster: GUID!, $provider: GUID!
) {
  clusterModelRuntimeSettings(
    organizationId: $organization, clusterId: $cluster,
    expectedProviderId: $provider
  ) {
    organizationId clusterId providerId clusterVersion providerVersion observedAt
    modes {
      computeMode configured reason hardwareAdmission
      declaration {
        image version packageVersion architecture
        nodeSelector { key value }
        supportedDtypes defaultDtype
        defaultMaxModelLen maxModelLenCeiling
        defaultMaxNumSeqs maxNumSeqsCeiling
        cpuRequestCeiling memoryRequestCeiling gpuCountCeiling hardwareCertified
      }
    }
  }
}
```

`updateClusterModelRuntime` requires that exact reviewed placement and the
cluster/provider versions. A changed target, version or authority refuses the
write. After an unconfirmed save, refresh this exact metadata before another
submission. A saved declaration with a failed follow-up read remains saved; a
refresh error does not justify repeating the mutation.

| Check | Evidence and next action |
|---|---|
| Repository access | Verify the exact revision and selected connection. Request gated access on Hugging Face when needed. |
| License | Review the model card/license yourself. Repository access is a separate fact. |
| vLLM support | Review the model architecture against the [vLLM 0.15.1 supported-model list](https://docs.vllm.ai/en/v0.15.1/models/supported_models/). Other vLLM versions or task types do not establish support in this flow. |
| CPU/GPU admission | Select explicit resources and an operator-certified runtime. Admission describes declared configuration, not measured capacity. |
| Weights and KV-cache fit | Check the selected hardware's available memory for the exact weights, context and cache. Fit remains unproven until actual deployment observations. |
| Launch/readiness | Follow the exact deployment and current generation. Queuing, an applied manifest or uploaded bytes is not a successful model server. |

The [vLLM 0.15.1 CPU installation guide](https://docs.vllm.ai/en/v0.15.1/getting_started/installation/cpu/)
describes CPU requirements and `VLLM_CPU_KVCACHE_SPACE`. Weight memory and cache
both consume capacity; CPU admission is not a model-specific memory test. The
[vLLM 0.15.1 server reference](https://docs.vllm.ai/en/v0.15.1/serving/openai_compatible_server/)
is the upstream serving contract, not a claim that every endpoint/task is exposed
through Astro's bounded model APIs.

A recorded readiness observation belongs to an exact deployment generation and
subscription revision. It is not a perpetual health guarantee. Refresh current
facts and inspect the operation's reason before retrying a failed or unconfirmed
request. Generic GraphQL has no durable CLI install-request recovery or automatic
write replay for this hosting flow.

## API boundaries for clients

| API | Boundary |
|---|---|
| `modelHostingAction` | Administrative eligibility hint for the current organization; not target-cluster authorization. |
| `huggingFaceConnectionsPage` | Super-admin-only bounded safe connection metadata; no token. |
| `connectHuggingFace` | The write-only form submits organization/name/token; success returns safe connection metadata. |
| `disconnectHuggingFace` | Exact organization, connection GUID and expected version; live model users prevent removal. |
| `clusterModelSourceAccess` | Independent pinned-revision download check, using the selected connection GUID/version or anonymous access. |
| `clusterModelRuntimeAdmission` | Declared target/provider/resource eligibility without deployment effects. |
| `clusterModelRuntimeSettings`, `updateClusterModelRuntime` | Typed, versioned Super-admin declarations for one compute mode; no raw provider configuration or deployment effects. |
| `provisionClusterModel` | Reviewed source and placement input; an accepted deployment remains asynchronous. |
| `clusterModelUpdateAdmission`, `updateClusterModel` | Exact stored source and reviewed version; Super-admin configuration authority, asynchronous reconciliation. |
| Local artifact lifecycle | Independent manifest reservation, private uploads and immutable finalization; metadata reads never include grants. |
| `subscribeClusterModel` | Subsequent exact deployment/app-environment/alias review; request acceptance is separate from applied access. |
| `astroliftModelSubscriptionMetrics` | One authenticated subscription's bounded observations under current `app.read_metrics` and organization visibility. |

These APIs return current facts or mutation envelopes, not a durable CLI
request-file protocol. Check both GraphQL `errors` and mutation `ok`, retain exact
identity/version bindings and inspect current metadata after unconfirmed writes.

## Read safe metadata with the existing CLI

Use [the existing CLI login/setup](../getting-started.md) and
[capability discovery](capabilities.md). `models.admin_hosting`,
`models.huggingface_connections` and `models.local_artifacts` advertise their
respective APIs, not permission, model access or working storage. Local import
requires its installed artifact schema and prerequisites above. Select the
current organization with
`--org`; any `organizationId` variable must identify that same organization.

Run bounded read-only documents through `astro api graphql`. It prints the
requested `data` as JSON and reports GraphQL errors on stderr. It does not inspect
mutation `ok` for you; API clients must check both. Never put an HF token or a
signed upload URL in shell arguments, GraphQL output selections or logs. Use the
write-only connection form and private upload client for those operations.

Save this as `hosting-metadata.graphql`:

```graphql
query HostingMetadata($organization: GUID!, $page: Int!, $pageSize: Int!) {
  modelHostingAction(organizationId: $organization) { allowed reason }
  huggingFaceConnectionsPage(
    organizationId: $organization, page: $page, pageSize: $pageSize
  ) {
    items { id version name accountUsername verifiedAt }
    page pageSize totalCount
  }
}
```

Save non-secret identifiers and bounds as `hosting-metadata.json`:

```json
{
  "organization": "00000000-0000-4000-8000-000000000001",
  "page": 1,
  "pageSize": 25
}
```

```bash
astro api graphql --org YOUR_ORG --file hosting-metadata.graphql \
  --vars-file hosting-metadata.json --json
```

Connection pages are numbered and bounded to 50 rows. Continue with the same
organization and reviewed context. A failed/denied read is not an empty verified
connection list; `allowed: true` still does not authorize an arbitrary cluster.

Read a known Hugging Face revision with the exact saved connection version:

```graphql
query ModelDownloadAccess(
  $organization: GUID!, $repo: String!, $revision: String!,
  $connection: GUID, $connectionVersion: Int
) {
  clusterModelSourceAccess(
    organizationId: $organization, modelRepo: $repo, revisionSha: $revision,
    connectionId: $connection, expectedConnectionVersion: $connectionVersion
  ) {
    accessible reason observedAt
    model { repoId revisionSha license gated architectures compatibility }
  }
}
```

Supply the full commit SHA and reviewed connection GUID/version in a non-secret
variables file. For anonymous public access, omit both optional connection
variables. A successful access check is a bounded observation, not authorization
for a later mutation or license acceptance.

Check declared runtime admission using the reviewed placement/resource input:

```graphql
query ModelRuntimeCheck($input: ProvisionClusterModelInput!) {
  clusterModelRuntimeAdmission(input: $input) {
    eligible reason runtimeVersion architecture hardwareAdmission
  }
}
```

Use the reviewed organization/cluster/provider GUIDs, model source identity and
explicit CPU/GPU resource fields in the input variables. This query does not
create a deployment. `eligible: true` describes operator-declared runtime
admission; it does not prove access, licensing, scheduling, memory fit or launch.

Read local artifact metadata without selecting private upload authorization:

```graphql
query LocalModelSources($limit: Int!, $after: String) {
  astroliftLocalModelArtifactsPage(limit: $limit, after: $after) {
    items { id version name state manifestSha256 fileCount sizeBytes }
    totalCount nextCursor
  }
}
```

Start with `limit: 25` and `after: null`; continue with the returned cursor in the
same organization. The import API requires current installation Super-admin
authority and admitted credentials even for this metadata read.

Follow one deployment by its immutable GUID, independently of catalogue paging:

```graphql
query HostedModel($organization: GUID!, $id: GUID!) {
  clusterModelDeployment(organizationId: $organization, id: $id) {
    id version organizationId clusterId providerId modelRepo revisionSha
    sourceKind localArtifactId localArtifactVersion localManifestSha256
    sharingMode dedicatedAppId dedicatedAppVersion dedicatedAppName dedicatedAppSlug
    status reason ready readinessObservedAt readinessGeneration
    desiredSubscriptionRevision appliedSubscriptionRevision
    operationId operationStartedAt operationCompletedAt
    runtimeSupported runtimeReason
    desiredResources {
      cpuRequest memoryRequest gpuCount replicas cpuKvCacheGiB dtype maxModelLen maxNumSeqs
    }
    appliedResources {
      cpuRequest memoryRequest gpuCount replicas cpuKvCacheGiB dtype maxModelLen maxNumSeqs
    }
  }
}
```

For a local source, `sourceKind` is `local_artifact`; use the artifact GUID,
version and manifest digest to identify its bytes. `modelRepo` is the stable
served identifier `local-<artifact GUID>` and `revisionSha` is null, not a
Hugging Face repository or commit. Hugging Face sources use `huggingface` as their
source kind and the pinned repository/revision. An `unknown` source kind does
not establish a valid source identity.

Read one app connection's observations with current app-metrics authority:

```graphql
query AppModelTraffic(
  $organization: GUID!, $model: GUID!, $subscription: GUID!,
  $cluster: GUID!, $provider: GUID!, $start: DateTime!, $end: DateTime!
) {
  astroliftModelSubscriptionMetrics(
    organizationId: $organization, serviceId: $model,
    subscriptionId: $subscription, expectedClusterId: $cluster,
    expectedProviderId: $provider, start: $start, end: $end
  ) {
    serviceId clusterId subscriptionId scope start end retrievedAt stepSeconds
    metrics {
      key unit source state observedAt value aggregationWindowSeconds
      samples { timestamp value }
    }
  }
}
```

Use the subscription's actual immutable GUID and placement, and a UTC interval
from one minute to 24 hours. Results contain at most 120 samples per measure and
use a five-minute rate window. The `models.authenticated_subscription_metrics`
capability advertises this API; it grants no access to another app's traffic.

Use the same `astro api graphql --file ... --vars-file ... --org ... --json`
shape for each document. Review the installed SDL before requesting fields; an
unknown field or explicit unavailable state must not be replaced with fabricated
successful metadata. Read [the control API contract](../reference/api.md) for
errors, credentials and tenancy boundaries.
