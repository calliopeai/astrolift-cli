# Set up and run a workflow

A definition contains ordered stages. A configured workflow binds those stages
to agent workloads and default inputs. A run executes that configuration.

## Author a definition

```bash
astro workflow init --pattern chained -o workflows/report.toml
astro workflow validate workflows/report.toml
astro workflow validate workflows/report.toml --server
astro workflow import workflows/report.toml --preview
astro workflow import workflows/report.toml
```

Edit the generated slug, name, agents, environment-spec slugs, prompts, and
output keys. Local validation checks shape; server validation resolves the
installed contract. Preview does not persist. Supported stage fields and input
composition are documented in the [Workflow TOML reference](../reference/workflow-toml.md).

Repo registration also reconciles workflow source files. Source-owned
workflows should be edited and committed at their source path so a subsequent
sync does not replace an unrelated platform edit.

## Bind and run

```bash
astro agent workloads ls --json
astro workflow definition report-chain --json
astro workflow run-manifest workflows/report.toml --dry-run
astro workflow run-manifest workflows/report.toml --input request_id=example-42
astro workflow list --json
astro workflow runs <configured-workflow-slug> --watch
```

`run-manifest` imports the definition, resolves bindings, creates a configured
workflow, enables the newly imported definition, and starts it. This requires
workflow update permission; existing definitions are not automatically enabled
by a later `workflow run`. Use repeatable `--bind <stageOrder>=<agentWorkloadGuid>` when a stage needs an
explicit workload binding. Review the dry-run plan first.

To review a persisted configuration before its first execution, use `--no-run`.
The imported definition remains disabled. Copy the two slugs printed by the
command: the definition slug identifies what to enable, while the configured
workflow slug identifies what to run.

```bash
astro workflow run-manifest workflows/report.toml --no-run
astro workflow definition <imported-definition-slug> --json
astro workflow definition-enable <imported-definition-slug>
astro workflow run <configured-workflow-slug> --input request_id=example-42
astro workflow runs <configured-workflow-slug> --watch
```

Enabling the definition does not start a run. A plain `workflow import` also
creates a disabled definition; enable it after review before running a
configuration that uses it.

A human gate pauses for the declared approvers; grant them access before the
run reaches that stage. Unique `output_key` values let later stages consume
named outputs. The agent receives workflow input, predecessor output, and
stage metadata under `_astrolift_workflow`.

Inspect the run's stage and execution diagnostics before retrying or cancelling.
Task completion callbacks are per-task notifications; workflow-level and
progress callbacks are separate capabilities and are not part of that contract.

## Review exact definition inputs and durable starts

For direct definition dispatch, use the
[reviewed-start guide](reviewed-starts.md). It explains immutable GUID selection,
JSON Schema 2020-12 inputs, unsupported and no-input states, private metadata
request files and read-only recovery after a lost reply. This is a separate path
from app-bound configured `workflow run`; do not substitute a same-slug definition
for an already reviewed GUID. Define the contract with
`workflow.input_schema_json` in the [TOML reference](../reference/workflow-toml.md),
then review its current revision and schema digest before dispatch.
