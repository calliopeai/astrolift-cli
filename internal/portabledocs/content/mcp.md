# MCP reference

!!! note "Agent notes"

    Live truth is `tools/list` on the installed server: it returns only the
    tools this token and this person may call, with their exact input schemas.
    `astro onboard` writes the MCP client entry for the selected install;
    `ASTROLIFT_TOKEN=alft_at_...` authenticates both the CLI and the gateway.
    The checked-in superset is `backend/contracts/mcp-tools.json` in
    `calliopeai/astrolift-app`. Every listed tool is stable; the workflow tools
    disappear from `tools/list` when the installation's workflows feature is off.
    Common mistakes: a token without an `mcp:*` scope (HTTP 403 before any JSON-RPC),
    a tool whose additional scope (`read:apps`, `workflow:write`, ...) the token lacks
    (`isError: true`, `code: permission_denied`), and treating a delivered control
    request or an uncertain write result as done instead of reading the record again.

Astrolift exposes an authenticated Streamable HTTP MCP server for agent
inspection, dispatch and cancellation, source reconciliation, package import,
native workflow discovery, reviewed starts and human gates, shared project
resources, semantic layers (brains) and temporary apps.

## Endpoint and credentials

```text
https://<install-host>/api/mcp/v1/
```

Use a user API token (`alft_at_...`) in the bearer header. Choose scopes by
capability:

| Scope | Capability |
|---|---|
| `mcp:read` | List agents, read immutable packages, tasks, runtimes, workflows, brains and project resources |
| `mcp:dispatch` | Dispatch and hard-stop agent tasks, start and control workflow executions, decide human gates |
| `mcp:write` | Reconcile agent repos, import agent specs, configure workflows, manage brains and project resources |

Scopes are ceilings over RBAC. For example, `mcp:dispatch` also requires the
user to hold `agent.dispatch`. `admin` is a token-scope wildcard but does not
create user permissions.

Many tools require a second scope besides the MCP one: brain tools need
`read:apps` or `write:apps` (sharing also needs `write:app-access`), workflow
tools need `workflow:write` or `workflow:trigger`, and project resource writes
need `project:write`. The tables below list every required scope. The token
must belong to the organization selected for the request; a token from another
organization is refused with `permission_denied`.

Generic client configuration:

```json
{
  "mcpServers": {
    "astrolift": {
      "type": "http",
      "url": "https://astrolift.example.com/api/mcp/v1/",
      "headers": {
        "Authorization": "Bearer ${ASTROLIFT_TOKEN}"
      }
    }
  }
}
```

Replace `astrolift.example.com` with the install host (`astro server list`
prints it) and keep `${ASTROLIFT_TOKEN}` as a reference to the token minted
under **Settings > API tokens** with the scopes above. Keep the token in the
client's secret/environment facility, not in a committed configuration file.
Browser-originated clients must send an `Origin` the installation operator
allowed (`ASTROLIFT_MCP_ALLOWED_ORIGINS`; the dashboard origin by default).

