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
def asgi_endpoint(settings):
    from config.asgi import application

    settings.DEBUG = False
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    sock.listen()
    server = uvicorn.Server(
        uvicorn.Config(application, lifespan="off", access_log=False, log_level="error")
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


def invoke(binary, tmp_path, endpoint, bearer, org, *arguments):
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
        timeout=40,
        check=False,
    )
    assert bearer not in result.stdout + result.stderr
    assert "PRIVATE_NATIVE_SCRIPT" not in result.stdout + result.stderr
    assert "BEGIN PRIVATE KEY" not in result.stdout + result.stderr
    return result


def test_pinned_cli_vocabulary_matches_every_paired_server_action():
    from nvke.contract import upstream_revision
    from nvke.operations import actions

    snapshot = json.loads((CLI_ROOT / "internal/nvkecatalog/catalog.json").read_text())
    assert snapshot["revision"] == upstream_revision()
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
        yield state, identity


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
