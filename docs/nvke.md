# NVKE management

`astro nvke` is a pure client of Astrolift's organization-owned NVKE sites and
operation API. It does not run upstream binaries or use local management-plane
credentials to bypass the control plane. The server enforces the exact action's
`nvke.read`, `nvke.manage` or `nvke.admin` permission, active membership and bearer
scope. Select the working server and `--org` as for other Astro commands.

## Sites and connections

```bash
astro nvke site create --name "GPU site" --slug gpu-site --namespace org-owned --json
astro nvke site list --limit 50 --after OPAQUE_CURSOR --json
astro nvke site get SITE_GUID --json
astro nvke site configure SITE_GUID datastore --site-version 3 --connection-version 0 \
  --endpoint https://management.example.test --credentials-file credentials.json --json
astro nvke site disable SITE_GUID --version 4 --json
astro nvke site enable SITE_GUID --version 5 --json
astro nvke site remove SITE_GUID --version 6 --json
```

Surfaces are `controller`, `tenant`, `operator` and `datastore`. Configure each
separately. A controller base normally includes `/api`; the datastore base is the
management Kubernetes API server. Tenant/operator bases host their `/v1` routes.
Never assume the upstream PoC tenant API authenticates or isolates callers: its
proxy/deployment must be dedicated to the site's recorded organization namespace.

Configuration requires the current `--site-version`; an existing connection also
requires its current `--connection-version`. A new connection uses version `0`.
Omit `--credentials-file` to preserve credentials. Use `--clear-credentials` to clear
them, or a new private file to rotate them. The credential object accepts:

```json
{"token":"...","caPem":"...","clientCertificatePem":"...","clientKeyPem":"..."}
```

Provide the client certificate and key together. Read credentials from a file with
POSIX mode `0600` or a protected Windows current-user/SYSTEM ACL, or pass
`--credentials-file -` for stdin. Credentials are never flags or response fields.
Connection timeouts use `--connection-timeout` (1–300 seconds). HTTPS is the default;
`--allow-http` explicitly enables an HTTP evaluation endpoint. Removal deletes
only the local registration and refuses outstanding/uncertain operations.

## Complete native API and resource vocabulary

`astro nvke catalog --json` returns the connected server's complete controller
OpenAPI, management-resource schemas and action/permission catalog. Offline help
shows the pinned command names. Each native leaf accepts a complete parameter
object with `--file`, or an empty object when omitted. Aliases preserve upstream
operation IDs, such as `createCluster` and `getNVKECluster`.

| Group | Command families |
|---|---|
| `controller` | Cluster create/get/list/update/delete, control-plane config/status/history, pools, nodes/node sets, releases/config profiles and SPIRE |
| `tenant` | Declarative NVKECluster CRUDL and health/readiness |
| `operator` | Machine images, cluster/node-pool releases, release channels, control-plane classes CRUDL and health/readiness |
| `datastore` | All six management resource kinds CRUDL, guarded release deprecation and workload kubeconfig retrieval |

For example:

```bash
astro nvke controller update-cluster --site SITE_GUID --file upgrade.json \
  --request-file upgrade-recovery.json --wait --json
astro nvke tenant list-nvke-cluster --site SITE_GUID --file page.json \
  --request-file page-recovery.json --wait --json
astro nvke datastore deprecate-cluster-release --site SITE_GUID --file deprecate.json \
  --request-file deprecate-recovery.json --wait --json
```

HTTP parameter objects contain `path`, `query`, `body` and applicable
`resource_version`. For example, an upgrade uses
`{"path":{"clusterID":"..."},"body":{"controlPlaneKubernetesVersion":"1.36.3"}}`.
Datastore parameters contain `name`, `document`, `resource_version`, `uid` and the
operation's declared options. A create uses `{"document":{"metadata":{"name":"..."},"spec":{...}}}`;
a guarded delete uses `{"name":"...","uid":"...","resource_version":"...","propagation_policy":"Background"}`.
Use the exact native schemas; the CLI does not narrow their fields or reinterpret
opaque versions. `query` supports the native pagination/selector fields, and
results retain upstream continuation tokens and future response fields.

`astro nvke operation request ACTION_KEY` exposes the same admission path with an
explicit catalog key (for example `datastore.createMachineImage`). Native shortcuts
refuse a server with a different pinned revision. Inspect the live contract before
using generic requests against a reviewed newer server.

## Recovery, waiting and results

Credential metrics have their own request and recovery commands described below;
they accept a reviewed controller receipt rather than native input JSON.

Every request requires `--site SITE_GUID` and `--request-file recovery.json`.
The CLI reviews the site and connected contract, verifies the current caller, and
exclusively creates and flushes a private recovery file before submitting anything.
The record contains server/org/actor/site/version, action, contract revision, timeout,
parameters SHA-256 and a UUID. It contains no native scripts, input body or bearer.
Treat the digest and metadata as private and retain the original input separately.

