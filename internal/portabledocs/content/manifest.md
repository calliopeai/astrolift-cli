# `astrolift.toml` reference

`astrolift.toml` is used by more than one source workflow. Identify the file's
role before choosing fields:

| Role | Discovery path | Required shape |
|---|---|---|
| App | `astrolift.toml` or `apps/<slug>/astrolift.toml` | top-level `name`; one or more non-agent `[[workloads]]` |
| Agent | `astrolift.toml` or `agents/<slug>/astrolift.toml` | top-level `name`; exactly one `kind = "agent"` workload |
| Agent config/brief include | selected by an environment spec | `[skills.*]`, `[environment]`, `[secrets]`, optional `include` |

Workflow definitions use `workflows/**/*.toml`, not `astrolift.toml`; see the
[workflow reference](workflow-toml.md). An agent manifest can additionally use
the Agent Package fields in [Agent packages](agent-packages.md).

## Minimal application

```toml
astrolift_version = 1
name = "orders-api"

[environments.production]

[app]
slug = "orders-api"
display_name = "Orders API"

[[workloads]]
name = "web"
kind = "deployment"
is_public = true
replicas = 2
cpu_request = "250m"
cpu_limit = "1"
memory_request = "256Mi"
memory_limit = "512Mi"
hpa_min = 2
hpa_max = 10
hpa_target_cpu_pct = 70

  [[workloads.containers]]
  name = "web"
  is_primary = true
  dockerfile_path = "Dockerfile"
  build_context = "."
  port = 8000

    [workloads.containers.env]
    LOG_LEVEL = "info"

    [workloads.containers.healthcheck]
    kind = "http"
    value = "/health"
    port = 8000
```

The top-level `name` is the server parser's required display name. The `[app]`
identity is read by the CLI registration flow; include both. Unknown TOML keys
may be retained for forward compatibility, but do not assume they affect the
runtime unless documented here.

There is not yet a generated JSON Schema for this filename. App manifests and
agent configuration/includes are two related parser families, and validation
also depends on cross-field rules, source paths, chroot boundaries, provider
capabilities, and organization policy. Treat target-server validation as
authoritative until those parsers consume shared typed contracts that can emit
separate app and agent schemas plus a dispatcher schema.

`[environments.<name>]` tables declare environment names that registration and
repo resync can bootstrap, as the empty `production` table above does. Manage
the environment's cluster binding and runtime configuration through the
control plane; arbitrary nested override keys are not a supported deployment
contract yet.

## Workloads

Every `[[workloads]]` requires `name` and `kind`. Names must be unique across
workloads, `[[jobs]]`, and `[[tasks]]`.

Supported kinds are `deployment`, `statefulset`, `job`, `cronjob`, `task`,
`agent`, `workflow`, `function`, `static_site`, and `faas`.

Common fields:

| Field | Default | Meaning |
|---|---:|---|
| `is_public` | `false` | Create the public service/hostname path |
| `replicas` | `1` | Fixed replica count when HPA is not active |
| `cpu_request`, `cpu_limit` | platform defaults | Kubernetes quantities |
| `memory_request`, `memory_limit` | platform defaults | Kubernetes quantities |
| `hpa_min`, `hpa_max` | unset | HPA bounds |
| `hpa_target_cpu_pct` | `80` | HPA CPU target |
| `storage_class`, `storage_size` | unset | Legacy workload storage defaults |

A `cronjob` additionally requires `schedule` and accepts
`concurrency_policy = "forbid" | "queue" | "replace"`.

## Metrics

A workload is not scraped unless it says so. Declaring the table is the
opt-in; `port` is required because a scrape target with no port would have
to be guessed.

```toml
[[workloads]]
name = "web"
kind = "deployment"
is_public = true

  [workloads.metrics]
  port = 9090
  path = "/metrics"   # default
  # enabled = false   # keep the config, stop the scrape
```

