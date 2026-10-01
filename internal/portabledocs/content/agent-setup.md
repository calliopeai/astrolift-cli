# Set up and dispatch an agent

A registered agent is a `Workload(kind = "agent")`. Its immutable package
contains the brief, ordered skills, tools, runtime, policy, and selected source
files. A task is one execution of that registration.

## Author and register

Put `astrolift.toml` and `brief/README.md` at the agent root, with local skills
in directories containing `SKILL.md`. Use the complete [agent package example](../reference/agent-packages.md#native-agent-manifest).
For a monorepo use `agents/<slug>/astrolift.toml`, or an explicit federation
file. Keep package include paths inside the selected package boundary.

Commit and push the source before registration:

```bash
astro agent register-repo owner/agents --project-id <project-guid> --ref main
astro agent workloads ls --project platform --json
```

Registration also reconciles supported `workflows/**/*.toml` files. Resolve
registration failures before dispatch; a missing brief, bad skill, invalid
manifest, or unavailable package store is an actionable failure.

## Select a runtime and provide credentials

```bash
astro org list --json
astro org use <organization-slug>
ASTROLIFT_ORG_ID="$(astro org current --json | python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])')"

astro agent env-spec upsert report-prod \
  --agent-type claude --runtime claude-code-vnc \
  --config-repo owner/agents --manifest-path agents/report/astrolift.toml \
  --secret "ANTHROPIC_API_KEY=secret://agents/${ASTROLIFT_ORG_ID}/anthropic"
printf '%s' "$ANTHROPIC_API_KEY" | \
  astro agent secret set report-prod ANTHROPIC_API_KEY --stdin
```

The organization `id` returned by `astro org current --json` is its GUID.
Agent secret references must use `secret://agents/<organization-guid>/<name>`
for the same organization selected by the command; a slug or an unscoped
`secret://agents/anthropic` is rejected. The GUID is non-secret metadata.

Environment specs select image and secret references. The secret command writes
the value through the configured secret backend. Do not commit its value or
put it in a trigger payload. Review runtime installation and tool permissions;
only enable additional privileges needed by your agent.

## Dispatch and observe

```bash
astro agent dispatch report --env-spec report-prod --input @input.json --wait
astro agent inspect <task-guid> --json
astro agent logs <task-guid>
```

`dispatch` targets an agent slug; `agent run` targets a workflow definition.
`--tail` streams task logs and implies waiting. A successful dispatch response
means the task was accepted, not that its work completed. Final states are
`completed`, `failed`, `cancelled`, and `timed_out`.

Use [completion callbacks](agent-completion-callbacks.md) for a signed final
notification with durable retries. Use [workflow setup](workflow-setup.md) when
several agents or human approval stages must run in order.
