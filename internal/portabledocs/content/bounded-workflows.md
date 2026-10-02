# Bounded review loops and serial collections

Use a reviewed definition when work needs finite retries, revision after human
review, or a serial body for each input record. Check `astro status --json` for
`workflows.bounded_review_loops` and `workflows.serial_collections`, and use a CLI
release containing this guide. Capability discovery describes the installation;
current workflow permissions and token scope still apply to every operation.

## Return rejected work to its author

```toml
[workflow]
slug = "bounded-review"
name = "Bounded review"
pattern = "review_loop"

[[stage]]
kind = "agent_dispatch"
agent = "author"
output_key = "draft"
on_failure = "retry"
max_attempts = 2
timeout = 600

[[stage]]
kind = "human_gate"
output_key = "review"
prompt = "Approve, or explain the required revision?"
approvers = ["team:reviewers"]
timeout = 3600

[stage.back_edge]
to = "draft"
when = "gate_rejected"
max_rounds = 3
on_exhausted = "fail"
```

Replace the agent and approver selectors with your actual organization targets.
Both ends of a return require explicit, unique `output_key` values. Rejection
returns to `draft`, carrying the review note and predecessor context; approval
continues forward. `max_rounds = 3` allows the first visit and at most two
returns. That edge's lifetime budget does not reset when another edge revisits
it. At exhaustion, `fail` ends the run; `escalate` opens an operator wait whose
clearance preserves the recorded escalation outcome.

`max_attempts` is an integer from 1 through 20, default 3. It includes the first
attempt. Agent and nested-workflow retry policies use that stage's cap; other
failure policies remain `fail`, `skip` and `escalate`. A retry cap and a review
return cap are separate bounds. Completed work under a skip/escalation policy
does not change a failed agent into a successful agent result.

Native return conditions are `gate_rejected` on a human gate, `stage_failed` on
an agent or nested workflow, and `output_equals` with a dotted `path` and an
explicit JSON scalar `value`. Round caps are 1 through 20. `always` and
`on_exhausted = "continue"` are control/output semantics used by the supported
Flowise import, rather than a replacement for native review approval.

The reviewed plan bounds all return visits, retry attempts, collection items
and fan-out branches before dispatch: at most 1,000 stage visits and 500
execution units. A combined plan can exceed the budget even when each
individual cap is valid. Dynamic/implicit fan-out reserves up to 50 workers;
an explicit smaller count uses its authored bound. Use authoritative server
validation and inspect the reviewed revision before starting.

## Run a body once per record, in order

This complete no-input example uses literal records and a deterministic
formatter, so it needs no agent workload to demonstrate serial scheduling:

```toml
[workflow]
slug = "bounded-records"
name = "Bounded records"
pattern = "chained"

[[stage]]
kind = "collection"
output_key = "each"
iteration_json = '{"max_items":2,"body_end":"render","items":[{"text":"first"},{"text":"second"}]}'

[[stage]]
kind = "format_record"
output_key = "render"
iteration_json = '{"source_format":"langflow_parser","pattern":"Item: {text}","separator":"\n"}'
```

A collection requires `max_items` from 1 through 50, a forward `body_end`
output key, and exactly one of literal `items` or a dotted `items_path` into
the predecessor output. Its records must be JSON objects. Missing or oversized
input is unavailable and dispatches no body items; an empty verified list
finishes with zero results. It never truncates an oversized list.

The body is the contiguous range after the collection through `body_end`.
Supported body kinds are `agent_dispatch`, `workflow`, `checkpoint`,
`human_gate` and `format_record`. Configure an agent stage's exact workload
binding, environment recipe, skills and prompt before dispatch. A nested stage
uses `kind = "workflow"` and a definition reference; review its resolved
identity using the exact-reference rules below. A native body binding does not translate an
arbitrary source-framework model/tool configuration automatically.

Each item executes in a separate durable child, with the exact parent execution
and recorded zero-based item index. An item gate blocks the next item until
decided. Bounded returns inside one body are supported. Nested collections,
parallel body stages and returns entering or leaving a body range are refused.
An outer return can repeat the collection under its edge's lifetime bound.

The parent output contains ordered `results`, `item_execution_ids`,
`finished_count`, `complete` and its frozen `collection` binding. A failed body
stops later items and reports an incomplete collection. `complete` records that
all bodies finished under the authored failure policy; inspect each item's
outcome separately. Input and collected output must be finite JSON with string
keys, at most 32 nesting levels, 16,384 nodes and 256 KiB encoded size. An
oversized aggregate fails explicitly.