| Field | Default | Meaning |
|---|---:|---|
| `port` | required | Container port serving the metrics endpoint |
| `path` | `/metrics` | Path scraped on that port |
| `enabled` | `true` | `false` keeps the table and turns the scrape off |

Rendering emits both scrape mechanisms, because a cluster may run either:
`prometheus.io/scrape`, `prometheus.io/port` and `prometheus.io/path`
annotations on the pod, and a `PodMonitor` scoped to the app's namespace for
a Prometheus Operator install. A private worker gets a `PodMonitor` with no
Service, which is the case annotation-only scraping handles badly.

### What the request panels need

Traffic, errors and latency (the RED half of the golden signals) are read
from one of two sources, and an app that matches neither leaves those three
panels empty:

* **Edge metrics.** If the cluster's ingress controller is one Astrolift has
  a mapping for, the request signals come from the controller's own metrics,
  keyed by the app's namespace. Nothing is required of the app.
* **Application metrics.** Otherwise the queries read the Prometheus HTTP
  conventions from the app itself:

  | Signal | Metric | Type |
  |---|---|---|
  | Traffic, errors, status codes | `http_requests_total` | counter, with a `code` label |
  | Latency quantiles | `http_request_duration_seconds_bucket` | histogram |

  Both must carry an `app="<app-slug>"` label, and — to answer the
  per-environment and per-workload views — `environment` and `workload`
  labels matching those slugs. The standard Prometheus client library for
  your language emits both metrics under these names; the labels are yours
  to add.

Saturation (CPU and memory) is read from the cluster's own cAdvisor and
kube-state-metrics series, so it populates whether or not the app is
instrumented. When the request panels have no source at all, they say so
rather than reporting an empty window.

## Edge identity

Apps behind the platform's auth gate receive the authenticated identity as
`X-Auth-Request-User` / `X-Auth-Request-Email` headers, and a shared secret on
`X-Astrolift-Gateway-Secret` that proves the request came through the gate
rather than from anything else that can reach the Service port in-cluster.

An app whose backend reads those under names of its own declares the mapping:

```toml
[edge]
gateway_secret_header = "x-qsr-proxy-secret"

[edge.identity_headers]
user = "x-qsr-user-id"
email = "x-qsr-email"
```

| Key | Meaning |
|---|---|
| `gateway_secret_header` | Header the gate's shared secret is stamped on for this app. The *value* is never declared here; it comes from the cluster's auth config |
| `identity_headers` | `identity = header-name` pairs. Valid identities are `user`, `email` and `access_token` — the ones the gate forwards |

The block is app-level, not per workload: every managed hostname for an app
shares one Ingress.

Header names must be valid HTTP header names; the value is written into the
ingress controller's configuration, so anything else is refused at parse time.
An identity the gate does not forward is refused too — a mapping for a header
that never arrives would render configuration that silently forwards nothing.

This is deliberately a mapping rather than a raw configuration snippet. A
snippet ties the app to one ingress controller's config language and hands it a
sharp edge, and ingress-nginx has been narrowing `allow-snippet-annotations`.
If your app needs something the mapping cannot express, that case is worth
filing rather than working around.

## Ingress and access

```toml
[ingress]
class = "nginx"

[ingress.auth]
kind = "oidc"
auth_proxy_host = "auth.apps.example.com"
discovery_url = "https://login.example.com/.well-known/openid-configuration"
client_id = "astrolift-edge"

[ingress.access]
groups = ["clinical-ops"]
users = ["oncall@example.com"]
```

### `[ingress]` and `[ingress.auth]`

The login gate is a cluster setting, not an app setting. A cluster on the ALB
ingress class gates with Cognito; other classes gate with the cluster's central
OIDC proxy. Every managed hostname on a gated cluster sits behind the gate.

