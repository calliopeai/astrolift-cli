"""Compiled NVKE CLI against the paired App, real Postgres/Kubernetes/Temporal.

Run with the paired backend pytest configuration and its conftest plugin. This
module starts an owned loopback ASGI server and refuses ambient Kubernetes.
ASTROLIFT_NVKE_CLI selects the owned compiled binary; NVKE_TEST_KUBECONFIG must
point at the local NVKE envtest control plane. No API or activity is substituted.
"""

import asyncio
import json
import os
import socket
import subprocess
import threading
import time
from pathlib import Path
from uuid import uuid4

import pytest
import uvicorn
from asgiref.sync import sync_to_async
from astrolift_clusters.tests.test_nvke_operations import (
    datastore_site as _datastore_site,
)
from astrolift_clusters.tests.test_nvke_sites import world as _world
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken
from core.testing.temporal import temporal_test_env, temporal_worker
from temporalio import workflow
from temporalio.worker import UnsandboxedWorkflowRunner, Worker

pytestmark = pytest.mark.django_db(transaction=True)
CLI_ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def world():
    return _world.__wrapped__()


@pytest.fixture
def datastore_site(world):
    yield from _datastore_site.__wrapped__(world)


@pytest.fixture
def binary():
    path = os.getenv("ASTROLIFT_NVKE_CLI")
    if not path:
        pytest.skip("set ASTROLIFT_NVKE_CLI to an owned compiled binary")
    assert Path(path).is_file()
    return path


@pytest.fixture
def dropped_cli_replies():
    return {}


@pytest.fixture
def asgi_endpoint(settings, dropped_cli_replies):
    from config.asgi import application

    async def qualified_application(scope, receive, send):
        request_body = bytearray()
        dropping = False
        status = None
        started = time.monotonic()

        async def observed_receive():
            event = await receive()
            request_body.extend(event.get("body", b""))
            return event

        async def observed_send(event):
            nonlocal dropping, status
            if event["type"] == "http.response.start":
                status = event["status"]
            if event["type"] == "http.response.start" and event["status"] == 200:
                for field, remaining in dropped_cli_replies.items():
                    if remaining and field.encode() in request_body:
                        dropped_cli_replies[field] -= 1
                        dropping = True
                        await send(
                            {
                                "type": "http.response.start",
                                "status": 503,
                                "headers": [],
                            }
                        )
                        return
            if dropping:
                if event["type"] == "http.response.body" and not event.get(
                    "more_body", False
                ):
                    await send(
                        {"type": "http.response.body", "body": b"Reply unavailable"}
                    )
                return
            await send(event)

        completed = False
        try:
            await application(scope, observed_receive, observed_send)
            completed = True
        finally:
            from request_timing_qualification import record_request_timing

            record_request_timing(request_body, time.monotonic() - started, status, completed, dropping)

    settings.DEBUG = False
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    sock.listen()
    server = uvicorn.Server(
        uvicorn.Config(
            qualified_application, lifespan="off", access_log=False, log_level="error"
        )
    )
    thread = threading.Thread(target=lambda: server.run(sockets=[sock]), daemon=True)
    thread.start()
    try:
        deadline = time.monotonic() + 10
        while not server.started and thread.is_alive() and time.monotonic() < deadline:
            time.sleep(0.01)
        assert server.started
        yield f"http://127.0.0.1:{sock.getsockname()[1]}"
    finally:
        server.should_exit = True
        thread.join(timeout=10)
        sock.close()
        assert not thread.is_alive()


def invoke(binary, tmp_path, endpoint, bearer, org, *arguments, process_timeout=40):
    env = {
        **os.environ,
        "XDG_CONFIG_HOME": str(tmp_path / "config"),
        "ASTROLIFT_TOKEN": bearer,
        "ASTROLIFT_NO_UPDATE_CHECK": "1",
        "CI": "1",
    }
    env.pop("ASTROLIFT_DEPLOY_TOKEN", None)
    result = subprocess.run(
        [
            binary,
            "--api-url",
            endpoint,
            "--org",
            org,
            "--json",
            "nvke",
            *map(str, arguments),
        ],
        env=env,
        capture_output=True,
        text=True,
        timeout=process_timeout,
        check=False,
    )
    assert bearer not in result.stdout + result.stderr
    assert "PRIVATE_NATIVE_SCRIPT" not in result.stdout + result.stderr
    assert "BEGIN PRIVATE KEY" not in result.stdout + result.stderr
    return result


def test_pinned_cli_vocabulary_matches_every_paired_server_action():
    from nvke.contract import upstream_revision
    from nvke.native_workflows import workflows
    from nvke.operations import actions

    snapshot = json.loads((CLI_ROOT / "internal/nvkecatalog/catalog.json").read_text())
    assert snapshot["revision"] == upstream_revision()
    assert snapshot["nativeWorkflows"] == [
        {"name": item["name"], "permission": item["permission"]} for item in workflows()
    ]
    assert snapshot["actions"] == [
        {
            "key": item.key,
            "surface": item.surface,
            "verb": item.verb,
            "mutates": item.mutates,
            "permission": item.permission,
        }
        for item in actions()
    ]


