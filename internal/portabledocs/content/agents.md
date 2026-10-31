# Agent Package and repository reference

!!! note "Agent notes"
    There is no local validator for an agent manifest; `astro agent
    register-repo` is the check, and `astro agent workloads ls --json` shows
    what registered. `astro agent inspect <task-guid> --json` shows a run's
    package, brains and status. Parsers in astrolift-app:
    `backend/astrolift_manifest/parser.py` (workloads, `brief`, `skills`),
    `backend/astrolift_agents/services/agent_package.py` (`[package]`,
    `[environment]`, `[secrets]`, `[workspace]` delivery) and
    `backend/astrolift_agents/services/brief_assembler.py` (config-repo
    includes). Legacy: `skill_slug`; the `aider` and `openhands` runtimes are
    retired. Frequent mistakes: a secret ref without the organization GUID;
    `[environment]` setting a dispatcher-owned variable; a skill ref with two
    `@`; `[package] include` paths that climb out of the manifest directory.

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
  "operations/runbooks/incident-triage@main",
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
  image_ref = "docker.io/calliopeai/astrolift-agent-claude-code:latest"

[environment]
LOG_LEVEL = "info"

[secrets]
ANTHROPIC_API_KEY = "secret://agents/<organization-guid>/anthropic"

[package]
root = "."
include = ["astrolift.toml", "brief/**", "skills/**", "scripts/**"]
exclude = ["**/*.tmp", "fixtures/**"]
executables = ["scripts/*.sh"]

[[package.shared]]
source = "shared/schemas"
mount = "shared/schemas"
```

Replace `<organization-guid>` with the `id` from `astro org current --json` for
the owning organization. Agent secret references must stay under that GUID's
`secret://agents/<organization-guid>/` namespace; an organization slug or an
unscoped reference is rejected. The declaration under `[secrets]` is a
reference, never plaintext: a bare string, or a table with exactly one of
`secret_name` or `uri`. The dispatcher owns callback, task, payload, and
workspace variables; a `[environment]` entry that names one is refused at
registration.

The runtime image is the primary container's `image_ref`, unless the
environment spec the agent is dispatched with pins an `--image-tag` or names
a `--runtime` from the catalog. The watchable `-vnc` variant is never named;
the dispatcher composes it when the spec enables VNC.

### `[workspace]`

```toml
[[workspace.repos]]
url = "https://github.com/acme/orders-api.git"
ref = "main"
path = "orders-api"
depth = 1
token_env = "GITHUB_TOKEN"

[workspace.deps]
python = ["requirements.txt"]
node = ["orders-api/package.json"]
```