After a lost reply, rerun the command with the same recovery file and identical
input. Recovery retains the original version, action, timeout and UUID; it does
not obtain a fresh review. Changed scopes, input or explicit execution flags are
refused. Never replace a recovery record to work around an uncertain outcome.
`--site-version` can assert the first review's version. `--operation-timeout`
sets the server deadline (30–86400 seconds, default 7200).

```bash
astro nvke operation list SITE_GUID --limit 50 --after OPAQUE_CURSOR --json
astro nvke operation get OPERATION_GUID --json
astro nvke operation wait OPERATION_GUID --wait-timeout 20m --poll-interval 5s --json
astro nvke operation cancel OPERATION_GUID --json
astro nvke operation resume OPERATION_GUID --version CURRENT_VERSION --operation-timeout 7200 --json
astro nvke operation result OPERATION_GUID --output private-result.json --json
```

`--wait` waits for observed `SUCCEEDED`, not mere acceptance. `FAILED`, `CANCELLED`
and `UNCERTAIN` produce nonzero exits and final metadata. Unknown statuses do not
establish success. A local timeout or Ctrl-C stops waiting without cancelling or
replaying the server operation. Cancellation before dispatch can stop a write;
after sending, the server continues observing the original effect. It does not
undo provisioning or fabricate an upstream cancellation.

An admin can explicitly resume an uncertain operation with its current version.
The backend requires original authority and a known resource identity and preserves
its saved write-sent state. The CLI never retries by creating a new operation.

Native results can contain kubeconfigs, cloud-init, SPIRE data or provider scripts.
Metadata commands never fetch them. `operation result` requires an explicit new
private output path and never overwrites a file. Its JSON receipt contains only
`{"saved":true}`. Use `--output -` only when intentionally writing the sensitive
JSON result to stdout. `--json` is available on every command.

## Credential metrics and certificate observations

```bash
astro nvke metrics catalog --json
astro nvke metrics get --site SITE_GUID --json
astro nvke metrics configure --site SITE_GUID --site-version SITE_VERSION \
  --authority-version 0 --endpoint https://nvke.example:8080/metrics \
  --credentials-file metrics-credentials.json --json
astro nvke metrics verify --site SITE_GUID --site-version SITE_VERSION \
  --authority-version AUTHORITY_VERSION --json
astro nvke metrics request --site SITE_GUID --site-version SITE_VERSION \
  --authority-version AUTHORITY_VERSION --source-operation GET_CLUSTER_OPERATION_GUID \
  --source-version SOURCE_VERSION --request-file credential-recovery.json --wait --json
astro nvke metrics recover --request-file credential-recovery.json --json
astro nvke metrics observation CREDENTIAL_OPERATION_GUID --site SITE_GUID --version VERSION --json
astro nvke metrics certificates CREDENTIAL_OPERATION_GUID --site SITE_GUID --version VERSION \
  --limit 25 --search cp-1 --json
astro nvke metrics samples CREDENTIAL_OPERATION_GUID --site SITE_GUID --version VERSION \
  --limit 25 --after OPAQUE_CURSOR --json
```

Configure the full metrics listener URL explicitly; the CLI does not infer it from
REST, reuse connection credentials or fetch the listener itself. The default is
HTTPS. `--allow-http` explicitly permits HTTP for an evaluation listener.
`--connection-timeout` bounds a scrape to 1–300 seconds, default 30. Private
credential JSON contains `token`, `caPem`, `clientCertificatePem`, `clientKeyPem`;
certificate and key must be supplied together. Use `--credentials-file -` for
stdin. Omission preserves existing credentials; `--clear-credentials` clears
them. Never put credentials in the URL or command arguments.

`configure`, `enable`, `disable` and `remove` advance site/authority versions and
clear verification. Always use their returned versions for the next change.
`verify` records `METRICS_READ` without advancing either version; readable metrics
do not prove collection success or cluster health. `remove` soft deletes the local
authority. After a lost configuration reply, inspect `metrics get` before reviewing
another change.

Run a successful controller `get-cluster` after the last authority change. A new
`metrics request` requires that source receipt/version, current site/metrics
authority versions, both NVKE admin and read access, and a new private recovery
file. It accepts no caller-supplied cluster name. The server derives the selector
from its encrypted source, then checks the live controller UUID/name and
provisioned/non-deleting state before and after the scrape. The request defaults
to a 600-second observation deadline; `--operation-timeout` accepts 30–86400.
Use ordinary `operation get/wait/cancel` for the returned durable receipt.