async def test_compiled_cli_native_temporal_authority_lifecycle(
    binary, tmp_path, asgi_endpoint, world
):
    from astrolift_clusters import nvke_temporal
    from astrolift_clusters.models import NvkeTemporalAuthority

    issued = mint_token()
    await sync_to_async(ApiToken.objects.create)(
        user=world.user,
        organization=world.org,
        name="owned native authority CLI",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin"],
    )

    async def run(*arguments, ok=True):
        result = await asyncio.to_thread(
            invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            world.org.slug,
            *arguments,
        )
        assert "PRIVATE_NATIVE_CREDENTIAL" not in result.stdout + result.stderr
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    catalog = await run("catalog")
    assert {item["name"] for item in catalog["nativeWorkflows"]} == {
        "RenewK8sCertsV2",
        "RotateEtcdEncryptionKey",
        "RetireLostEtcdRotationNode",
    }
    site = await run(
        "site",
        "create",
        "--name",
        "Native Temporal CLI",
        "--slug",
        "temporal-cli",
        "--namespace",
        "org-owned",
    )
    scope = ("--site", site["id"])
    assert await run("temporal", "get", *scope) is None
    credentials = tmp_path / "native-temporal.json"
    credentials.write_text(
        json.dumps(
            {"apiKey": "PRIVATE_NATIVE_CREDENTIAL", "caPem": "PRIVATE_NATIVE_CA"}
        )
    )
    credentials.chmod(0o600)

    async with temporal_test_env() as env:
        target = (
            "--address",
            env.client.service_client.config.target_host,
            "--namespace",
            env.client.namespace,
            "--task-queue",
            "native-cli-explicit",
        )
        row = await run(
            "temporal",
            "configure",
            *scope,
            "--site-version",
            site["version"],
            *target,
            "--credentials-file",
            credentials,
        )
        assert row["hasCredentials"] and row["useTls"] and row["verificationCode"] == ""
        stored = await sync_to_async(NvkeTemporalAuthority.objects.get)(guid=row["id"])
        assert (await sync_to_async(nvke_temporal.credentials_for)(stored))[
            "api_key"
        ] == "PRIVATE_NATIVE_CREDENTIAL"
        original_ciphertext = bytes(stored.credential_ciphertext)
        assert b"PRIVATE_NATIVE_CREDENTIAL" not in original_ciphertext

        def review(value):
            return (
                *scope,
                "--site-version",
                value["siteVersion"],
                "--authority-version",
                value["version"],
            )

        row = await run("temporal", "configure", *review(row), *target)
        await sync_to_async(stored.refresh_from_db)()
        assert bytes(stored.credential_ciphertext) == original_ciphertext
        refused = await run(
            "temporal", "configure", *review(row), *target, "--plaintext", ok=False
        )
        assert "INVALID_INPUT" in refused.stderr
        row = await run(
            "temporal",
            "configure",
            *review(row),
            *target,
            "--plaintext",
            "--clear-credentials",
        )
        assert not row["hasCredentials"] and not row["useTls"]
        verified = await run("temporal", "verify", *review(row))
        assert (
            verified["verificationCode"] == "NAMESPACE_VERIFIED"
            and verified["verifiedAt"]
        )
        assert (
            verified["version"] == row["version"]
            and verified["siteVersion"] == row["siteVersion"]
        )
        row = await run("temporal", "disable", *review(verified))
        assert not row["isEnabled"] and row["verifiedAt"] is None
        refused = await run("temporal", "verify", *review(row), ok=False)
        assert "SOURCE_CHANGED" in refused.stderr
        row = await run("temporal", "enable", *review(row))
        assert row["isEnabled"] and row["verificationCode"] == ""
        row = await run("temporal", "verify", *review(row))
        stale = await run("temporal", "disable", *review(verified), ok=False)
        assert "VERSION_MISMATCH" in stale.stderr
        removed = await run("temporal", "remove", *review(row))
        assert removed["id"] == row["id"] and removed["deletedAt"]
        assert await run("temporal", "get", *scope) is None
        await sync_to_async(stored.refresh_from_db)()
        assert stored.deleted_at and not stored.is_enabled


async def test_compiled_cli_runs_native_crud_and_recovers_original_requests(
    binary, tmp_path, asgi_endpoint, datastore_site, monkeypatch
):
    from astrolift_clusters import nvke_sites
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow

    state = datastore_site
    issued = mint_token()
    await sync_to_async(ApiToken.objects.create)(
        user=state.world.user,
        organization=state.world.org,
        name="owned NVKE CLI",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin"],
    )

    async def run(*arguments, ok=True):
        result = await asyncio.to_thread(
            invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            state.world.org.slug,
            *arguments,
        )
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    async with temporal_test_env() as env:
        queue = "nvke-cli-" + uuid4().hex

        async def get_client():
            return env.client

        monkeypatch.setattr(workflow_client, "_get_client_async", get_client)
        monkeypatch.setattr(workflow_client, "_temporal_enabled", lambda: True)
        monkeypatch.setattr(workflow_client, "_task_queue", lambda: queue)
        async with temporal_worker(
            env,
            task_queue=queue,
            workflows=[NvkeOperationWorkflow],
            activities=[nvke_operation_attempt],
        ):
            catalog = await run("catalog")
            assert len(catalog["actions"]) == 88
            page = await run("site", "list", "--limit", "1")
            assert page["items"][0]["id"] == str(state.site.guid)
            shown = await run("site", "get", state.site.guid)
            assert shown["version"] == state.site.version

            request = tmp_path / "create-image.json"
            name = "astro-cli-" + uuid4().hex[:12]
            request.write_text(
                json.dumps(
                    {
                        "document": {
                            "metadata": {
                                "name": name,
                                "labels": {"astrolift.test/run": str(state.site.guid)},
                            },
                            "spec": {
                                "architecture": "amd64",
                                "buildDate": "2026-11-07",
                                "nico": {
                                    "ipxeScript": "#!ipxe\nchain https://boot.example.test/PRIVATE_NATIVE_SCRIPT.ipxe\n"
                                },
                                "operatingSystem": {
                                    "family": "ubuntu",
                                    "version": "24.04",
                                },
                            },
                        }
                    }
                )
            )
            request.chmod(0o600)
            recovery = tmp_path / "create-recovery.json"
            args = (
                "datastore",
                "create-machine-image",
                "--site",
                state.site.guid,
                "--file",
                request,
                "--request-file",
                recovery,
                "--wait",
                "--poll-interval",
                "500ms",
                "--wait-timeout",
                "15s",
            )
            operation = await run(*args)
            assert operation["status"] == "SUCCEEDED"
            assert "PRIVATE_NATIVE_SCRIPT" not in recovery.read_text()
            assert (await run(*args))["id"] == operation["id"]
            assert (await run("operation", "get", operation["id"]))[
                "status"
            ] == "SUCCEEDED"
            assert (await run("operation", "wait", operation["id"]))[
                "status"
            ] == "SUCCEEDED"
            history = await run("operation", "list", state.site.guid, "--limit", "1")
            assert history["items"][0]["id"] == operation["id"]
            output = tmp_path / "private-result.json"
            assert (
                await run("operation", "result", operation["id"], "--output", output)
            )["saved"]
            result = json.loads(output.read_text())
            assert "PRIVATE_NATIVE_SCRIPT" in output.read_text()
            metadata = result["observation"]["data"]["metadata"]
            assert metadata["uid"]

            filters = tmp_path / "inventory-filters.json"
            filters.write_text(
                json.dumps({"label_selector": f"astrolift.test/run={state.site.guid}"})
            )
            filters.chmod(0o600)
            snapshot = await run(
                "datastore",
                "list-machine-image",
                "--inventory",
                "--site",
                state.site.guid,
                "--file",
                filters,
                "--request-file",
                tmp_path / "inventory-recovery.json",
                "--wait",
                "--poll-interval",
                "500ms",
                "--wait-timeout",
                "15s",
            )
            assert snapshot["isInventory"] is True
            inventory = await run(
                "resource",
                "list",
                snapshot["id"],
                "--site",
                state.site.guid,
                "--search",
                name,
            )
            assert inventory["totalCount"] == 1
            key = inventory["items"][0]["key"]
            resource = await run(
                "resource",
                "get",
                snapshot["id"],
                "--site",
                state.site.guid,
                "--resource-key",
                key,
            )
            assert resource["item"]["name"] == name
            native_document = tmp_path / "private-resource-document.json"
            assert (
                await run(
                    "resource",
                    "document",
                    snapshot["id"],
                    "--site",
                    state.site.guid,
                    "--resource-key",
                    key,
                    "--output",
                    native_document,
                )
            )["saved"]
            assert "PRIVATE_NATIVE_SCRIPT" in native_document.read_text()
            editor_path = tmp_path / "private-editor.json"
            editor = await run(
                "resource",
                "editor",
                "datastore.replaceMachineImage",
                "--site",
                state.site.guid,
                "--source-operation",
                snapshot["id"],
                "--source-version",
                resource["operationVersion"],
                "--resource-key",
                key,
                "--output",
                editor_path,
            )
            assert (
                editor["concurrency"] == "native_version"
                and "parametersText" not in editor
            )
            draft = json.loads(editor_path.read_text())
            draft["document"]["metadata"]["annotations"] = {
                "astrolift.test/reviewed": "yes"
            }
            editor_path.write_text(json.dumps(draft))
            replaced = await run(
                "datastore",
                "replace-machine-image",
                "--site",
                state.site.guid,
                "--source-operation",
                snapshot["id"],
                "--source-version",
                resource["operationVersion"],
                "--resource-key",
                key,
                "--file",
                editor_path,
                "--request-file",
                tmp_path / "replace-recovery.json",
                "--wait",
                "--poll-interval",
                "500ms",
                "--wait-timeout",
                "15s",
            )
            assert replaced["resourceSource"]["operationId"] == snapshot["id"]
            replaced_result = tmp_path / "private-replaced-result.json"
            await run(
                "operation", "result", replaced["id"], "--output", replaced_result
            )
            metadata = json.loads(replaced_result.read_text())["observation"]["data"][
                "metadata"
            ]
            assert metadata["annotations"]["astrolift.test/reviewed"] == "yes"
            editor_path.unlink()
            recovered = await run(
                "operation",
                "recover",
                "--request-file",
                tmp_path / "replace-recovery.json",
            )
            assert recovered["id"] == replaced["id"]
            assert (await run("operation", "cancel", operation["id"]))[
                "status"
            ] == "SUCCEEDED"
            await run(
                "operation",
                "resume",
                operation["id"],
                "--version",
                operation["version"],
                ok=False,
            )

            delete = tmp_path / "delete-image.json"
            delete.write_text(
                json.dumps(
                    {
                        "name": name,
                        "uid": metadata["uid"],
                        "resource_version": metadata["resourceVersion"],
                        "propagation_policy": "Background",
                    }
                )
            )
            delete.chmod(0o600)
            deleted = await run(
                "operation",
                "request",
                "datastore.deleteMachineImage",
                "--site",
                state.site.guid,
                "--file",
                delete,
                "--request-file",
                tmp_path / "delete-recovery.json",
                "--wait",
                "--poll-interval",
                "500ms",
                "--wait-timeout",
                "15s",
            )
            assert deleted["status"] == "SUCCEEDED"
            assert (await run(*args))["id"] == operation["id"]

            # All site mutation documents are validated against the actual schema.
            site = await run(
                "site",
                "create",
                "--name",
                "CLI configured site",
                "--slug",
                "cli-site",
                "--namespace",
                "cli-owned",
            )
            credentials = await sync_to_async(nvke_sites.credentials_for)(
                state.connection
            )
            file = tmp_path / "credentials.json"
            file.write_text(
                json.dumps(
                    {
                        "caPem": credentials["ca_pem"],
                        "clientCertificatePem": credentials["client_certificate_pem"],
                        "clientKeyPem": credentials["client_key_pem"],
                    }
                )
            )
            file.chmod(0o600)
            site = await run(
                "site",
                "configure",
                site["id"],
                "datastore",
                "--site-version",
                site["version"],
                "--endpoint",
                state.connection.endpoint,
                "--credentials-file",
                file,
            )
            assert site["connections"][0]["hasCredentials"]
            site = await run(
                "site", "disable", site["id"], "--version", site["version"]
            )
            assert not site["isActive"]
            site = await run("site", "enable", site["id"], "--version", site["version"])
            assert site["isActive"]
            await run("site", "remove", site["id"], "--version", site["version"])
            await run("site", "get", site["id"], ok=False)


