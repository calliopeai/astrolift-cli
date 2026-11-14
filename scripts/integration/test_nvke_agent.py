"""Compiled remote CLI against real Astro/Postgres/Temporal/mTLS/native utilities."""

import asyncio
import importlib.util
import json
from pathlib import Path
from uuid import uuid4

import pytest
import test_nvke_contracts as paired
from asgiref.sync import sync_to_async
from astrolift_clusters.models import NvkeOperation
from astrolift_clusters.tests.test_nvke_sites import world as _world
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken
from core.testing.temporal import temporal_test_env, temporal_worker

pytestmark = pytest.mark.django_db(transaction=True)


@pytest.fixture
def world():
    return _world.__wrapped__()


@pytest.fixture
def binary():
    return paired.binary.__wrapped__()


@pytest.fixture
def dropped_cli_replies():
    return {}


@pytest.fixture
def asgi_endpoint(settings, dropped_cli_replies):
    yield from paired.asgi_endpoint.__wrapped__(settings, dropped_cli_replies)


def launch_agent(root, site_id):
    from django.conf import settings

    source = (
        Path(settings.BASE_DIR) / "providers/tests/nvke/test_operator_agent_client.py"
    )
    spec = importlib.util.spec_from_file_location("paired_nvke_agent_helpers", source)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    yield from module.operator_agent(root, site_id=site_id)


