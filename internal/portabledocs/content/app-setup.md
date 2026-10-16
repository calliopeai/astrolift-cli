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
owning project by GUID where a flag asks for an ID. A monorepo app built in CI
also needs `--build-context`; see [Build a monorepo app in CI](monorepo-ci.md).

## What registration creates

`astro app register` does more than record the app:

* It applies the manifest: workloads, and every `[[managed_services]]` entry.
  An app-scoped entry becomes a service owned by this app and starts
  provisioning. A project-scoped entry creates or attaches a shared project
  resource, which needs `project.update`. See the
  [manifest reference](../reference/astrolift-toml.md#managed-services).
* It creates an environment on a managed cluster for each
  `[environments.<name>]` table and each environment a managed service names,
  or `production` when the manifest names none.
* It starts onboarding, which creates the app's namespace and, for an app that
  builds an image, its registry repository. On AWS that is the ECR repository
  `<org-slug>/<app-slug>`, plus, for a GitHub source, the OIDC push role CI
  assumes to push to it.
* It tries to wire CI: the managed workflow, the push webhook and the CI
  secrets. Steps that fail are recorded on the app; `astro ci setup` runs them
  again. See [the managed workflow](monorepo-ci.md#the-managed-workflow).

Onboarding and provisioning finish after the command returns. Check the app's
status before the first deploy.

`astro project resources add` and `attach` are the other path to a shared
service: an operator provisions it once and attaches app environments by GUID,
outside any manifest. The app's manifest does not change or remove an
attachment made that way, and declaring the same service in the manifest is
refused. See [Shared services](shared-services.md).

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
confirmation unless `--yes` is supplied.

A secret value is stored in the top-level `[env]` table of the app's staged
manifest draft, on the platform, and is never written to the repository:

* **Push to repo** on the app's Config page leaves `[env]` out of the pull
  request it opens. A draft whose only changes are secrets has nothing to push.
* **Pull from repo**, and every sync from the repo including the one at deploy
  time, keeps the staged secrets and applies them to the manifest it brings
  in. For a key both sides set, the platform's value wins; a key removed with
  `astro app secrets delete` stays removed.

Secrets alone do not make the draft diverge from the repo. Other draft edits
still do, and Pull from repo discards those.

See the [app operations guide](../working-with-apps.md)
for environment, domain, secret, and removal workflows.
