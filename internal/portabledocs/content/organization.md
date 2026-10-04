# Running an Organization

The dashboard's Admin module holds organization identity, infrastructure, governance, and platform signals. Visibility is server-controlled; an organization administrator may still need a token with the matching bearer scope when using the CLI or API.

## Dashboard and workspace

`/dashboard` summarizes the current install and organization. The workspace tree in the sidebar is the durable map from teams to projects and their apps, agents, and workflows.

Astrolift currently treats one organization as the tenant attached to an install's dashboard. The CLI can store multiple Astrolift servers and select between them with `astro server use`; it does not use the dashboard brand chip as an organization switcher.

## Organization structure

Admin → Organization contains:

- **Organization** — organization identity and install-facing defaults.
- **Members** — users, invitations, and organization access.
- **Teams** — team ownership and membership.
- **Projects** — the app-registration boundary; each app belongs to a project.
- **Policies** and **Permissions** — effective policy and permission inspection.

Routes are under `/administration/...`; older `/settings/...`, `/members`, `/teams`, and `/projects` links may redirect to the canonical pages.

Role bindings are hierarchical. An organization, team, or project grant can authorize an app operation even if there is no app-local binding. API tokens add a second constraint: a request needs both the RBAC permission and a token scope that allows it.

## Manage people and team membership

The prepared membership release adds a complete, paged user-to-team flow. Check
that the selected installation advertises `identity.reviewed_team_membership` before
using its new API fields or expecting these screens. A documentation update does
not upgrade your server.

To manage a team, open **Access → Teams**, select the team, then open **Members**
at `/administration/access/teams/<team-slug>/members`. Search or page through the
roster; **Add member** selects an existing, active organization member and a team
role you are allowed to grant. Review the exact person, team and role before
confirming. Joining a team does not create a new user or organization membership.

To manage one person, open their **Teams** page at
`/administration/access/people/<organization-member-guid>/teams`. It shows the
teams visible to your current credentials and supports the same reviewed
add/remove flow. Use the person's organization-member GUID, not a team-member
GUID, email or username.

Removing direct membership removes the reviewed team attachment and that
person's reviewed direct team role bindings. It preserves organization grants,
other teams, inherited grants and identity-provider groups. The page distinguishes
those remaining sources and expired grants; a person can still have access after
their direct attachment is removed. Manage inherited or provider-controlled
access at its source rather than repeatedly removing the team attachment.

Team managers need `team.read` to inspect a named team and
`team.manage_members` to change its membership. The role picker and mutation
respect their current grant ceiling, organization policy and any required recent
authentication. Team-only managers can reach Teams without gaining access to
People, organization settings or infrastructure. A navigation hint never grants
an action. Existing bearer scopes are unchanged: `team:write` does not currently
include `team.manage_members`. Use the authenticated browser or a credential
whose existing scope ceiling actually permits the operation; account permission
diagnosis alone is insufficient.

After a change, refresh the roster. A committed receipt records the original
change; its remaining-source metadata is not a fresh effective-access decision.
If a reply is lost, retain the original request UUID and reviewed input. Replay
can recover that original outcome under current authority; a changed person,
team, credential or review is refused. Do not generate a new request merely
because the response or subsequent refresh failed.

Organization member managers retain the team's separate **Assign roles** page
at `/administration/access/teams/<team-slug>/assign-roles`. This legacy bulk role
assignment shows at most 500 direct attachments; use the complete Members page
for paged membership management. Role assignment and direct-membership removal
remain separate actions.

The [API reference](reference/api.md#reviewed-team-membership) names the read,
review and mutation contracts. The CLI exposes this guide through its bundled
knowledge catalogue and supports generic GraphQL requests; it does not yet have
native membership CRUD commands.

## Infrastructure

Admin → Infrastructure contains Clusters, Domains, Providers, and Webhooks.

- `/clusters` lists tenant clusters and their management/health state. The cluster detail separates activity, health, status, and settings.
- `/providers` contains cloud-provider plugin configuration and source-provider connections. Source connections are used to browse repositories and install or synchronize source webhooks.
- `/domains` is the organization-wide domain inventory.
- `/webhooks` manages outbound event subscriptions and delivery history. App-scoped subscriptions also appear on the app.

Provider capabilities are discovered from the installed plugin set. A cluster using one provider may expose a different managed-service, identity, DNS, model, or observability surface from another cluster.

## Usage and governance

Admin → Usage & Governance contains Cost, Quotas, Metrics, API Keys, and Audit.

- **Cost** displays collected provider cost data. An empty view can mean the provider collector or required credentials are not configured; it is not an estimate generated by the UI.
- **Quotas** displays enforced organization limits and usage where the backend exposes them.
- **Metrics** shows platform-level measurements available to the active install.
- **API Keys** (`/tokens`) creates and revokes scoped bearer credentials. Plaintext is one-time output.
- **Audit** records security-relevant control-plane actions. Use it to correlate actor, request, target, decision, and mutation outcome.

Use `astro perms` to diagnose the permission side of a failure. If the user is allowed but a CLI request says the token scope does not permit the operation, refresh or replace the token with one carrying the required scope.

## Platform signals

Admin → Platform Signals contains Platform Activity, Alerts, and Events.

- **Events** are operational facts emitted by the platform.
- **Audit** is the security and change-control record; it is not merely a filtered event list.
- **Alerts** manages alert rules and their firing events. Acknowledgement and resolution are distinct states.
- **Platform Activity** provides a cross-resource operational timeline.

The exact event types, metrics, and alert evaluators depend on the enabled modules and providers. Do not build automation by scraping labels from these pages; use GraphQL, REST, MCP, or the CLI's JSON output.

## Secrets administration

Applications, agents, workflows, and pipelines have resource-scoped secret
surfaces. Reusable organization Secret Bundles are managed in the dashboard or
GraphQL API. The current `astro org secret-bundles` group is a placeholder and
has no CRUD subcommands yet.

Secret values can be revealed only when all of the following are true:

1. the configured backend supports disclosure;
2. the caller has the read permission;
3. the bearer token scope permits the operation;
4. any elevation or approval requirement is satisfied.

Reveal, write, rotate, bundle attach/detach, and delete are separate audited operations. GitHub Actions secrets are a common write-only exception.

## CLI equivalents

Useful discovery commands include:

```bash
astro org --help
astro operator --help
astro perms --help
```

`astro org audit`, `events`, `alerts`, and `cost` are additional help topics in
the current CLI, not runnable data commands. Use the dashboard or GraphQL API
for those resources until their CLI operations land.

Use `--json` for automation and `--api-url` or `astro server use` to make the target install explicit.
