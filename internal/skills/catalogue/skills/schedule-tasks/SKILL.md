---
name: schedule-tasks
description: Schedule recurring or one-off work on Astrolift — "run this every X hours" or "at a certain time" — by declaring a cronjob workload in astrolift.toml. Use instead of CI cron or any external scheduler.
version: "0.1.0"
---

# Scheduling tasks on Astrolift

Recurring work is a **`cronjob` workload** in `astrolift.toml`. The platform runs it in-cluster on the schedule, records every run under the app's Jobs, and lets you trigger it manually ("Run once") from the UI.

## Declaring one

```toml
[[workloads]]
name = "nightly-sync"
kind = "cronjob"
schedule = "0 6 * * 1"          # 5-field cron, UTC: 06:00 every Monday
concurrency_policy = "forbid"    # forbid | queue | replace overlapping runs

  [[workloads.containers]]
  name = "sync"
  is_primary = true
  # entrypoint comes from the app image; the container should do the
  # work and exit 0 (non-zero marks the run failed).

  [workloads.env]
  SOME_SETTING = "value"
```

- `schedule` is required for cronjobs — manifest validation rejects it otherwise.
- Every-X-hours: `0 */6 * * *` (every 6h). At-a-time: `30 14 * * *` (14:30 UTC daily).
- Failures surface on the app page and can alert.

## Operating it

- Deploy the manifest change like any other change (merge to the deploy branch).
- **Run once now**: app page → the job's row → Run once — test before waiting for the schedule.
- Logs and run history: app page → Jobs / Logs.
- Secrets the job needs: see the `app-secrets-and-env` skill — injected as env vars like any workload.

## When a cronjob is not the right shape

- Work triggered by an event (webhook, new data) rather than a clock → an `agent` (trigger run mode) or `workflow` workload.
- Multi-step orchestration with retries and human gates → a `workflow` workload.
