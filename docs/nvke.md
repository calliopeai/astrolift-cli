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

## Qualification and maintenance

The 88 API/resource actions are wired through the durable API. Local native tooling
is exposed through `astro nvke tooling` as described below. Live bootstrap/install,
node preparation, telemetry and managed workload access remain part of the broader
integration work; these commands alone
do not claim their completion. A controller create whose response/ID is lost
requires operator investigation. OIDC enablement lacks a direct upstream read-back
field, and declared pool readiness counters are not all populated by the pinned
management engine; the backend waits for positive evidence rather than labelling
acceptance as Ready. Real management-plane/live qualification remains required.

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
