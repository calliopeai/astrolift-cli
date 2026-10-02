# Signed agent completion callbacks

Register a callback when dispatching an agent task. Astrolift persists the final
event and delivers it from its production network. A receiver outage does not
require your service to keep polling every task.

This contract applies to `runAstroliftAgent` tasks. It is separate from the
pod-to-platform `AGENT_CALLBACK_URL` and generic application webhook envelopes.
It does not send progress or workflow-level events.

## Configure the organization

Use a CLI release that includes `astro agent callbacks` and a server whose
schema includes `configureAgentTaskCallbacks`. The server advertises
`agents.completion_callbacks` and `agents.completion_callback_redelivery`. Inspect `astro version`, `astro
status --json`, and `astro agent callbacks --help` before automating the setup.

```bash
astro agent callbacks configure --allow-host hooks.internal.example.org
astro agent callbacks show --json
astro agent callbacks secret-set completion-key --file /secure/path/completion-key
```

`configure` replaces the allow-list. Repeat `--allow-host` for additional hosts.
Exact hosts and `*.internal.example.org` are supported; the wildcard matches
subdomains, not the bare parent domain. `--clear` disables all allowed hosts.
The destination must be HTTPS. URL credentials and fragments are rejected;
redirects cannot bypass the allow-list. Internal destinations must resolve and
be reachable from the production network, with a valid TLS certificate.

Configuring hosts requires organization update permission. Writing the signing
key requires organization secret-write permission and the corresponding token
scope. Dispatching with a secret reference requires the normal agent dispatch
grant plus organization secret-read permission and token scope `secret:read`.
Team-limited tokens cannot configure organization callbacks or dispatch with
an organization signing secret. Secret names start with an ASCII letter or digit,
use only ASCII letters, digits, `_`, `.`, and `-`, and are at most
128 characters. Keys contain 32 to 4096 UTF-8 bytes; use a cryptographically
random key. FULL dispatch also requires app-read authorization for the target
app; NOTIFY does not add that result-read requirement. The signing key itself is not part
of dispatch input or its response.
Use `--stdin` instead of `--file` when a secret manager supplies the value. The
CLI offers no literal signing-key argument and never echoes its value. It strips
trailing CR/LF from text input; configure the receiver with the same key bytes.

## Register at dispatch

```bash
astro agent dispatch report --env-spec report-prod --input @input.json \
  --callback-url https://hooks.internal.example.org/tasks \
  --callback-secret-ref completion-key \
  --correlation-id request-42 --callback-mode FULL --json
```

| Input field | Meaning |
|---|---|
| `callbackUrl` | Allowed HTTPS receiver URL |
| `callbackSecretRef` | Organization secret name storing the HMAC key |
| `correlationId` | Opaque caller ID, at most 128 characters; returned unchanged |
| `callbackMode` | `FULL` includes result JSON; `NOTIFY` omits it; default `FULL` |

Callback options are optional. A dispatch without them keeps its existing
behavior. The server rejects disallowed destinations before launching the task.
`--wait` waits for task execution; callback delivery has its own state and can
remain pending while the receiver is unavailable.

The equivalent GraphQL operation is:

```graphql
mutation Dispatch($input: RunAstroliftAgentInput!) {
  runAstroliftAgent(input: $input) {
    ok
    errors { code message field }
    data { id status callbackStatus callbackAttempts callbackLastError }
  }
}
```

```json
{
  "input": {
    "agentSlug": "report",
    "callbackUrl": "https://hooks.internal.example.org/tasks",
    "callbackSecretRef": "completion-key",
    "correlationId": "request-42",
    "callbackMode": "FULL",
    "triggerPayload": {"request_id": "request-42"}
  }
}
```

Check both GraphQL `errors` and mutation `ok`. For authentication, tenancy, and
HTTP transport see the [API reference](../reference/api.md).

## Final event and delivery attempts

Astrolift creates one logical `agent_task.finished` event when a task reaches
`completed`, `failed`, `cancelled`, or `timed_out`, including platform-enforced
timeouts. Intermediate states and internal execution retries do not notify the
caller. Only the final outcome generates an event.

Delivery is **at least once**. A lost success response can cause another POST.
Atomically deduplicate by `task_id` before changing downstream state. A fresh
`X-Astrolift-Delivery` UUID identifies every HTTP attempt, including replay.
Do not deduplicate only by that attempt UUID.

```http
Content-Type: application/json
X-Astrolift-Event: agent_task.finished
X-Astrolift-Delivery: <fresh-uuid-per-attempt>
X-Astrolift-Timestamp: <unix-seconds>
X-Astrolift-Signature: v1=<lowercase-hex-hmac-sha256>
```

```json
{
  "event": "agent_task.finished",
  "task_id": "00000000-0000-4000-8000-000000000042",
  "correlation_id": "request-42",
  "agent_slug": "report",
  "status": "completed",
  "exit_code": 0,
  "failure_message": null,
  "attempt": 1,
  "started_at": "2026-10-01T19:02:11Z",
  "finished_at": "2026-10-01T19:04:37Z",
  "usage": {
    "duration_ms": 146000,
    "input_tokens": null,
    "output_tokens": null,
    "total_cost_usd": null,
    "model": null
  },
  "result": {"output": {"summary": "Processing completed"}}
}
```

