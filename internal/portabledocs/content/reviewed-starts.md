# Review exact workflow and pipeline starts

Use immutable GUIDs and saved request identities when dispatching work whose
response might be lost. These commands require a CLI release containing
`workflow definition-review` and `pipeline reconcile`, plus an installation
exposing the reviewed-start API. Check `astro <command> --help` and
`astro status --json` before using them; older clients do not gain these
commands from a server upgrade.

The selected server, organization and authenticated user apply throughout.
`astro whoami --json` shows that context without credentials. Account grants
alone do not establish bearer-token scope or current target permission. Definition
review and recovery require `workflow.read`; dispatch requires `workflow.trigger`.
A token can use `read:apps` and `workflow:trigger` within its existing user grants.
Pipeline reads require `app.read` and writes require `app.update`, within the
token's `read:apps` / `write:apps` ceiling. Current target permissions are checked
again by the API. `--yes` confirms the local operation; it does not replace
server approval, policy or an action-admission proof.

## Review a workflow definition

Find a definition GUID in the dashboard or the definition list:

```sh
astro workflow definitions --json
astro workflow definition-review <definition-guid> --json
```

Review output includes the exact definition GUID, revision, enabled state and
JSON Schema digest. Global definitions and organization overrides can share a
slug: the start command accepts a GUID, and never substitutes an override by
slug. The input contract reports `supported`, `acceptsInputs`,
`supportsSimpleForm`, and typed field metadata. An unsupported schema can be
inspected but cannot start. A disabled definition cannot start either.

Inputs use JSON Schema draft 2020-12. The server validates the whole object,
applies direct property and array-item defaults, and checks the current schema.
Object, array and composition fields marked `simple: false` still work through
a JSON file when the contract is supported. Composition/reference defaults
remain annotations. An explicit closed no-input schema accepts no inputs.

For an input-bearing definition, save one JSON object in a private local file:

```json
{
  "message": "Example task",
  "options": {"limit": 3},
  "credential": "secret://opaque-reference"
}
```

The keys must match your reviewed schema; this example does not define a
universal workflow input. Fields marked `writeOnly` or
`x-astrolift-sensitive` accept secret references, never literal credentials or
defaults. References are opaque inputs and are not expanded by this start
mechanism; configure the agent's actual secret bindings separately.

Protect the input file yourself: use `chmod 600 inputs.json` on POSIX systems,
or a private file ACL on Windows. Input files are user-supplied files; they are
not created or given the CLI's recovery-file protections automatically.
The CLI accepts a readable regular JSON file of at
most 64 KiB, containing exactly one object. It preserves JSON number values and
supports nested structures. It refuses symlink input files. Input values are
sent to the API, which stores frozen inputs encrypted; they are never written
to the CLI's request file or control output. Remove the local input file when
you no longer need it.

```sh
astro workflow definition-start <definition-guid> \
  --expected-revision <reviewed-revision> \
  --expected-input-schema-digest <reviewed-schema-digest> \
  --inputs-file ./inputs.json \
  --request-file ./definition-request.json --yes --json
```

The two expected-value flags are optional as a pair. Supplying both binds the
start to a prior review; a change refuses dispatch. Without them, the CLI reads
and binds the current exact revision and schema immediately before reserving
the request. The API rechecks that revision, schema, enabled state and current
authority under its dispatch locks. A no-input definition omits `--inputs-file`;
an input-bearing contract requires a file, including `{}` when defaults suffice.

## Current authority and safe refusal recovery

Review is advisory at the time of the read. Servers containing the reviewed-start
admission repair refresh the active actor, organization membership, bearer scope
ceiling and scoped grants after
reservation, dispatch and recovery locks. Before creating or submitting work,
it checks the complete locked definition/stage graph, current project/team
ownership and current agent-cluster region against fresh policy decisions.
A retained role binding does not authorize a removed member. An active
platform-operator browser session retains its existing membership exception;
that exception does not extend to a bearer token without active membership.
These checks do not cancel an execution that was already accepted before a
later permission or policy change.

| Observed result | Next step |
|---|---|
| `PERMISSION_DENIED` | Confirm the selected organization, active membership, exact target grants and credential ceiling. Reauthentication can refresh a credential; it cannot grant missing authority. |
| `PRECONDITION` from changed revision/schema or enabled state | Re-read the exact GUID and review its current contract. Preserve an existing request file and reconcile its original identity before considering a separate new request. |
| `STEP_UP_REQUIRED` in a browser | Follow the server's `supportedMethods` and `requiresAttestation` result. Browser SSO setup is below; a local `--yes` is not elevation. |
| Lost response, transport error or `dispatchStatus: uncertain` | Retain the original request file and reconcile. Do not infer that no work started or create a replacement blindly. |
| Recovery refused after authority withdrawal | Preserve the request identity; restore legitimate access or ask an authorized operator to inspect the execution through its own permitted read surface. Recovery does not bypass current authority. |

