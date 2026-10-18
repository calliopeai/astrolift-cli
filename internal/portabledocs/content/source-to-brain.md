# Build source and host it on an existing brain

Use `builder-brain-source-v1` to build an immutable image from a private Builder
source environment, then host that exact artifact on a readable brain in a
selected project. These are two separate steps. `artifact_ready` means the image
was built and verified; it has no app, binding, deployment or running URL.

This is a bearer REST protocol. It adds no `astro` command. The API contract and
source checks do not certify an installation's IAM policy, registry, image or
running workload. An operator must configure the target and separately authorize
an artifact and runtime acceptance check.

## Check access and the project target

Require `builder.brain_source_artifacts` in installation discovery. The
authenticated `GET /api/builder/v1/capabilities/` response includes:

```json
{
  "brain_source": {
    "protocol": "builder-brain-source-v1",
    "phases": ["build", "host"],
    "requires_project_target": true,
    "providers": ["aws"],
    "runtime_verified": false
  }
}
```

Discovery advertises API availability. It does not prove a configured or healthy
target. This protocol currently supports an operator-prepared managed AWS target;
generic Kaniko support does not establish another provider's registry identity.

Use the original person's current `alft_at_…` bearer with `write:apps` and the
organization's `chat_studio_integration` module enabled. Sessions and deploy
tokens refuse. Source access requires current TEAM/ORG authority plus the source
creator or team administrator. Destination admission requires current project
`app.create` and `app.deploy`, coherent organization/team/project ancestry, the
bearer's team ceiling, and a readable served brain in that exact project. A
request header cannot select a trusted Chat Studio surface or widen access.

The operator configures `BUILDER_BRAIN_BUILD_TARGETS`, keyed by the exact project
GUID:

```json
{
  "<project-guid>": {
    "organization_guid": "<organization-guid>",
    "cluster_guid": "<managed-aws-cluster-guid>",
    "namespace": "project-source-builds",
    "namespace_uid": "<actual-namespace-uid>",
    "service_account": "project-source-builds",
    "service_account_uid": "<actual-serviceaccount-uid>",
    "role_arn": "arn:aws:iam::123456789012:role/project-source-build-push",
    "registry_repo_name": "project-source-builds",
    "registry_repo_uri": "123456789012.dkr.ecr.us-east-1.amazonaws.com/project-source-builds",
    "attested_project_isolation": true
  }
}
```

The resources must already exist. The cluster must be live, managed and owned by
the organization or explicitly shared, with its enabled AWS provider account,
region and recorded OIDC issuer. Brain token issuance also needs the install's
HTTPS `APP_BASE_URL`. Missing or unsupported targets refuse without provisioning,
resource adoption or IAM grants.

The isolation attestation is an operator trust decision. Each project needs its
own repository, role and cluster/namespace/service-account tuple. Limit the IRSA
role to required Kaniko push/pull actions for that repository and ECR's
authorization-token action. It must not grant other tenants' image reads,
deletion, IAM mutation or Kubernetes API/Secret access. Server reads check the
namespace and service-account UIDs, role annotation and strict immutable ECR
tags; they do not inspect IAM policy or prove network isolation.

## Step 1: build the admitted source

