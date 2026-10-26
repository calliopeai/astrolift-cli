# Review a complete workflow and approve its schedule

Require `workflows.atomic_full_review` in installation discovery. The full-review
API captures the native root definition, reachable children, stages and source
agent identities in one PostgreSQL `REPEATABLE READ, READ ONLY` transaction.
Review does not edit, enable or execute the definition.

Use the [control API](../reference/api.md) with the selected organization and
current authenticated person. These operations add no dedicated CLI command.
`astro api graphql` can submit supported GraphQL operations; the executing CLI
and server still have their own version and authorization requirements.

## Review the full definition graph

```graphql
query ReviewDefinition($id: GUID!) {
  workflowDefinitionFullReview(id: $id) {
    definitionId revision capturedAt consistency canonicalization
    reviewDigest canonicalReview rootManifest inputSchemaDigest
  }
}
```

`canonicalReview` contains the authoritative complete graph. `reviewDigest` is
SHA-256 of those exact UTF-8 bytes; preserve them without reformatting when
checking the digest. `revision` is the native definition revision from those
same captured rows. `capturedAt` is the capture transaction's statement time.
`rootManifest` is a native TOML projection of the root only; reading it alone
does not review nested definitions.

Every root and child needs current `workflow.read` at its actual scope. Source
and configured agents independently require `agent.read`, including a source
agent replaced by a configured binding. Current membership, bearer scope/team
ceilings, owners, IdP groups and policy apply. A missing, foreign or unreadable
child or agent refuses the whole review before returning raw content.

Enable a disabled owned definition separately, using its exact GUID and reviewed
revision:

```graphql
mutation EnableDefinition($id: GUID!, $revision: String!) {
  setWorkflowDefinitionEnabled(
    definitionId: $id, expectedRevision: $revision, isEnabled: true
  ) {
    ok errors { field messages }
    definitionId revision isEnabled changed
  }
}
```

This is revision-bound native definition activation. It neither publishes a
global recipe nor approves a schedule or starts a run. If content changed,
review again. Once enabled, capture a fresh configured schedule review.

## Review the configured schedule

```graphql
query ReviewSchedule($workflowId: GUID!) {
  workflowScheduleFullReview(workflowId: $workflowId) {
    workflowId definitionId capturedAt consistency canonicalization
    canonicalReview rootManifest canonicalConfiguration
    reviewDigest revision inputSchemaDigest configurationDigest
    compiledPlansDigest scheduleReviewDigest activationIntent
  }
}
```

Review `canonicalReview` together with `canonicalConfiguration`. Configuration
includes supplied inputs and resolved defaults, stage bindings, trigger kind
and reference, cron, enabled state, owner, and selected delivery channel
identities/versions. Delivery credentials are excluded. This API reviews
existing native configuration; it provides no offline writeback stage or new
Slack/Teams destination configuration or authorization contract.

`compiledPlansDigest` covers root and nested compiled plans, with configured
root bindings applied. `scheduleReviewDigest` binds the graph, configuration,
compiled plans and `ENABLE_REVIEWED_SCHEDULE` intent. An operational `version`
counter or engine observation does not invalidate its own approval. Review
content changes deliberately before capturing a new digest.

## Save the person's exact schedule consent

Only after the person reviews the complete returned content and activation
intent, submit its exact digest with a new stable `requestId` and
`confirmed: true`:

```graphql
mutation ApproveSchedule($input: ActivateReviewedWorkflowScheduleInput!) {
  activateReviewedWorkflowSchedule(input: $input) {
    ok errors { code message field supportedMethods requiresAttestation }
    data {
      id workflowId definitionId approvedByUserId approvedAt
      scheduleReviewDigest reviewDigest definitionRevision
      configurationDigest compiledPlansDigest credentialKind credentialExpiresAt
      isCurrent databaseStatus engineState
    }
  }
}
```

```json
{
  "input": {
    "workflowId": "<configured-workflow-guid>",
    "expectedScheduleReviewDigest": "<captured-64-lowercase-hex-digest>",
    "requestId": "person-reviewed-schedule-unique-id",
    "confirmed": true
  }
}
```

Activation requires current root `workflow.update`, root/child
`workflow.trigger`, selected agent `agent.dispatch`, and all review authority.
The server rechecks under content locks. The receipt records the actual
approver and optional API-token row/ceiling, without plaintext bearer material.
Repeating the same person's request ID and arguments recovers the saved receipt;
changed arguments refuse. Recovery does not reactivate a superseded receipt.

Where the installation requires step-up, browser consent uses the existing
native elevation check and rechecks the fresh authenticated session after
content locks. API bearers retain the native exemption from browser elevation,
with current token ceilings, revocation and expiry enforced. This adds no new
sign-in flow or background session/MFA requirement.
See [native browser step-up setup](reviewed-starts.md#browser-sso-setup-for-sensitive-operations).

The returned `scheduleReviewDigest` preserves the original reviewed, pre-enable
configuration. The receipt's `configurationDigest` covers the resulting
configuration with `isEnabled: true`, so it can differ from the review query's
configuration digest. `isCurrent` means the configured workflow currently points
to this receipt; it does not prove unchanged content, authority or engine state.

The mutation saves `databaseStatus: SAVED` and returns
`engineState: UNCONFIRMED`. It performs no Temporal transport. Inspect/reconcile
the configured schedule through the existing schedule APIs to request or
observe engine activation. A saved approval is not an active-engine receipt or
a completed run. For an immediate reviewed start, use the separate
[reviewed-start contract](reviewed-starts.md).

## Later timer reservations and human gates

Each timer reservation rechecks the approver's account, membership, live grants,
IdP groups, policy and recorded bearer validity. Revocation, expiry, deletion,
rotation or a changed/narrowed ceiling stops future reservations. Approval
cannot expand the recorded scope, team or expiry ceiling. The background timer
does not fabricate a client IP, sign-in, MFA or IdP request assertion; policy
requiring unavailable human request facts refuses it.

A changed graph, inputs, bindings, trigger, owner or selected delivery channel
requires review before another run is reserved. Accepted reservations execute
encrypted immutable root/nested plans, with the approval linked as provenance.
They do not resolve replacement live bindings after reservation. Changed agent
or app identity, version or ownership can still refuse later dispatch.

Run-specific human gates remain required. Their saved stage descriptors include
kind, role, prompt and approvers, so later definition edits cannot substitute an
approver or turn an existing gate into a checkpoint. Schedule consent creates
no human-gate exemption.

Limits are 128 reachable definitions, 2,048 stages/agent candidates, nesting
depth 8, 50 selected delivery channels, and 2 MiB per canonical review or sealed
approval payload. Unsupported graphs, invalid inputs/bindings, and unavailable
or corrupt sealed plans refuse. Existing schedules without a reviewed receipt
keep their prior behavior.

## Operator rollout requirement

The matching release includes `workflows.0021_reviewed_schedule_approval` for
durable receipts, run associations, saved stage descriptors and immutability
guards. Apply its normal release migration before adopting reviewed schedules.

Deploy and drain old worker images before exposing this activation API, then
deploy the matching API image. Old schedulers can reserve an unlinked run
without understanding reviewed approval, and old API images lack immutable gate
projections. Do not activate reviewed schedules while those images remain in
service. After adoption, drain reviewed schedules before rolling back to old
API/worker code; prefer forward repair. Database guards do not make arbitrary
mixed versions safe.

Contract source:
[reviewed schedule protocol at b1fb41a](https://github.com/calliopeai/astrolift-app/blob/b1fb41afdd7111bf726859a2fb4ab443e22707e0/docs/runbooks/atomic-workflow-review.md).