`astro whoami --permissions --json` reports account grants held somewhere in the
selected organization. It does not report this token's scope ceiling or permission
to act on the selected definition. A server capability is API availability, not
an authorization decision or proof that Temporal and its worker are ready.

## Browser SSO setup for sensitive operations

This section applies to a compatible server with the reviewed SSO admission
contract. CLI device-flow login yields a bearer credential; it does not elevate
the separate browser session. The existing elevation decorator checks browser
session recency when the installation enables `REQUIRE_STEP_UP_AUTH` (default
`false`); API-token calls use the existing bearer/grant checks instead. Never
copy browser cookies into CLI configuration or retry a browser ceremony with a
bearer token. There is no dedicated `astro` SSO-elevation command.

Operators configure the existing OIDC client with `AUTH0_DOMAIN`,
`AUTH0_CLIENT_ID`, `AUTH0_CLIENT_SECRET`, `AUTH0_CLIENT_SCOPES` including `openid`,
and optionally `AUTH0_SERVER_METADATA_URL`. Keep the client secret in the
installation's secret configuration. Register the exact callback URL generated
by the server's `auth1-elevate-sso-callback` route with that provider. With the
normal `/app/` API prefix and HTTPS origin, it is:

```text
https://astrolift.example.com/app/auth1/elevate-sso/callback/
```

Use the actual API origin and configured route prefix, including the trailing
slash. A frontend-only origin or localhost callback does not substitute for it.
The flow requires persisted Django database sessions. Verify the signed ID token's
configured issuer/audience, requested nonce and fresh `auth_time` are supported
by the IdP. User-info JSON or matching email is not authentication proof: the
verified issuer/subject must link to the same active internal actor already
using that browser session. An unlinked account must use the normal supported
login/linking procedure first; step-up does not create or link accounts.

The browser uses `auth1-elevate-sso-start` with a safe relative return path, for
example `/app/auth1/elevate-sso/?return=/app/admin/`. The server requests
`prompt=login` and `max_age=0`. `STEP_UP_SSO_FRESHNESS_SECONDS` defaults to 60
seconds and is checked after admission locks. Elevation lasts for the existing
`STEP_UP_AUTH_TTL_SECONDS`, capped by `STEP_UP_AUTH_MAX_TTL_SECONDS` (both default
900 seconds). Do not increase a time limit simply to reuse a stale proof.

Concurrent logout, session expiry or key rotation, authentication-hash/password
changes, account switching and removed/reassigned identity links refuse the
ceremony. A refusal returns a short `stepUp` code such as `session_mismatch`,
`identity_mismatch`, `stale_auth_time` or `token_exchange_failed`. Start a new
ceremony in the current authenticated session; ceremonies started before the
binding upgrade must also be restarted. Do not log provider bodies, claims,
state/nonce values or credentials while diagnosing a refusal. Elevation is
recent authentication, not approval or new target authority.

The API exposes metadata about the current caller's elevation:

```graphql
query CurrentElevation {
  astroliftElevationStatus {
    elevated elevatedUntil secondsRemaining method requiredFor
  }
}
```

This query does not elevate a session. A query through `astro api graphql` uses
the CLI bearer context, not the browser's session. `requiredFor` is a discovery
list; enforce the actual mutation envelope rather than treating it as a complete
admission matrix. When implementing a client, request every public error field
so version and elevation refusals remain usable:

```graphql
mutation ReviewedDefinitionStart($input: StartWorkflowDefinitionInput!) {
  startWorkflowDefinition(input: $input) {
    ok
    errors {
      code message field currentVersion requestedVersion
      supportedMethods requiresAttestation
    }
    data { requestId executionId temporalWorkflowId temporalRunId dispatchStatus }
  }
}
```

Use the stable reviewed request/body described above. This mutation example is a
contract illustration, not a setup probe: do not submit it merely to test SSO or
installation availability. Configure and verify an explicitly disposable
installation before collecting end-to-end acceptance receipts.

## Keep the request file and reconcile

Before dispatch, the CLI exclusively creates and flushes a private metadata
file. On POSIX it must be a regular file with mode `0600`. On Windows the owner
must be the current process user, with a protected, non-inherited DACL allowing
only that user and optionally `LOCAL_SYSTEM`. Windows reads validate the actual
file handle's owner and ACL and refuse broader, inherited or unsupported ACLs
and final reparse-point targets. Recovery files are never automatically replaced;
an existing file with inadequate protection is refused. File contents are
flushed before dispatch; POSIX also synchronizes the containing directory.