The recovery file contains scope, reviewed versions, source receipt, pinned
revision, deadline and request UUID, with no native inputs, metrics URL or bearer.
Reusing `metrics request` checks the original receipt before current authority or
source state. If no receipt is visible, it reuses the identical recorded request;
changed explicit versions, source or deadline are refused. `metrics recover`
never re-admits. Preserve the file after uncertain transport outcomes.

Historical `observation`, `certificates` and `samples` require only NVKE read access
and the exact successful credential operation version. They do not contact the
native listener and remain readable after authority rotation/removal. Certificate
and sample pages search the complete saved observation on the server. Cursors
bind actor, organization, site, operation/version, view, search and page size.
Use `operation result --output PRIVATE_PATH` only when intentionally retrieving
the encrypted raw result with NVKE admin access.

All views preserve scrape time, null unknowns and reported numeric values as
decimal strings, including large CSR counts. Latest inspection availability,
retained certificate NotAfter, observation availability/age, collection success,
monitoring and renewal automation enablement are separate facts. Expiry comparison
is **at scrape time**. A scheduled renewal time is not certificate expiry. A failed
collection or absent series never establishes healthy credentials. Native metrics
label clusters by name rather than UUID; even live controller checks cannot prove
which incarnation produced a retained gauge after name reuse. Local CLI/API/TLS/
Temporal tests do not qualify collector behavior on live NVKE nodes.

## Qualification and maintenance

The 88 API/resource actions are wired through the durable API. Local native tooling
is exposed through `astro nvke tooling` as described below. Paired local source-built
qualification includes seed/node bootstrap, the default management stack, private
operator/tenant APIs and one SDK-driven NICo-simulator tenant with two Ready nodes.
Hosted API/UI/CLI flows against that site, declarative reconciliation, production
artifact/telemetry qualification and physical providers remain broader integration
work. These commands alone do not claim their completion. A controller create whose response/ID is lost
requires operator investigation. OIDC enablement lacks a direct upstream read-back
field, and declared pool readiness counters are not all populated by the pinned
management engine; the backend waits for positive evidence rather than labelling
acceptance as Ready. Live deployment qualification remains required.

Normal builds use the committed offline command vocabulary. To refresh it from an
explicitly selected paired provider, use that provider's Python environment:

```bash
/path/to/app/backend/providers/.venv/bin/python scripts/vendor-nvke-catalog.py \
  --providers /path/to/app/backend/providers --check
```

Omit `--check` to regenerate, then review the revision and all action changes.
The paired integration test checks exact agreement with the server catalog and
executes the compiled CLI against real local services. Run it with the paired
backend's configured pytest environment and conftest plugin:

```bash
ASTROLIFT_NVKE_CLI=/path/to/astro NVKE_TEST_KUBECONFIG=/path/to/local-envtest.yaml \
  python -m pytest -p conftest -c pytest.ini \
  /path/to/cli/scripts/integration/test_nvke_contracts.py --reuse-db
```

The harness refuses non-loopback Kubernetes and never contacts production.

## Native operator toolchain

`astro nvke tooling catalog` returns the offline runner provenance and supported
commands. The runner is embedded at an immutable source commit, with its SHA-256
checked against the CLI's pinned upstream revision. Refresh/check the snapshot with:

```bash
python3 scripts/vendor-nvke-tooling.py --check
python3 scripts/vendor-nvke-tooling.py --source /path/to/opscode \
  --revision FULL_IMMUTABLE_COMMIT
```

`tooling prepare --checkout DIR --output NEW_DIR` builds the native binary from a
pristine archive of the pinned upstream commit. Git and Go are required; Go downloads
the declared toolchain and locked dependencies. The resulting owner-only bundle holds
source/licenses, native CLI, source-tree/binary hashes, CAPI pin and private build log.

`tooling run --bundle DIR --workspace DIR -- NATIVE_COMMAND ...` accepts:

- `bootstrap site` and its full native options, including dry-run.
- `generate sitespec`, `generate oidc-kubeconfig`, `validate sitespec`.
- `inspect sitespec`, `inspect stack-config`, `inspect values`, `inspect nvkr-release`.
- `render deploy-path`, `get bootstrap-manifest`, `version`.
- `management install`, `management check`, `management configure-access`.

Paths in native parameters resolve from the private workspace. Put all native flags
after `--`; put Astro flags (including `--json`) before it. Python 3.12+ is detected
automatically or selected with `--python`. Native operator execution supports Linux
and macOS. Use the managed API commands on every supported CLI platform.
Native stdin is forwarded, including CSV input through `generate sitespec --nodes -`.

Management commands delegate to the pinned installer, site preflight/diagnostics and
CAPI SPIFFE exec-auth scripts. They require an explicit private kubeconfig. The runner
flattens/minifies that context into a per-run snapshot, removes ambient Helm connection
overrides, and refuses credential-changing Helm passthrough. The verified source tree
is copied for each run so renders and chart dependency builds preserve the bundle.
Management installation defaults to the native NICo deploy path, with native release,
architecture, stack config, additional values and custom deploy-path options intact.