`[ingress]` and `[ingress.auth]` are the platform's record of that gate in the
app's repo. When an operator changes a cluster's gate, Astrolift AI writes the
matching table into the manifest of every app on that cluster, as a commit on
the deploy branch, or as a pull request when the branch is protected. The
renderer reads the cluster's configuration, not this table.

| `kind` | Fields |
|---|---|
| `cognito` | `user_pool_arn`, `user_pool_client_id`, `user_pool_domain` |
| `oidc` | `auth_proxy_host`, `discovery_url`, `client_id`, optional `logout_url` |

`[ingress]` carries `class`, the cluster's ingress class. The OIDC cookie secret
is never written to the manifest.

Editing or removing `[ingress.auth]` does not change the gate. An app whose
manifest has no `[ingress.auth]` is still gated while its cluster is, and the
platform writes the table back the next time the cluster's gate changes. To
take an app off the gate, change the cluster, not the manifest.

### `[ingress.access]`

`[ingress.access]` narrows who may enter once signed in. `groups` are identity
provider groups, `users` are email addresses. A signed-in user outside both
lists gets a no-access page, never the app. Empty lists restrict nobody.

The rule is enforced on clusters using the Envoy edge. On any other edge the
app is gated by login only, and `astro app access show` says the rule is not
enforced yet.

`astro app access` manages the same rule from the CLI:

```bash
astro app access show my-app
astro app access allow my-app --group clinical-ops
astro app access deny my-app --user former@example.com
astro app access clear my-app
```

| Manifest state | Effect |
|---|---|
| `[ingress.access]` declared | The manifest's lists replace the rule on every sync. `show` reports it as set by the manifest; `allow`, `deny` and `clear` are refused |
| Removed from a manifest that declared it | The rule is cleared on the next sync: every signed-in user may enter. `astro app access` can set a rule again |
| Never declared | A rule set with `astro app access` or the dashboard is left alone |

`allow` on an open app restricts it to what you name. `allow` and `deny`
preview who would lose access and ask before saving; pass `--yes` to skip the
question.

## Containers

`[[workloads.containers]]` supports `name`, `is_primary`, `image_ref`,
`dockerfile_path` (default `Dockerfile`), `build_context` (default `.`), `port`,
`command`, `args`, and `[workloads.containers.env]`.

Health checks are structured:

```toml
[workloads.containers.healthcheck]
kind = "http" # none | http | tcp | exec
value = "/health"
port = 8080
```

When no container is explicitly primary, normalization selects the first. An
explicit primary is clearer. A portless worker does not receive a fabricated
HTTP probe.

## Jobs and tasks shorthand

```toml
[[jobs]]
name = "nightly-report"
schedule = "0 3 * * *"
concurrency_policy = "forbid"
dockerfile_path = "Dockerfile.jobs"
command = ["python", "-m", "jobs.report"]

  [jobs.env]
  REPORT_MODE = "full"

[[tasks]]
name = "reindex"
dockerfile_path = "Dockerfile.jobs"
command = ["python", "-m", "jobs.reindex"]
```

`[[jobs]]` becomes a single-container cronjob. `[[tasks]]` becomes a
single-container one-shot Job and has no schedule.

## Volumes

```toml
[[workloads.volumes]]
name = "data"
kind = "pvc" # pvc | empty_dir | config_map | secret
mount_path = "/data"
size = "20Gi"
storage_class = ""
access_mode = "ReadWriteOnce"
performance_tier = "balanced"
durability = "zonal"
```

PVC volumes require `size`. `config_map` and `secret` require `source_name`.
`empty_dir` may set `size_limit`. Paths must be absolute mount paths.

## Managed services

```toml
[[managed_services]]
kind = "postgres"
name = "primary"
variant = "rds"

  [managed_services.config]
  version = "17"
  size = "small"
```

Choose a non-empty variant advertised by the target cluster catalogue; `rds`
is an example and requires a configured AWS provider.

