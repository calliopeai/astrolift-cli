"""Opt-in compiled CLI -> real Astro -> isolated native NVKE site qualification.

Run with the paired backend pytest settings/conftest. The explicit private profile
binds the owned native site. Only HTTP transport uses the local SSH SOCKS route;
the real API, authorization, encrypted outbox, activities and Temporal remain intact.
"""

import asyncio
import base64
import hashlib
import importlib.util
import ipaddress
import json
import os
import ssl
import stat
import tempfile
from datetime import UTC, datetime
from pathlib import Path
from types import SimpleNamespace
from urllib.parse import urlsplit
from uuid import uuid4

import httpx
import pytest
import test_nvke_contracts as paired
import yaml
from asgiref.sync import sync_to_async
from astrolift_clusters.tests.test_nvke_sites import context, put
from astrolift_clusters.tests.test_nvke_sites import world as _world
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken
from core.testing.temporal import temporal_test_env, temporal_worker

pytestmark = pytest.mark.django_db(transaction=True)
REVISION = "92c39b8ad563ebe5ef705f781b3038b901aba10f"


def check_workload_access(content, profile, root):
    __tracebackhide__ = True
    try:
        return _check_workload_access(content, profile, root)
    except Exception as error:  # noqa: BLE001 - tracebacks can contain the real kubeconfig
        pytest.fail(
            "Native workload access qualification failed ("
            + type(error).__name__
            + ")",
            pytrace=False,
        )


def _check_workload_access(content, profile, root):
    from nvke._private_http import private_http

    config = yaml.safe_load(content)
    ctx = next(
        c["context"]
        for c in config["contexts"]
        if c["name"] == config["current-context"]
    )
    cluster = next(
        c["cluster"] for c in config["clusters"] if c["name"] == ctx["cluster"]
    )
    user = next(c["user"] for c in config["users"] if c["name"] == ctx["user"])
    endpoint = urlsplit(cluster["server"])
    assert endpoint.scheme == "https" and endpoint.port == 6443
    assert ipaddress.ip_address(endpoint.hostname) in ipaddress.ip_network(
        "172.18.0.0/16"
    )
    assert not cluster.get("insecure-skip-tls-verify") and "exec" not in user
    with tempfile.TemporaryDirectory(dir=root) as directory:
        files = []
        for name, value in (
            ("ca", cluster["certificate-authority-data"]),
            ("cert", user["client-certificate-data"]),
            ("key", user["client-key-data"]),
        ):
            path = Path(directory) / name
            with os.fdopen(
                os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb"
            ) as stream:
                stream.write(base64.b64decode(value, validate=True))
            files.append(str(path))
        tls = ssl.create_default_context(cafile=files[0])
        tls.load_cert_chain(files[1], files[2])
        with (
            private_http(),
            httpx.Client(
                base_url=cluster["server"],
                transport=httpx.HTTPTransport(
                    proxy=profile["proxy"], verify=tls, retries=0
                ),
                trust_env=False,
                follow_redirects=False,
                timeout=30,
            ) as client,
        ):
            ready = client.get("/readyz")
            ready.raise_for_status()
            assert ready.text.strip() == "ok"
            version = client.get("/version")
            version.raise_for_status()
            response = client.get("/api/v1/nodes")
            response.raise_for_status()
            nodes = []
            for node in response.json()["items"]:
                assert node["metadata"]["name"].startswith(profile["clusterName"] + "-")
                assert any(
                    c["type"] == "Ready" and c["status"] == "True"
                    for c in node["status"]["conditions"]
                )
                nodes.append(
                    {
                        "name": node["metadata"]["name"],
                        "ready": True,
                        "controlPlane": "node-role.kubernetes.io/control-plane"
                        in node["metadata"].get("labels", {}),
                        "kubeletVersion": node["status"]["nodeInfo"]["kubeletVersion"],
                    }
                )
            assert len(nodes) == 2 and sum(n["controlPlane"] for n in nodes) == 1
            return {
                "readyz": True,
                "serverVersion": version.json()["gitVersion"],
                "nodes": nodes,
                "tlsVerification": True,
            }


@pytest.fixture
def binary():
    return paired.binary.__wrapped__()


@pytest.fixture
def dropped_cli_replies():
    return {}


@pytest.fixture
def asgi_endpoint(settings, dropped_cli_replies):
    yield from paired.asgi_endpoint.__wrapped__(settings, dropped_cli_replies)


def native_connection(kubeconfig):
    __tracebackhide__ = True
    try:
        return _native_connection(kubeconfig)
    except Exception as error:  # noqa: BLE001 - tracebacks can contain real credentials
        pytest.fail(
            "Native connection qualification failed (" + type(error).__name__ + ")",
            pytrace=False,
        )


