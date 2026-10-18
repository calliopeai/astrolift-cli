# Read task usage and cost provenance

Require `agents.task_usage_snapshot` in the installation's capabilities before
querying usage. Use your own credential with current `app.read` and `agent.read`
on the exact task. Organization membership, token scopes and team limits still
apply. A previous successful read does not preserve access after revocation.

Save this query as `task-usage.graphql`:

```graphql
query TaskUsage($id: ID!) {
  agentTaskUsageSnapshot(id: $id) {
    taskId taskVersion taskStatus asOf aggregationScope
    linkedRunId linkedRunRetryCount
    inputTokens { value unit source sourceRecordedAt completeness reason }
    outputTokens { value unit source sourceRecordedAt completeness reason }
    duration { value unit source sourceRecordedAt completeness reason }
    reportedCost { amount currency source sourceRecordedAt completeness reconciliation reason }
    actualCost { amount currency source sourceRecordedAt completeness reconciliation reason }
    model modelSource modelSourceRecordedAt
  }
}
```

```bash
astro api graphql --file task-usage.graphql --var id=<task-guid> --json
```

Check GraphQL `errors` before using the response. A null snapshot does not mean
zero usage. This is a root query for one task; task-list items have no nested
usage field.

## Interpret the observations

`asOf` is the server read time. `taskVersion` and `taskStatus` come from the same
task row as the snapshot. `sourceRecordedAt` identifies a known source-record
time. Completion reports have no independently recorded report timestamp, so
their timestamp stays null. An unrelated update time is never substituted.

All decimal values, including integer token counts, are **strings**. Keep them as
strings or use decimal and big-integer libraries. JavaScript numbers cannot
represent every supported count. Transport precision cannot recover precision
already lost when an incoming JSON cost was normalized to a float.

| Completeness | Meaning |
|---|---|
| `REPORTED` | The source reported a value; it is not independently verified billing. |
| `OBSERVED` | Platform timestamps establish a terminal task's duration. |
| `PARTIAL` | A linked-run meter cannot be established as a coherent post-terminal observation. |
| `UNKNOWN` | No usable observation is available; inspect `reason`. |

Token units are `TOKENS`; duration uses `MILLISECONDS`. Duration requires coherent
recorded start/end times on a terminal task. It is neither a live clock nor billed
runtime. Missing, malformed, negative and overflowed observations remain null.
An explicitly reported zero remains zero.

Aggregation is `EXACT_TASK_PREFER_COMPLETION_THEN_LINKED_RUN_NO_SUM`. Each valid
terminal completion token field takes precedence over the exact linked run's
meter. The two sources are never added together. Linked-run identity must still
match the live agent and organization. Its retry count is metadata, not a count
of independently metered attempts. Other task GUIDs are not included.

## Reported cost and actual billed cost

`reportedCost` is the runner's normalized `total_cost_usd`, with currency `USD`,
source `COMPLETION_REPORT` and reconciliation `NOT_RECONCILED`. A valid reported
amount can still differ from the provider's eventual invoice.

There is currently no exact-task billing source. `actualCost` therefore returns
null amount, currency, source and timestamp, with `UNKNOWN` completeness,
`NO_SOURCE` reconciliation and reason `NO_TASK_BILLING_SOURCE`. Display it as
unavailable. App or organization estimates, token rates and a zero amount must
not fill that gap.

Use [completion callbacks](agent-completion-callbacks.md) to receive a signed
final-task event, and this query to inspect the exact task's recorded provenance.