Declaring a service provisions it. When the manifest is applied (at
`astro app register`, and on every later sync from the repo), each entry is
reconciled into a managed service and a provisioning run starts. The parser
itself provisions nothing; the reconcile that follows it does, once the app has
an environment bound to a cluster. Provisioning is asynchronous, so a
registration can return before the database exists.

| Field | Default | Meaning |
|---|---|---|
| `kind` | required | Service kind from the cluster catalogue, such as `postgres` or `redis` |
| `name` | the kind | Name, unique per kind and environment |
| `variant` | catalogue default | Provider variant, such as `rds` |
| `owner_scope` | `app` | `app`: the app owns the service. `project`: a shared project resource the app attaches to |
| `environment` | `production` | App environment the service belongs to. Registration creates it; a later sync refuses an environment that does not exist |
| `bind_workloads` | `["*"]` | Workloads that receive the binding |
| `size` | `small` | `small`, `medium`, `large`, `xlarge` or `custom` |
| `deletion_policy` | `retain` | `delete` also requires `confirm_delete = true` |

With `owner_scope = "app"` the service belongs to this app and environment, and
appears under `astro app services`. Removing the entry from the manifest
deprovisions it; provider data is deleted only under `deletion_policy =
"delete"`. The manifest cannot take over a service of the same kind and name
that was created outside it; the sync fails instead.

With `owner_scope = "project"` the entry creates the project resource if the
project has none of that kind and name, then attaches this app environment to
it. An existing project resource must match the entry's variant and
configuration; an app manifest cannot change a shared resource. Creating or
attaching a project resource this way needs `project.update` on the project,
the same permission `astro project resources attach` needs. Without it, a
`--manifest-raw` registration is refused, and other registrations and syncs
leave those changes out. Removing the entry detaches the app environment; the
project resource stays. See [Shared services](../guides/shared-services.md) for
the CLI path.

Bound services reach workloads through stable environment envelopes such as
`DATABASE_URL` and `REDIS_URL`. Do not put credentials in
`[workloads.containers.env]`; use secret bundles, managed-service bindings, or
agent secret references.

## Kind-specific fields

Agent workload fields are `run_family = "task" | "service"`, `max_retries`
(default 5), `tool_timeout_seconds` (300), and `result_ttl_hours` (72).

A workflow-worker workload requires `workflow_type` and `task_queue`; it also
accepts `temporal_namespace`, `max_concurrent_activities`, and
`max_concurrent_workflows`.

A Knative `function` accepts `min_scale`, `max_scale`, `concurrency`, and
`timeout_seconds`.

A `static_site` has no containers. With platform build mode, set both
`static_build_command` and `static_output_dir`; optional fields are
`static_spa` and `static_index`.

A `faas` has no containers. `faas_package_type = "image"` must not set runtime
or handler. `"zip"` requires `faas_runtime`, `faas_handler`, and
`faas_output_dir`. Other fields include `faas_memory_mb`,
`faas_timeout_seconds`, `faas_architecture`, `faas_public`, and
`faas_build_command`.

## Monorepos

Astrolift recognizes only the bounded declarative layouts:

```text
apps/<slug>/astrolift.toml
agents/<slug>/astrolift.toml
workflows/**/*.toml
```

Agent package `[package]` rules create a separate chrooted source slice. Use
explicit `--manifest-path` selections to register only part of a large repo.

The image build context is an app setting, and its default depends on how the
app was registered. Whole-repo discovery sets it to the manifest's directory.
`astro app register --manifest-path apps/web/astrolift.toml` leaves it at the
repository root unless you also pass `--build-context apps/web`. A container's
own `build_context` and `dockerfile_path` are read relative to the manifest's
directory, and apply only while the app-level value is still the default. See
[Build a monorepo app in CI](../guides/monorepo-ci.md).

## Validate and render

```bash
astro ci render --app orders-api
```

The target control plane is authoritative because provider capabilities and
organization defaults participate in normalization and rendering. TOML syntax
validation alone cannot prove a deployment is portable to a selected cluster.