An abort during a blocked item cancels and awaits that child's closure, then
fails the parent and its open stage mirrors with an incomplete collection.
SDK cancellation settles the parent and open mirrors as cancelled. Both stop
later items. A control acknowledgement does not establish run closure or
resource cleanup; inspect the terminal metadata.

## Preserve an explicitly selected target

Use `agent = "guid:<workload-guid>"` or
`workflow = "guid:<definition-guid>"` to retain the selected target identity.
The UUID after `guid:` must use canonical lowercase, hyphenated spelling,
for example `guid:123e4567-e89b-42d3-a456-426614174000`. TOML export/re-import,
repository reconciliation and frozen reviewed plans preserve that reference.
An agent slug can exist in two apps; a GUID selects the reviewed workload.
A nested definition rename preserves its GUID. A malformed, unavailable,
deleted or foreign GUID target is refused, including when another target now
uses its former slug. No slug fallback replaces an explicit GUID.

Literal references such as `agent = "author"` and
`workflow = "your-definition-slug"` remain compatible. They keep their existing
slug-resolution behavior; they do not establish immutable target identity.
An agent slug may resolve after import when a matching workload is registered
in the run's organization. Review the resolved target before dispatch. Nested
project visibility, current trigger/dispatch permissions and token scope still
apply to GUID references. Configured stage binding overrides remain explicit
choices in the reviewed plan; a conflicting default agent mapping is refused.
A target GUID does not prove an imported framework's model/tool equivalence.

## Validate, bind, review and observe

```sh
astro workflow init --pattern review_loop -o review.toml
astro workflow validate review.toml
astro workflow validate review.toml --server
astro workflow import review.toml --preview
astro workflow run-manifest review.toml --dry-run --bind 0=<agent-workload-guid>
```

Local validation checks shape and preserves bounded fields; the server owns
graph budgets, collection ranges, target resolution and authorization. Review
the dry-run plan before choosing the configured-workflow start path from
[workflow setup](workflow-setup.md). For an enabled persisted definition, the
[reviewed-start path](reviewed-starts.md) binds its GUID, revision, schema digest
and durable request identity. Frozen starts keep the reviewed plan after a live
edit. Recover a lost start reply by its existing request file, rather than
dispatching a new request.

```sh
astro workflow execution-stages <execution-guid> --json
astro workflow gates --json
astro workflow gate <definition-slug> --run <run-guid> --decision reject --note "Revise the draft"
```

Use the original server and organization. Stage metadata includes one-based
`roundNumber` and `attemptNumber`, return cause (`edge`, `reason`, `maxRounds`,
`edgeRound`), timestamps and separate collection/fan-out parent identities.
JSON keeps zero-based `collectionIndex` and `fanoutIndex`; text item/branch
labels start at one. The CLI reads the exact execution identity across every
page. Missing metadata stays unavailable and schema/permission failures do not
select a different run. A gate decision is authorized by the server and routed
to the recorded item child.

## Source imports and their boundaries

TOML, YAML and GraphQL preserve the same bounded native contract. Export uses
`back_edge_json` and `iteration_json` to preserve JSON `null` values; authoring
accepts either that string or the corresponding object, never both.

The Flowise Loop 1.2 mapping is based on
[source revision 9291856](https://github.com/FlowiseAI/Flowise/tree/9291856d1ea4a4ceea9f8fef8ce14f4f6c81e8eb).
Its supported terminal sequential loop becomes an explicit bounded control
return, preserving max-loop-count, continuation at cap and literal fallback
output. State, router, parallel and unresolved-variable semantics outside that
mapping are refused.

The Langflow mapping is based on
[source revision f9b2832](https://github.com/langflow-ai/langflow/tree/f9b283243d2fdd8502cb4ffd606c3058cff5017e).
The supported graph connects CreateList to Loop, Loop's item into a built-in
Parser and Parser feedback to that same Loop; an optional done edge can feed
explicit TypeConverter JSON conversion. It preserves ordered records, plain
named Parser fields, separator, missing fields as empty strings and the final
record table, then executes the serial body. Custom code, routers/state, nested
source loops, Stringify mode and other source body component types are refused.
Native agent/workflow collection bodies do not certify broader Langflow Agent
or RunFlow source translation. Source importer tests do not invoke the Langflow
runtime, and unavailable-target agent tests do not prove successful container
execution.