Success check: the client's first `initialize` returns `serverInfo.name:
"astrolift"` and a `Mcp-Session-Id` header. Failure signatures: HTTP 401 means
no usable `alft_at_` token; HTTP 403 with `API token has no MCP scope` means the
token exists but carries none of `mcp:read`, `mcp:dispatch`, `mcp:write` or
`admin`; HTTP 403 with `origin is not allowed` means a browser origin outside
the operator's list.

### Write the client config with `astro onboard`

`astro onboard` writes, for the selected server, an MCP server entry pointing
at this install's gateway, the bundled Astrolift skill catalogue and the
offline guides:

```bash
astro onboard --target claude --only mcp --dry-run --json   # report, write nothing
astro onboard --target claude --only mcp                    # write it
```

`--target` is `claude`, `codex` or `both` (default `both`); `--only` narrows
to `mcp`, `skills`, `docs`; `--dir` chooses the directory (default: the working
directory); `--force` replaces existing files. The command reports
authentication and never performs it: export `ASTROLIFT_TOKEN=alft_at_...`
for headless use, or run `astro auth login` (device flow). The
[capabilities guide](../guides/capabilities.md#find-the-right-setup-guide)
shows the docs component.

## Tools

The server filters `tools/list` to operations allowed by both scope and RBAC.
The checked-in [generated capability document](https://github.com/calliopeai/astrolift-app/blob/main/backend/contracts/mcp-tools.json)
is the caller-independent superset used for release tooling and documentation.
Pin a tag or commit instead of `main` for reproducible generation. It includes
the required token scopes, RBAC permissions, and input JSON Schemas; it does
not imply that a particular caller may invoke every listed tool.

A tool is listed when the caller holds its grant anywhere in the organization;
the call itself checks the exact target. Project resource tools are listed only
when at least one reachable project exists. Workflow tools are hidden when the
installation's workflows feature is off.

### Agents and tasks

| Tool | Required scopes | Purpose |
|---|---|---|
| `astrolift_list_agents` | `mcp:read` | List registered agents and their source/package delivery state |
| `astrolift_get_agent` | `mcp:read` | Read one canonical Agent Package, including skills/tools and source slice |
| `astrolift_list_runtimes` | `mcp:read` | List selectable agent runtimes and their configured container images |
| `astrolift_list_tasks` | `mcp:read` | Discover agent runs, lifecycle and runtime placement, newest first, with pagination |
| `astrolift_get_task` | `mcp:read` | Read one agent task's lifecycle, result, and failure telemetry |
| `astrolift_get_task_by_client_request_id` | `mcp:read` | Recover the caller's visible task by dispatch request UUID without dispatching |
| `astrolift_run_agent` | `mcp:dispatch` | Dispatch one registered Task-family agent from its immutable package |
| `astrolift_cancel_task` | `mcp:dispatch` | Hard-stop a running/queued agent and delete its spawned workload |
| `astrolift_sync_agent_repo` | `mcp:write` | Re-scan a single-agent or monorepo source and freeze new package snapshots |
| `astrolift_import_agent_spec` | `mcp:write` | Preview or persist an AGENTS.md, native Agent Package, Langflow, or Flowise definition |

`astrolift_run_agent` takes the same completion-callback and `brain_slugs`
inputs as `runAstroliftAgent`; see [agent setup](../guides/agent-setup.md) and
[completion callbacks](../guides/agent-completion-callbacks.md). Import returns
explicit conversion gaps and never persists a non-runnable result.

### Workflows

| Tool | Required scopes | Purpose |
|---|---|---|
| `astrolift_list_workflow_definitions` | `mcp:read` | Discover permitted native workflow definitions and stage topology |
| `astrolift_get_workflow_definition` | `mcp:read` | Review an exact definition ID with its revision and input-schema contract |
| `astrolift_preview_workflow_manifest` | `mcp:read` | Validate native Workflow TOML and preview its stages or parse errors |
| `astrolift_export_workflow_manifest` | `mcp:read` | Export canonical Workflow TOML for an exact permitted definition ID |
| `astrolift_import_workflow_manifest` | `mcp:write`, `workflow:write` | Import Workflow TOML; preview by default, `preview=false` persists a disabled definition |
| `astrolift_set_workflow_definition_enabled` | `mcp:write`, `workflow:write` | Enable or disable an exact owned definition at its reviewed revision; enabling also needs `mcp:dispatch` and `workflow:trigger` |
| `astrolift_list_workflows` | `mcp:read` | Discover configured workflows, inputs, bindings and trigger settings |
| `astrolift_get_workflow` | `mcp:read` | Read one configured workflow, its configuration version and recent runs |
| `astrolift_create_workflow` | `mcp:write`, `workflow:write` | Configure an exact visible definition; defaults to disabled |
| `astrolift_update_workflow` | `mcp:write`, `workflow:write` | Edit a configured workflow at its reviewed version |
| `astrolift_delete_workflow` | `mcp:write`, `workflow:write` | Soft-delete a configured workflow and request schedule cleanup |
| `astrolift_get_workflow_schedule` | `mcp:read` | Inspect engine-confirmed schedule state, including pending cleanup |
| `astrolift_reconcile_workflow_schedule` | `mcp:write`, `workflow:write` | Reconcile the reviewed configuration version against Temporal |
| `astrolift_start_workflow_definition` | `mcp:dispatch`, `workflow:trigger` | Start a reviewed definition with exact revision, schema digest, persisted `request_id` and `confirmed` |
| `astrolift_get_workflow_start` | `mcp:read` | Recover the caller's original start request without starting another |
| `astrolift_list_workflow_runs` | `mcp:read` | Page permitted runs with status, definition and project filters |
| `astrolift_get_workflow_execution` | `mcp:read` | Observe one exact execution: terminal state, cleanup state, engine errors |
| `astrolift_list_workflow_execution_stages` | `mcp:read` | Page one execution's stage history, child runs and pending gates |
| `astrolift_control_workflow_execution` | `mcp:dispatch`, `workflow:trigger` | Request `cancel`, `terminate` (needs `reason`) or `cleanup` (terminal only) |
| `astrolift_list_human_gates` | `mcp:read`, `workflow:trigger` | Page open gates the caller may decide |
| `astrolift_get_human_gate_decision` | `mcp:read`, `workflow:trigger` | Recover a gate decision without sending another |
| `astrolift_decide_human_gate` | `mcp:dispatch`, `workflow:trigger` | Submit the person's explicit `approved`/`rejected` decision with `confirmed: true` |

Active schedules created or edited through `astrolift_create_workflow`,
`astrolift_update_workflow` and `astrolift_reconcile_workflow_schedule`
additionally require `mcp:dispatch` and `workflow:trigger`. A delivered control
request or a `requested` gate decision is not proof the workflow moved; read the
execution again. See [reviewed starts](../guides/reviewed-starts.md).

### Semantic layers (brains) and hosted apps

| Tool | Required scopes | Purpose |
|---|---|---|
| `astrolift_list_brains` | `mcp:read`, `read:apps` | List readable hosted brains with environment endpoints and shares |
| `astrolift_get_brain` | `mcp:read`, `read:apps` | Describe one hosted brain by app slug |
| `astrolift_connect_brain` | `mcp:read`, `read:apps` | Endpoints, header and a 15-minute read token for one brain |
| `astrolift_get_brain_status` | `mcp:read`, `read:apps` | Latest deployment, running commit against branch head, compile time, last ingest |
| `astrolift_get_brain_creation` | `mcp:read`, `read:apps` | Progress of one `astrolift_create_brain` request |
| `astrolift_create_brain` | `mcp:write`, `write:apps` | Scaffold a repository from the Mimwell edition, register it as a brain and deploy it |
| `astrolift_update_brain_substrate` | `mcp:write`, `write:apps` | Graduate the brain's substrate against the substrate matrix |
| `astrolift_rebuild_brain` | `mcp:write`, `write:apps` | Redeploy the branch head, or `ingest=true` to run the knowledge refresh first |
| `astrolift_share_brain` | `mcp:write`, `write:apps`, `write:app-access` | Share with a team (`viewer`/`deployer`) or an agent (`viewer`) |
| `astrolift_unshare_brain` | `mcp:write`, `write:apps`, `write:app-access` | Stop sharing with a team or an agent; revokes that agent's run tokens |
| `astrolift_delete_brain` | `mcp:write`, `write:apps` | Archive: scale to zero, drop from the register, revoke hosted apps' tokens |
| `astrolift_host_app_on_brain` | `mcp:write`, `write:apps` | Create a public web app given the brain's MCP URL and a read token on every deploy |
| `astrolift_set_app_expiry` | `mcp:write`, `write:apps` | Set, extend, shorten or clear a [temporary app](../guides/temporary-apps.md)'s expiry |

Share, delete and expiry-clear are refused for agent callers unless the
organization's `brain.share`, `brain.delete` or `app.expiry.clear` rule allows
agents, and approval rules still apply. The CLI equivalents live under
`astro brain`.

### Shared project resources

| Tool | Required scopes | Purpose |
|---|---|---|
| `astrolift_list_project_resource_clusters` | `mcp:read`, `read:apps` | Managed clusters that can host shared resources for a project |
| `astrolift_list_project_resource_catalog` | `mcp:read`, `read:apps` | Available and planned resource variants and configuration schemas for a cluster |
| `astrolift_list_project_resources` | `mcp:read`, `read:apps` | A project's shared resources, lifecycle state and app/agent attachments |
| `astrolift_provision_project_resource` | `mcp:write`, `project:write` | Provision a shared resource and optionally attach consumers |
| `astrolift_attach_project_resource` | `mcp:write`, `project:write` | Attach a resource to exactly one app environment or agent spec |
| `astrolift_detach_project_resource` | `mcp:write`, `project:write` | Detach one consumer |
| `astrolift_update_project_resource` | `mcp:write`, `project:write` | Update name or in-place-editable configuration and reconcile |
| `astrolift_reprovision_project_resource` | `mcp:write`, `project:write` | Start a full reprovision cycle |
| `astrolift_deprovision_project_resource` | `mcp:write`, `project:write` | Destructive deprovision; echo the ID in `confirm_managed_service_id`; `delete_data` and `force_destroy` default false |

See [shared services](../guides/shared-services.md) for the lifecycle these
tools drive.

Call `tools/list` instead of hard-coding input schemas. Tool arguments reject
unknown properties.

## Resources

`resources/list` exposes immutable package resources (at most 500):

```text
astrolift://agents/<agent-slug>/package
```

`resources/read` returns the JSON Agent Package. Prompts are currently empty.
Secret values are never tools or resources.

## Protocol check with curl

Initialize first and retain the returned `Mcp-Session-Id` for later requests.
Set `ASTROLIFT_TOKEN` to an `alft_at_` token with an MCP scope and replace the
host with the install host:

```bash
curl -i https://astrolift.example.com/api/mcp/v1/ \
  -H "Authorization: Bearer $ASTROLIFT_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}'