def _native_connection(kubeconfig):
    info = kubeconfig.lstat()
    assert stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid()
    assert stat.S_IMODE(info.st_mode) == 0o600
    config = yaml.safe_load(kubeconfig.read_bytes())
    ctx = next(
        c["context"]
        for c in config["contexts"]
        if c["name"] == config["current-context"]
    )
    cluster = next(
        c["cluster"] for c in config["clusters"] if c["name"] == ctx["cluster"]
    )
    user = next(c["user"] for c in config["users"] if c["name"] == ctx["user"])
    assert cluster["server"] == "https://172.26.0.10:6443"
    assert not cluster.get("insecure-skip-tls-verify") and "exec" not in user
    credentials = {
        target: base64.b64decode(source, validate=True).decode()
        for target, source in (
            ("ca_pem", cluster["certificate-authority-data"]),
            ("client_certificate_pem", user["client-certificate-data"]),
            ("client_key_pem", user["client-key-data"]),
        )
    }
    return cluster, credentials


@pytest.fixture
def native_site(monkeypatch):
    from astrolift_clusters import nvke_connections, nvke_sites

    selected = os.getenv("NVKE_TEST_NATIVE_SITE_PROFILE")
    if not selected:
        pytest.skip("select the explicit owned native-site profile")
    if importlib.util.find_spec("socksio") is None:
        pytest.skip("install test-only socksio for the private native-site SSH route")
    path = Path(selected)
    for candidate in (path,):
        info = candidate.lstat()
        assert stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid()
        assert stat.S_IMODE(info.st_mode) == 0o600 and info.st_size < 65536
    profile = json.loads(path.read_text())
    assert profile["scope"] == "isolated-astro-nvke-native-qualification"
    assert profile["nativeRevision"] == REVISION
    assert profile["proxy"] == "socks5://127.0.0.1:27445"
    assert profile["controllerEndpoint"] == "http://172.26.0.10:30080/api"
    assert profile["organizationNamespace"] == "tenant-test"
    assert profile["managementNamespace"] == "nvke-system"
    assert profile["clusterName"] == "tenant-1"
    assert profile["clusterID"] == "1f04b152-7684-4ee4-a6ca-f96ff8b4d044"
    cluster, credentials = native_connection(Path(profile["kubeconfig"]))
    state = SimpleNamespace(
        world=_world.__wrapped__(),
        calls=[],
        allowed_image=None,
        profile=profile,
        transport_failures=[],
    )
    with context(state.world):
        state.site = nvke_sites.create_site(
            name="Owned native qualification",
            slug="native-owned",
            organization_namespace=profile["organizationNamespace"],
        )
        state.controller = put(
            state.site,
            endpoint=profile["controllerEndpoint"],
            allow_http=True,
            credentials={},
        )
        state.site.refresh_from_db()
        state.datastore = put(
            state.site,
            surface="datastore",
            endpoint=cluster["server"],
            credentials=credentials,
        )
        state.site.refresh_from_db()

    class Routed(httpx.BaseTransport):
        def __init__(self, verify):
            try:
                self.inner = httpx.HTTPTransport(
                    proxy=profile["proxy"], verify=verify, retries=0
                )
            except Exception as error:
                state.transport_failures.append(type(error).__name__)
                raise

        def handle_request(self, request):
            if request.method != "GET":
                assert request.url.host == "172.26.0.10" and request.url.port == 6443
                base = "/apis/internal.nvke.nvidia.com/v1/machineimages"
                assert state.allowed_image
                assert (
                    request.method == "POST"
                    and request.url.path == base
                    and json.loads(request.content)["metadata"]["name"]
                    == state.allowed_image
                ) or (
                    request.method == "DELETE"
                    and request.url.path == base + "/" + state.allowed_image
                )
            state.calls.append((request.method, request.url.path))
            try:
                return self.inner.handle_request(request)
            except Exception as error:
                state.transport_failures.append(type(error).__name__)
                raise

        def close(self):
            self.inner.close()

    original_client, original_datastore = (
        nvke_connections.NvkeClient,
        nvke_connections.NvkeDatastore,
    )

    class RoutedClient(original_client):
        def __init__(self, *args, **options):
            options["transport"] = Routed(options["verify"])
            super().__init__(*args, **options)

    class RoutedDatastore(original_datastore):
        def __init__(self, *args, **options):
            options["transport"] = Routed(options["verify"])
            super().__init__(*args, **options)

    monkeypatch.setattr(nvke_connections, "NvkeClient", RoutedClient)
    monkeypatch.setattr(nvke_connections, "NvkeDatastore", RoutedDatastore)
    return state