Render the production NKTP export override with the embedded, source-pinned renderer:

```bash
astro nvke tooling nktp-backend --bundle /private/native-bundle \
  --profile /private/backend-profile.json --output /private/nktp-backend.values.json
```

The JSON profile selects `protocol` (`grpc` or `http`) and `endpoint` (TLS host:port
for gRPC; HTTPS base URL for HTTP). Optional `authorization` contains only `secret`
and `key` references; that key holds the complete Authorization header. Optional
`tls` contains `secret` and `caKey`, with paired `certKey`/`keyKey` for mutual TLS.
The referenced Secrets must already exist in namespace `nktp`. No credentials are
read by the renderer or accepted inline. Output is a new mode-0600 Helm values file;
stdout contains only its digest and pinned revision. Pass the file to the existing
management installation with `--values-for nktp=/private/nktp-backend.values.json`,
retaining every other site override.

The override disables the evaluation LGTM backend and configures all five outbound
Collectors, including the watchdog sidecar. Source checks reject drift in pinned
chart/site values and Collector composition. Actual chart renders check internal
TLS mounts, receivers/processors, site labels and queue/retry settings for both
protocols. These checks do not establish runtime Collector readiness or ingestion:
validate certificates, all three signals, site identity and backend failure recovery
on the installed site. Management NKTP does not collect workload-cluster-local data.

Render the private management-engine operator/tenant API manifests with:

```bash
astro nvke tooling management-apis --bundle /private/native-bundle \
  --profile /private/management-api-profile.json --output /private/management-apis.json
```

This renderer additionally requires PyYAML in the selected Python 3.12+ runtime.
The profile supplies a cluster-unique `name`, `namespace`, `tenantNamespace`, and
`images.operator`/`images.tenant` immutable repository-at-SHA256 references. Optional
`clients` entries require both `namespace` and nonempty `podLabels`; an empty list
denies pod ingress. The output preserves native workload RBAC and pod security,
binds the tenant API to its namespace and exposes ClusterIP services only. It adds
the three management-engine CRDs; the default stack's three release/image CRDs
must already exist. `imagePullPolicy` optionally selects `Always`, `IfNotPresent`
or `Never` for local preloaded images.

Upstream caller authentication is deferred, so API access must remain private.
Rendering does not apply manifests, install the controller manager, establish
declarative reconciliation or prove image build/release provenance. CLI output
contains only `manifestSourceRevision` and the output `sha256`; the manifest is a
new mode-0600 file. Existing output is never replaced, including after a lost reply.

Render the tenant-scoped management controller with:

```bash
astro nvke tooling management-controller --bundle /private/native-bundle \
  --profile /private/controller-profile.json --output /private/controller.json
```

Its profile requires `name`, runtime `namespace`, `tenantNamespace`, immutable
`image`, `infraTemplateConfigMap`, an internal Service `controllerEndpoint` ending
in `/api`, and explicit `controllerAuth`. Projected authentication uses
`{"mode":"projected","audience":"https://CONFIGURED_CONTROLLER_AUDIENCE"}`;
an isolated controller without caller authentication must explicitly select
`{"mode":"private-network"}` and retain its private access boundary. This does
not configure the controller's verifier or NetworkPolicy. `imagePullPolicy` has
the same options as the API renderer.

All six NVKE CRDs and the reviewed provider-template ConfigMap must already exist.
The ConfigMap belongs in the tenant namespace. The output confines ConfigMap and
NVKECluster permissions to that tenant, retains cluster-wide release reads and
native pod security, enables leader election, disables metrics and denies ingress.
Use a unique name and a separate runtime namespace per installation; the native
leader-election Lease name is fixed. The embedded renderer and its helper module
are both pinned and run in a private directory under isolated Python 3.12+ with
PyYAML. Ambient module paths and the working directory cannot replace the helper.
Output is exclusive and mode 0600; stdout contains only revision and digest.

The paired ops qualification exercised native controller startup and a declarative
local NICo-simulator tenant reaching two Ready nodes, including SDK ownership-checked
workload access. This CLI command renders manifests; hosted installation controls,
physical providers and production image/release qualification remain separate work.

Native stdout/stderr remain private files. CLI stdout contains only run metadata and
artifact paths. Astro API-token environment variables are not forwarded to native
tools. Effectful bootstrap/install/access attempts are flushed before execution and
cannot be blindly repeated in the same workspace. Interruption kills the local process
group and preserves its attempt record; remote outcomes still require investigation.
Process completion is distinct from infrastructure readiness.

