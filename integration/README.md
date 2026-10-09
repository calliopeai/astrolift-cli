# Real environment-action consumer checks

The Python suite invokes the built Go CLI through the app repository's real
`config.asgi` application, test PostgreSQL, current bearer authentication and
native Kubernetes provider. It imports the app's exact-environment Kind fixtures;
HTTP, authentication and provider transports are never replaced.

Build this checkout with `go build -o /tmp/astro-environment-actions .`. Use an
app checkout containing `core/tests/test_exec_environment_contracts_2209.py` and
its Django test configuration. Point the app's PostgreSQL configuration at a
disposable test database and use two task-owned local Kind clusters named
`astrolift-env-target-2217-a` and `astrolift-env-target-2217-b`. Save their separate
kubeconfigs and load `busybox:1.36` in both clusters; the exec fixture deliberately
uses `imagePullPolicy: Never`. The fixtures reject unrelated contexts and
non-loopback Kubernetes API endpoints.

From that checkout's backend directory, using its Python test environment:

```sh
export ASTROLIFT_CLI_TEST_BINARY=/tmp/astro-environment-actions
export ASTROLIFT_WORKLOAD_TEST_KUBECONFIG_A=/path/to/kind-a.yaml
export ASTROLIFT_WORKLOAD_TEST_KUBECONFIG_B=/path/to/kind-b.yaml
pytest -c pyproject.toml --reuse-db /path/to/astrolift-cli/integration/test_environment_actions.py
```

Keep `DJANGO_SETTINGS_MODULE` and the test database configuration consistent
with the app's normal pytest runner. The module skips unless the CLI binary
environment variable is set. Each fixture creates unique namespaces and removes
only those namespaces afterward. CLI subprocesses use temporary configuration
directories and freshly minted test-only tokens; no saved cloud, CLI or production
credentials are read.

The Go suite also checks review-file parsing, unsupported target authority,
legacy exec behavior and portable docs exports. Run `go fmt ./...`,
`go vet ./...`, `make lint` with a v2 golangci-lint, and `make test` before commit.

## Exact workflow Definition starts

`test_definition_reviewed.py` uses the same real bearer ASGI helpers plus an
actual SDK-managed disposable Temporal server and an independent PostgreSQL
test database. Build the CLI and run this module from the reviewed-start app
checkout with its `pytest.ini` and backend `conftest` loaded. External test
paths must retain `asyncio_mode=auto` and the backend's real-schema truncation
fixtures. Use a dedicated database, for example `astrolift_cli_definition_2236`.

The suite dispatches nested JSON and a sensitive secret reference, drops the
actual accepted reply at a local forwarding proxy, and recovers the original
execution after deleting the input file and disabling the definition. It checks
schema revision changes, literal-sensitive-input refusal, private metadata-file
contents and unknown absent recovery. The task queue intentionally has no worker:
these tests prove native CLI engine submission and read-only recovery, without
claiming stage execution, completion or Kubernetes job cleanup. Temporary token
and configuration fixtures never read saved production credentials.

## Exact workload identity beyond the inventory cap

`test_environment_selector.py` validates the environment review/restart/scale
GraphQL documents against the actual app schema and invokes the compiled CLI
through real bearer ASGI and PostgreSQL. It inserts 201 newer siblings before
reviewing the original workload, checks the active app-and-slug uniqueness
constraint, and verifies exact organization, actor, app, workload and environment
identities. The lookup uses `astroliftWorkload(appSlug, slug)` and does not depend
on a workload list or its page boundaries. Use the app's `pytest.ini` and backend
`conftest`, with a separate test database and local cache settings as described
for the other receipts. This review-only case needs no Kind cluster and does not
claim a provider mutation or workload execution.

## Managed resource paging and exact context

`test_managed_resources.py` invokes the compiled CLI against real ASGI,
PostgreSQL and bearer/RBAC authorization. A dedicated database such as
`astrolift_cli_resources_2207` keeps this fixture separate from other runs.
It walks 251 project resources and 251 independently visible consumers, checks
the compatible default JSON array, reads exact app/project GUID context,
attaches one disposable local consumer with a reviewed revision, then refuses
a changed cluster, deleted same-name target, foreign/malformed cursor and
revoked role. Basic reads perform no pricing query and expose no configuration
markers. This suite does not contact a provider or dispatch a Temporal worker;
worker fencing and provider lifecycle acceptance are covered by the app's
separate real PostgreSQL/Temporal tests.

## Recoverable human gates

`test_human_gates.py` invokes the compiled CLI against real bearer ASGI,
PostgreSQL and production Temporal workers. It exercises root, collection and
nested gates; a local forwarding proxy drops the actual accepted decision reply.
Read-only `gate-status` recovers the same recorded decision, identical retries
acknowledge it, and conflicting retries are refused. No API, authentication,
workflow, activity or Temporal client is replaced. Use a backend containing the
recoverable human-gate API and the usual CLI binary environment variable, with
its pytest configuration and backend conftest loaded. Temporary test tokens and
configuration directories never read production credentials.