async def test_compiled_cli_native_site_receipt_recovery_enrollment_access_and_image_lifecycle(
    binary, tmp_path, asgi_endpoint, native_site, dropped_cli_replies, monkeypatch
):
    from astrolift_clusters.models import NvkeOperation
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow

    state = native_site
    issued, reader = mint_token(), mint_token()
    for token, scopes in (
        (issued, ["read:nvke", "manage:nvke", "admin:nvke"]),
        (reader, ["read:nvke"]),
    ):
        await sync_to_async(ApiToken.objects.create)(
            user=state.world.user,
            organization=state.world.org,
            name="owned native qualification",
            token_hash=token.token_hash,
            token_last_4=token.last4,
            scopes=scopes,
        )

    async def run(*args, ok=True, read_only=False):
        response = await asyncio.to_thread(
            paired.invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            reader.plaintext if read_only else issued.plaintext,
            state.world.org.slug,
            *args,
        )
        if ok:
            if response.returncode:
                rows = await sync_to_async(list)(
                    NvkeOperation.objects.filter(site=state.site).values(
                        "operation_key", "status", "stage", "error_code"
                    )
                )
                pytest.fail(
                    "Native CLI failed: "
                    + json.dumps(
                        {
                            "operations": rows,
                            "transportFailures": state.transport_failures,
                            "nativeRequestCount": len(state.calls),
                        }
                    )
                )
            return json.loads(response.stdout)
        assert response.returncode != 0
        return response

    def parameters(name, content):
        path = tmp_path / (name + ".json")
        path.write_text(json.dumps(content))
        path.chmod(0o600)
        return path

    scope = ("--site", state.site.guid)
    wait = ("--wait", "--poll-interval", "500ms", "--wait-timeout", "30s")
    async with temporal_test_env() as env:
        queue = "nvke-native-site-" + uuid4().hex

        async def client():
            return env.client

        monkeypatch.setattr(workflow_client, "_get_client_async", client)
        monkeypatch.setattr(workflow_client, "_temporal_enabled", lambda: True)
        monkeypatch.setattr(workflow_client, "_task_queue", lambda: queue)
        async with temporal_worker(
            env,
            task_queue=queue,
            workflows=[NvkeOperationWorkflow],
            activities=[nvke_operation_attempt],
        ):
            recovery = tmp_path / "native-read-recovery.json"
            request = parameters(
                "cluster-read", {"path": {"clusterID": state.profile["clusterID"]}}
            )
            dropped_cli_replies["requestNvkeOperationText("] = 1
            lost = await run(
                "controller",
                "get-cluster",
                *scope,
                "--file",
                request,
                "--request-file",
                recovery,
                ok=False,
            )
            assert "preserve" in lost.stderr
            assert dropped_cli_replies["requestNvkeOperationText("] == 0
            retained = recovery.read_bytes()
            source = await run(
                "operation",
                "recover",
                "--request-file",
                recovery,
                *wait,
                read_only=True,
            )
            assert source["status"] == "SUCCEEDED" and recovery.read_bytes() == retained
            assert (
                await sync_to_async(
                    NvkeOperation.objects.filter(
                        site=state.site, operation_key="getCluster"
                    ).count
                )()
                == 1
            )
            output = tmp_path / "native-read-private.json"
            await run("operation", "result", source["id"], "--output", output)
            observed = json.loads(output.read_text())["response"]["data"]
            assert (
                observed["id"] == state.profile["clusterID"]
                and observed["name"] == state.profile["clusterName"]
            )
            binding = await run(
                "access",
                "enroll",
                source["id"],
                *scope,
                "--site-version",
                state.site.version,
                "--source-version",
                source["version"],
            )
            assert binding["clusterId"] == state.profile["clusterID"]
            access = parameters(
                "workload-access",
                {
                    "cluster_name": state.profile["clusterName"],
                    "management_namespace": state.profile["managementNamespace"],
                },
            )
            downloaded = await run(
                "datastore",
                "get-workload-kubeconfig",
                *scope,
                "--file",
                access,
                "--request-file",
                tmp_path / "access-recovery.json",
                *wait,
            )
            assert downloaded["status"] == "SUCCEEDED"
            destination = tmp_path / "workload-access-private.json"
            await run("operation", "result", downloaded["id"], "--output", destination)
            result = json.loads(destination.read_text())
            kubeconfig = result["response"]["data"]["kubeconfig"]
            assert yaml.safe_load(kubeconfig)["kind"] == "Config"
            assert destination.stat().st_mode & 0o777 == 0o600
            workload = await asyncio.to_thread(
                check_workload_access, kubeconfig, state.profile, tmp_path
            )
            stored = await sync_to_async(NvkeOperation.objects.get)(
                guid=downloaded["id"]
            )
            if b"client-key-data" in bytes(stored.result_ciphertext):
                pytest.fail("Native access result was stored without encryption")
            state.allowed_image = "astro-hosted-" + uuid4().hex[:12]
            image = parameters(
                "machine-image",
                {
                    "document": {
                        "metadata": {"name": state.allowed_image},
                        "spec": {
                            "architecture": "arm64",
                            "buildDate": "2026-11-08",
                            "operatingSystem": {"family": "ubuntu", "version": "24.04"},
                            "nico": {
                                "ipxeScript": "#!ipxe\necho hosted-native-qualification\n"
                            },
                        },
                    }
                },
            )
            created = await run(
                "datastore",
                "create-machine-image",
                *scope,
                "--file",
                image,
                "--request-file",
                tmp_path / "image-create-recovery.json",
                *wait,
            )
            assert created["status"] == "SUCCEEDED"
            image_result = tmp_path / "image-create-private.json"
            await run("operation", "result", created["id"], "--output", image_result)
            metadata = json.loads(image_result.read_text())["observation"]["data"][
                "metadata"
            ]
            removal = parameters(
                "image-delete",
                {
                    "name": state.allowed_image,
                    "uid": metadata["uid"],
                    "resource_version": metadata["resourceVersion"],
                },
            )
            removed = await run(
                "datastore",
                "delete-machine-image",
                *scope,
                "--file",
                removal,
                "--request-file",
                tmp_path / "image-delete-recovery.json",
                *wait,
            )
            assert removed["status"] == "SUCCEEDED"
            secret_reads = sum("/secrets/" in path for _, path in state.calls)
            await run(
                "access",
                "revoke",
                binding["id"],
                *scope,
                "--site-version",
                state.site.version,
                "--version",
                binding["version"],
            )
            refused = await run(
                "datastore",
                "get-workload-kubeconfig",
                *scope,
                "--file",
                access,
                "--request-file",
                tmp_path / "revoked-access-recovery.json",
            )
            await run(
                "operation",
                "wait",
                refused["id"],
                "--poll-interval",
                "500ms",
                "--wait-timeout",
                "30s",
                ok=False,
            )
            refused = await run("operation", "get", refused["id"], read_only=True)
            assert (
                refused["status"] == "FAILED"
                and refused["errorCode"] == "OWNERSHIP_UNVERIFIED"
            )
            assert sum("/secrets/" in path for _, path in state.calls) == secret_reads
            assert (
                await run(
                    "operation", "recover", "--request-file", recovery, read_only=True
                )
            )["id"] == source["id"]
            for row in await sync_to_async(list)(
                NvkeOperation.objects.filter(site=state.site)
            ):
                history = await env.client.get_workflow_handle(
                    row.workflow_id, run_id=row.workflow_run_id
                ).fetch_history()
                text = history.to_json()
                private_values = (
                    issued.plaintext,
                    reader.plaintext,
                    "client-key-data",
                    "BEGIN PRIVATE KEY",
                    "BEGIN CERTIFICATE",
                    state.profile["clusterName"],
                    state.allowed_image,
                )
                if any(private in text for private in private_values):
                    pytest.fail("Sensitive native data entered Temporal history")
            assert sum(method == "POST" for method, _ in state.calls) == 1
            assert sum(method == "DELETE" for method, _ in state.calls) == 1
            report_path = os.getenv("NVKE_TEST_NATIVE_SITE_REPORT")
            if report_path:
                report = {
                    "scope": "compiled-cli-real-astro-postgres-temporal-native-site",
                    "observedAt": datetime.now(UTC).isoformat(),
                    "nativeRevision": REVISION,
                    "testSha256": hashlib.sha256(
                        Path(__file__).read_bytes()
                    ).hexdigest(),
                    "cliBinarySha256": hashlib.sha256(
                        Path(binary).read_bytes()
                    ).hexdigest(),
                    "sourceOperationId": source["id"],
                    "accessOperationId": downloaded["id"],
                    "imageCreateOperationId": created["id"],
                    "imageDeleteOperationId": removed["id"],
                    "imageUID": metadata["uid"],
                    "kubeconfigSha256": hashlib.sha256(kubeconfig.encode()).hexdigest(),
                    "lostAdmissionReplyRecoveredReadOnly": True,
                    "nativeAccessEnrolledAndDownloadedPrivately": True,
                    "workload": workload,
                    "revocationRefusedBeforeSecretRead": True,
                    "temporalHistoryPrivateDataAbsent": True,
                    "nativeRequests": state.calls,
                    "nativePosts": 1,
                    "nativeDeletes": 1,
                    "transportFailures": state.transport_failures,
                    "limitations": [
                        "Test-only private SSH transport; real connection TLS/authorization and activities retained",
                        "Existing simulator tenant access plus MachineImage metadata lifecycle; not hosted tenant provisioning or physical providers",
                        "Site connections were configured through their production service in the fixture; not CLI/UI connection configuration qualification",
                    ],
                }
                with os.fdopen(
                    os.open(report_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600),
                    "w",
                ) as stream:
                    json.dump(report, stream, indent=2)
                    stream.write("\n")
