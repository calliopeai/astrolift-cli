# Provision shared services and attach consumers

Project resources are provider-managed services owned by a project. Provision
one resource and attach multiple app environments or agent environment specs.
Their lifetime is independent of any one consumer. App-owned services have a
separate list; an app slug does not identify a shared project's resources.

The paged metadata and reviewed-action commands below require a CLI release
containing the managed-resource update and the corresponding installation SDL.
CLI v0.8.0 predates these additions. Check `astro version`, `astro project
resources --help` and the selected server before using the new flags.

## Discover the target cluster's support

```bash
astro project resources catalog --project platform --cluster production --json
astro project resources catalog --project platform --cluster production --available-only
```

Choose a kind, variant and size advertised as available. Planned entries are
visible for discovery but cannot be provisioned. Configuration, networking and
provider permissions remain specific to that cluster.

## Page safe metadata and review an exact target

```bash
astro project resources list --project platform --page --limit 50 --json
astro project resources list --project platform --page --limit 50 \
  --search cache --kind redis --status active --environment production --json
astro project resources list --project platform --page --limit 50 \
  --after '<previous-nextCursor>' --json
astro project resources show <resource-guid> --project platform --json
```

`--page` returns `{items,totalCount,nextCursor}`. Limits range from 1 to 200;
`totalCount` counts the currently visible filtered set. Continue with the same
filters, server, organization and identity. A malformed, foreign, inaccessible
or deleted-anchor cursor is refused; restart from the first page instead of
turning that refusal into an empty result. This is a live cursor walk, not an
atomic snapshot across concurrent changes.

The default list preserves the CLI's JSON array shape. It walks authorized
server pages with a 200-page safety bound and emits JSON only after reaching
the end. A refused continuation, repeated cursor or safety-bound exhaustion
returns an error with no partial JSON array. Use `--page` for explicit
continuation. `--after` requires `--page`. `--cluster` on a resource list is an
exact cluster GUID; catalogue selection also accepts a cluster slug.

A detail read returns the service GUID, `contextRevision`, owner organization,
project or app, cluster/environment GUIDs and versions, current status and
operation identity/timestamps. It contains no configuration, credentials,
connection material, provider failure bodies or unrequested grants. A missing,
deleted or inaccessible GUID never selects a same-name replacement. A name on
project show/cost/actions uses at most two discovery rows, then rereads the
selected GUID; ambiguous names require a GUID.

## Provision once

This example assumes the catalogue advertises `postgres` / `rds` and `medium`.
Put only non-secret provider options in `database.json`.

```bash
astro project resources add --project platform --cluster production \
  --kind postgres --variant rds --name report-db --size medium \
  --config @database.json --agent report-prod
```

Provisioning is asynchronous. Acceptance or enqueue is separate from provider
completion. Review the exact GUID's status and operation workflow/run IDs before
assuming the resource is usable. These actions do not have durable CLI request
recovery; after a lost reply, inspect the target's operation metadata before
issuing another mutation. The CLI does not retry writes automatically.

## Review consumers and attach exact environments

```bash
astro project resources attachments <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision> --limit 50 --json
astro project resources attachments <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision> --limit 50 \
  --after '<previous-nextCursor>' --json
astro project resources attach <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision> --app-env <app-environment-guid>
astro project resources attach <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision> --agent reviewer-prod
```

Consumer pages independently return `{items,totalCount,nextCursor}` and only
identities currently readable by the caller. They contain no binding credentials
or grant values. A refused or missing target is distinct from zero visible
consumers. Changed context requires a fresh exact review and a new cursor chain.

An attachment targets one app environment GUID or one agent environment-spec
slug. The consumer must share the service's project and tenant cluster. An agent
must also belong to that project through a workload or workflow stage and resolve
to that cluster. Matching environment names alone do not establish ownership.
Bindings may supply secret references, workload identities, environment values
or mounts; metadata access does not grant permission to reveal them.

## Keep app-owned resources separate

```bash
astro app services list my-app --environment production --page --limit 50 --json
astro app services show <app-owned-resource-guid> --app my-app --json
```

App-owned list defaults also preserve the JSON array and walk to completion or
return an explicit incomplete-walk error. Its `--page` mode exposes one page.
Exact detail validates the selected app and immutable resource GUID. Project
mutations operate on shared project resources, not an app-owned substitute.

## Request pricing explicitly

```bash
astro project resources cost <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision> --json
```

Basic browsing never requests pricing. Cost is a separate permission-checked
read of the reviewed context. A valid estimate includes amount, currency,
retrieval time, HTTPS source and approximation flag. A missing amount, invalid
source/time/currency, negative amount or unavailable provider is unavailable,
not zero. A valid explicit zero still needs the same pricing evidence. Provider
list prices can omit discounts and usage; the amount is not an actual bill.

## Service exporter metrics

Where supported, request `astroliftAppManagedServiceMetrics` using the exact
service ID and `expectedContextRevision` from the metadata review. The reviewed
path validates the current owner and placement before runtime reads, uses the
recorded cluster rather than environment-name discovery, and refuses a changed
context. Legacy callers may omit the proof; new triage should always supply it.
These exporter series are separate from [physical workload signals](workload-signals.md)
and do not establish current pod/container membership or CPU/memory limit evidence.
A missing or refused read is unavailable, not healthy zero.

## Change or remove the reviewed resource

```bash
astro project resources update <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision> --config @database.json
astro project resources reprovision <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision>
astro project resources detach <attachment-guid> --project platform \
  --resource <resource-guid> --expected-context-revision <reviewed-revision>
astro project resources remove <resource-guid> --project platform \
  --expected-context-revision <reviewed-revision>
```

The existing `detach <attachment-guid>` invocation remains supported: it reads
that exact attachment's currently visible owner, then rereads the owner GUID with
its captured revision before writing. It does not scan resources or fall back to
a service name. The optional `--resource` flag pins the owner explicitly. Deleted,
foreign or inaccessible attachments are refused.

New CLI actions always send the exact fresh `contextRevision`; the optional flag
also pins an earlier explicit review. The server checks current permissions and
owner/service/cluster/environment state under the write transaction. Stale
bindings are refused before effects. Reviewed lifecycle workers carry the
accepted binding and refuse ownership changes before provider work; exact GUIDs
alone would not prevent following a moved service.

Project reads require `PROJECT_READ`; exact generic/app reads and pricing require
`APP_READ` on the actual owner. App consumers require current app-read permission;
agent consumers require visible agent-environment-spec access. Writes require
project RBAC (`PROJECT_UPDATE`) and token scope `project:write`. A token scope
narrows existing grants. Refresh authentication only when an added scope is
needed; a read does not prove write or secret-reveal authorization.

Detach removes a binding. Removal deprovisions infrastructure and normally
retains provider data. `--delete-data` and `--force-destroy` request separate
behavior; review backups, consumers and provider protection first. This catalogue
is for shared cloud services; model-serving capabilities are separate contracts.