Create and sync the source environment through the existing
[Builder API](https://github.com/calliopeai/astrolift-app/blob/ada29b4f02e9e28efd159f121408daf62f4ffc88/docs/builder-api.md).
Then read its exact current archive descriptor:

```http
GET /api/builder/v1/dev-environments/<dev-guid>/source/
Authorization: Bearer alft_at_...
```

The response contains `archive_sha256`, `archive_bytes` and `expanded_bytes`.
This read constructs ZIP bytes in memory; it does not persist an archive or
create cloud resources. Use the returned digest in the build request:

```http
POST /api/builder/v1/dev-environments/<dev-guid>/brain-source-builds/
Authorization: Bearer alft_at_...
Content-Type: application/json
```

```json
{
  "project_guid": "<project-guid>",
  "brain_slug": "existing-brain",
  "app_name": "SQL explorer",
  "app_slug": "sql-explorer",
  "idempotency_key": "conversation-reviewed-action-unique-id",
  "expected_archive_sha256": "<64-lowercase-hex>",
  "expires_in": "4h",
  "delete_data_on_expiry": false
}
```

All fields except expiry are required, including the explicit boolean deletion
preference. ISO `expires_at` is an alternative to `expires_in`; sending both
refuses. Current [temporary-app policy](temporary-apps.md) applies at admission
and again at app creation. A relative TTL starts at app creation; an absolute
deadline must still satisfy policy then. Omitted expiry requests permanence
only where the current person and Chat Studio policy permit it.

Admission freezes the archive and recipe under a source-row lock. Later syncs
cannot replace those bytes. The key is unique per organization/person: an exact
repeat recovers the same intent, while changed source, destination, app, TTL or
deletion preference conflicts. Keep the intent GUID and key after an uncertain
response. Repeating a closed failed build never restarts it; a new reviewed
build requires a new intent and key.

Poll `GET /api/builder/v1/brain-source-builds/<build-guid>/` with the original
current bearer. Status is `queued`, `building`, `failed`, `artifact_ready` or
`hosted`. `failure_code` is fixed metadata, without source, build output or
credentials. Continue to step 2 only after `artifact_ready`.

## Step 2: host the verified artifact in a fresh request

After reviewing the observed image digest, make a new authenticated HTTP request
as the original person using the original current bearer:

```http
POST /api/builder/v1/brain-source-builds/<build-guid>/host/
Authorization: Bearer alft_at_...
Content-Type: application/json
```

```json
{"expected_image_digest":"sha256:<64-lowercase-hex>"}
```

The server checks that digest against the intent and registry again and rechecks
current source, credential, destination and brain access. Host cannot change the
source, target or initial TTL. App, normal brain binding, deployment and intent
association commit atomically. Existing deployment approvals, action safety and
native-origin admission remain in force. No background system deployer is
substituted for the person's request.

An exact repeat preserves the same app, deployment and current expiry; it cannot
recreate a deleted app. Use the existing `setAppExpiry` operation for later
extensions. Do not substitute legacy team `promote/`, fabricate an image or put
brain secrets into the raw Builder runtime.

## Read the receipt and observe deployment

Reads and responses include the intent `id`, `protocol`, `status`, archive
digest, destination project/brain, image digest/reference, app and deployment
GUIDs, deployment/provisioning statuses, `runtime_url`, expiry, deletion
preference, `data_persistent` and `runtime_verified`. App, deployment, URL and
expiry are null before hosting.

`hosting_status` distinguishes `not_created` and `created`.
`build_submission` and `deployment_submission` distinguish `pending`,
`submitting`, `enqueued`, `uncertain` and `refused`; deployment also has
`not_requested` and `approval_pending`. Independent
`build_execution_status` and `deployment_execution_status` are engine
observations and remain null until observed. `hosted` means records exist.
Inspect the ordinary deployment separately before reporting a running app: an
assigned URL or submitted deployment does not prove reachability.
Deployment health and `runtime_verified` do not prove that the app can make an
authorized brain-MCP read. Check that compatibility separately with an
authorized brain-read canary.

Each execution commits its first-submission marker before its single elected
start attempt. Later requests recover exact matching history, including closed
runs. If history is absent after an attempt, a blind restart is refused, even
after engine retention expires. A crash between the marker and start requires
a new explicit review/intent. Revoked authority stops later effects or recovery.

## Source, SQLite and retained dependencies

The server generates the recipe for Python, Node, Ruby, Go or static runtimes;
an uploaded Dockerfile cannot replace it. Runtime environment values are not
baked into the image. `BRAIN_*` and `ASTROLIFT_*` overrides refuse; ordinary
per-deployment brain binding supplies scoped `BRAIN_*` credentials.

Files and an optional SQLite seed reach the image. The entrypoint seeds a
missing file into `/data` on an `emptyDir`. Pod replacement resets that data:
`data_persistent` is `false` with a seed and null without one. This flow creates
no PVC or managed database.

Expiry removes the app runtime and brain binding. Image/archive retirement is
not automated. Operators must preserve `intent-*` image tags and source
archives while builds or apps need them, alongside normal deployment retention
pins. Cleanup only targets recorded created Job/Secret UIDs after checking the
original namespace UID; it never deletes the preprovisioned namespace,
repository, service account or role. An uncertain resource creation without a
UID receipt cannot be adopted by name.

Contract source:
[operator protocol at ada29b4](https://github.com/calliopeai/astrolift-app/blob/ada29b4f02e9e28efd159f121408daf62f4ffc88/docs/operators/builder-brain-source.md).
Check the selected installation's current schema, discovery and target before
using it. See [the control API](../reference/api.md) for bearer and tenancy
conventions.
