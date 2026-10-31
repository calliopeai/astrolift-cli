# Running an Organization

The dashboard's Admin area holds organization identity, access, infrastructure, governance, and platform signals. Visibility is server-controlled; an organization administrator may still need a token with the matching bearer scope when using the CLI or API.

## Home and the projects rail

`/dashboard` is Home, a panel layout summarizing the current install and organization. The projects rail on the right is the durable map from the organization to its projects and their apps, agents, and workflows.

The dashboard operates in one active organization at a time. The CLI can store multiple Astrolift servers and select between them with `astro server use`, and selects the working organization with `astro org use`; it does not use the dashboard's organization label as a switcher.

## Organization structure

Admin › Organization contains:

- **Organization** (`/administration/organization`): organization identity, install-facing defaults, the house theme, trusted domains, and the **Create accounts for trusted domains** setting. With that setting on, a verified sign-in under a trusted domain creates its own account and joins with the domain's default role; off (the default) admits only people who already have an account. Turning it on or off is audited.
- **Access** (`/administration/access`): People, Teams, Roles, Policies, and Check access (permission diagnostics). Roles also carry Assignments, Easy setup presets, and Action safety.
- **Projects** (`/administration/projects`): the app-registration boundary; each app belongs to a project.

Routes are under `/administration/...`; older `/settings/...`, `/members`, `/teams`, and `/projects` links redirect to the canonical pages.

Role bindings are hierarchical. An organization, team, or project grant can authorize an app operation even if there is no app-local binding. API tokens add a second constraint: a request needs both the RBAC permission and a token scope that allows it.

## Manage people and team membership

The prepared membership release adds a complete, paged user-to-team flow. Check
that the selected installation advertises `identity.reviewed_team_membership` before
using its new API fields or expecting these screens. A documentation update does
not upgrade your server.

To manage a team, open **Access › Teams**, select the team, then open **Members**
at `/administration/access/teams/<team-slug>/members`. Search or page through the
roster; **Add people** selects an existing, active organization member and a team
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
an action. Existing bearer scopes are unchanged: `team:write` covers
`team.create`, `team.update` and `team.delete`, not `team.manage_members`. Use
the authenticated browser or a credential whose existing scope ceiling actually
permits the operation; account permission diagnosis alone is insufficient.

Role definitions must belong to the current organization or be installation/global
roles. A role owned by another organization cannot authorize a user or group
binding in this organization. Current organization and global roles retain their
existing scope, inheritance and expiry checks.

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
knowledge catalogue and supports generic GraphQL requests (`astro api graphql`);
`astro team` creates and lists teams and `astro role` manages roles and
bindings, but there are no native membership CRUD commands yet.

## Infrastructure

Admin › Infrastructure contains Clusters, Domains, Providers, and Webhooks.

- `/clusters` lists tenant clusters and their management/health state. The cluster detail separates activity, health, status, and settings.
- `/providers` contains cloud-provider plugin configuration, source-provider connections, and identity providers. Source connections are used to browse repositories and install or synchronize source webhooks.
- `/domains` is the organization-wide domain inventory, including the managed zones that apps and previews are routed under; `/domains/connect` connects a provider zone. See [Domains and DNS diagnostics](guides/domains-dns.md).
- `/webhooks` manages outbound event subscriptions and delivery history. App-scoped subscriptions also appear on the app.

Provider capabilities are discovered from the installed plugin set. A cluster using one provider may expose a different managed-service, identity, DNS, model, or observability surface from another cluster.

## Usage and governance

Admin › Usage & governance contains Metrics, API keys, Audit, Runs, Features, and Platform settings.

- **Metrics** (`/administration/metrics`) shows platform-level measurements available to the active install.
- **API keys** (`/tokens`) creates and revokes scoped bearer credentials. Plaintext is one-time output.
- **Audit** (`/administration/audit`) records security-relevant control-plane actions. Use it to correlate actor, request, target, decision, and mutation outcome.
- **Runs** (`/administration/runs`) is the combined run audit: agent and workflow runs, deployments, and job runs in one list.
- **Features** (`/administration/features`) toggles the install's public runtime feature flags; install-time features need a redeploy.
- **Platform settings** (`/administration/platform-settings`) holds the operator routes and the secret-store configuration story.

Use `astro perms diagnose` to diagnose the permission side of a failure. If the user is allowed but a CLI request says `api token scope does not allow this permission`, the credential's scopes are the limit: sign in again with the scope the operation needs (`astro auth login --help` lists them) or replace the token with one carrying the required scope.

## Platform signals

Admin › Platform signals contains Platform activity, Alerts, Events, Logs, Traces, and Operations.

- **Events** (`/events`) are operational facts emitted by the platform.
- **Audit** is the security and change-control record; it is not merely a filtered event list.
- **Alerts** (`/alerts`) manages alert rules and their firing events. Acknowledgement and resolution are distinct states.
- **Platform activity** (`/platform-activity`) provides a cross-resource operational timeline.
- **Logs** (`/logs`) and **Traces** (`/traces`) read the install's collected telemetry; see [Exact environment logs and traces](guides/logs-traces.md).
- **Operations** (`/ops`) is a one-screen summary: clusters, deployment metrics, unresolved alerts, recent audit events, and recent workflow runs.

The exact event types, metrics, and alert evaluators depend on the enabled modules and providers. Do not build automation by scraping labels from these pages; use GraphQL, REST, MCP, or the CLI's JSON output.

## Secure

The **Secure** area shows **Zentinelle** (`/secure/zentinelle`) once the organization is connected and you hold a Zentinelle permission. From the CLI, `astro zentinelle connect --url <zentinelle-url> --code-stdin` connects the working organization with an enrollment code, `astro zentinelle status` shows the connection and each cluster's gateway, and `astro zentinelle cluster` registers clusters and controls their gateways. Reads need a sign-in made with `astro auth login --scope zentinelle`; writes and audit export need `--scope admin`. See [Read Zentinelle without an admin credential](reference/cli.md#read-zentinelle-without-an-admin-credential).

## Secrets administration

Applications, agents, workflows, and pipelines have resource-scoped secret
surfaces. Reusable organization Secret Bundles are managed in the dashboard (a
project's Resources page lists them with their consumers) or through the
GraphQL API. The current `astro org secret-bundles` group is a placeholder and
has no CRUD subcommands yet; `astro api graphql` reaches the mutations.

Secret values can be revealed only when all of the following are true:

1. the configured backend supports disclosure;
2. the caller has the read permission;
3. the bearer token scope permits the operation;
4. any elevation or approval requirement is satisfied.

Reveal, write, rotate, bundle attach/detach, and delete are separate audited operations. GitHub Actions secrets are a common write-only exception.

Secret reveal and change ask for a confirmation and a fresh sign-in by default. See [Action safety](guides/action-safety.md) for those gates and for approval on destructive deletes.

## CLI equivalents

Useful discovery commands include:

```bash
astro org --help
astro operator --help
astro perms --help
astro role --help
astro team --help
astro project --help
```

`astro alert list` lists alert rules. `astro org audit`, `events`, and
`secret-bundles` are additional help topics in the current CLI, not runnable
data commands; use the dashboard or GraphQL API for those resources until their
CLI operations land. `astro audit` is Zentinelle's agent evidence stream, not
the organization audit log.

Use `--json` for automation and `--api-url` or `astro server use` to make the target install explicit.
