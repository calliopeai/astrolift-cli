---
name: astrolift-platform
description: The capability map of the Astrolift platform an app runs on — workload kinds, delivery, scheduling, secrets, managed services, observability, agents/workflows, tokens, and RBAC. Read before designing anything that touches deployment, infrastructure, scheduling, or integrations, so you build WITH the platform instead of around it.
version: "0.1.1"
---

# Astrolift platform capabilities

Apps on Astrolift are declared, deployed, and operated by the platform. The install's own Docs section (sidebar of the platform UI) is the authoritative reference; this is the map of what exists so you reach for the right primitive instead of hand-rolling infrastructure.

**Golden rule: if it feels like infrastructure, the platform probably already does it.** Check here (and platform Docs) before adding a CI cron, hand-rolled scheduler, or external service.

## Apps & the manifest (`astrolift.toml`)

Every app is declared by `astrolift.toml` at the repo root. It defines **workloads** — each one deployed and scaled by the platform:

| Kind | Use for |
|---|---|
| `deployment` | Long-running HTTP services |
| `statefulset` | Services needing stable identity/storage |
| `job` | One-shot batch work, runs to completion |
| `cronjob` | Scheduled work — requires `schedule` (5-field cron); `concurrency_policy` = forbid / queue / replace |
| `task` | Short-lived one-off task (job sugar) |
| `agent` | An AI agent run (once / loop / schedule / trigger run modes) |
| `workflow` | Durable multi-step orchestration (Temporal-backed) |
| `function` / `faas` | Scale-to-zero functions (in-cluster or provider-managed) |
| `static_site` | Static assets served from object storage + CDN (no containers) |

Per workload: containers, `port`, `env` (plain key/values), healthchecks (`http`/`tcp`/`exec`/`none`), resources, hostnames. The platform validates the manifest on every deploy and fails loudly with the offending field path.

## Delivery

- **Deploy-on-merge**: push to the deploy branch → the managed CI workflow builds the image → notifies the platform → rollout. The app's Deployments tab shows every rollout with commit SHA, status, and **one-click rollback**.
- **Approvals**: environments can require N approvals before a rollout proceeds.
- **Preview environments**: PR-scoped ephemeral deploys are a platform feature.
- **Managed CI files + `ASTROLIFT_*` repo secrets are platform-managed** — never hand-edit; re-sync/rotate from app Settings → CI setup.

## Scheduling

Declare a `cronjob` workload (see the `schedule-tasks` skill). The platform runs it in-cluster, tracks runs under the app's Jobs, and supports a manual **Run once** from the UI. No external scheduler needed.

## Secrets & environment

App secrets are stored encrypted by the platform and injected into workloads as env vars — see the `app-secrets-and-env` skill. Plain (non-secret) config goes in the manifest's `env` table. Nothing sensitive ever goes in git.

## Managed services

The platform provisions databases, caches, and object-storage buckets bound to an app (credentials injected as secrets), plus workload identity for cloud-API access — "this app needs a Postgres / a bucket" is a platform request, not new Terraform.

## Networking & domains

Hostnames come from the manifest; DNS + TLS are automatic under the install's managed domain. Custom domains are a Settings-level feature.

## Observability and measurement scope

Inspect the API's per-signal scope, source, identity basis and availability before using a panel as evidence. Namespace ingress/ALB traffic and resource aggregates describe an app environment; they can include other namespace resources and do not establish one workload's usage. A workload request never borrows environment totals. Missing samples, source access, collectors or limits are unavailable, not healthy zero.

Exact workload request/error/latency metrics need application instrumentation carrying canonical app/environment/workload GUID labels and verified runtime membership. Physical CPU/memory measurements need current pod/controller GUID labels, verified container identities, cAdvisor usage series and matching UID-bound limits from kube-state-metrics. Existing unstamped pods need a normal redeploy; a Prometheus endpoint alone does not provide every panel. A valid CPU signal does not prove request instrumentation or application health.

For setup and scope limits, use `astro docs show workload-signals` in a CLI release containing that topic, or https://astrolift.dev/guides/workload-signals/. Check the selected server's schema and actual deployment/collector configuration. Legacy service exporter/resource-gauge APIs are separate contracts; do not describe them as verified physical workload measurements. Uptime probes, alerts and logs remain separate evidence.

## Agents & workflows

The platform hosts AI agents (one-shot, looping, scheduled, or webhook-triggered) and durable workflows with run history — automation like "act when new data arrives" is an agent/workflow workload, not a bespoke service.

## Events, webhooks & tokens

- Inbound SCM webhooks drive deploy-on-push (wired by app autowire).
- Deploys, rollouts, and alerts are recorded per app (Events/Audit).
- **Deploy tokens** (CI) and **API tokens** (programmatic access) are minted per app/user in Settings — scoped, rotatable, never shared secrets in git.

## People & access

Org → Teams → Projects → Apps, with role-based access at each scope (viewer / developer / deployer / approver / admin). Adding a collaborator = a members grant at the right scope, done from the app/team page.

## Where to act

- **Web UI first** — the app page covers deploys, logs, metrics, secrets, settings, members.
- **`astro` CLI** for terminal workflows — see the `astro-cli` skill.
- Anything the UI doesn't expose yet: ask your platform operator rather than working around the platform.
