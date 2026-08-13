# Workflow TOML reference

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
`fan_out`, `supervisor_worker`, and `review_loop`). `description` is optional.

The old `WorkflowDefinition.states`, `transitions`, and `model_label` fields do
not select an LLM or drive an agent pipeline. Ordered `WorkflowStage` rows do.
The model comes from the selected agent's runtime/environment package.

## Stage mapping

One `[[stage]]` is one ordered `WorkflowStage`; array index becomes `order`.

| TOML | Runtime meaning |
|---|---|
| `kind` | Stage executor such as `agent_dispatch`, `human_gate`, or aggregation |
| `role` | Human-readable responsibility used by builders/importers |
| `agent` | Organization-local agent workload slug; may resolve after import |
| `environment_spec_slug` | Image/config/secret recipe frozen onto the task |
| `skills` | Ordered catalogue/local/org-repo overlays using the agent skill grammar |
| `prompt` | Agent instruction overlay or human-gate question |
| `output_key` | Unique key in `named_outputs`; defaults to `stage_<order>` |
| `on_failure` | Server-advertised failure policy; default `fail` |
| `timeout` | Non-negative seconds; default 300 |
| `fan_out` | `0`, a positive integer, or `"dynamic"` |
| `approvers` | Human-gate principal selectors |

There is no `skill_slug` field in Workflow TOML. Author `skills = [...]`;
Astrolift persists the ordered list as the stage's `skill_refs` and resolves
each reference into the immutable task package. `skill_slug` is a legacy
single-skill execution/compatibility field, not a pipeline model selector.

Each agent receives the original workflow input, immediate predecessor, all
named prior outputs, and its stage metadata under `_astrolift_workflow`.

## Source reconciliation

Every TOML file directly or recursively under `workflows/` is parsed. A bad
workflow fails reconciliation with its source path. Definitions are owned by
`(organization, source_repo, source_path)`; slug collisions with another
definition fail loudly. Removing a source-owned workflow disables and
soft-deletes its definition while retaining history.

## CLI authoring

```bash
astro workflow init --pattern chained -o workflows/triage.toml
astro workflow validate workflows/triage.toml
astro workflow validate workflows/triage.toml --server
astro workflow import workflows/triage.toml --preview
astro workflow import workflows/triage.toml
astro workflow pull ooda -o workflows/ooda.toml
```

Local validation checks TOML shape. Server validation is authoritative for the
installed version. Import preview does not persist; apply creates an org-scoped
definition. `pull` is the easiest way to start from a built-in catalogue
definition.
