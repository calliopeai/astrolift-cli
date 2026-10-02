---
name: astrolift-workflows
description: Author and operate Astrolift workflows with finite stage attempts, human-review return edges, serial record collections and exact reviewed starts. Use for Astrolift workflow setup or migration; generic standalone framework execution is outside this skill.
version: "0.1.1"
---

# Astrolift workflow authoring and operation

Read `astro docs show workflow-setup`, `astro docs show bounded-workflows` and,
for direct definition dispatch, `astro docs show reviewed-starts`. These are
release-matched offline guides. Public equivalents live under
https://astrolift.dev/guides/. Inspect `astro status --json` and the selected
server schema before authoring a contract that an older install may not ship.
Look for `workflows.bounded_review_loops` and `workflows.serial_collections`.
Capabilities describe available surfaces; current permissions and bearer-token
scope still govern reads, binding, dispatch and gate decisions.

## Select the actual execution contract

A definition contains ordered stages. A configured workflow binds agents and
inputs to a definition; its `workflow run` path differs from the direct reviewed
Definition start. Preserve the user's intended path. For a direct start, select
the definition GUID, review its revision and schema digest, and retain the
private metadata request file. After a lost reply, reconcile that original file
read-only rather than creating another key or dispatching a replacement.

Author TOML with `astro workflow init`, validate locally, then use
`astro workflow validate <file> --server` and import preview. Review explicit
agent-workload GUID bindings with `run-manifest --dry-run --bind <order>=<guid>`.
Resolve nested definitions through their actual reviewed identity. Labels and
same-slug targets in another app or organization are insufficient proof.

Use `agent = "guid:<workload-guid>"` or
`workflow = "guid:<definition-guid>"` for an explicitly selected target. Require
canonical lowercase, hyphenated UUID spelling. Export/re-import and source sync
preserve these references. Refuse unavailable, deleted or foreign GUID targets
without replacing them with a same-slug target. Literal slugs retain their
existing compatibility resolution, including late agent registration; review
the resolved identity before dispatch. GUID selection preserves child visibility
and current trigger/dispatch checks. Configured overrides are explicit reviewed
choices; a conflicting default agent mapping is refused. An exact GUID does not
prove source-framework model/tool equivalence.

## Keep every loop finite

- `max_attempts` is 1–20 including the first attempt, default 3. It bounds
  agent/nested-workflow retries under the authored failure policy.
- A `review_loop` needs an explicit `human_gate` rejection return to an earlier
  unique `output_key`. Set `max_rounds` from 1–20: one first visit and at most
  `max_rounds - 1` returns. Each edge's lifetime budget persists across revisits.
- Native return conditions are `gate_rejected`, `stage_failed` and scalar
  `output_equals` with an explicit field path/value. Exhaustion fails or
  escalates as authored. Imported `always`/continue-at-cap behavior requires
  the supported source control/output contract.
- Serial `collection` stages require `max_items` from 1–50, a forward
  `body_end` output key and exactly one literal record list or `items_path`.
  Missing/oversized data is unavailable; it is never truncated or converted
  into a healthy empty list. A verified empty list is a valid zero-item result.

Collection bodies are contiguous agent, nested-workflow, checkpoint, gate or
record-formatter stages. One durable item child finishes before the next starts.
Returns wholly inside a body are supported; nested collections, parallel bodies
and returns crossing the body boundary are refused. Respect the server's total
plan budget: caps, repeated visits, items and fan-out multiply execution cost.
Local validation does not certify that composition or authorize the targets.

## Observe the recorded outcome

Use `astro workflow execution-stages <execution-guid> --json` under the original
server/org. Inspect recorded rounds, attempts, return cause, timestamps and
collection parent/index separately from fan-out identities. JSON item/branch
indexes start at zero; text labels start at one. Missing metadata stays unknown.
Use `workflow gates` and select the intended run when deciding an item gate.

A completed body under skip or cleared escalation retains that failure outcome;
`complete` does not prove every agent succeeded. A failed body stops later
items. Abort cancels and awaits the active item child, then fails the parent
with an incomplete collection; SDK cancellation settles the parent as
cancelled. Control acceptance is an acknowledgement, so inspect run closure and
resource cleanup separately.

## Preserve source semantics honestly

TOML/YAML/GraphQL round-trip native attempts, return edges and iteration. The
JSON-string forms preserve `null` values; never supply both object and string
representations. Check the bounded-workflow guide's pinned source subset before
importing Flowise or Langflow. Supported Flowise Loop 1.2 maps finite sequential
control returns and fallback output. Supported Langflow CreateList/Loop/Parser
maps ordered record bodies and done output. Native agent/workflow bodies do not
establish arbitrary Langflow Agent/RunFlow translation. Refuse unsupported
custom code, routing/state or unresolved bindings explicitly; do not flatten a
source cycle into unrelated stages or claim source-runtime parity.