FULL uses the stored task result wrapper; agent output commonly lives under
`result.output`. A failed task without a normal result can return its recorded
failure output in `result`.

`attempt` is the final agent execution attempt, separate from HTTP delivery
attempts. Usage values that were not observed are `null`; they are not reported
as zero. Duration uses the actual task timing. A task that failed before
starting can have a null `started_at` and duration. In `NOTIFY`, `result` is absent
and free-text failure content is not sent; fetch the result through
`agentTask(id)` using an authorized API client.

## Receiver outage and backoff

Each POST has a **10-second timeout**. The event survives worker restarts and a
receiver outage. Automatic retries span **at least 24 elapsed hours**, using
exponential backoff and jitter: nominally 30 seconds, 2 minutes, 10 minutes,
30 minutes, then approximately hourly. Exact timing varies with jitter and
worker availability. A new timestamp, signature, and delivery UUID are created
for every actual attempt. The retry window begins when the final event is
recorded, rather than when the agent was dispatched.

| Outcome | Delivery action |
|---|---|
| Any `2xx` | Mark delivered; stop retries |
| `4xx` except `408` and `429` | Mark failed; do not retry automatically |
| `408`, `429`, `5xx`, transport failure, or timeout | Retry with backoff |
| Retry window exhausted | Mark failed; allow authorized manual replay |

Return a `2xx` after durably accepting the event, and process it asynchronously
if the downstream work might take longer than the timeout. Return `503` or
`429` for temporary unavailability; `400` declares a permanent problem.

```bash
astro agent inspect <task-guid> --json
astro agent callbacks redeliver <task-guid> --json
```

`agentTask` exposes `callbackStatus` (`pending`, `delivered`, or `failed`),
`callbackAttempts`, and a sanitized `callbackLastError`. No-callback tasks have
null callback state. Pending can include a registered task that has not yet
finished. The replay mutation is `redeliverAgentTaskCallback(taskId: GUID!)`.
It resends the final event without rerunning the agent and requires task-scoped
agent-dispatch and app-read authorization, plus their token scope ceilings.
Replay is subject to task-result access and retention; it does not resurrect a
deleted or expired result. It starts a new retry window and resets the attempt
count for that delivery cycle. An already pending delivery continues its
automatic retry cycle; replay is available after delivery settles.

## Verify the signature before parsing

The signed input is `ASCII(timestamp) + b"." + raw_body`. Use the exact bytes
received, not reserialized JSON. Receivers reject timestamps more than five
minutes old or in the future beyond that window, then compare in constant time.

```python
import hashlib
import hmac
import re
import time


def verify_agent_completion(*, keys: list[str], raw_body: bytes,
                            timestamp_header: str, signature_header: str,
                            now_unix: int | None = None) -> bool:
    if not isinstance(timestamp_header, str) or not isinstance(signature_header, str):
        return False
    if not re.fullmatch(r"[0-9]{1,12}", timestamp_header):
        return False
    if not re.fullmatch(r"v1=[0-9a-f]{64}", signature_header):
        return False
    timestamp = int(timestamp_header)
    now = int(time.time()) if now_unix is None else now_unix
    if abs(now - timestamp) > 300:
        return False
    message = timestamp_header.encode("ascii") + b"." + raw_body
    for key in keys:
        if not isinstance(key, str) or not key:
            continue
        expected = "v1=" + hmac.new(key.encode("utf-8"), message,
                                   hashlib.sha256).hexdigest()
        if hmac.compare_digest(expected, signature_header):
            return True
    return False
```

Read the request's raw bytes before JSON decoding. After verifying, require the
expected event type, parse the body, and apply the `task_id` deduplication rule.
A valid signature proves possession of the configured key, not authorization to
modify an unrelated downstream object; validate the correlation ID locally.

## Rotate without interrupting delivery

1. Configure the receiver to accept both the previous and new signing keys.
2. Replace the same named organization secret with the new key through
   `astro agent callbacks secret-set completion-key --file <new-key-file>`.
3. Confirm a fresh delivery verifies with the new key before removing the old
   key from the receiver after its overlap window.

Astrolift reads the named secret for every attempt, so queued events use the
current key without storing a plaintext key in the queue. Accepting both keys
at the receiver covers in-flight requests during rotation. Retry signatures
use a fresh timestamp; the previous event timestamp is not reused.

## Sensitive data and retention

FULL results can contain sensitive data. Callback request and response bodies
are excluded from control-plane logs, metrics, and traces. Operational records
contain identifiers, final status, HTTP code, attempt count, and sanitized
error categories. A queued FULL body is encrypted and has access no broader
than the task result. It is removed after delivery or retry exhaustion. FULL
delivery also stops and
purges queued data when the source task or linked run is deleted, the result is
erased, or the source result retention expires. The default retention is
72 hours; a shorter configured result lifetime bounds FULL delivery even when
the normal retry window has not elapsed. Use NOTIFY to retain outage retries
without keeping a short-lived result in the webhook path.

Choose NOTIFY when the webhook path should carry status and identifiers rather
than the result or free-text error. Correlation IDs must be opaque identifiers;
do not put sensitive descriptions into them. Receivers should apply equivalent
body logging and retention controls.