@pytest.fixture
def controller_site(world):
    from astrolift_clusters.tests.test_nvke_access import controller
    from astrolift_clusters.tests.test_nvke_operations import (
        configured as configured_fixture,
    )
    from astrolift_clusters.tests.test_nvke_sites import context, put

    state = configured_fixture.__wrapped__(world)
    identity = str(uuid4())
    details = {
        "parent_override": {
            "nextCertRenewalDue": "2000-01-01T00:00:00Z",
            "conditions": {
                "provisioned": {"status": "True"},
                "updateInProgress": {
                    "type": "certificate_renewal",
                    "message": "PRIVATE_MAINTENANCE_ERROR",
                },
                "certificateRenewal": {
                    "cycleID": "operator-更新-" + identity,
                    "state": "failed",
                    "forced": False,
                    "phase": "renew-node",
                    "message": "PRIVATE_CREDENTIAL_DETAIL",
                },
            },
        },
        "control_plane": {
            "config": {"desiredCount": 9007199254740993},
            "status": {
                "nodeCount": 1,
                "readyNodeCount": 0,
                "kubernetesVersion": "1.36.0",
                "controlPlaneUpgrading": {
                    "status": "Unknown",
                    "message": "PRIVATE_NATIVE_MESSAGE",
                },
                "cpNodesRecycling": {"status": "False"},
                "deletingNodeCount": 0,
                "deleteRequestedNodeCount": 0,
            },
        },
    }
    with controller({"body": {"name": "diagnostic-cluster"}}, identity, details) as (
        endpoint,
        _,
        calls,
    ):
        with context(world):
            put(
                state.site,
                expected_connection_version=state.connection.version,
                endpoint=endpoint,
                allow_http=True,
            )
            state.site.refresh_from_db()
        state.native_calls = calls
        state.native_details = details
        yield state, identity


@workflow.defn(name="RenewK8sCertsV2")
class NativeRenewalFixture:
    @workflow.run
    async def run(self, parameters: dict) -> None:
        return None


@workflow.defn(name="RotateEtcdEncryptionKey")
class NativeRotationFixture:
    @workflow.run
    async def run(self, parameters: dict) -> None:
        return None


@workflow.defn(name="RetireLostEtcdRotationNode")
class NativeRetirementFixture:
    @workflow.run
    async def run(self, parameters: dict) -> None:
        return None


