"""Opt-in compiled CLI -> real ASGI/PostgreSQL/Temporal dispatch receipts.

The queue deliberately has no worker: an engine signal acknowledgment must not
claim workflow closure or Kubernetes cleanup. No HTTP response, authorization,
pipeline resolver, Temporal client, workflow or activity is replaced. A local
forwarding proxy can drop a real accepted response or submit an unknown field
to the real schema. This suite does not certify job execution or cleanup.
"""

import asyncio
import http.client
import json
import os
import socket
import subprocess
import threading
import time
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import SimpleNamespace
from uuid import uuid4

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip("requires the opt-in compiled CLI and app backend", allow_module_level=True)

from asgiref.sync import sync_to_async
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken, Member, Organization
from astrolift_pipelines.models import Pipeline, PipelineRun
from core.permissions import Permission
from core.testing.temporal import temporal_test_env
from core.tests.utils.scope_world import bind_role
from django.contrib.auth import get_user_model

pytestmark = pytest.mark.django_db(transaction=True)


@contextmanager
def running_api():
    import uvicorn
    from config.asgi import application

    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    sock.listen(128)
    server = uvicorn.Server(uvicorn.Config(application, lifespan="off", log_level="critical"))
    thread = threading.Thread(target=server.run, kwargs={"sockets": [sock]}, daemon=True)
    thread.start()
    deadline = time.monotonic() + 5
    while not server.started:
        assert thread.is_alive() and time.monotonic() < deadline
        time.sleep(0.01)
    try:
        yield sock.getsockname()[1]
    finally:
        server.should_exit = True
        thread.join(5)
        sock.close()
        assert not thread.is_alive()


@contextmanager
def forwarding_proxy(api_port):
    """Fault real transport only; all GraphQL results come from config.asgi."""
    state = SimpleNamespace(requests=[], drop_start=False, reject_schema=False)

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_):
            pass

        def do_POST(self):
            body = self.rfile.read(int(self.headers["Content-Length"]))
            query = json.loads(body)
            state.requests.append((query, self.headers.get("X-Astrolift-Organization")))
            starting = "mutation StartPipelineRun" in query["query"]
            if starting and state.reject_schema:
                query["query"] = query["query"].replace(
                    "startPipelineRun(", "unsupportedReviewedPipelineRun("
                )
                body = json.dumps(query).encode()
            headers = {
                key: value
                for key, value in self.headers.items()
                if key.lower() not in {"host", "content-length", "connection"}
            }
            connection = http.client.HTTPConnection("127.0.0.1", api_port, timeout=20)
            try:
                connection.request("POST", self.path, body, headers)
                response = connection.getresponse()
                result = response.read()
                if starting and state.drop_start:
                    state.drop_start = False
                    self.close_connection = True
                    self.connection.shutdown(socket.SHUT_RDWR)
                    return
                self.send_response(response.status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(result)))
                self.end_headers()
                self.wfile.write(result)
            finally:
                connection.close()

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    state.url = f"http://127.0.0.1:{server.server_port}"
    try:
        yield state
    finally:
        server.shutdown()
        server.server_close()
        thread.join(5)


def create_world():
    suffix = uuid4().hex[:12]
    org = Organization.objects.create(name="Disposable CLI pipeline", slug=f"cli-pipeline-{suffix}")
    user = get_user_model().objects.create_user(username=f"cli-pipeline-{suffix}")
    Member.objects.create(user=user, scope_kind="ORG", scope_id=org.pk)
    bind_role(
        user,
        permissions=[Permission.APP_READ, Permission.APP_UPDATE],
        kind="ORG",
        scope_id=org.pk,
        slug=f"cli-pipeline-{suffix}",
    )
    pipeline = Pipeline.objects.create(
        organization=org,
        name="reviewed",
        repo_url="https://example.test/disposable",
        default_branch="main",
    )
    issued = mint_token()
    ApiToken.objects.create(
        user=user,
        organization=org,
        name="Disposable CLI proof",
        token_hash=issued.token_hash,
        scopes=["read:apps", "write:apps"],
    )
    return SimpleNamespace(org=org, user=user, pipeline=pipeline, token=issued.plaintext)


