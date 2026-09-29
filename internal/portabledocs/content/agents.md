# Agent Package and repository reference

An Astrolift agent is exactly one `Workload(kind = "agent")` plus an immutable
Agent Package snapshot. The package composes a brief, ordered skills, resolved
tools, runtime image, environment references, execution policy, and a chrooted
source payload.

## Repository layouts

Single agent:

```text
astrolift.toml
brief/README.md
skills/reviewer/SKILL.md
scripts/run-checks.sh
```

Monorepo:

```text
agents/triage/astrolift.toml
agents/triage/brief/README.md
agents/triage/skills/emr/SKILL.md
agents/reporter/astrolift.toml
workflows/triage.toml
astrolift.agents.toml
shared/schemas/finding.json
```

Without a federation file, discovery accepts a root `astrolift.toml` and exact
`agents/<slug>/astrolift.toml` paths. Deeper arbitrary manifest paths are not
auto-discovered.

## Native agent manifest

```toml
astrolift_version = 1
name = "emr-triage"
brief = "brief/README.md"
skills = [
  { emr = "skills/emr" },
  "pr-review@1.2.0",
  "steadymd/runbooks/clinical-triage@main",
]

[[workloads]]
name = "emr-triage"
kind = "agent"
run_family = "task"
max_retries = 3
tool_timeout_seconds = 1200
result_ttl_hours = 168

  [[workloads.containers]]
  name = "agent"
  is_primary = true
  image_ref = "ghcr.io/calliopeai/astrolift-agent-claude-code-vnc:latest"

[environment]
LOG_LEVEL = "info"

[secrets]
ANTHROPIC_API_KEY = "secret://agents/anthropic"

[package]
root = "."
include = ["astrolift.toml", "brief/**", "skills/**", "scripts/**"]
exclude = ["**/*.tmp", "fixtures/**"]
executables = ["scripts/*.sh"]

[[package.shared]]
source = "shared/schemas"
mount = "shared/schemas"
```

The declaration under `[secrets]` is a reference, never plaintext. The
dispatcher owns callback, task, payload, and workspace variables; package
environment values cannot override them.

## Briefs and skills

`brief` points to a Markdown README. Relative links to sibling files inside the
brief directory become immutable context files. Links outside that directory
are ignored.

Skill references have three forms:

| Form | Source |
|---|---|
| `{ local_name = "skills/path" }` or `"./skills/path"` | This agent's package root |
| `"skill-name@version"` | Built-in catalogue |
| `"repo-alias/path/to/skill@ref"` | Registered organization skill repo |

Each local skill is an agentskills.io directory with `SKILL.md`. Its YAML
frontmatter requires `name` and `description`; the Markdown body supplies
instructions. `scripts/`, `references/`, and `assets/` are packaged with it.

The resolved skill order is preserved. Duplicate tool slugs are de-duplicated.
Harness runtimes receive the brief, skill instructions, and command-backed tool
contract as one composed system prompt.

## Package source boundary

Declaring `[package]` opts into immutable runtime files. Paths are relative to
the manifest directory, POSIX-only, and may not be absolute or contain `..`.

- `root` defines the virtual chroot.
- `include` defaults to `**`.
- `exclude` defaults to common VCS/cache/dependency paths.
- `executables` adds executable bits to matched files.
- `[[package.shared]]` is the only way to mount a repo-root path across the
  package boundary; `mount` must remain inside the resulting package.

The control plane fetches a byte-preserving source archive, filters it, hashes
the result, stores it, and supplies a presigned payload URL. Symlinks, path
escapes, duplicate destinations, oversized archives, and an empty slice fail
registration instead of silently producing a partial agent.

## Modular brief/config manifests

Environment specs may select a config repo and `manifest_path`. That format can
split large prompts and assets across files:

```toml
include = ["base/runtime.toml", "skills/emr.toml"]

[skills.triage]
system_prompt_file = "prompts/triage.md"
tools = ["jira-search", "jira-create"]
files = ["references/severity.md"]
scripts = ["scripts/collect.sh"]
binaries = ["bin/redactor"]

[assets]
files = ["schemas/finding.json"]

[environment]
tool_preset = "dev+cloud"
allow_install = false
LOG_LEVEL = "info"

[secrets]
JIRA_TOKEN = { secret_name = "secret://agents/jira" }
```

Includes are relative to the including file, loaded in order, and recursively
deep-merged; later includes and then the main file win. Cycles, more than 16
levels, missing files, non-UTF-8 prompts, and path escapes fail assembly.
Scripts and binaries are marked executable. If files are requested but no blob
store can deliver the payload, dispatch fails rather than reporting success.