@pytest.mark.parametrize(
    "kind", ["RenewK8sCertsV2", "RotateEtcdEncryptionKey", "RetireLostEtcdRotationNode"]
)
async def test_compiled_cli_native_workflow_admission_and_receipt_recovery(
    binary, tmp_path, asgi_endpoint, controller_site, monkeypatch, kind
):
    from astrolift_clusters.models import NvkeOperation
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow

    state, identity = controller_site
    issued = mint_token()
    await sync_to_async(ApiToken.objects.create)(
        user=state.world.user,
        organization=state.world.org,
        name="owned native workflow CLI",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin"],
    )

    async def run(*arguments, ok=True):
        result = await asyncio.to_thread(
            invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            state.world.org.slug,
            *arguments,
        )
        assert "PRIVATE" not in result.stdout + result.stderr
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    async with temporal_test_env() as env:
        native_queue, astro_queue = (
            "native-cli-" + uuid4().hex,
            "astro-native-cli-" + uuid4().hex,
        )

        async def get_client():
            return env.client

        monkeypatch.setattr(workflow_client, "_get_client_async", get_client)
        monkeypatch.setattr(workflow_client, "_temporal_enabled", lambda: True)
        monkeypatch.setattr(workflow_client, "_task_queue", lambda: astro_queue)
        authority = await run(
            "temporal",
            "configure",
            "--site",
            state.site.guid,
            "--site-version",
            state.site.version,
            "--address",
            env.client.service_client.config.target_host,
            "--namespace",
            env.client.namespace,
            "--task-queue",
            native_queue,
            "--plaintext",
        )
        owner_id = "retained-更新-" + uuid4().hex
        if kind == "RetireLostEtcdRotationNode":
            state.native_details["parent_override"]["conditions"] = {
                "updateInProgress": {"type": "etcd_key_rotation"},
                "etcdKeyRotation": {
                    "state": "failed",
                    "workflowID": owner_id,
                    "phase": "rewrite",
                },
            }
            owner = await env.client.start_workflow(
                "RotateEtcdEncryptionKey",
                {"clusterID": identity},
                id=owner_id,
                task_queue=native_queue,
            )
            await owner.terminate()
        async with (
            Worker(
                env.client,
                task_queue=native_queue,
                workflows=[
                    NativeRenewalFixture,
                    NativeRotationFixture,
                    NativeRetirementFixture,
                ],
                workflow_runner=UnsandboxedWorkflowRunner(),
            ),
            temporal_worker(
                env,
                task_queue=astro_queue,
                workflows=[NvkeOperationWorkflow],
                activities=[nvke_operation_attempt],
            ),
        ):
            read_input = tmp_path / "read-cluster.json"
            read_input.write_text(json.dumps({"path": {"clusterID": identity}}))
            read_input.chmod(0o600)
            source = await run(
                "controller",
                "get-cluster",
                "--site",
                state.site.guid,
                "--file",
                read_input,
                "--request-file",
                tmp_path / "read-recovery.json",
                "--wait",
                "--wait-timeout",
                "15s",
                "--poll-interval",
                "500ms",
            )
            parameters = {"clusterID": identity}
            if kind == "RenewK8sCertsV2":
                parameters.update(thresholdDays=120, force=True)
            if kind == "RetireLostEtcdRotationNode":
                parameters.update(nodeID=str(uuid4()), rotationWorkflowID=owner_id)
            native_input = tmp_path / "native-input.json"
            native_input.write_text(json.dumps(parameters, ensure_ascii=False))
            native_input.chmod(0o600)
            recovery = tmp_path / "native-recovery.json"
            flags = (
                "--site",
                state.site.guid,
                "--site-version",
                authority["siteVersion"],
                "--authority-version",
                authority["version"],
                "--source-operation",
                source["id"],
                "--source-version",
                source["version"],
                "--file",
                native_input,
                "--request-file",
                recovery,
            )
            if kind == "RetireLostEtcdRotationNode":
                refused = await run(
                    "temporal", "workflow", "request", kind, *flags, ok=False
                )
                assert "teardown acknowledgement" in refused.stderr
                assert not recovery.exists()
                flags += ("--acknowledge-lost-node-teardown",)
            row = await run("temporal", "workflow", "request", kind, *flags)
            assert row["operationKey"] == "temporal." + kind
            record = json.loads(recovery.read_text())
            assert (
                record["authorityVersion"] == authority["version"]
                and record["sourceVersion"] == source["version"]
            )
            assert (
                "clusterID" not in recovery.read_text()
                and "parametersText" not in recovery.read_text()
            )
            operation = await sync_to_async(NvkeOperation.objects.get)(guid=row["id"])
            astro_handle = env.client.get_workflow_handle(operation.workflow_id)
            assert (
                await asyncio.wait_for(astro_handle.result(), timeout=45) == "succeeded"
            )
            assert identity not in (await astro_handle.fetch_history()).to_json()
            native_handle = env.client.get_workflow_handle("astro-nvke-" + row["id"])
            started = (
                (await native_handle.fetch_history())
                .events[0]
                .workflow_execution_started_event_attributes
            )
            assert await env.client.data_converter.decode(started.input.payloads) == [
                parameters
            ]
            native_input.unlink()
            repeated = await run("temporal", "workflow", "request", kind, *flags)
            recovered = await run(
                "temporal", "workflow", "recover", "--request-file", recovery, "--wait"
            )
            assert (
                repeated["id"] == recovered["id"] == row["id"]
                and recovered["status"] == "SUCCEEDED"
            )
            assert (
                await sync_to_async(
                    NvkeOperation.objects.filter(
                        site=state.site, operation_key="temporal." + kind
                    ).count
                )()
                == 1
            )