```

Expected: HTTP 200, a `Mcp-Session-Id` response header, and a result whose
`serverInfo.name` is `astrolift`. Then list tools with that session:

```bash
curl -sS https://astrolift.example.com/api/mcp/v1/ \
  -H "Authorization: Bearer $ASTROLIFT_TOKEN" \
  -H "Mcp-Session-Id: $MCP_SESSION_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

`MCP_SESSION_ID` is the header value from the initialize response. An empty
`tools` array with HTTP 200 means the token's scopes and the person's grants
allow nothing; a JSON-RPC error `Mcp-Session-Id is required` means the header
was omitted on a `2025-11-25` request, and `invalid or expired` means the
session outlived `ASTROLIFT_MCP_SESSION_TTL_SECONDS` (3600 by default) or was
minted for another token: initialize again.

The current server supports protocol versions `2025-03-26`, `2025-06-18`, and
`2025-11-25`. A request without `MCP-Protocol-Version` is treated as
`2025-03-26`. Current-protocol requests after initialization require the
session header. Sessions are signed, bound to the token and organization, and
expire, so clients must be able to initialize again. `DELETE` ends a session
and returns 204.

Transport requirements:

- `POST` or `DELETE` only; responses are JSON (no server-sent event stream).
- `Content-Type: application/json` for POST.
- `Accept` includes both `application/json` and `text/event-stream`.
- JSON must contain `jsonrpc: "2.0"`.
- Request bodies are limited by the installation (`ASTROLIFT_MCP_MAX_REQUEST_BYTES`,
  2 MiB by default; HTTP 413).