The local test evidence includes real native spec utilities and full management-stack
dry-run through a Kubernetes proxy that rejects mutation requests. Actual installation,
verified release supply chain, provider/hardware qualification and access/readiness
observations remain required before the full integration is qualified.

Run the compiled local native qualification (the Kubernetes target must be loopback):

```bash
ASTROLIFT_NVKE_CLI=/path/to/astro \
NVKE_TEST_NATIVE_BUNDLE=/private/native-bundle \
NVKE_TEST_KUBECONFIG=/private/local-test.kubeconfig \
NVKE_TEST_UPSTREAM=/path/to/pinned/nvke \
NVKE_TEST_OPSCODE=/path/to/opscode \
  python3 -m unittest discover -s scripts/integration -p test_nvke_tooling.py -v
```

This drives real bundle preparation, native spec utilities, mutation-rejecting
management dry-run, and SIGINT/recovery behavior through the compiled executable.

The opt-in `scripts/integration/test_nvke_native_site.py` instead drives the
compiled CLI through an owned loopback Astro GraphQL server, real Postgres and a
real Temporal worker to the isolated native NVKE site. Set
`NVKE_TEST_NATIVE_SITE_PROFILE` to its mode-0600 qualification profile and install
the test-only `socksio` dependency in the backend test environment. The fixture
accepts only the explicitly bound local topology, pinned revision, namespace,
native CA/client-certificate kubeconfig and imported simulator tenant identity.
Its SSH SOCKS route changes HTTP transport only; production authorization, TLS,
encrypted outbox, activities and receipt recovery execute unchanged.

The case loses an admission reply and recovers the original receipt using a
read-only token. It enrolls imported workload access from the actual controller
read, downloads the native kubeconfig to a private file, and checks that revoking
the grant refuses a new download before another Secret read.
The exported kubeconfig must also reach the workload `/readyz`, `/version` and
node inventory with native CA/client-certificate TLS verification; two Ready
nodes and one control plane are required for the bound simulator tenant.
One uniquely named MachineImage is created and deleted through Astro with
UID/version guards; the
transport rejects all other native writes. Temporal history must contain no
native payload or credentials. `NVKE_TEST_NATIVE_SITE_REPORT` optionally selects
a new exclusive private metadata-only report. This qualifies those local hosted
paths, not new tenant provisioning, site-connection UI configuration, production
network reachability or physical providers.


## Reviewed resources and typed observations

New request files use exact-text, actor-checked admission. Parameter files must
contain one UTF-8 JSON object and retain their original bytes (including whitespace)
while an admission is unresolved. Legacy `nvke-operation` recovery files continue
using their original canonical JSON protocol. They cannot switch to inventory or
resource mode. No recovery file is overwritten or cleared automatically.

```sh
astro nvke datastore list-machine-image --site SITE_GUID --inventory \
  --request-file inventory-request.json --wait
astro nvke resource list SNAPSHOT_GUID --site SITE_GUID --search image-a --limit 50
astro nvke resource get SNAPSHOT_GUID --site SITE_GUID --resource-key SIGNED_KEY
astro nvke resource editor datastore.replaceMachineImage --site SITE_GUID \
  --source-operation SNAPSHOT_GUID --source-version 7 --resource-key SIGNED_KEY \
  --output private-edit.json
astro nvke datastore replace-machine-image --site SITE_GUID \
  --source-operation SNAPSHOT_GUID --source-version 7 --resource-key SIGNED_KEY \
  --file private-edit.json --request-file replace-request.json --wait
astro nvke operation recover --request-file replace-request.json
astro nvke operation diagnostics READ_OPERATION_GUID --site SITE_GUID --version 9 --limit 50
```

All ordinary pinned native commands accept these source flags. Omit resource-key
when the source is a successful single-resource GET. Use the source operation's
actual returned version, not the site's version or a Kubernetes resource version.
The API owns native schemas, immutable target binding, ETag/resource-version/UID
preconditions, authority checks and conflict detection. The CLI does not apply
native configuration directly. Controller edits retain their weaker observed-only
concurrency; the editor reports that limitation.

An editor may return a preparationActionKey instead of parameter text. Admit that
read with the same original source flags and its own new request file, wait for it
to succeed, then load the editor using that successful read's ID/version without
a resource-key. SPIRE is one such flow. The CLI never launches this read or retries
a mutation implicitly. Editor text and native documents require an explicit new
private output path, or `--output -` to deliberately print sensitive JSON. When
printing parameter text to stdout, the concurrency note goes to stderr. When a
preparation read is required, metadata is printed and no editor file is created.