async def test_compiled_cli_bound_control_plane_read_and_typed_diagnostics(
    binary, tmp_path, asgi_endpoint, controller_site, monkeypatch
):
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow

    state, identity = controller_site
    issued = mint_token()
    await sync_to_async(ApiToken.objects.create)(
        user=state.world.user,
        organization=state.world.org,
        name="owned NVKE diagnostic CLI",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin"],
    )

    async def run(*args, ok=True):
        result = await asyncio.to_thread(
            invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            state.world.org.slug,
            *args,
        )
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    async with temporal_test_env() as env:
        queue = "nvke-cli-diagnostics-" + uuid4().hex

        async def get_client():
            return env.client

        monkeypatch.setattr(workflow_client, "_get_client_async", get_client)
        monkeypatch.setattr(workflow_client, "_temporal_enabled", lambda: True)
        monkeypatch.setattr(workflow_client, "_task_queue", lambda: queue)
        async with temporal_worker(
            env,
            task_queue=queue,
            workflows=[NvkeOperationWorkflow],
            activities=[nvke_operation_attempt],
        ):
            native = tmp_path / "get-cluster.json"
            native.write_text(json.dumps({"path": {"clusterID": identity}}))
            native.chmod(0o600)
            source = await run(
                "controller",
                "get-cluster",
                "--site",
                state.site.guid,
                "--file",
                native,
                "--request-file",
                tmp_path / "source-recovery.json",
                "--wait",
                "--poll-interval",
                "500ms",
                "--wait-timeout",
                "15s",
            )
            access_scope = ("--site", state.site.guid)
            before = len(state.native_calls)
            maintenance = await run(
                "operation",
                "maintenance",
                source["id"],
                *access_scope,
                "--version",
                source["version"],
            )
            assert maintenance["clusterId"] == identity
            assert maintenance["renewal"]["workflowId"] == "operator-更新-" + identity
            assert maintenance["renewal"]["retainedMutationLock"] is True
            assert maintenance["renewal"]["forced"] is False
            assert maintenance["renewal"]["outcome"] is None
            assert maintenance["etcdKeyRotation"] is None
            assert maintenance["renewalScheduleOverdueAtObservation"] is True
            assert len(state.native_calls) == before
            assert "PRIVATE" not in json.dumps(maintenance)
            enrollment = (
                "access",
                "enroll",
                source["id"],
                *access_scope,
                "--site-version",
                state.site.version,
                "--source-version",
                source["version"],
            )
            native_calls = len(state.native_calls)
            assert (await run("access", "list", *access_scope))["totalCount"] == 0
            binding = await run(*enrollment)
            assert binding["clusterId"] == identity
            assert binding["sourceOperationId"] == source["id"]
            assert (await run(*enrollment))["id"] == binding["id"]
            page = await run("access", "list", *access_scope, "--limit", "1")
            assert page["totalCount"] == 1
            assert page["items"][0]["id"] == binding["id"]
            revoke = (
                "access",
                "revoke",
                binding["id"],
                *access_scope,
                "--site-version",
                state.site.version,
                "--version",
            )
            stale = await run(*revoke, binding["version"] + 1, ok=False)
            assert "VERSION_MISMATCH" in stale.stderr
            revoked = await run(*revoke, binding["version"])
            assert revoked["deletedAt"] is not None
            assert (await run("access", "list", *access_scope))["totalCount"] == 0
            replay = await run(*enrollment, ok=False)
            assert "SOURCE_CHANGED" in replay.stderr
            assert len(state.native_calls) == native_calls
            read = await run(
                "controller",
                "get-cluster-control-plane",
                "--site",
                state.site.guid,
                "--source-operation",
                source["id"],
                "--source-version",
                source["version"],
                "--request-file",
                tmp_path / "diagnostic-recovery.json",
                "--wait",
                "--poll-interval",
                "500ms",
                "--wait-timeout",
                "15s",
            )
            page = await run(
                "operation",
                "diagnostics",
                read["id"],
                "--site",
                state.site.guid,
                "--version",
                read["version"],
                "--limit",
                "1",
            )
            assert page["summary"]["clusterId"] == identity
            counts = {item["name"]: item["value"] for item in page["summary"]["counts"]}
            assert (
                counts["desiredCount"] == "9007199254740993"
                and counts["readyNodeCount"] == "0"
            )
            assert counts["managedNodeCount"] is None
            assert "PRIVATE" not in json.dumps(page)