The file contains a new UUID, exact definition GUID, reviewed revision
and schema digest, server, organization and user identity. It contains no
runtime inputs, results or bearer credentials. Protect it and retain it after a
timeout, refusal or lost reply; replacing an uncertain request with a new file
would create a different request identity.

```sh
astro workflow definition-reconcile --request-file ./definition-request.json --json
```

Recovery reads only the original actor-scoped request. Repeating
`definition-start` with an existing file does the same read before considering
any input flags: it never opens or resubmits inputs, never reviews a replacement
definition, and never mints a new key. Server, organization, user and definition
GUID must match the saved context. An absent record means the original outcome
is unknown; the CLI returns an error and preserves the file. It does not assume
that dispatch failed or restart the workflow.

The receipt includes execution GUID, request UUID, exact definition/revision/
schema identities, dispatch status and recorded Temporal workflow/run IDs.
Treat engine IDs as opaque. A successful start means the engine accepted the
submission, not that execution completed. Known `reserved`, `uncertain` or
`refused` records are printed by reconciliation; the start command prints their
metadata and returns an unconfirmed error. Recovery never submits a replacement
execution or restarts a closed one. Read outages also leave the outcome unknown.
Server-side resubmission has its own 24-hour bound; it does not authorize this
CLI to retain or resend input values for an existing file.

These commands address a `WorkflowDefinition` directly. Existing
`astro workflow run` remains the separate app-bound configured-workflow path.
The next CLI adapts legacy `astro agent run` to this reviewed path; see the
[migration guide](agent-setup.md#migrate-a-legacy-workflow-definition-run).
Older slug-only API calls are refused with `PRECONDITION` rather than silently
dispatching without revision/schema/request proof.

## Start and inspect a pipeline

```sh
astro pipeline list --limit 50 --json
astro pipeline run <pipeline-guid> --branch main \
  --request-file ./pipeline-request.json --yes --json
astro pipeline reconcile --request-file ./pipeline-request.json --json
astro pipeline show <run-guid> --json
astro pipeline runs --pipeline <pipeline-guid> --limit 20 --json
```

Lists are bounded server pages. Continue with the returned `nextCursor` as
`--after`; use `--search` to filter on the server. Ambiguous pipeline names
require an exact GUID. A pipeline request file binds the server, organization,
actor, exact pipeline GUID, version, ref and original UUID before dispatch.
Retain it if a response is lost.
Pipeline recovery files use the same POSIX/Windows protections described above.

Pipeline recovery differs from definition recovery because its saved inputs are
only reviewed metadata. Reusing a pipeline file first reads the original request.
A known record is read-only. A successful **null** recovery can submit the same
original key and ref only with `--yes` and an unchanged exact pipeline version.
A read outage never dispatches. No replacement key or fallback is created.
`pipeline reconcile` is always read-only. Definition recovery never resubmits
sensitive inputs, including after a null lookup.

## Cancel the exact pipeline run

```sh
astro pipeline cancel <run-guid> --yes --json
astro pipeline show <run-guid> --json
```

Cancellation uses the exact run GUID, current version and recorded Temporal
workflow/run IDs. An engine acknowledgement means the cancellation request was
accepted. Inspect `status`, `cancellationStatus` and `cleanupStatus` separately:
accepted cancellation is not workflow closure, and closure is not successful
Kubernetes cleanup. Cleanup may be pending or failed after execution stops.
An unconfirmed start or cancellation returns an error and preserves its identity.

Pipeline jobs and step records expose bounded pages and recorded diagnostic log
excerpts through the API. Recorded excerpts are not a live log stream and do not
establish that a Kubernetes job is still present. Cleanup targets the recorded
cluster, namespace and Job UID; it does not report complete until owned jobs,
pods and secrets are gone. The CLI's `pipeline show` is metadata only.

## Discover the contract

The installation advertises `workflows.reviewed_definition_starts`,
`workflows.definition_input_contracts`, `workflows.definition_start_recovery`, `pipelines.reviewed_starts`,
`pipelines.versioned_start_requests` and `pipelines.start_request_recovery`.
Capability availability is not authorization for a particular target.

The API uses `workflowDefinitionById`, `startWorkflowDefinition`,
`workflowDefinitionStartRequest`, `startPipelineRun`, `pipelineStartRequest` and
`cancelPipelineRun`. Unsupported API fields fail closed without a legacy write
fallback. See the [control API](../reference/api.md),
[workflow setup](workflow-setup.md) and
[exact environment controls](environment-actions.md) for adjacent contracts.

The CLI's local confirmation is not a mobile consent receipt, device attestation
or action-admission proof. These commands do not certify those separate contracts.