Retrying a new-protocol request first looks up its exact actor-bound receipt.
A confirmed matching receipt returns the original operation even if the input file
has been removed. An absent receipt does not prove no effect: only the same action,
mode, source, reviewed versions, timeout and exact text can re-use that nonce.
Changes require a different explicit request file after the old outcome is resolved.
`operation recover` only reads a receipt and never re-admits native work.

Saved inventory/resource/diagnostic commands perform no native I/O. Pagination
cursors and search remain server-side; use the returned nextCursor with --after.
Diagnostic counts stay strings, missing booleans/counts stay null, and the views
remain historical. Free-text messages, history values and credentials remain in
the explicit native result download. `operation result` uses the text endpoint to
preserve native values without a floating-point round trip.

## Imported workload access

Use a successful controller `get-cluster` operation to explicitly enroll a
controller-managed cluster created outside Astro. Enrollment requires both admin
and read authority. Use the exact returned observation and current site versions;
the CLI never refreshes these versions on your behalf after a conflict.

```sh
astro nvke controller get-cluster --site SITE_GUID --file private-cluster-read.json \
  --request-file imported-cluster-read.json --wait
astro nvke site get SITE_GUID
astro nvke access enroll SOURCE_OPERATION_GUID --site SITE_GUID \
  --site-version 7 --source-version 9
astro nvke access list --site SITE_GUID --limit 50
astro nvke access revoke BINDING_GUID --site SITE_GUID --site-version 7 --version 1
```

The private read input contains `{"path":{"clusterID":"NATIVE_CLUSTER_UUID"}}`.
The API verifies that the encrypted successful response matches that exact UUID
and current controller authority; a list result, name match, caller-supplied
ownership claim or uncertain create is insufficient. This does not reconcile an
uncertain create. The current actor is captured immediately before admission and
checked again by the server.

Bindings contain metadata, not credentials. `access list` uses the server's
nextCursor with `--after`; no page is filtered or combined locally. A binding can
retain its original sourceOperationId when enrollment from a newer read returns
the same existing grant. It survives source observation soft deletion and
credential rotation on the same controller connection. Controller replacement
invalidates its authority. Enrollment performs no native I/O or credential
download; every `datastore get-workload-kubeconfig` still verifies live CAPI UID,
Secret owner reference and exact UUID/name/admin-mode labels. Unlabelled Secrets
remain refused.

Revocation withdraws the imported grant. Independent native CRD or Astro-create
proof can still authorize access. Re-enrollment requires a new successful read
completed after revocation, so an old enrollment retry cannot restore a revoked
grant. After a lost reply, inspect `access list` before retrying the original
site/source or binding/version tuple. Neither write retries automatically.

## Historical credential maintenance

```sh
astro nvke operation maintenance GET_CLUSTER_OPERATION_GUID --site SITE_GUID --version 9
```

Use the ID and returned version of a successful controller `get-cluster`
operation. The server checks the actor, organization, site, source version and
fixed native cluster UUID before projecting the saved encrypted observation.
Output includes the observation time, next scheduled renewal check, lock owner,
renewal and etcd-rotation workflow IDs, lifecycle state, phase, timestamps and
retained-lock evidence. Unknown values stay null; explicit false stays false.
Native workflow IDs, including printable Unicode, remain intact.

The scheduled check is not X.509 expiry. Schedule-overdue refers to the saved
observation time and is false while a cycle is recorded as accepted. A successful
unchanged cycle is a no-op, not renewed credentials. Provisioning status does not
override a failed maintenance cycle or retained ownership. Run a new controller
read for a new observation. This command performs no native I/O, downloads no
raw result, and does not start, reset, retry or unlock native workflows.

## Native Temporal authority

```sh
astro nvke temporal get --site SITE_GUID --org acme --json
astro nvke temporal configure --site SITE_GUID --site-version 7 --authority-version 0 \
  --address native-temporal.example.test:7233 --namespace native-controller \
  --task-queue native-controller-tasks --credentials-file private-native-temporal.json
astro nvke temporal verify --site SITE_GUID --site-version 8 --authority-version 1
```

Use the returned `siteVersion` and authority `version` for the next review.
`authority-version` is zero only for a new authority. `enable`, `disable` and
`remove` take the same site and reviewed versions as `verify`; removal soft-deletes
the registration. Replacement gets a new identity. These writes do not retry
automatically. If the reply is lost, run `temporal get` and review the resulting
metadata before making another change.

The private credential JSON accepts `apiKey`, `caPem`, `clientCertificatePem`,
and `clientKeyPem`; certificate and key must be paired. It uses the same private
file protections as other NVKE inputs, or `--credentials-file -` for stdin.
Values never appear in normal command output. Omit credentials to preserve the
encrypted bundle; use `--clear-credentials` to clear it. `--server-name` sets a
TLS name override. `--plaintext` is explicit, cannot carry credentials or a TLS
name, and requires clearing any existing credentials on the server.