async def test_compiled_cli_native_owner_history_and_reset_recovery(
    binary, tmp_path, asgi_endpoint, controller_site, monkeypatch
):
    from astrolift_clusters import nvke_native_reset
    from astrolift_clusters.models import NvkeOperation
    from astrolift_clusters.tests.test_nvke_native_reset import (
        REWRITE_CALLS,
        FailedRotation,
        begin_activity,
        prepare_activity,
        record_phase,
        rewrite_activity,
    )
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow
    from temporalio.client import WorkflowFailureError

    state, identity = controller_site
    issued = mint_token()
    await sync_to_async(ApiToken.objects.create)(
        user=state.world.user,
        organization=state.world.org,
        name="owned native reset CLI",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["read:nvke", "admin:nvke"],
    )

    async def run(*arguments, ok=True):
        result = await asyncio.to_thread(
            invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            state.world.org.slug,
            *arguments,
        )
        assert "PRIVATE" not in result.stdout + result.stderr
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    async with temporal_test_env() as env:
        native_queue, astro_queue = (
            "native-reset-cli-" + uuid4().hex,
            "astro-reset-cli-" + uuid4().hex,
        )

        async def get_client():
            return env.client

        monkeypatch.setattr(workflow_client, "_get_client_async", get_client)
        monkeypatch.setattr(workflow_client, "_temporal_enabled", lambda: True)
        monkeypatch.setattr(workflow_client, "_task_queue", lambda: astro_queue)
        authority = await run(
            "temporal",
            "configure",
            "--site",
            state.site.guid,
            "--site-version",
            state.site.version,
            "--address",
            env.client.service_client.config.target_host,
            "--namespace",
            env.client.namespace,
            "--task-queue",
            native_queue,
            "--plaintext",
            "--connection-timeout",
            "120",
        )
        owner_id = "rotation-更新-" + uuid4().hex
        state.native_details["parent_override"]["conditions"] = {
            "updateInProgress": {"type": "etcd_key_rotation"},
            "etcdKeyRotation": {
                "state": "failed",
                "workflowID": owner_id,
                "phase": "rewrite",
            },
        }
        reset_calls = []
        send = nvke_native_reset.send_reset

        async def lose_reset_reply(*args):
            reset_calls.append(args[3])
            await send(*args)
            raise RuntimeError("PRIVATE_LOST_RESET_REPLY")

        monkeypatch.setattr(nvke_native_reset, "send_reset", lose_reset_reply)
        async with (
            Worker(
                env.client,
                task_queue=native_queue,
                workflows=[FailedRotation, NativeRenewalFixture],
                activities=[
                    begin_activity,
                    record_phase,
                    prepare_activity,
                    rewrite_activity,
                ],
                workflow_runner=UnsandboxedWorkflowRunner(),
            ),
            temporal_worker(
                env,
                task_queue=astro_queue,
                workflows=[NvkeOperationWorkflow],
                activities=[nvke_operation_attempt],
            ),
        ):
            handle = await env.client.start_workflow(
                "RotateEtcdEncryptionKey",
                {"clusterID": identity},
                id=owner_id,
                task_queue=native_queue,
            )
            with pytest.raises(WorkflowFailureError):
                await asyncio.wait_for(handle.result(), timeout=15)
            read_input = tmp_path / "read-for-reset.json"
            read_input.write_text(json.dumps({"path": {"clusterID": identity}}))
            read_input.chmod(0o600)
            source = await run(
                "controller",
                "get-cluster",
                "--site",
                state.site.guid,
                "--file",
                read_input,
                "--request-file",
                tmp_path / "source-reset-recovery.json",
                "--wait",
                "--wait-timeout",
                "20s",
                "--poll-interval",
                "500ms",
            )
            review_flags = (
                "--site",
                state.site.guid,
                "--site-version",
                authority["siteVersion"],
                "--authority-version",
                authority["version"],
                "--source-operation",
                source["id"],
                "--source-version",
                source["version"],
            )
            page = await run(
                "temporal",
                "workflow",
                "history",
                "RotateEtcdEncryptionKey",
                *review_flags,
                "--limit",
                "10",
            )
            assert (
                page["workflowId"] == owner_id and page["runId"] == handle.result_run_id
            )
            assert page["status"] == "FAILED" and page["recordedPhase"] == "rewrite"
            assert page["retainedMutationLock"] is True and page["sourceObservedAt"]
            old_cursor = page["nextCursor"]
            assert old_cursor
            events = list(page["items"])
            while page["nextCursor"]:
                page = await run(
                    "temporal",
                    "workflow",
                    "history",
                    "RotateEtcdEncryptionKey",
                    *review_flags,
                    "--limit",
                    "10",
                    "--after",
                    page["nextCursor"],
                )
                assert page["runId"] == handle.result_run_id
                events.extend(page["items"])
            selected = next(
                e for e in events if e["activityType"] == "StartEtcdEncryptionRewrite"
            )
            assert all(isinstance(e["eventId"], str) for e in events)
            reset_input = tmp_path / "reviewed-reset.json"
            review = {
                "workflowId": owner_id,
                "baseRunId": handle.result_run_id,
                "workflowTaskEventId": selected["workflowTaskCompletedEventId"],
                "activityEventId": selected["eventId"],
                "activityType": "StartEtcdEncryptionRewrite",
                "phase": "rewrite",
                "mode": "restart_rewrite",
                "rationale": "PRIVATE_RESET_REASON",
                "quiescenceEvidence": "PRIVATE_AGENT_EVIDENCE",
                "acknowledgeBoundaryReviewed": True,
                "acknowledgeConflictingWorkStopped": True,
            }
            reset_input.write_text(json.dumps(review, ensure_ascii=False))
            reset_input.chmod(0o600)
            recovery = tmp_path / "rotation-reset-recovery.json"
            flags = (
                *review_flags,
                "--file",
                reset_input,
                "--request-file",
                recovery,
                "--wait",
                "--wait-timeout",
                "1ms",
                "--poll-interval",
                "500ms",
            )
            local_wait = await run("temporal", "workflow", "reset", *flags, ok=False)
            assert (
                "local wait ended" in local_wait.stderr
                and "not been cancelled" in local_wait.stderr
            )
            deadline = time.monotonic() + 150
            while time.monotonic() < deadline:
                row = await run(
                    "temporal",
                    "workflow",
                    "reset",
                    "recover",
                    "--request-file",
                    recovery,
                )
                if row["status"] == "SUCCEEDED":
                    break
                if row["status"] not in {
                    "QUEUED",
                    "RUNNING",
                    "AWAITING_READY",
                    "RECONCILING",
                }:
                    pytest.fail(
                        "Original reset receipt did not complete: "
                        + json.dumps(
                            {k: row.get(k) for k in ["status", "stage", "errorCode"]}
                        )
                    )
                await asyncio.sleep(0.5)
            else:
                pytest.fail(
                    "Original reset receipt did not reach completion within qualification deadline"
                )
            assert (
                row["operationKey"] == "temporal.reset.RotateEtcdEncryptionKey"
                and row["status"] == "SUCCEEDED"
            )
            assert len(reset_calls) == 1 and REWRITE_CALLS[identity] == 2
            metadata = recovery.read_text()
            assert "PRIVATE" not in metadata and owner_id not in metadata
            reset_input.unlink()
            repeated = await run("temporal", "workflow", "reset", *flags)
            recovered = await run(
                "temporal",
                "workflow",
                "reset",
                "recover",
                "--request-file",
                recovery,
                "--wait",
            )
            assert (
                repeated["id"] == recovered["id"] == row["id"] and len(reset_calls) == 1
            )
            assert (
                await sync_to_async(
                    NvkeOperation.objects.filter(
                        site=state.site,
                        operation_key="temporal.reset.RotateEtcdEncryptionKey",
                    ).count
                )()
                == 1
            )
            operation = await sync_to_async(NvkeOperation.objects.get)(guid=row["id"])
            astro = env.client.get_workflow_handle(operation.workflow_id)
            astro_history = (await astro.fetch_history()).to_json()
            assert "PRIVATE" not in astro_history and identity not in astro_history
            stale = await run(
                "temporal",
                "workflow",
                "history",
                "RotateEtcdEncryptionKey",
                *review_flags,
                "--limit",
                "10",
                "--after",
                old_cursor,
                ok=False,
            )
            assert "SOURCE_CHANGED" in stale.stderr and not stale.stdout
            refreshed = await run(
                "temporal",
                "workflow",
                "history",
                "RotateEtcdEncryptionKey",
                *review_flags,
            )
            assert (
                refreshed["status"] == "COMPLETED"
                and refreshed["recordedState"] == "failed"
            )
            assert refreshed["runId"] != handle.result_run_id

            renewal_owner = "renewal-history-更新-" + uuid4().hex
            renewal = await env.client.start_workflow(
                "RenewK8sCertsV2",
                {"clusterID": identity, "thresholdDays": 120, "force": True},
                id=renewal_owner,
                task_queue=native_queue,
            )
            await asyncio.wait_for(renewal.result(), timeout=15)
            state.native_details["parent_override"]["conditions"] = {
                "certificateRenewal": {
                    "cycleID": renewal_owner,
                    "state": "succeeded",
                    "forced": True,
                },
            }
            renewal_source = await run(
                "controller",
                "get-cluster",
                "--site",
                state.site.guid,
                "--file",
                read_input,
                "--request-file",
                tmp_path / "renewal-history-source.json",
                "--wait",
                "--wait-timeout",
                "20s",
                "--poll-interval",
                "500ms",
            )
            renewal_page = await run(
                "temporal",
                "workflow",
                "history",
                "RenewK8sCertsV2",
                "--site",
                state.site.guid,
                "--site-version",
                authority["siteVersion"],
                "--authority-version",
                authority["version"],
                "--source-operation",
                renewal_source["id"],
                "--source-version",
                renewal_source["version"],
            )
            assert renewal_page["workflowId"] == renewal_owner
            assert (
                renewal_page["status"] == "COMPLETED"
                and renewal_page["recordedState"] == "succeeded"
            )


@pytest.fixture
def metrics_listener():
    from astrolift_clusters.tests.test_nvke_credential_observations import listener

    yield from listener.__wrapped__()


