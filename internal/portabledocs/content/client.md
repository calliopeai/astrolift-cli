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

For an AI coding agent (Claude Code, Codex, or any MCP client), `astro onboard`
writes the MCP server entry for the selected install, the bundled skill
catalogue and the offline guides into a workspace:

```bash
astro server add prod https://astrolift.example.com
astro onboard --dry-run
astro onboard
```

Substitute `prod` with a slug of your choice and the URL with the API URL your
operator gave you (`astro server list` shows registered servers). The dry run
prints `Dry run: nothing written.` and one `would write <path>` line per file;
the real run prints `Install: <url>` and `wrote` lines for `.mcp.json` and/or
`.codex/mcp.json`, the skills under `.claude/skills/` or `.codex/skills/`, and
the guides under `.astrolift/docs/`. `skipped <path> (exists; pass --force to
replace)` leaves an existing file alone. `Auth: NOT configured` is expected on a
fresh machine: export `ASTROLIFT_TOKEN` (an API token from Settings > API
tokens) or run `astro auth login`, then re-run `astro onboard` to see `Auth:`
report the source. `no current server` means the `server add` step was skipped.
`--only mcp,skills,docs` and `--target claude|codex|both` narrow what is
written. The MCP entry references `${ASTROLIFT_TOKEN}`, so one API token
authenticates both the CLI and the MCP gateway; the token model is in the
[API reference](../reference/api.md) and the gateway contract in the
[MCP reference](../reference/mcp.md).

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
release in CI. `astro auth refresh` renews the current server's access token;
a sign-in's scopes are fixed when it is approved, so a scope the server added
later (for example `--scope zentinelle`) needs a new `astro auth login`.

Continue with the [CLI](../reference/cli.md), [API](../reference/api.md), or
[MCP](../reference/mcp.md) reference.
