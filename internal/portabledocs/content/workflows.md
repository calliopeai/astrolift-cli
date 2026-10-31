# Workflow TOML reference

!!! note "Agent notes"
    `astro workflow pull <slug>` exports a definition's canonical TOML, and
    `astro workflow validate <file> --server` is the authoritative check; the
    local check knows a subset of the rules and does not know the
    `github_checks` kind, so validate that kind with `--server`. The parser is
    `backend/workflows/manifest.py` in astrolift-app. `supervisor_worker` and
    `advisor` parse but are refused at import; `skill_slug` is legacy and not
    a TOML key. Frequent mistakes: `[[stage]]`, not `[[stages]]`; `skills` is
    an array; `workflow` is set only when `kind = "workflow"`; two stages
    sharing an `output_key`; an `input_schema_json` written as a TOML table.

Workflow definitions are ordered pipelines stored in `workflows/**/*.toml`.
Agent-repo registration and source webhook reconciliation discover and converge
them independently from each agent's `astrolift.toml`.

## Chained example

```toml
[workflow]
slug = "emr-triage-chain"
name = "EMR Triage Chain"
pattern = "chained"
description = "Collect evidence, classify it, then prepare a report."

[[stage]]
kind = "agent_dispatch"
role = "investigator"
agent = "emr-triage"
environment_spec_slug = "emr-triage-prod"
skills = ["emr-evidence"]
prompt = "Collect reproduction evidence and return structured JSON."
output_key = "evidence"
on_failure = "retry"
timeout = 1200

[[stage]]
kind = "human_gate"
role = "clinical-review"
prompt = "Approve sending this result downstream?"
output_key = "approval"
approvers = ["team:emr-triage-reviewers"]
timeout = 86400

[[stage]]
kind = "agent_dispatch"
role = "reporter"
agent = "emr-triage"
environment_spec_slug = "emr-triage-prod"
skills = ["jira-reporting"]
prompt = "Prepare the final issue payload from approved evidence."
output_key = "report"
timeout = 900
```

## Definition fields

`slug` and `name` are required. `pattern` defaults to `single` and must be one
of the patterns advertised by the target server (including `single`, `chained`,
`fan_out`, and `review_loop`). Inert `supervisor_worker` and `advisor` labels are
not supported authoring choices. `description` is optional.

`input_schema_json` is an optional JSON string in `[workflow]`, supported by
installations advertising `workflows.definition_input_contracts`. It contains
a JSON Schema draft 2020-12 root object. Omission imports the explicit closed
no-input schema. A raw TOML `input_schema` table is not the importer contract.
The importer validates supported constraints and export preserves the schema
as a canonical JSON string. Review the resulting schema digest before a
[direct definition start](../guides/reviewed-starts.md).

```toml
[workflow]
slug = "example-review"
name = "Example review"
pattern = "single"
input_schema_json = '''{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "message": {"type": "string"},
    "limit": {"type": "integer", "minimum": 1, "default": 3}
  },
  "required": ["message"],
  "additionalProperties": false
}'''

[[stage]]
kind = "checkpoint"
```

The server accepts supported nested objects, arrays, local JSON pointers and
composition. External/dynamic references and regular-expression constraints
are refused. Sensitive string fields use `writeOnly` or
`x-astrolift-sensitive`; supplied values must be `secret://` references, and
sensitive defaults are refused. Use authoritative `--server` validation against
the intended installation; local TOML shape validation is insufficient for a
new schema contract.

The old `WorkflowDefinition.states`, `transitions`, and `model_label` fields do
not select an LLM or drive an agent pipeline. Ordered `WorkflowStage` rows do.
The model comes from the selected agent's runtime/environment package.

## Stage mapping

One `[[stage]]` is one ordered `WorkflowStage`; array index becomes `order`.

| TOML | Runtime meaning |
|---|---|
| `kind` | `agent_dispatch`, `workflow`, `human_gate`, `checkpoint`, `aggregation`, `collection`, `format_record`, or `github_checks` |
| `role` | Human-readable responsibility used by builders/importers |
| `agent` | Agent workload `guid:<canonical-UUID>` or organization-local literal slug; a slug may resolve after import |
| `workflow` | Definition `guid:<canonical-UUID>` or literal slug, required for `kind = "workflow"`; review its resolved identity |
| `environment_spec_slug` | Image/config/secret recipe frozen onto the task |
| `skills` | Ordered catalogue/local/org-repo overlays using the agent skill grammar |
| `prompt` | Agent instruction overlay or human-gate question |
| `output_key` | Unique key in `named_outputs`; defaults to `stage_<order>` |
| `on_failure` | `fail` (default), `retry`, `skip` or `escalate` |
| `max_attempts` | Integer 1–20 including the first attempt; default 3 |
| `back_edge` / `back_edge_json` | Finite earlier-stage return object or JSON string; exactly one representation |
| `iteration` / `iteration_json` | Kind-specific stage contract; exactly one representation. A bounded collection or record-formatter contract on `collection` and `format_record`; the gate configuration on `github_checks`; a source-port contract on an imported Langflow `agent_dispatch` or `workflow` stage. Empty elsewhere |
| `timeout` | Non-negative seconds; default 300 |
| `fan_out` | `0`, a positive integer, or `"dynamic"` |
| `approvers` | Human-gate principal selectors. Email addresses are enforced: when a gate names at least one, only a signed-in user with a matching email may decide it. Team or role slugs are stored but not resolved, so a gate naming only slugs defers to `workflow.trigger` at the run's scope. The platform operator may always decide |

