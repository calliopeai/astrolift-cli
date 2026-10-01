# Set up an application

An app owns its source registration, workload manifest, environments, and
rollouts. Project resources can be shared by several apps and agents.

## Prepare source and ownership

```bash
astro auth login
astro status --json
astro project list --json
astro app init
```

Edit the generated `astrolift.toml`. Select the image, workload port, deployment
policy, and environment configuration from the [manifest reference](../reference/astrolift-toml.md).
Keep literal credentials in the secret store. Commit and push the manifest;
registration reads the source provider, not your uncommitted local file.
The organization needs a connected source provider and a managed tenant cluster.

```bash
astro app register --project-id <project-guid> \
  --source-repo owner/my-app --manifest-path astrolift.toml
astro app show --app my-app
```

For a monorepo, each app has its own manifest path and registration. Select the
owning project by GUID where a flag asks for an ID.

## Deploy an existing image

Build and publish the image in your source pipeline first. Manual deployment
requires a tag already present in the app's registry.

```bash
astro app deploy --app my-app --env production --image-tag sha-abc1234 --wait
astro app logs --app my-app
astro app services list my-app --environment production --json
```

A successful source build is separate from rollout success. Check the terminal
deployment state, workload readiness, and application health before routing
traffic. Use the dashboard deployment detail for approval, rollback, or retry
when your permissions and the current state permit the action.

## Bind shared services and secrets

Provision shared infrastructure with [project resources](shared-services.md),
then attach the app environment by GUID. Wait for both provider readiness and
binding readiness. Attachments expose references, identities, configuration,
or mounts according to the provider; they do not require copying credentials
into a manifest.

Manage literal app secrets with the CLI:

```bash
astro app secrets list my-app --environment production --json
astro app secrets create DATABASE_URL my-app --stdin --scope production < /secure/path/database-url
astro app secrets create DATABASE_URL my-app
astro app secrets delete UNUSED_KEY my-app --yes
```

`create` sets a new value or updates an existing key. Use `--stdin` with shell
file redirection, or omit it for a hidden terminal prompt. There is no `--file`
flag for this command; avoid `--value`, which puts a literal secret in process
arguments and shell history. Listing returns metadata, never values.

`--scope` accepts `all`, an environment name, or `preview:<branch>`. Omitting it
preserves an existing key's scope; a new key defaults to `all`. An installation
with secret-change approval can queue a proposal instead of applying the write.
Check that proposal before assuming a new value is live. Deletion prompts for
confirmation unless `--yes` is supplied. See the [app operations guide](../working-with-apps.md)
for environment, domain, secret, and removal workflows.