def other_actor_token(world):
    suffix = uuid4().hex[:12]
    user = get_user_model().objects.create_user(username=f"cli-other-{suffix}")
    Member.objects.create(user=user, scope_kind="ORG", scope_id=world.org.pk)
    bind_role(
        user,
        permissions=[Permission.APP_READ, Permission.APP_UPDATE],
        kind="ORG",
        scope_id=world.org.pk,
        slug=f"cli-other-{suffix}",
    )
    issued = mint_token()
    ApiToken.objects.create(
        user=user,
        organization=world.org,
        name="Other legitimate actor",
        token_hash=issued.token_hash,
        scopes=["read:apps", "write:apps"],
    )
    return issued.plaintext


def invoke(world, proxy, tmp_path, args, *, token=None, org=None):
    return subprocess.run(
        [
            os.environ["ASTROLIFT_CLI_TEST_BINARY"],
            "--api-url",
            proxy.url,
            "--org",
            str(org or world.org.guid),
            "--no-prompt",
            *args,
        ],
        env={**os.environ, "XDG_CONFIG_HOME": str(tmp_path), "ASTROLIFT_TOKEN": token or world.token},
        capture_output=True,
        text=True,
        timeout=30,
    )


async def test_real_pipeline_dispatch_recovery_and_cancel_acknowledgment(settings, tmp_path):
    # This is an actual SDK-managed disposable server, not a substituted client.
    async with temporal_test_env() as engine:
        settings.ASTROLIFT_TEMPORAL_ENABLED = True
        settings.TEMPORAL_ADDRESS = engine.client.service_client.config.target_host
        settings.TEMPORAL_NAMESPACE = "default"
        settings.TEMPORAL_TASK_QUEUE = f"cli-pipeline-{uuid4().hex}"
        world = await sync_to_async(create_world)()
        path = tmp_path / "request.json"
        with running_api() as port, forwarding_proxy(port) as proxy:
            run_args = [
                "pipeline",
                "run",
                str(world.pipeline.guid),
                "--request-file",
                str(path),
                "--yes",
                "--json",
            ]
            proxy.drop_start = True
            lost = await asyncio.to_thread(invoke, world, proxy, tmp_path, run_args)
            assert lost.returncode != 0 and "unknown" in lost.stderr, lost.stderr
            saved = json.loads(path.read_text())
            original = path.read_bytes()
            assert path.stat().st_mode & 0o777 == 0o600
            assert saved["organizationId"] == str(world.org.guid)
            assert saved["actorUserId"] == world.user.pk
            assert saved["targetId"] == str(world.pipeline.guid)
            assert saved["version"] == world.pipeline.version and saved["ref"] == "main"
            assert world.token not in path.read_text()
            run = await sync_to_async(PipelineRun._unscoped.get)(request_id=saved["requestId"])
            assert run.organization_id == world.org.pk and run.pipeline_id == world.pipeline.pk
            assert run.pipeline_version == saved["version"] and run.actor_key == f"user:{world.user.pk}"
            assert run.temporal_run_id and run.dispatch_status == "submitted"
            handle = engine.client.get_workflow_handle(run.temporal_workflow_id, run_id=run.temporal_run_id)
            try:
                description = await handle.describe()
                assert description.run_id == run.temporal_run_id and description.status.name == "RUNNING"
                # Definitions can change after dispatch; exact request recovery comes first.
                await sync_to_async(Pipeline.objects.filter(pk=world.pipeline.pk).update)(
                    version=world.pipeline.version + 1
                )
                before = len(proxy.requests)
                recovered = await asyncio.to_thread(invoke, world, proxy, tmp_path, run_args)
                assert recovered.returncode == 0, recovered.stderr
                receipt = json.loads(recovered.stdout)["run"]
                assert receipt["id"] == str(run.guid) and receipt["requestId"] == saved["requestId"]
                assert receipt["temporalRunId"] == run.temporal_run_id
                assert not any("mutation" in q["query"] for q, _ in proxy.requests[before:])
                assert path.read_bytes() == original
                reconciled = await asyncio.to_thread(
                    invoke,
                    world,
                    proxy,
                    tmp_path,
                    ["pipeline", "reconcile", "--request-file", str(path), "--json"],
                )
                assert reconciled.returncode == 0, reconciled.stderr
                assert json.loads(reconciled.stdout)["run"]["id"] == str(run.guid)
                shown = await asyncio.to_thread(
                    invoke, world, proxy, tmp_path, ["pipeline", "show", str(run.guid), "--json"]
                )
                assert shown.returncode == 0, shown.stderr
                cancelled = await asyncio.to_thread(
                    invoke, world, proxy, tmp_path, ["pipeline", "cancel", str(run.guid), "--yes", "--json"]
                )
                assert cancelled.returncode == 0, cancelled.stderr
                ack = json.loads(cancelled.stdout)["run"]
                assert ack["cancellationStatus"] == "acknowledged"
                assert ack["cancellationObservedAt"] is None and ack["cleanupStatus"] == "pending"
                assert ack["status"] == "pending" and (await handle.describe()).status.name == "RUNNING"
                assert await sync_to_async(PipelineRun._unscoped.count)() == 1
                starts = [q for q, _ in proxy.requests if "mutation StartPipelineRun" in q["query"]]
                assert len(starts) == 1
                assert starts[0]["variables"]["input"] == {
                    "pipelineId": str(world.pipeline.guid),
                    "expectedVersion": saved["version"],
                    "requestId": saved["requestId"],
                    "ref": "main",
                    "confirmed": True,
                }
                # Org discovery precedes SetOrg; every pipeline request is scoped.
                assert all(
                    org == str(world.org.guid) for q, org in proxy.requests if "Pipeline" in q["query"]
                )
                assert not any("triggerPipelineRun" in q["query"] for q, _ in proxy.requests)

                # A real schema validation failure cannot cause a legacy dispatch.
                proxy.reject_schema = True
                failed_path = tmp_path / "unsupported-schema.json"
                before = len(proxy.requests)
                failed = await asyncio.to_thread(
                    invoke,
                    world,
                    proxy,
                    tmp_path,
                    [
                        "pipeline",
                        "run",
                        str(world.pipeline.guid),
                        "--request-file",
                        str(failed_path),
                        "--yes",
                    ],
                )
                assert failed.returncode != 0, failed.stdout
                assert failed_path.exists() and await sync_to_async(PipelineRun._unscoped.count)() == 1
                assert sum("mutation" in q["query"] for q, _ in proxy.requests[before:]) == 1
                assert not any("triggerPipelineRun" in q["query"] for q, _ in proxy.requests[before:])

                # A real authorized second actor cannot adopt the original key.
                another_token = await sync_to_async(other_actor_token)(world)
                for options in ({"token": "invalid-bearer"}, {"token": another_token}, {"org": uuid4()}):
                    before = len(proxy.requests)
                    refused = await asyncio.to_thread(invoke, world, proxy, tmp_path, run_args, **options)
                    assert refused.returncode != 0
                    assert not any("mutation" in q["query"] for q, _ in proxy.requests[before:])
                    assert path.read_bytes() == original

                # New dispatch requires review metadata and explicit confirmation.
                for args in (
                    ["pipeline", "run", str(world.pipeline.guid)],
                    [
                        "pipeline",
                        "run",
                        str(world.pipeline.guid),
                        "--request-file",
                        str(tmp_path / "unconfirmed.json"),
                    ],
                    ["pipeline", "cancel", str(run.guid)],
                ):
                    before = len(proxy.requests)
                    refused = await asyncio.to_thread(invoke, world, proxy, tmp_path, args)
                    assert refused.returncode != 0
                    assert not any("mutation" in q["query"] for q, _ in proxy.requests[before:])
                assert not (tmp_path / "unconfirmed.json").exists()

                # A never-submitted saved request cannot silently refresh its version.
                await sync_to_async(Pipeline.objects.filter(pk=world.pipeline.pk).update)(
                    version=world.pipeline.version + 2
                )
                before = len(proxy.requests)
                stale = await asyncio.to_thread(
                    invoke,
                    world,
                    proxy,
                    tmp_path,
                    [
                        "pipeline",
                        "run",
                        str(world.pipeline.guid),
                        "--request-file",
                        str(failed_path),
                        "--yes",
                    ],
                )
                assert stale.returncode != 0 and "changed" in stale.stderr
                assert not any("mutation" in q["query"] for q, _ in proxy.requests[before:])
                assert await sync_to_async(PipelineRun._unscoped.count)() == 1
            finally:
                await handle.terminate(reason="Disposable CLI receipt cleanup")
