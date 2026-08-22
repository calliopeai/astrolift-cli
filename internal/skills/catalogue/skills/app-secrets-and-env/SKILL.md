---
name: app-secrets-and-env
description: Store credentials (API tokens, passwords) securely on Astrolift and wire configuration into workloads — secrets injected as env vars, plain config in the manifest. Use whenever an app needs to talk to an external service or hold any key.
version: "0.1.0"
---

# Secrets & environment configuration

Two layers, one rule: **nothing sensitive ever goes in git.**

## Plain configuration (not secret)

Lives in `astrolift.toml` under the workload:

```toml
  [workloads.env]
  DATA_WINDOW_WEEKS = "4"
  FEATURE_FLAG_X = "true"
```

Deployed like any code change; visible in the repo — so only non-sensitive values.

## Secrets (API keys, tokens, passwords)

Stored by the platform, encrypted at rest, synced into the cluster, and injected into the app's workloads as environment variables. Managed on the app page → **Settings → Secrets** (create / update / rotate; values are write-only after saving).

Example — connecting to an external service (e.g. Jira):
1. Obtain the API token (your platform operator can provision service accounts).
2. App page → Settings → Secrets → add `JIRA_API_TOKEN` (base URL / account email can be plain env if non-sensitive).
3. The workload reads `os.environ["JIRA_API_TOKEN"]` — nothing in the repo, nothing on a laptop.
4. Rotation = update the value in Settings; the next rollout picks it up.

## What NOT to touch

- The `ASTROLIFT_*` repo secrets on the source host are the platform's own CI credentials — managed by Settings → CI setup → Push & rotate, never by hand.
- Never commit a key "temporarily", never bake one into the image, never echo one into logs.

## Access from outside the app

For scripts/agents that call the platform API itself: mint a scoped **API token** (Settings → Tokens) rather than borrowing a person's session.