async def test_compiled_cli_metrics_authority_observations_and_read_only_recovery(
    binary,
    tmp_path,
    asgi_endpoint,
    controller_site,
    metrics_listener,
    monkeypatch,
    dropped_cli_replies,
):
    from astrolift_clusters.models import NvkeMetricsAuthority, NvkeOperation
    from astrolift_clusters.nvke_sites import credentials_for
    from astrolift_clusters.tests.test_nvke_credential_observations import sample
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow

    state, identity = controller_site
    issued, reader = mint_token(), mint_token()
    for token, scopes in (
        (issued, ["read:nvke", "admin:nvke"]),
        (reader, ["read:nvke"]),
    ):
        await sync_to_async(ApiToken.objects.create)(
            user=state.world.user,
            organization=state.world.org,
            name="owned credential metrics CLI",
            token_hash=token.token_hash,
            token_last_4=token.last4,
            scopes=scopes,
        )

    async def run(*arguments, ok=True, read_only=False):
        result = await asyncio.to_thread(
            invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            reader.plaintext if read_only else issued.plaintext,
            state.world.org.slug,
            *arguments,
        )
        assert "PRIVATE" not in result.stdout + result.stderr
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    scope = ("--site", state.site.guid)
    catalog = await run("metrics", "catalog", read_only=True)
    assert len(catalog["credentialMetrics"]["metrics"]) == 18
    assert len(catalog["credentialMetrics"]["credentials"]) == 16
    assert await run("metrics", "get", *scope, read_only=True) is None
    credentials = tmp_path / "metrics-credentials.json"
    credentials.write_text(json.dumps({"token": "PRIVATE_METRICS_TOKEN"}))
    credentials.chmod(0o600)
    target = ("--endpoint", metrics_listener.url, "--allow-http")
    authority = await run(
        "metrics",
        "configure",
        *scope,
        "--site-version",
        state.site.version,
        *target,
        "--credentials-file",
        credentials,
    )
    stored = await sync_to_async(NvkeMetricsAuthority.objects.get)(guid=authority["id"])
    original_ciphertext = bytes(stored.credential_ciphertext)
    assert b"PRIVATE_METRICS_TOKEN" not in original_ciphertext
    assert await sync_to_async(credentials_for)(stored) == {
        "token": "PRIVATE_METRICS_TOKEN"
    }

    def review(value):
        return (
            *scope,
            "--site-version",
            value["siteVersion"],
            "--authority-version",
            value["version"],
        )

    verified = await run("metrics", "verify", *review(authority))
    assert verified["verificationCode"] == "METRICS_READ" and verified["verifiedAt"]
    assert verified["version"] == authority["version"]
    authority = await run("metrics", "configure", *review(authority), *target)
    await sync_to_async(stored.refresh_from_db)()
    assert bytes(stored.credential_ciphertext) == original_ciphertext
    assert not authority["verifiedAt"] and not authority["verificationCode"]
    node = {"tenant_cluster": "diagnostic-cluster", "node": "cp-1"}
    metrics_listener.body += "".join(
        [
            sample("credential_monitoring_enabled", 0),
            sample("certificate_inspection_available", 0, **node),
            sample("certificate_observation_age_seconds", "7200.125", **node),
            sample("certificate_required", 0, **node, credential="super-admin"),
            sample(
                "certificate_observation_available", 1, **node, credential="super-admin"
            ),
            sample(
                "certificate_not_after_timestamp_seconds",
                1,
                **node,
                credential="super-admin",
            ),
            sample(
                "serving_csr_count",
                "9007199254740993",
                tenant_cluster="diagnostic-cluster",
                state="pending",
            ),
            sample(
                "certificate_inspection_available",
                1,
                tenant_cluster="PRIVATE_FOREIGN",
                node="PRIVATE_NODE",
            ),
        ]
    )
    async with temporal_test_env() as env:
        queue = "nvke-cli-metrics-" + uuid4().hex

        async def get_client():
            return env.client

        monkeypatch.setattr(workflow_client, "_get_client_async", get_client)
        monkeypatch.setattr(workflow_client, "_temporal_enabled", lambda: True)
        monkeypatch.setattr(workflow_client, "_task_queue", lambda: queue)
        async with temporal_worker(
            env,
            task_queue=queue,
            workflows=[NvkeOperationWorkflow],
            activities=[nvke_operation_attempt],
        ):
            native = tmp_path / "metric-source.json"
            native.write_text(json.dumps({"path": {"clusterID": identity}}))
            native.chmod(0o600)
            wait = ("--wait", "--poll-interval", "500ms", "--wait-timeout", "15s")
            source = await run(
                "controller",
                "get-cluster",
                *scope,
                "--file",
                native,
                "--request-file",
                tmp_path / "source-recovery.json",
                *wait,
            )
            before_native, before_metrics = (
                len(state.native_calls),
                len(metrics_listener.calls),
            )
            recovery = tmp_path / "credential-recovery.json"
            dropped_cli_replies["requestNvkeCredentialObservation("] = 1
            lost = await run(
                "metrics",
                "request",
                *scope,
                "--site-version",
                authority["siteVersion"],
                "--authority-version",
                authority["version"],
                "--source-operation",
                source["id"],
                "--source-version",
                source["version"],
                "--request-file",
                recovery,
                *wait,
                ok=False,
            )
            assert "preserve" in lost.stderr
            assert dropped_cli_replies["requestNvkeCredentialObservation("] == 0
            row = await run("metrics", "recover", "--request-file", recovery, *wait)
            assert (
                row["status"] == "SUCCEEDED"
                and row["operationKey"] == "metrics.credentials"
            )
            assert len(metrics_listener.calls) == before_metrics + 1
            assert len(state.native_calls) == before_native + 2
            assert metrics_listener.calls[-1] == (
                "/private-metrics",
                "Bearer PRIVATE_METRICS_TOKEN",
            )
            original_recovery = recovery.read_bytes()
            assert recovery.stat().st_mode & 0o777 == 0o600
            assert (
                identity.encode() not in original_recovery
                and b"diagnostic-cluster" not in original_recovery
            )
            record = json.loads(original_recovery)
            assert (
                record["sourceOperationId"] == source["id"]
                and record["sourceVersion"] == source["version"]
            )
            operation = await sync_to_async(NvkeOperation.objects.get)(guid=row["id"])
            history = (
                await env.client.get_workflow_handle(
                    operation.workflow_id
                ).fetch_history()
            ).to_json()
            for private in ("PRIVATE", "diagnostic-cluster", "cp-1", "samples"):
                assert private not in history
            observed = await run(
                "metrics",
                "observation",
                row["id"],
                *scope,
                "--version",
                row["version"],
                read_only=True,
            )
            assert (
                observed["clusterId"] == identity
                and observed["clusterReported"] is True
            )
            assert (
                observed["collectionSucceeded"] is False
                and observed["monitoringEnabled"] is False
            )
            assert observed["renewalAutomationEnabled"] is None
            certificates = await run(
                "metrics",
                "certificates",
                row["id"],
                *scope,
                "--version",
                row["version"],
                read_only=True,
            )
            assert certificates["totalCount"] == 1
            cert = certificates["items"][0]
            assert (
                cert["required"] is False and cert["latestInspectionAvailable"] is False
            )
            assert (
                cert["observationAvailable"] is True
                and cert["reportedExpiredAtScrape"] is True
            )
            samples = await run(
                "metrics",
                "samples",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--search",
                "serving_csr",
                read_only=True,
            )
            assert samples["items"][0]["value"] == "9007199254740993"
            first = await run(
                "metrics",
                "samples",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--limit",
                2,
                read_only=True,
            )
            second = await run(
                "metrics",
                "samples",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--limit",
                2,
                "--after",
                first["nextCursor"],
                read_only=True,
            )
            assert not {item["key"] for item in first["items"]} & {
                item["key"] for item in second["items"]
            }
            await run(
                "metrics",
                "certificates",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--limit",
                2,
                "--after",
                first["nextCursor"],
                read_only=True,
                ok=False,
            )
            await run(
                "metrics",
                "observation",
                row["id"],
                *scope,
                "--version",
                row["version"] - 1,
                read_only=True,
                ok=False,
            )
            await run(
                "operation",
                "result",
                row["id"],
                "--output",
                tmp_path / "forbidden-result.json",
                read_only=True,
                ok=False,
            )
            assert not (tmp_path / "forbidden-result.json").exists()
            authority = await run(
                "metrics",
                "configure",
                *review(authority),
                *target,
                "--clear-credentials",
            )
            assert not authority["hasCredentials"]
            await sync_to_async(stored.refresh_from_db)()
            assert await sync_to_async(credentials_for)(stored) == {}
            authority = await run("metrics", "disable", *review(authority))
            assert not authority["isEnabled"]
            await run("metrics", "verify", *review(authority), ok=False)
            authority = await run("metrics", "enable", *review(authority))
            assert authority["isEnabled"]
            authority = await run("metrics", "remove", *review(authority))
            assert authority["deletedAt"] and not authority["isEnabled"]
            assert await run("metrics", "get", *scope, read_only=True) is None
            repeated = await run(
                "metrics",
                "request",
                *scope,
                "--request-file",
                recovery,
                "--authority-version",
                999,
                read_only=True,
            )
            recovered = await run(
                "metrics", "recover", "--request-file", recovery, read_only=True
            )
            assert repeated["id"] == recovered["id"] == row["id"]
            assert recovery.read_bytes() == original_recovery
            assert (
                await run(
                    "metrics",
                    "observation",
                    row["id"],
                    *scope,
                    "--version",
                    row["version"],
                    read_only=True,
                )
            )["clusterId"] == identity
            assert (
                await sync_to_async(
                    NvkeOperation.objects.filter(
                        site=state.site, operation_key="metrics.credentials"
                    ).count
                )()
                == 1
            )
            assert len(metrics_listener.calls) == before_metrics + 1
            assert len(state.native_calls) == before_native + 2


