# Control API reference

Astrolift's typed control-plane API is GraphQL. Focused REST, SSE, and WebSocket
routes exist where device authentication, callbacks, streaming, or CI semantics
do not fit GraphQL well.

## Base URLs

Given an install at `https://astrolift.example.com`:

| Surface | URL |
|---|---|
| GraphQL HTTP/explorer | `https://astrolift.example.com/app/gql/config/` |
| GraphQL subscriptions | `wss://astrolift.example.com/app/gql/config/ws/` |
| CLI device flow | `https://astrolift.example.com/api/cli/v1/auth/...` |
| MCP | `https://astrolift.example.com/api/mcp/v1/` |
| Health | `https://astrolift.example.com/health/` |

The server root returns a link map when requested as JSON:

```bash
curl -sS -H 'Accept: application/json' https://astrolift.example.com/
```

## Authentication and tenancy

User API tokens start with `alft_at_`. App-scoped deploy tokens start with
`alft_dt_` and are accepted only by their intended CI/deploy routes.

```http
Authorization: Bearer alft_at_...
X-Astrolift-Organization: <organization-guid>
Content-Type: application/json
```

The organization header selects one organization when the identity belongs to
more than one. Never use a slug where the API requires a GUID. The token's
scopes narrow the user's RBAC grants; requests must pass both checks.

Current API-token scopes are `read:apps`, `write:apps`, `read:clusters`,
`agent-env-spec:write`, `secret:read`, `secret:write`, `mcp:read`,
`mcp:dispatch`, `mcp:write`, and `admin`. Use the smallest set that supports
the integration.

## GraphQL request

```bash
curl -sS https://astrolift.example.com/app/gql/config/ \
  -H "Authorization: Bearer $ASTROLIFT_TOKEN" \
  -H "X-Astrolift-Organization: $ASTROLIFT_ORG_ID" \
  -H 'Content-Type: application/json' \
  --data-binary @- <<'JSON'
{
  "query": "query { astroliftServerInfo { version serverTime capabilities } }",
  "variables": {}
}
JSON
```

A successful HTTP response can still contain GraphQL `errors`; check that
array before reading `data`. Mutations generally return an envelope with
`ok`, structured `errors`, and `data`. Do not treat HTTP 200 as mutation
success without checking `ok`.

The installation's explorer and introspection expose the exact schema supported
by that server. The platform repository also publishes `schema.graphql` for
client generation. Prefer generated types or committed operations over building
query strings from user input.

## Compatibility

- Query server capabilities before assuming an optional module exists.
- Additive GraphQL fields are backward compatible; clients should ignore
  response fields they do not understand.
- Pin a CLI/SDK version in unattended automation.
- Use idempotency keys on deploy routes when retrying after a network failure.
- Expect `401` for invalid/expired credentials and permission errors when RBAC
  or the token scope ceiling denies the action.

There is no single catch-all REST/OpenAPI surface for control-plane CRUD. Use
GraphQL unless a documented workflow explicitly names a REST, SSE, or WebSocket
route. This avoids depending on internal Django paths.

## Secrets

Secret-list operations return names, references, and presence metadata. A
caller needs `secret:read` plus `secret.read` RBAC to reveal a value on the few
surfaces that support reveal. `secret:write` permits write-through operations,
not readback. MCP intentionally never exposes secret values.