@pytest.mark.asyncio
async def test_compiled_cli_agent_configuration_original_recovery_and_private_export(
    world, binary, asgi_endpoint, dropped_cli_replies, tmp_path, monkeypatch
):
    from astrolift_workflows import client as workflow_client
    from astrolift_workflows.activities.nvke import nvke_operation_attempt
    from astrolift_workflows.workflows.nvke import NvkeOperationWorkflow

    issued = mint_token()
    token = await sync_to_async(ApiToken.objects.create)(
        user=world.user,
        organization=world.org,
        name="owned operator CLI",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin", "read"],
    )

    async def run(*arguments, ok=True):
        result = await asyncio.to_thread(
            paired.invoke,
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            world.org.slug,
            *arguments,
        )
        for private in ("cli-agent-private-site", "BEGIN PRIVATE KEY", "nodes.csv"):
            assert private not in result.stdout + result.stderr
        if ok:
            assert result.returncode == 0, result.stderr
            return json.loads(result.stdout)
        assert result.returncode != 0
        return result

    def private_file(name, value):
        path = tmp_path / name
        path.write_text(json.dumps(value))
        path.chmod(0o600)
        return path

    site = await run(
        "site",
        "create",
        "--name",
        "Agent CLI",
        "--slug",
        "agent-cli",
        "--namespace",
        "org-owned",
    )
    catalog = await run("agent", "catalog")
    assert len(catalog["commands"]) == 17
    scope = ("--site", site["id"])
    assert await run("agent", "get", *scope) is None
    root = tmp_path / "agent"
    root.mkdir(mode=0o700)
    agent_context = launch_agent(root, site["id"])
    state = await asyncio.to_thread(next, agent_context)
    try:
        credentials = private_file(
            "mtls.json",
            {
                "caPem": (root / "ca.pem").read_text(),
                "clientCertificatePem": (root / "client.pem").read_text(),
                "clientKeyPem": (root / "client.key").read_text(),
            },
        )
        authority = await run(
            "agent",
            "configure",
            *scope,
            "--site-version",
            site["version"],
            "--endpoint",
            str(state.client._http.base_url).rstrip("/"),
            "--instance",
            state.instance,
            "--bundle-sha256",
            state.digest,
            "--credentials-file",
            credentials,
        )
        review = (
            "--site-version",
            authority["siteVersion"],
            "--authority-version",
            authority["version"],
        )
        authority = await run("agent", "verify", *scope, *review)
        assert authority["verificationCode"] == "AGENT_VERIFIED"
        request = private_file(
            "native.json",
            {
                "format": 1,
                "revision": catalog["revision"],
                "siteId": site["id"],
                "command": "generate.sitespec",
                "positionals": ["cli-agent-private-site"],
                "options": {"nodes": "input:nodes.csv"},
                "files": {"nodes.csv": "name,ip\ncp-1,192.0.2.10\n"},
            },
        )
        recovery = tmp_path / "recovery.json"
        async with temporal_test_env() as env:
            queue = "nvke-cli-agent-" + uuid4().hex

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
                dropped_cli_replies["requestNvkeOperatorJob("] = 1
                await run(
                    "agent",
                    "request",
                    *scope,
                    *review,
                    "--file",
                    request,
                    "--request-file",
                    recovery,
                    ok=False,
                )
                request.unlink()
                original = await run("agent", "recover", "--request-file", recovery)
                row = await run(
                    "operation",
                    "wait",
                    original["id"],
                    "--wait-timeout",
                    "35s",
                    "--poll-interval",
                    "500ms",
                )
                assert (
                    row["status"] == "SUCCEEDED"
                    and row["operationKey"] == "tooling.generate.sitespec"
                )
                assert (
                    len(list((root / "state/workspace/runs").glob("*/started.json")))
                    == 1
                )
                saved = json.loads(recovery.read_text())
                assert (
                    "nodes.csv" not in recovery.read_text()
                    and "cli-agent-private-site" not in recovery.read_text()
                )
                source = await sync_to_async(NvkeOperation.objects.get)(guid=row["id"])
                assert str(source.request_id) == saved["requestId"]
                handle = env.client.get_workflow_handle(
                    source.workflow_id, run_id=source.workflow_run_id
                )
                history = (await handle.fetch_history()).to_json()
                assert (
                    "nodes.csv" not in history
                    and "cli-agent-private-site" not in history
                )
                output = tmp_path / "stdout.yaml"
                downloaded = await run(
                    "agent",
                    "artifact",
                    source.guid,
                    *scope,
                    *review,
                    "--version",
                    row["version"],
                    "--name",
                    "stdout",
                    "--output",
                    output,
                )
                assert int(downloaded["size"]) == output.stat().st_size
                assert "cli-agent-private-site" in output.read_text()
                assert output.stat().st_mode & 0o777 == 0o600
                source.status = "uncertain"
                await sync_to_async(source.save)(update_fields=["status", "version"])
                settled = await run(
                    "agent",
                    "reconcile",
                    source.guid,
                    *scope,
                    *review,
                    "--version",
                    source.version,
                )
                assert settled["status"] == "SUCCEEDED" and settled["id"] == row["id"]
                assert (
                    len(list((root / "state/workspace/runs").glob("*/started.json")))
                    == 1
                )
                from django.db import transaction
                from nvke.client import NvkeError

                from astrolift_clusters import nvke_operator_jobs as jobs
                from astrolift_clusters.models import NvkeOperatorAuthority, NvkeSite
                from astrolift_clusters.tests.test_nvke_sites import context

                native = {
                    "format": 1,
                    "revision": catalog["revision"],
                    "siteId": site["id"],
                    "command": "bootstrap.site",
                    "positionals": [],
                    "options": {"sitespec": "input:site.yaml"},
                    "files": {"site.yaml": "never-dispatch-cli-stop-fixture"},
                }

                def never_dispatched_original():
                    current_site = NvkeSite.objects.get(guid=site["id"])
                    current_authority = NvkeOperatorAuthority.objects.get(
                        site=current_site
                    )
                    # The outbox sees uncertainty before its on-commit dispatch.
                    with transaction.atomic(), context(world):
                        original = jobs.reserve(
                            site_id=current_site.guid,
                            expected_actor_id=world.user.pk,
                            expected_site_version=current_site.version,
                            expected_authority_version=current_authority.version,
                            request_id=uuid4(),
                            request_text=json.dumps(native),
                            acknowledge_infrastructure_change=True,
                        )
                        original.status = "uncertain"
                        original.save(update_fields=["status", "version"])
                    return original

                for lose_stop_reply in (False, True):
                    unknown = await sync_to_async(never_dispatched_original)()
                    if lose_stop_reply:
                        dropped_cli_replies["stopNvkeOperatorJob("] = 1
                    stopped = await run(
                        "agent",
                        "stop",
                        unknown.guid,
                        *scope,
                        *review,
                        "--version",
                        unknown.version,
                        ok=not lose_stop_reply,
                    )
                    if lose_stop_reply:
                        stopped = await run("operation", "get", unknown.guid)
                    assert stopped["status"] == "CANCELLED"
                    assert stopped["id"] == str(unknown.guid)
                    with pytest.raises(NvkeError) as delayed:
                        state.client.submit(
                            str(unknown.guid), native, intent=unknown.request_digest
                        )
                    assert delayed.value.code == "OPERATOR_JOB_FENCED"
                    assert (
                        len(
                            list((root / "state/workspace/runs").glob("*/started.json"))
                        )
                        == 1
                    )
                await sync_to_async(ApiToken.objects.filter(pk=token.pk).update)(
                    is_revoked=True
                )
                forbidden = tmp_path / "forbidden.yaml"
                await run(
                    "agent",
                    "artifact",
                    source.guid,
                    *scope,
                    *review,
                    "--version",
                    settled["version"],
                    "--name",
                    "stdout",
                    "--output",
                    forbidden,
                    ok=False,
                )
                assert not forbidden.exists()
    finally:
        await asyncio.to_thread(agent_context.close)
