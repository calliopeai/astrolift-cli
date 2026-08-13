# Choose an Astrolift client

Every client reaches the same control plane. Choose the surface that matches
the job; authorization, tenant isolation, audit events, and lifecycle policy
remain server-side.

| Surface | Best for | Contract |
|---|---|---|
| Dashboard | Discovery, approvals, secret entry, visual workflow editing | Browser session + GraphQL |
| `astro` CLI | Developer operations, scripts, CI, log/exec streams | GraphQL + focused REST/WebSocket routes |
| GraphQL API | Custom integrations and full typed control-plane access | `/app/gql/config/` |
| REST routes | Device auth, CI deploy, streaming, callbacks | Route-specific JSON/SSE/WebSocket contracts |
| MCP | AI coding tools and remote agent automation | Authenticated Streamable HTTP at `/api/mcp/v1/` |

## A safe progression

1. Use the dashboard or `astro auth login` for human work.
2. Use `astro --json` in shell automation before writing a custom API client.
3. Mint a narrowly scoped API token for unattended GraphQL or MCP access.
4. Use an app deploy token, not a user token, for a single app's CI deployment.

An API token is a ceiling over the user's RBAC grants. Both must allow an
operation. Giving a token `admin` does not grant its owner an RBAC permission,
and making the user an administrator does not add missing token scopes.

## Discover the installed platform

```bash
astro status --json
astro version
astro version-check
```

The status response identifies the server version and capabilities. Pin a CLI
release in CI, and refresh a device-flow token after the server adds a new CLI
scope:

```bash
astro auth refresh
```

Continue with the [CLI](../reference/cli.md), [API](../reference/api.md), or
[MCP](../reference/mcp.md) reference.
