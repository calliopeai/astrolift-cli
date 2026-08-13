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
variant = ""

  [managed_services.config]
  version = "17"
  size = "small"
```

Managed-service values are provisioned outside the manifest parser and exposed
through stable environment envelopes such as `DATABASE_URL` and `REDIS_URL`.
Do not put credentials in `[workloads.containers.env]`; use secret bundles,
managed-service bindings, or agent secret references.

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

An app manifest's build context starts at its manifest directory. Agent package
`[package]` rules create a separate chrooted source slice. Use explicit
`--manifest-path` selections to register only part of a large repo.

## Validate and render

```bash
astro ci render --app orders-api
```

The target control plane is authoritative because provider capabilities and
organization defaults participate in normalization and rendering. TOML syntax
validation alone cannot prove a deployment is portable to a selected cluster.
