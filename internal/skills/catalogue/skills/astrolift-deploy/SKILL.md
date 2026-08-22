---
name: astrolift-deploy
description: Check, debug, or explain an Astrolift app's deploy — CI run status, what each step failure means, rollout state, logs, and rollback. Use after merging, or whenever "the deploy failed" or the live site looks stale.
version: "0.1.0"
---

# Astrolift deploy status & debugging

Deploys happen automatically when commits land on the app's deploy branch: the managed `astrolift deploy` workflow builds the image, pushes it to the registry, and notifies Astrolift, which rolls it out.

## Quick checks

```bash
# Latest deploy runs (only deploy-branch pushes trigger them — PRs show no checks)
gh run list --workflow astrolift-ci.yml --limit 5

# Watch the current one / inspect a failure
gh run watch <run-id>
gh run view <run-id> --log-failed
```

Rollout + history live on the platform: app page → Deployments tab (status per commit, one-click rollback). The Observability tab shows live traffic/errors/latency after the rollout.

## Reading a failure by step

| Failing step | Meaning | Fix |
|---|---|---|
| Configure AWS credentials (OIDC) | The workflow file's role is missing/stale | Re-sync from the platform (app Settings → CI setup → Sync workflow file). Never hand-edit the file. |
| Build and push image | The image build broke | Read the build log; re-runs on already-built commits skip the build automatically. |
| Notify Astrolift | The platform rejected the deploy — the response body in the log says why (token scope, environment, manifest) | Push & rotate secrets from app Settings, fix what the body names, re-run. |

## Rules

- The managed workflow file and the `ASTROLIFT_*` repo secrets are platform-managed — re-sync/rotate from the app's Settings instead of editing them.
- A green run = image built AND the platform accepted the deploy; the Deployments tab then shows the rollout completing.
- If the live site looks stale after a green run, check the Deployments tab for the rollout state before assuming CI lied.