Native address, namespace and queue are always explicit, with no Astro Temporal
or HTTP-credential fallback. `--connection-timeout` bounds connection/inspection
(1–300 seconds), not native workflow duration. `NAMESPACE_VERIFIED` proves access
to that namespace, not a polling worker or workflow availability. `catalog`
includes the three separately declared native workflow input schemas alongside
the 88 HTTP/datastore actions. Authority commands do not start, reset, retire
nodes, or clear locks; reviewed submissions use the separate commands below.

## Native maintenance submissions and recovery

```sh
astro nvke temporal workflow renew-k8s-certs-v2 --site SITE_GUID \
  --site-version 8 --authority-version 1 \
  --source-operation GET_CLUSTER_OPERATION_GUID --source-version 9 \
  --file private-renewal-input.json --request-file renewal-recovery.json
astro nvke temporal workflow recover --request-file renewal-recovery.json --wait
```

Named commands are `renew-k8s-certs-v2`, `rotate-etcd-encryption-key`, and
`retire-lost-etcd-rotation-node`. `request WORKFLOW` accepts the exact native name
from `catalog`: `RenewK8sCertsV2`, `RotateEtcdEncryptionKey`, or
`RetireLostEtcdRotationNode`. Every input field remains available through private
UTF-8 JSON (`--file -` for stdin), including native integer values and optional
renewal scheduling. The server owns validation and live native admission.

First submission requires explicit positive site/authority/source versions and
a successful controller `get-cluster` observation of the exact cluster under
current authority. The CLI checks current metadata and the pinned catalog before
persisting a new recovery identity. It flushes the private recovery file before
admission. That file holds actor/server/org/site, versions, source, workflow,
schema/input digests, deadline and a request UUID; it does not store native input
or credentials. Preserve both recovery file and original input after any uncertain
reply. A pending retry retains the identical tuple and exact input text; it never
refreshes a version, changes an owner, or generates another request ID.

A repeated request first looks up the original receipt. When found, it returns
that operation without rereading input or another admission. `workflow recover`
always performs receipt lookup only and needs no site/version/input flags.
An unavailable or incomplete receipt reply is refused. An explicitly absent
receipt does not prove that an in-flight request had no effect: retain the
original identity and input. Existing HTTP/resource recovery files and native
workflow files have separate protocols and cannot replace each other.

Retirement additionally requires `--acknowledge-lost-node-teardown`. Its input
must name the exact native `clusterID`, retained `rotationWorkflowID`, and lost
`nodeID`. The server checks the reviewed failed retained owner/phase and closed
native rotation; the native workflow then performs live node/quorum checks and
can permanently remove that control-plane node. Keep backups and retained locks;
this command does not reset the owner or release its lock.

`--operation-timeout` (30–86400 seconds) bounds Astro observation, not the native
workflow's execution or six-hour rewrite. `--wait-timeout` only bounds local
waiting. Neither timeout cancels native work. Use existing `operation get/wait`,
`operation resume --version ...`, and explicit private `operation result --output`
for metadata, continued observation and native execution references. Successful
native execution is not proof of changed certificates, encryption convergence
or site health; collect fresh controller/metrics and qualification evidence.
Owner history inspection and rotation reset use the separate reviewed commands below.


## Native owner history and phase-aware rotation reset

```sh
astro nvke temporal workflow history RotateEtcdEncryptionKey --site SITE_GUID \
  --site-version 8 --authority-version 1 \
  --source-operation GET_CLUSTER_OPERATION_GUID --source-version 9 --limit 50 --json
astro nvke temporal workflow reset --site SITE_GUID \
  --site-version 8 --authority-version 1 \
  --source-operation GET_CLUSTER_OPERATION_GUID --source-version 9 \
  --file private-reset-review.json --request-file rotation-reset-recovery.json
astro nvke temporal workflow reset recover --request-file rotation-reset-recovery.json --wait
```

History supports `RenewK8sCertsV2` and `RotateEtcdEncryptionKey`. The server derives
the native owner from the successful `get-cluster` source operation. The response
separates native run/status and inspection time from the controller's recorded
state/phase, source time and retained lock. It excludes activity payloads/results,
failure messages and credentials. Event and scheduling IDs are decimal strings,
including values above JavaScript's exact integer range. Pass `nextCursor` as
`--after` with the same versions, source and `--limit`; cursors expire after 30
minutes and a changed native run invalidates them. There is no automatic selection
of a reset boundary or mixing of history pages from different runs.

Reset supports only `RotateEtcdEncryptionKey`. Its private JSON review has exactly
these fields (replace placeholders with reviewed native values):