Every `[skills.<slug>]` table is composed. The stored `skill_slug` value is a
first-skill compatibility alias for older runtimes; do not author it as a
top-level selector. Workflow stages add skills with their `skills = [...]`
array, which becomes ordered `skill_refs` at runtime.

## Explicit federated bundle

`astrolift.agents.toml` selects a controlled subset of a monorepo:

```toml
schema = "astrolift.agent.federation/v1"
name = "support-agents"
include = ["agents/*/astrolift.toml"]
exclude = ["agents/experimental-*/astrolift.toml"]
auto_register_new = false

[[agents]]
manifest = "agents/triage/astrolift.toml"
alias = "triage"

[[agents]]
manifest = "agents/reporter/astrolift.toml"
enabled = false
```

When present, the federation file replaces legacy discovery selection.
`auto_register_new` adds matching manifests not explicitly listed. Exclusions
still win. Every alias must be unique.

## Register and run

```bash
astro agent register-repo owner/repo --project-id <guid> --ref main \
  --manifest-path agents/triage/astrolift.toml

astro agent env-spec upsert triage-prod \
  --agent-type claude --runtime claude-code-vnc \
  --config-repo owner/repo \
  --manifest-path agents/triage/astrolift.toml

astro agent dispatch emr-triage --env-spec triage-prod --tail
```

Repo sync is idempotent by source repo and manifest path. Added and changed
agents are reconciled. Removing a source manifest does not automatically delete
the registered agent; teardown is explicit so audit history is retained.

## Runtime environment

The platform injects these into the agent container. They are reserved: a
manifest `[environment]` block that sets one of them is ignored with a warning,
because the dispatcher owns the value.

| Variable | Carries | Absent when |
|---|---|---|
| `ASTROLIFT_TRIGGER_PAYLOAD` | The per-dispatch input, JSON-encoded — `astro agent dispatch --input '{...}'`, a workflow stage's payload, or a trigger-bound webhook's mapped body | No input was supplied. An empty payload and no payload are the same thing, so nothing is set rather than the string `null` |
| `ASTROLIFT_TASK_ID` | The task's GUID | never |
| `ASTROLIFT_CONTROLLER_URL` | Base URL of the control plane | never |
| `AGENT_CALLBACK_URL` + `ASTROLIFT_CLUSTER_KEY` | Where a one-shot pod reports its terminal result, and the task-scoped credential to do it with. The key is minted per spawn, so a token from a retried render is dead | The task has no callback route |
| `ASTROLIFT_BRIEF_ID`, `ASTROLIFT_BRIEF_HASH` | Identify the Brief the run was assembled from | The task has no Brief |
| `ASTROLIFT_PAYLOAD_URL`, `ASTROLIFT_PAYLOAD_HASH` | Where to fetch the agent package archive, and its digest | The package ships inline |
| `AGENT_SYSTEM`, `AGENT_PROMPT` | The assembled system prompt and the kickoff turn | The runtime idles in listener mode instead of running once |
| `ASTROLIFT_WORKSPACE` | Working directory the runtime checks out into | never |
| `ASTROLIFT_SNAPSHOT_URL`, `ASTROLIFT_SNAPSHOT_INTERVAL`, `ASTROLIFT_SNAPSHOT_LOCAL_PATH` | Where and how often a VNC session uploads its snapshot | Not a VNC runtime, or no blob store configured |
| `ASTROLIFT_TMUX_SESSION` | The tmux session name a VNC runtime attaches to | Not a VNC runtime |

Everything else in the container comes from your own manifest `[environment]`
block and the environment spec's secret refs.

### Reading the trigger input

```python
import json, os

payload = json.loads(os.environ.get("ASTROLIFT_TRIGGER_PAYLOAD") or "{}")
mode = payload.get("mode", "smoke")
```

A prompt-driven agent does not have to read the variable at all: the kickoff
turn in `AGENT_PROMPT` carries the same JSON inline, so `--input` reaches a
harness that only ever sees its prompt.

## Import other formats

The Agent Package importer accepts `agents_md`, `astrolift_package`, `langflow`,
and `flowise`. Imports return semantic gaps. Langflow/Flowise graphs may be
flattened to one task or preserved as a federation requiring stage bindings.
Runtime image selection is required before a package is runnable; unresolved
external tool names remain warnings until matching ToolDefs are bound.
