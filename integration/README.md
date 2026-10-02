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