The runner reads `[workspace]` from the `astrolift.toml` inside the delivered
payload and sets the workspace up after extracting it and before the driver
starts: `[[workspace.repos]]` clones repositories (`url`, optional `ref`,
`path`, `depth` and `token_env`, the environment variable holding a private
repository's token), `[workspace.deps]` installs Python dependencies into a
virtualenv at `<workspace>/.venv` and Node dependencies into each package's
own `node_modules`. One invalid entry, failed clone or failed install stops
the boot with an error naming the entry. Declaring `[workspace]` requires a
payload, the same as `[package]`; an environment spec with `--box-workspace`
runs the same setup before an interactive box session.

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
JIRA_TOKEN = { secret_name = "secret://agents/<organization-guid>/jira" }
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

The full walkthrough, including the secret value, is
[Set up and dispatch an agent](../guides/agent-setup.md). `owner/repo` is the
pushed source repository; `<guid>` is the owning project's `id` from
`astro project list --json`; `emr-triage` is the manifest's `name`.

```bash
astro agent register-repo owner/repo --project-id <guid> --ref main \
  --manifest-path agents/triage/astrolift.toml

astro agent env-spec upsert triage-prod \
  --agent-type claude --runtime claude-code --vnc \
  --config-repo owner/repo \
  --manifest-path agents/triage/astrolift.toml

astro agent dispatch emr-triage --env-spec triage-prod --tail
astro agent dispatch emr-triage --env-spec triage-prod --brain handbook --input @payload.json
```

`register-repo` prints one `Agent <slug> (workload <slug>, <state>)` line per
manifest, with any notes beneath it; `register failed: <reason>` names the
manifest key or file at fault. `env-spec upsert` prints `Created env-spec
triage-prod in org <org-slug>` or `Updated env-spec triage-prod`; a secret ref
answered with `is outside this organization's agent secret namespace` needs the
organization GUID from `astro org current --json` in place of a slug. `dispatch`
prints `AgentTask ID:` and `Status:`, then with `--wait` or `--tail` each status
change and `Final status: completed`; `failed`, `cancelled` and `timed_out` are
the other terminal states, and exit code 1 means the dispatch or run failed.

Repo sync is idempotent by source repo and manifest path. Added and changed
agents are reconciled. Removing a source manifest does not automatically delete
the registered agent; teardown is explicit so audit history is retained.

`--runtime` names a catalog runtime, published as
`docker.io/calliopeai/astrolift-agent-<name>`:

| Runtime | Harness |
|---|---|
| `claude`, `claude-code` | Claude Code |
| `codex`, `codex-cli` | Codex |
| `opencode` | OpenCode |
| `goose` | Goose |
| `mistral`, `deepseek` | Vendor CLIs |
| `calliope-cli` | Calliope AI CLI |
| `base` | The shared base layer, for a bare environment |

An explicit `--image-tag` wins over `--runtime`; with neither, the workload's
primary container image is used. `--vnc` composes the `-vnc` variant of
whichever image was resolved. The `aider` and `openhands` runtimes are retired
and refused, by name or by image. `--brain` on dispatch adds a hosted brain the
run reads (at most 10), beyond the agent's own `brains`.

## Runtime environment

The platform injects these into the agent container. They are reserved: a
native manifest `[environment]` block that sets one of them is refused at
registration, and a config-repo brief that sets one is dropped with a warning
at dispatch, because the dispatcher owns the value.

| Variable | Carries | Absent when |
|---|---|---|
| `ASTROLIFT_TRIGGER_PAYLOAD` | The per-dispatch input, JSON-encoded: `astro agent dispatch --input '{...}'`, a workflow stage's payload, or a trigger-bound webhook's mapped body | No input was supplied. An empty payload and no payload are the same thing, so nothing is set rather than the string `null` |
| `ASTROLIFT_TASK_ID` | The task's GUID | never |
| `ASTROLIFT_CONTROLLER_URL` | Base URL of the control plane | never |
| `AGENT_CALLBACK_URL` + `ASTROLIFT_CLUSTER_KEY` | Where a one-shot pod reports its terminal result, and the task-scoped credential to do it with. The key is minted per spawn, so a token from a retried render is dead | never |
| `ASTROLIFT_MESSAGES_URL`, `ASTROLIFT_RUN_MCP_URL` | The run's message endpoint (REST) and its MCP endpoint, authenticated with the same `ASTROLIFT_CLUSTER_KEY` | never |
| `ASTROLIFT_BRIEF_ID`, `ASTROLIFT_BRIEF_HASH` | Identify the Brief the run was assembled from | The task has no Brief |
| `ASTROLIFT_PAYLOAD_URL`, `ASTROLIFT_PAYLOAD_HASH` | Where to fetch the agent package archive, and its `sha256:` digest | The Brief stored no bundle (no `[package]` or `[workspace]`) |
| `ASTROLIFT_MANIFEST_PATH` | The manifest's path inside that archive | No archive |
| `AGENT_SYSTEM`, `AGENT_PROMPT` | The assembled system prompt and the kickoff turn | The runtime idles in listener mode instead of running once |
| `ASTROLIFT_WORKLOAD_KIND`, `ASTROLIFT_MAX_RETRIES`, `ASTROLIFT_TOOL_TIMEOUT` | `agent`, and the workload's `max_retries` and `tool_timeout_seconds` | Not rendered from an agent workload |
| `ASTROLIFT_SNAPSHOT_URL`, `ASTROLIFT_SNAPSHOT_INTERVAL` | Where and how often a VNC session uploads its snapshot | Not a VNC task, or no blob store configured |
| `ASTROLIFT_WORKSPACE`, `ASTROLIFT_TMUX_SESSION` | The working directory and tmux session of an interactive agent box | Not an agent box |

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

## Receive the final result

Register an optional callback at dispatch, using an organization signing secret
reference. The [completion callback guide](../guides/agent-completion-callbacks.md)
covers FULL and NOTIFY modes, signed final events, durable outage retries and
backoff for at least 24 hours, delivery-state queries, and manual replay.
The pod reporting URL `AGENT_CALLBACK_URL` is a separate platform-owned field.
