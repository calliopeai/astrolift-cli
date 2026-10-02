# Review and act on an exact app environment

Use environment GUIDs for workload controls and shell attachment. Find them with
`astro api graphql` against `astroliftEnvironments(appSlug: "<app>") { id name }`.
An environment name is a display label, not an immutable target identifier.

```sh
astro api graphql --query 'query { astroliftEnvironments(appSlug: "api") { id name clusterSlug } }'
```

## Restart or scale

Save a review before making a change:

```sh
astro app workload review web --app api --environment <environment-GUID> > review.json
astro app workload scale web 2 --app api --environment <environment-GUID> --review review.json --yes
astro app workload review web --app api --environment <environment-GUID> > review.json
astro app workload restart web --app api --environment <environment-GUID> --review review.json --yes
```

Review output is JSON even without `--json`. It includes workload/app/environment/
cluster GUIDs and versions, namespace, advisory permissions, selected server,
organization and current actor. Keep the file local; it contains installation
identifiers and no credentials. Execution requires the same server, organization,
actor, app/workload selectors and environment GUID. The CLI checks current
selector identities and sends every saved version and mapping precondition
unchanged. It does not refresh a stale review automatically.

The API rechecks current authority and target under its locks. A stale, replaced,
deleted, denied or unavailable target is refused. Schema and transport failures
do not select the primary environment or retry a write. `--yes` records local
confirmation; it is not a server approval, attestation or action-admission proof.
Without `--yes`, an interactive prompt displays the environment GUID, cluster
GUID and namespace. `--no-prompt` still requires `--yes`.

Scale accepts zero through 20 replicas; the selected environment may impose a
lower limit. A successful JSON receipt includes the operation GUID, exact target,
new workload version, `accepted: true` and `completed: null`. This means Kubernetes
accepted a patch, not that rollout completed. A transport error can leave the
effect unknown. Inspect the current workload before reviewing and repeating a
write. A saved review is not an idempotency key or a promise of safe repetition.

## Open a shell

```sh
astro exec --app api --environment <environment-GUID> --workload web -- sh
astro app exec web --app api --environment <environment-GUID> --pod web-abc -c main -- sh
printf 'select 1;\n' | astro exec --app api --environment <environment-GUID> --no-tty -- psql
```

With all of `--workload`, `--pod` and `--container`, the CLI requests the exact
`astroliftAppExecTarget` directly using current `app.exec_pod` authority. Otherwise
it reads the selected environment's inventory to fill in missing selectors;
inventory reads require their normal read permissions. It displays the reviewed
immutable app/workload/environment/cluster identities, namespace, pod UID and
container on stderr. `--pod`, `--workload` and `--container` constrain that same
environment; missing selections are refused without fallback. An explicit
environment requires this API contract; an older or unauthorized server fails
closed.

The WebSocket handshake uses the bearer Authorization header and selected
organization header. Credentials never appear in the URL or review. Opening
sends the complete target and versions. Input, resize and interrupt forwarding
start only after the server echoes that exact target and a connection session ID
with `resumable: false`, `disconnect: "END"`, and `inputReplay: false`.
The CLI leaves piped input in its source until admission; it keeps no application
queue for a later connection. Subsequent control frames carry the session ID. The current server rechecks
credential/grant/mapping authority before forwarding input.

Disconnect ends the transport session. It does not guarantee that every process
started by the shell has exited. A connection lost before an exit receipt is an
operational error: the last input's outcome may be unknown. There is no automatic
reconnection, buffered-input replay or durable resume. Start another command
explicitly after inspecting state. An explicit new attach creates a new session.
Without `--environment`, existing exec callers keep the API's legacy primary
environment behavior; those calls do not acquire explicit-target guarantees.

The pod UID check is **preflight only**. Kubernetes exec addresses a pod by name,
so recreation between the check and opening remains possible. This contract does
not supply atomic pod binding, mobile-audience credentials, action-admission
proofs or durable audit before opening. Server policy still applies; local
confirmation does not replace it. App shell permission remains `app.exec_pod`;
agent-box attachment uses its separate permission and `astro box attach` command.