@pytest.fixture
def provider_listener():
    from astrolift_clusters.tests.test_nvke_provider_observations import peer

    yield from peer.__wrapped__()


@pytest.fixture
def provider_site(provider_listener):
    from astrolift_clusters.tests.test_nvke_provider_observations import configured

    return configured.__wrapped__(provider_listener)


async def test_compiled_cli_provider_collection_recovery_history_and_identity_pages(
    binary, tmp_path, asgi_endpoint, provider_site, monkeypatch, dropped_cli_replies
):
    from astrolift_clusters.models import NvkeOperation
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow

    state = provider_site
    issued, reader = mint_token(), mint_token()
    for token, scopes in (
        (issued, ["read:nvke", "admin:nvke"]),
        (reader, ["read:nvke"]),
    ):
        await sync_to_async(ApiToken.objects.create)(
            user=state.world.user,
            organization=state.world.org,
            name="owned provider CLI",
            token_hash=token.token_hash,
            token_last_4=token.last4,
            scopes=scopes,
        )

    async def run(*arguments, ok=True, read_only=False):
        result = await asyncio.to_thread(
            invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            reader.plaintext if read_only else issued.plaintext,
            state.world.org.slug,
            "providers",
            *arguments,
        )
        for secret in (issued.plaintext, reader.plaintext, "PRIVATE"):
            assert secret not in result.stdout + result.stderr
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    async with temporal_test_env() as env:
        queue = "nvke-cli-providers-" + uuid4().hex

        async def get_client():
            return env.client

        monkeypatch.setattr(workflow_client, "_get_client_async", get_client)
        monkeypatch.setattr(workflow_client, "_temporal_enabled", lambda: True)
        monkeypatch.setattr(workflow_client, "_task_queue", lambda: queue)
        async with temporal_worker(
            env,
            task_queue=queue,
            workflows=[NvkeOperationWorkflow],
            activities=[nvke_operation_attempt],
        ):
            catalog = await run("catalog", read_only=True)
            assert (
                catalog["infrastructureProviders"]["operationKey"]
                == "infrastructure.providers"
            )
            scope = ("--site", state.site.guid)
            recovery = tmp_path / "provider-request.json"
            wait = ("--wait", "--poll-interval", "500ms", "--wait-timeout", "15s")
            dropped_cli_replies["requestNvkeProviderObservation("] = 1
            lost = await run(
                "request",
                *scope,
                "--site-version",
                state.site.version,
                "--connection",
                state.connection.guid,
                "--connection-version",
                state.connection.version,
                "--request-file",
                recovery,
                ok=False,
            )
            assert "preserve" in lost.stderr
            assert dropped_cli_replies["requestNvkeProviderObservation("] == 0
            original = recovery.read_bytes()
            assert recovery.stat().st_mode & 0o777 == 0o600
            for private in (
                state.peer.url.encode(),
                b"PRIVATE",
                b"capnico-system",
                b"nico-credentials",
            ):
                assert private not in original
            row = await run(
                "recover", "--request-file", recovery, *wait, read_only=True
            )
            assert row["status"] == "SUCCEEDED"
            assert row["providerSource"]["connectionId"] == str(state.connection.guid)
            assert (
                row["providerSource"]["connectionVersion"] == state.connection.version
            )
            assert (
                await sync_to_async(
                    NvkeOperation.objects.filter(
                        site=state.site, operation_key="infrastructure.providers"
                    ).count
                )()
                == 1
            )
            calls = len(state.peer.calls)
            assert calls > 0
            await sync_to_async(state.connection.soft_delete)()
            await sync_to_async(state.site.save)()
            again = await run("recover", "--request-file", recovery, read_only=True)
            assert again["id"] == row["id"] and recovery.read_bytes() == original
            observed = await run(
                "observation", row["id"], *scope, "--version", row["version"]
            )
            health = observed["providers"][0]["credentials"]
            assert health["identityGeneration"] == "9007199254740993"
            assert health["lastCheckedTime"].endswith("123456789Z")
            await run(
                "observation",
                row["id"],
                *scope,
                "--version",
                row["version"],
                read_only=True,
                ok=False,
            )
            first = await run(
                "identities", row["id"], *scope, "--version", row["version"]
            )
            assert first["totalCount"] == 31 and len(first["items"]) == 25
            second = await run(
                "identities",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--after",
                first["nextCursor"],
            )
            assert len(second["items"]) == 6
            searched = await run(
                "identities",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--search",
                "extra-030",
            )
            assert (
                searched["totalCount"] == 1
                and searched["items"][0]["name"] == "extra-030"
            )
            await run(
                "identities",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--search",
                "extra-030",
                "--after",
                first["nextCursor"],
                ok=False,
            )
            launchlayer = await run(
                "identities",
                row["id"],
                *scope,
                "--version",
                row["version"],
                "--provider",
                "launchlayer",
            )
            assert launchlayer["items"] == [] and launchlayer["totalCount"] == 0
            assert len(state.peer.calls) == calls
            operation = await sync_to_async(NvkeOperation.objects.get)(guid=row["id"])
            history = (
                await env.client.get_workflow_handle(
                    operation.workflow_id
                ).fetch_history()
            ).to_json()
            for private in (
                "PRIVATE",
                "capnico-system",
                "nico-credentials",
                "9007199254740993",
            ):
                assert private not in history
