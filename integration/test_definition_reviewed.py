"""Opt-in compiled Definition CLI -> real bearer ASGI/PostgreSQL/Temporal.

The disposable engine queue intentionally has no worker. These checks establish
exact native review/start/read recovery, not stage execution or Kubernetes jobs.
A forwarding proxy drops an actual accepted response; no API/auth/client or
workflow implementation is substituted.
"""

import asyncio
import http.client
import json
import os
import socket
import threading
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import SimpleNamespace
from uuid import uuid4

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip(
        "requires compiled CLI and reviewed-start backend", allow_module_level=True
    )

from asgiref.sync import sync_to_async
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken, Member, Organization
from core.permissions import Permission
from core.testing.temporal import temporal_test_env
from core.tests.utils.scope_world import bind_role
from django.contrib.auth import get_user_model
from workflows.models import WorkflowDefinition, WorkflowDefinitionStart, WorkflowStage

from test_pipeline_reviewed import invoke, running_api

pytestmark = pytest.mark.django_db(transaction=True)


@pytest.fixture(autouse=True)
def clear_disposable_engine_connection_cache():
    # The application caches its actual SDK connection per process. Each test
    # changes TEMPORAL_ADDRESS to a fresh disposable server; expire that cache,
    # without replacing the connector or injecting a client/response.
    from astrolift_workflows import client

    client._client = None
    yield
    client._client = None


def create_definition_world(*, accepts_inputs=False):
    suffix = uuid4().hex[:12]
    org = Organization.objects.create(
        name="Disposable Definition CLI", slug=f"cli-definition-{suffix}"
    )
    user = get_user_model().objects.create_user(username=f"cli-definition-{suffix}")
    Member.objects.create(user=user, scope_kind="ORG", scope_id=org.pk)
    bind_role(
        user,
        permissions=[Permission.WORKFLOW_READ, Permission.WORKFLOW_TRIGGER],
        kind="ORG",
        scope_id=org.pk,
        slug=f"cli-definition-{suffix}",
    )
    issued = mint_token()
    ApiToken.objects.create(
        user=user,
        organization=org,
        name="Disposable Definition proof",
        token_hash=issued.token_hash,
        scopes=["read:apps", "workflow:trigger"],
    )
    definition = WorkflowDefinition.objects.create(
        name="Example", slug=f"example-{suffix}", organization=org, model_label=""
    )
    if accepts_inputs:
        definition.input_schema = {
            "type": "object",
            "additionalProperties": False,
            "properties": {
                "nested": {
                    "type": "object",
                    "properties": {
                        "values": {"type": "array", "items": {"type": "integer"}}
                    },
                },
                "message": {"type": "string"},
                "credential": {"type": "string", "writeOnly": True},
            },
            "required": ["nested", "credential"],
        }
        definition.save()
    WorkflowStage.objects.create(
        definition=definition, slug="checkpoint", order=0, kind="checkpoint"
    )
    return SimpleNamespace(
        org=org, user=user, definition=definition, token=issued.plaintext
    )


@contextmanager
def definition_proxy(api_port):
    state = SimpleNamespace(requests=[], drop_start=False)

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_):
            pass

        def do_POST(self):
            body = self.rfile.read(int(self.headers["Content-Length"]))
            query = json.loads(body)
            state.requests.append(query)
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
                if (
                    "mutation StartWorkflowDefinition" in query["query"]
                    and state.drop_start
                ):
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


async def test_native_definition_lost_response_sensitive_inputs_readonly_recovery(
    settings, tmp_path
):
    async with temporal_test_env() as engine:
        settings.ASTROLIFT_TEMPORAL_ENABLED = True
        settings.TEMPORAL_ADDRESS = engine.client.service_client.config.target_host
        settings.TEMPORAL_NAMESPACE = "default"
        settings.TEMPORAL_TASK_QUEUE = f"cli-definition-{uuid4().hex}"
        world = await sync_to_async(create_definition_world)(accepts_inputs=True)
        path = tmp_path / "request.json"
        inputs = tmp_path / "inputs.json"
        marker = "PRIVATE_DEFINITION_INPUT_MARKER"
        inputs.write_text(
            json.dumps(
                {
                    "nested": {"values": [9007199254740993]},
                    "message": marker,
                    "credential": "secret://opaque-reference",
                }
            )
        )
        inputs.chmod(0o600)
        with running_api() as port, definition_proxy(port) as proxy:
            reviewed = await asyncio.to_thread(
                invoke,
                world,
                proxy,
                tmp_path,
                ["workflow", "definition-review", str(world.definition.guid), "--json"],
            )
            assert reviewed.returncode == 0, reviewed.stderr
            review = json.loads(reviewed.stdout)
            args = [
                "workflow",
                "definition-start",
                str(world.definition.guid),
                "--request-file",
                str(path),
                "--inputs-file",
                str(inputs),
                "--expected-revision",
                review["revision"],
                "--expected-input-schema-digest",
                review["inputContract"]["digest"],
                "--yes",
                "--json",
            ]
            proxy.drop_start = True
            lost = await asyncio.to_thread(invoke, world, proxy, tmp_path, args)
            assert lost.returncode != 0 and "unknown" in lost.stderr, lost.stderr
            saved = json.loads(path.read_text())
            assert path.stat().st_mode & 0o777 == 0o600
            assert saved["targetId"] == str(world.definition.guid)
            assert (
                saved["actorUserId"] == world.user.pk
                and saved["revision"] == review["revision"]
            )
            assert marker not in path.read_text() + lost.stderr + lost.stdout
            assert world.token not in path.read_text()
            row = await sync_to_async(
                WorkflowDefinitionStart.objects.select_related("execution").get
            )(request_id=saved["requestId"])
            assert row.dispatch_status == "submitted" and row.execution.run_id
            handle = engine.client.get_workflow_handle(
                row.execution.workflow_id, run_id=row.execution.run_id
            )
            try:
                description = await handle.describe()
                assert (
                    description.run_id == row.execution.run_id
                    and description.status.name == "RUNNING"
                )
                inputs.unlink()
                await sync_to_async(
                    WorkflowDefinition.objects.filter(pk=world.definition.pk).update
                )(is_enabled=False)
                before = len(proxy.requests)
                recovered = await asyncio.to_thread(
                    invoke, world, proxy, tmp_path, args
                )
                assert recovered.returncode == 0, recovered.stderr
                receipt = json.loads(recovered.stdout)["start"]
                assert receipt["executionId"] == str(row.execution.guid)
                assert receipt["requestId"] == saved["requestId"]
                assert receipt["temporalRunId"] == row.execution.run_id
                assert all(
                    "mutation" not in q["query"]
                    and "workflowDefinitionById" not in q["query"]
                    for q in proxy.requests[before:]
                )
                reconciled = await asyncio.to_thread(
                    invoke,
                    world,
                    proxy,
                    tmp_path,
                    [
                        "workflow",
                        "definition-reconcile",
                        "--request-file",
                        str(path),
                        "--json",
                    ],
                )
                assert reconciled.returncode == 0, reconciled.stderr
                assert (
                    marker
                    not in recovered.stdout
                    + recovered.stderr
                    + reconciled.stdout
                    + reconciled.stderr
                )
                assert await sync_to_async(WorkflowDefinitionStart.objects.count)() == 1
            finally:
                await handle.terminate(reason="Disposable CLI proof complete")