A `github_checks` stage waits for named GitHub check runs on a pull request's
head commit. Its configuration is the stage's `iteration` table with exactly
four keys: `repo` (`owner/name`), `sha_source = "pull_request_head"`,
`pull_request_path` (a dotted field in the preceding stage's output, or the
run input for a first stage, holding a pull request number or that
repository's pull request URL) and `checks` (1 to 20 distinct check names).
A check passes on the conclusions GitHub branch protection accepts; a gate
still pending at the stage `timeout` is decided `timed_out`. Polling uses the
organization's GitHub connection.

```toml
[[stage]]
kind = "github_checks"
role = "ci-gate"
output_key = "checks"
timeout = 3600

  [stage.iteration]
  repo = "acme/orders-api"
  sha_source = "pull_request_head"
  pull_request_path = "pr"
  checks = ["lint", "test"]
```

Explicit `guid:` references retain the selected target through export and source
reconciliation. Malformed, deleted or inaccessible GUID targets are refused
without a slug fallback. Literal slugs preserve existing compatibility behavior.
See [exact target references](../guides/bounded-workflows.md#preserve-an-explicitly-selected-target)
for UUID spelling, permissions and configured binding overrides.

There is no `skill_slug` field in Workflow TOML. Author `skills = [...]`;
Astrolift persists the ordered list as the stage's `skill_refs` and resolves
each reference into the immutable task package. `skill_slug` is a legacy
single-skill execution/compatibility field, not a pipeline model selector.

Each agent receives the original workflow input, immediate predecessor, all
named prior outputs, and its stage metadata under `_astrolift_workflow`.

See [bounded workflows](../guides/bounded-workflows.md) for executable finite
review and serial-collection examples, cap composition, run metadata, and the
supported source-import subset. `review_loop` requires an explicit bounded
gate-rejection return; the pattern name alone does not repeat stages.

## Source reconciliation

Every TOML file directly or recursively under `workflows/` is parsed, in
sorted path order. A bad workflow fails reconciliation with its source path.
Definitions are owned by `(organization, source_repo, source_path)`; slug
collisions with another definition fail loudly. A reconciled definition is
enabled and owned by the registering project. Removing a source-owned workflow
disables and soft-deletes its definition while retaining history.

## CLI authoring

The end-to-end flow, including binding and running, is in
[Set up and run a workflow](../guides/workflow-setup.md). `workflows/triage.toml`
is your file; `ooda` is a catalogue definition slug from `astro workflow
definitions`.

```bash
astro workflow init --pattern chained -o workflows/triage.toml
astro workflow validate workflows/triage.toml
astro workflow validate workflows/triage.toml --server
astro workflow import workflows/triage.toml --preview
astro workflow import workflows/triage.toml
astro workflow import workflows/triage.toml --replace
astro workflow pull ooda -o workflows/ooda.toml
astro workflow run-manifest workflows/triage.toml --input ticket=INC-42
```

Local validation prints `OK (local shape check): workflows/triage.toml` and
lists the stages; `--server` prints the parsed definition, or `server validation
failed at stage[<n>].<key>: <reason>`, which names the key to fix. A persisting
import prints the created definition slug. Server validation is authoritative
for the installed version. Import preview does not persist; apply creates an
org-scoped definition, disabled for review until `astro workflow definition-enable
<slug>`. A second plain import of the same slug creates `<slug>-1`;
`--replace` upserts the org's own definition instead: in place when the stage
kinds are unchanged, otherwise as a new version with every configured Workflow
repointed to it, or a refusal that changes nothing when a repoint would break
a Workflow's bindings. `pull` is the easiest way to start from a built-in
catalogue definition. `run-manifest` imports, binds each `agent_dispatch`
stage to its declared agent (override with `--bind <order>=<guid>`), enables
the new definition and starts a run in one call; `--dry-run` shows the
resolved bindings and `--no-run` stops before the run.
