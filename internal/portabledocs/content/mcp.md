# MCP reference

Astrolift exposes an authenticated Streamable HTTP MCP server for agent
inspection, dispatch, cancellation, source reconciliation, and package import.

## Endpoint and credentials

```text
https://<install-host>/api/mcp/v1/
```

Use a user API token (`alft_at_...`) in the bearer header. Choose scopes by
capability:

| Scope | Capability |
|---|---|
| `mcp:read` | List agents, read immutable packages and tasks |
| `mcp:dispatch` | Dispatch and hard-stop agent tasks |
| `mcp:write` | Reconcile agent repos and import agent specs |

Scopes are ceilings over RBAC. For example, `mcp:dispatch` also requires the
user to hold `agent.dispatch`. `admin` is a token-scope wildcard but does not
create user permissions.

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

Keep the token in the client's secret/environment facility, not in a committed
configuration file. Browser-originated clients must use an origin allowed by
the installation operator.

## Tools

The server filters `tools/list` to operations allowed by both scope and RBAC.
The checked-in [generated capability document](https://github.com/calliopeai/astrolift-app/blob/main/backend/contracts/mcp-tools.json)
is the caller-independent superset used for release tooling and documentation.
Pin a tag or commit instead of `main` for reproducible generation. It includes
the required token scopes, RBAC permissions, and input JSON Schemas; it does
not imply that a particular caller may invoke every listed tool.

| Tool | Required scope | Purpose |
|---|---|---|
| `astrolift_list_agents` | `mcp:read` | List registered agents and package delivery state |
| `astrolift_get_agent` | `mcp:read` | Read one canonical Agent Package |
| `astrolift_get_task` | `mcp:read` | Read lifecycle, result, and failure telemetry |
| `astrolift_run_agent` | `mcp:dispatch` | Dispatch a registered task-family agent |
| `astrolift_cancel_task` | `mcp:dispatch` | Hard-stop the task and delete its spawned workload |
| `astrolift_sync_agent_repo` | `mcp:write` | Reconcile a single-agent or monorepo source |
| `astrolift_import_agent_spec` | `mcp:write` | Preview/persist AGENTS.md, native package, Langflow, or Flowise |

Call `tools/list` instead of hard-coding input schemas. Tool arguments reject
unknown properties. Import returns explicit conversion gaps and never persists
a non-runnable result.

## Resources

`resources/list` exposes immutable package resources:

```text
astrolift://agents/<agent-slug>/package
```

`resources/read` returns the JSON Agent Package. Prompts are currently empty.
Secret values are never tools or resources.

## Protocol check with curl

Initialize first and retain the returned `Mcp-Session-Id` for later requests:

```bash
curl -i https://astrolift.example.com/api/mcp/v1/ \
  -H "Authorization: Bearer $ASTROLIFT_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}'
```

The current server supports protocol versions `2025-03-26`, `2025-06-18`, and
`2025-11-25`. Current-protocol requests after initialization require the
session header. Sessions expire, so clients must be able to initialize again.

Transport requirements:

- `POST` or `DELETE` only.
- `Content-Type: application/json` for POST.
- `Accept` includes both `application/json` and `text/event-stream`.
- JSON must contain `jsonrpc: "2.0"`.
- Request bodies are limited by the installation (2 MiB by default).

Every tool call is audited. Errors from a valid tool call use MCP
`isError: true` with a structured `code` and `message`; transport/protocol
errors use JSON-RPC errors.