```json
{
  "workflowId": "retained-native-rotation-owner",
  "baseRunId": "EXACT_NATIVE_RUN_GUID",
  "workflowTaskEventId": "14",
  "activityEventId": "15",
  "activityType": "StartEtcdEncryptionRewrite",
  "phase": "rewrite",
  "mode": "restart_rewrite",
  "rationale": "Incident reference and reason for this exact boundary",
  "quiescenceEvidence": "Evidence reference proving earlier conflicting node work is terminal",
  "acknowledgeBoundaryReviewed": true,
  "acknowledgeConflictingWorkStopped": true,
  "retirementWorkflowId": ""
}
```

The review file uses private file protections or `--file -` for stdin. Duplicate,
unknown or differently cased fields, non-string event IDs and missing acknowledgements
are refused. Rationale/evidence are each bounded to 4,000 UTF-8 bytes, encrypted by
the server and redacted in mutation audit variables. They never enter recovery
metadata or the native reset reason. The CLI stores a review-format marker and
exact original UTF-8 digest, not the private review.

Choose `retry_activity`, `resume_rewrite`, `restart_rewrite` or `refresh_survivors`
according to the pinned upstream rotation procedure and exact failed phase.
Phase names are `prepare`, `add_secondary`, `promote_primary`, `rewrite`,
`remove_old_keys` and `finalize`. The server validates the current native owner,
retained lock, successful Begin, native phase history and selected scheduling
boundary. A first-task shortcut or earlier successful mutation is refused.
Poll recovery preserves the acknowledged rewrite node/operation within its
original six-hour allowance. Restart requires confirming the old rewrite is
terminal. Survivor refresh additionally requires a completed
`retirementWorkflowId` proving this cluster and retained owner. A recorded prepare
failure needs a fresh workflow ID after lock release; renewal reset is unsupported.

Node-work evidence is an operator attestation, not an independent agent-process
or hardware check. A closed Temporal run alone does not prove node work stopped.
Coordinate external Temporal operators: the native reset API has no cross-client
atomic compare-and-swap, and an older base run does not protect a newer run from
termination. This command never unlocks the cluster or automatically terminates
native workflows before reset.

A private recovery identity is flushed before Astro admission. A repeated command
looks up the original receipt before opening the review file. If no receipt is
visible, only the identical original review, versions, source, actor and request
UUID may be re-admitted to Astro. `reset recover` is always lookup-only. This is
separate from retrying the native RPC: after persisting sending, Astro sends at
most one reset RPC with SDK retry disabled. It recovers a lost reply from exact
native reset history proof. Even a crash between persisting sending and the RPC
leaves an unconfirmed intent that is never resent. Retain the original files and
inspect operation/native history before any further recovery. Reset, native start
and HTTP/resource recovery files cannot substitute for one another.

Observation and local wait deadlines do not cancel the native owner, release its
lock or restart a rewrite. A succeeded Astro reset operation means its proved
native reset execution completed; encryption health and hardware qualification
still require independent evidence. Local compiled-CLI qualification uses real
PostgreSQL/HTTP/Temporal with fixture workflows, not production node mutations.


## Hosted operator agent

`astro nvke agent catalog` exposes the server's complete pinned command descriptors.
`get/configure/verify/enable/disable/remove` manage a dedicated site-bound mTLS
executor independently of the controller, datastore, metrics and native Temporal.
Configuration requires an exact instance GUID, bundle SHA-256 and HTTPS base URL.
New authorities require a private `caPem/clientCertificatePem/clientKeyPem` JSON
bundle; subsequent omission preserves the sealed existing pair.

Submit the complete native envelope from a protected UTF-8 file with `agent request`.
Its `--request-file` is created and flushed before admission and binds caller,
server, organization, request UUID, site/authority versions, native revision,
private input digest, acknowledgement and observation deadline. A lost reply must
be recovered with that original file. `agent recover` performs a receipt lookup
only and needs no native input file. Resubmission before a receipt is visible
requires the identical input text and original reviewed versions.

All native options and artifact references stay inside the server-validated
request envelope. Infrastructure-changing requests need explicit
`--acknowledge-infrastructure-change`. Process completion does not establish
infrastructure readiness. Local wait expiry does not cancel remote work.

Existing operation metadata/wait/cancel/private-result commands support agent
receipts. `agent reconcile` performs a read-only settlement of an uncertain job
under the current reviewed administrator, site, authority and operation versions.
It never submits a new native job. Changing the original agent identity is refused
by the backend, including after credentials rotate.

`agent artifact` downloads a retained artifact to an exclusive protected file.
Every chunk binds the original operation/name/offset/size/SHA-256, and the assembled
file must match the complete hash. `--max-bytes` defaults to 64 MiB and can be
increased for larger diagnostics. Nothing writes artifact contents to stdout.
An interrupted or rejected download retains its protected partial file; inspect it
before using any resulting credential. See the README for command flag examples.
