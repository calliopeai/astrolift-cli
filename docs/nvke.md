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

The 88 API/resource actions are wired through the durable API. Bootstrap/install,
node preparation, local spec/render utilities, telemetry and managed workload
access remain part of the broader integration work; these native commands alone
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
