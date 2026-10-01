# Provision shared services and attach consumers

Project resources are provider-managed services owned by a project. Provision
one resource and attach multiple app environments or agent environment specs.
The resource lifetime is independent of any one consumer.

## Discover support on the target cluster

```bash
astro project resources catalog --project platform --cluster production --json
astro project resources catalog --project platform --cluster production --available-only
```

Choose a kind, variant, and size advertised as available. Planned entries are
visible for discovery but cannot be provisioned. Provider configuration and
networking remain specific to the selected cluster.

## Provision once

The following example assumes the catalogue advertises `postgres` / `rds` and
its `medium` preset. Put only non-secret provider options in `database.json`.

```bash
astro project resources add --project platform --cluster production \
  --kind postgres --variant rds --name report-db --size medium \
  --config @database.json --agent report-prod
astro project resources show report-db --project platform --json
astro project resources list --project platform --json
```

Provisioning is asynchronous. Wait for ready provider status and binding
readiness. A pending or failed operation includes its diagnostic; creation
acceptance alone does not make the service usable.

## Attach apps and other agents

```bash
astro project resources attach report-db --project platform --app-env <app-environment-guid>
astro project resources attach report-db --project platform --agent reviewer-prod
astro app services list my-app --environment production --json
astro project resources cost report-db --project platform --json
```

An attachment targets exactly one consumer. Agent targets are environment-spec
slugs; app targets are environment GUIDs. The binding can provide secret
references, identity grants, environment values, or mounts. Inspect attachment
and grant state rather than assuming credentials are automatically readable.

Writes require project RBAC and token scope `project:write`. A newly added scope
can require `astro auth refresh` or a new login. Read-only discovery does not
prove write authorization.

## Change or remove

```bash
astro project resources update report-db --project platform --config @database.json
astro project resources reprovision report-db --project platform
astro project resources detach <attachment-guid> --project platform
astro project resources remove report-db --project platform
```

Detach removes a consumer binding. Removing the resource deprovisions it and
normally retains provider data. `--delete-data` and `--force-destroy` request
separate destructive behavior; inspect backups, attachments, and provider
protection before using them. This catalogue describes shared cloud services;
model serving support should be checked against the target server's actual API.