async def test_native_definition_schema_change_and_sensitive_literal_refusal(
    settings, tmp_path
):
    world = await sync_to_async(create_definition_world)(accepts_inputs=True)
    with running_api() as port, definition_proxy(port) as proxy:
        reviewed = await asyncio.to_thread(
            invoke,
            world,
            proxy,
            tmp_path,
            ["workflow", "definition-review", str(world.definition.guid), "--json"],
        )
        assert reviewed.returncode == 0, reviewed.stderr
        review = json.loads(reviewed.stdout)
        await sync_to_async(
            WorkflowStage.objects.filter(definition=world.definition).update
        )(prompt="Changed stage metadata")
        inputs = tmp_path / "inputs.json"
        marker = "PRIVATE_LITERAL_INPUT_MARKER"
        inputs.write_text(json.dumps({"nested": {"values": [1]}, "credential": marker}))
        path = tmp_path / "changed.json"
        args = [
            "workflow",
            "definition-start",
            str(world.definition.guid),
            "--request-file",
            str(path),
            "--inputs-file",
            str(inputs),
            "--yes",
            "--json",
        ]
        changed = await asyncio.to_thread(
            invoke,
            world,
            proxy,
            tmp_path,
            [
                *args,
                "--expected-revision",
                review["revision"],
                "--expected-input-schema-digest",
                review["inputContract"]["digest"],
            ],
        )
        assert changed.returncode != 0 and "changed since review" in changed.stderr
        assert not path.exists()
        refused = await asyncio.to_thread(invoke, world, proxy, tmp_path, args)
        assert refused.returncode != 0, refused.stdout
        assert marker not in refused.stdout + refused.stderr + path.read_text()
        assert await sync_to_async(WorkflowDefinitionStart.objects.count)() == 0
        before = len(proxy.requests)
        absent = await asyncio.to_thread(invoke, world, proxy, tmp_path, args)
        assert absent.returncode != 0 and "unknown" in absent.stderr
        assert all("mutation" not in q["query"] for q in proxy.requests[before:])


async def test_native_definition_no_inputs_and_changed_actor_refuse_recovery(
    settings, tmp_path
):
    async with temporal_test_env() as engine:
        settings.ASTROLIFT_TEMPORAL_ENABLED = True
        settings.TEMPORAL_ADDRESS = engine.client.service_client.config.target_host
        settings.TEMPORAL_NAMESPACE = "default"
        settings.TEMPORAL_TASK_QUEUE = f"cli-no-input-{uuid4().hex}"
        world = await sync_to_async(create_definition_world)()
        other = await sync_to_async(create_definition_world)()
        path = tmp_path / "no-input-request.json"
        with running_api() as port, definition_proxy(port) as proxy:
            args = [
                "workflow",
                "definition-start",
                str(world.definition.guid),
                "--request-file",
                str(path),
                "--yes",
                "--json",
            ]
            started = await asyncio.to_thread(invoke, world, proxy, tmp_path, args)
            assert started.returncode == 0, started.stderr
            saved = json.loads(path.read_text())
            row = await sync_to_async(
                WorkflowDefinitionStart.objects.select_related("execution").get
            )(request_id=saved["requestId"])
            handle = engine.client.get_workflow_handle(
                row.execution.workflow_id, run_id=row.execution.run_id
            )
            try:
                assert json.loads(started.stdout)["start"]["executionId"] == str(
                    row.execution.guid
                )
                before = len(proxy.requests)
                wrong_scope = await asyncio.to_thread(
                    invoke, other, proxy, tmp_path, args
                )
                assert (
                    wrong_scope.returncode != 0
                    and "another server, organization, actor" in wrong_scope.stderr
                )
                assert all(
                    "mutation" not in q["query"]
                    and "workflowDefinitionStartRequest" not in q["query"]
                    for q in proxy.requests[before:]
                )
                assert await sync_to_async(WorkflowDefinitionStart.objects.count)() == 1
            finally:
                await handle.terminate(reason="Disposable no-input CLI proof complete")
