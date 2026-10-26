# Temporary apps

Give a demo, review build or generated app an expiry when you create it. Astrolift
stops and tears down the app when the timer expires. You can extend the timer
while the app is still active.

Start with [app setup](app-setup.md) for source registration and deployments.
A semantic layer itself is permanent; an app connected to that semantic layer
can be temporary.

## Check your installation

```bash
astro status --json
astro app register --help
astro app extend --help
astro app expiry --help
```

The server must advertise `apps.temporary_expiry`. The executing CLI must contain
the expiry commands. A capability identifies the API; your project grants and
token scopes still decide whether you can create or change an app.

## Set an expiry

For an app you are registering from an existing manifest:

```bash
astro app register --file astrolift.toml --ttl 7d
```

Use `--expires-at` with an RFC 3339 timestamp when the deadline is fixed.
Supply one deadline form. Durations accept `30m`, `4h`, `7d`, `6w` and `3mo`;
a month is 30 days. The minimum is 15 minutes. The organization's maximum
defaults to 90 days.

You can put the duration in your existing manifest's `[app]` table:

```toml
[app]
expires_in = "7d"
```

An explicit registration deadline takes precedence over the manifest. The
server applies the organization's policy to the final deadline.

The GraphQL creation inputs `RegisterAppInput` and `HostAppOnBrainInput` accept
`expiresIn` or `expiresAt`, plus `deleteDataOnExpiry`. This lets an API client
create the app and its deadline together. For an app connected to an existing
semantic layer, use `hostAppOnBrain`; creating the semantic layer is a separate
operation. See the [API reference](../reference/api.md) for authentication and
mutation error handling.

## Read or change a deadline

```bash
astro app extend demo --by 2d
astro app expiry demo --clear --yes
```

An extension moves the existing deadline. Clearing the deadline makes the app
permanent and asks for confirmation unless you supply `--yes`. That flag does
not bypass server permissions or action-safety rules. A noninteractive clear
without explicit confirmation is refused.

The app's API representation exposes `expiresAt` and `deleteDataOnExpiry`.
The dashboard's countdown shows the current deadline. An expired app whose
teardown has started cannot be extended; use a new app when you need another
temporary deployment.

```bash
astro app list --temporary --json
astro app show demo --json
```

`astro app extend demo --until <RFC3339>` selects a new absolute deadline.
That deadline can shorten the remaining lifetime; `--by` adds to the existing
deadline.
`astro brain host-app` also accepts `--ttl` or `--expires-at` when connecting
a new app to a semantic layer.

API clients can choose exactly one of `expiresIn`, `expiresAt`, `extendBy` or
`clear`:

```graphql
mutation ChangeAppExpiry($input: SetAppExpiryInput!) {
  setAppExpiry(input: $input) {
    ok
    errors { code message field }
    data { id slug expiresAt deleteDataOnExpiry }
  }
}
```

For example, `{"input":{"appSlug":"demo","extendBy":"2d"}}` extends the
existing deadline. Check GraphQL `errors` and mutation `ok` before treating the
change as accepted.

## Organization policy

```graphql
query TemporaryAppPolicy {
  astroliftTemporaryAppPolicy {
    maxTtlSeconds
    minTtlSeconds
    temporaryOnlyForMembers
    ttlRequiredSurfaces
    dataGraceDays
  }
}
```

An organization can limit the maximum lifetime, require members to create only
temporary apps, or require deadlines for `chat_studio`, `mcp` and `api_token`
creation surfaces. Policy changes require `org.update`. Changing an existing
app's expiry requires `app.update` on that app.

Making an app permanent also follows `app.expiry.clear` action safety. Agent
tokens cannot clear an expiry unless the organization permits that action for
agents. See [action safety](action-safety.md) for confirmation, verification and
approval rules.

## Warnings and retained data

The timer sends warnings before expiry through the app's notification and alert
subscription channels. Notification preferences and the installation's sending
configuration affect whether a warning reaches you. Do not rely on an email as
the timer itself.

At expiry, Astrolift removes the app's runtime and its owned routing resources.
Managed-service data is retained for the organization's grace period, seven
days by default. `deleteDataOnExpiry: true` requests immediate data deletion.
Choose that option only for disposable data. Inspect the app's audit history
for teardown, retained resources and later purge; a timer firing does not prove
that every provider operation has completed.

The timer is durable across worker restarts. Failed cleanup is retried, and a
periodic sweep recovers apps whose timer is missing. Extending an active app
reschedules its timer and resets its warning schedule.