- 240 requests per minute per token.

Every tool call is audited. Errors from a valid tool call use MCP
`isError: true` with a structured `code` and `message` (for example
`permission_denied`, `not_found`); transport/protocol errors use JSON-RPC
errors. `initialize` advertises `tools` and `resources` without list-change
notifications; `notifications/initialized` is acknowledged with 202.

## Other MCP surfaces

Two more MCP servers exist. Neither accepts a user API token, and the person-
facing gateway above does not carry their tools.

**A run's own server.** Every dispatched task is given
`ASTROLIFT_RUN_MCP_URL`, which is
`/api/dispatch/v1/agents/<task-id>/mcp/` on the control plane, authorized by the
run's own `alft_cb_` callback key (`ASTROLIFT_CLUSTER_KEY`). It is stateless (no
session), accepts bodies up to 64 KiB, and offers `astrolift_message_user`
(send an `update`, `question`, `approval`, `result` or `alert` to the people who
can read the agent) and `astrolift_get_message_replies` (collect queued
answers). People read and answer through the [agent inbox](../guides/agent-inbox.md).

**A semantic layer's server.** A hosted brain serves MCP under `/mcp` on its
environment hosts. `astrolift_connect_brain`, `issueBrainReadToken` in GraphQL
and `astro brain connect <slug>` return the endpoints, the
`X-Astrolift-Brain-Token` header and a 15-minute read token for the caller and
that brain; ask again for a fresh one. `astro brain connect --format mcp`
prints a ready `mcpServers` entry, `--format claude` a `claude mcp add-json`
command, and `--write <file>` stores it with mode 0600. Since the 2026-10-25
release the brain's `/mcp` route carries its own edge policy: the brain token is
required there, its audience must be that brain, and the route is never passed
through the shared sign-in gate; every other route, including the brain's own
`/`, strips the header. A hosted app created with `astrolift_host_app_on_brain`
receives the same URL and a read token on every deploy.
