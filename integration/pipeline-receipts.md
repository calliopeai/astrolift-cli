# Pipeline CLI acceptance

`test_pipeline_reviewed.py` runs the compiled CLI against the app backend's
real `config.asgi` application, PostgreSQL and an SDK-managed disposable
Temporal service. It requires the backend Python dependencies and its real
migrations, with a separate task-owned PostgreSQL database.

Build this checkout with `go build -o /tmp/astro-pipeline .`, then run the test
using the backend's pytest configuration. Set `ASTROLIFT_CLI_TEST_BINARY` to
that absolute binary path, `DJANGO_SETTINGS_MODULE=config.test_settings` and
the backend's documented database/environment settings. Include the backend
and `backend/providers` on `PYTHONPATH`. The database user needs permission to
create the corresponding `test_` database. Use a local cache backend and disable
Constance's database cache in the test settings to avoid a shared Redis service.
An existing disposable Temporal server can instead be selected through
`ASTROLIFT_TEST_TEMPORAL_ADDRESS`.

The receipt proves exact organization, actor, pipeline version and request key;
private request-file persistence before dispatch; recovery after an actual
accepted HTTP response is dropped; read-only reconcile/show; rejected schema,
credentials, actor reuse and missing confirmation without legacy fallback;
and cancellation bound to the reviewed engine execution.

The queue has no worker by design. A real signal acknowledgment leaves the
engine running and cleanup pending, which the CLI reports honestly. This test
does not certify repository fetch, job execution or Kubernetes cleanup. Those
require the backend's separate production-workflow/provider acceptance suite.
The local forwarding proxy records query shapes and organization headers only;
it forwards actual schema responses and does not store bearer credentials.
