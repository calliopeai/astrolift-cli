"""Compiled CLI against actual App ASGI, PostgreSQL and Temporal; opt-in locally.

Run using the compatible App backend's configured pytest runner and pytest.ini.
Set ASTROLIFT_BOUNDED_CLI to a compiled owned binary and
ASTROLIFT_BOUNDED_DOCS to the canonical docs checkout. This module never opens
production endpoints and contains no substituted API, permission or activity.
"""

import asyncio
import json
import os
import re
import socket
import subprocess
import threading
import time
from pathlib import Path

import pytest
import uvicorn
from asgiref.sync import sync_to_async
from django.contrib.auth import get_user_model
from graphql import build_schema, parse, validate

from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken, Member, Organization
from astrolift_operations.models import WorkflowRun
from astrolift_workflows.tests.test_serial_collections_temporal_2156 import (
    REGISTERED,
    WorkflowDefinitionRunWorkflow,
    create_plan,
    rows,
    start,
)
from core.testing.temporal import temporal_worker
from workflows.manifest import emit_workflow_manifest, parse_workflow_manifest

pytestmark = pytest.mark.django_db(transaction=True)
CLI_ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def binary():
    path = os.getenv("ASTROLIFT_BOUNDED_CLI")
    if not path:
        pytest.skip("set ASTROLIFT_BOUNDED_CLI to an owned compiled binary")
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
        assert server.started, "owned ASGI server did not start"
        yield f"http://127.0.0.1:{sock.getsockname()[1]}"
    finally:
        server.should_exit = True
        thread.join(timeout=10)
        sock.close()
        assert not thread.is_alive(), "owned ASGI server did not stop"


def caller(org):
    user = get_user_model().objects.create_superuser(
        username="bounded-cli-reader",
        email="bounded-reader@example.test",
        password="test",
    )
    Member.objects.create(user=user, scope_kind="ORG", scope_id=org.pk)
    minted = mint_token()
    token = ApiToken.objects.create(
        user=user,
        organization=org,
        name="owned-bounded-cli-reader",
        token_hash=minted.token_hash,
        token_last_4=minted.last4,
        scopes=["read:apps"],
    )
    return minted.plaintext, token.pk


def invoke(binary, tmp_path, endpoint, bearer, *arguments):
    env = {
        **os.environ,
        "XDG_CONFIG_HOME": str(tmp_path / "config"),
        "ASTROLIFT_TOKEN": bearer,
        "ASTROLIFT_NO_UPDATE_CHECK": "1",
        "CI": "1",
    }
    env.pop("ASTROLIFT_DEPLOY_TOKEN", None)
    result = subprocess.run(
        [binary, "--api-url", endpoint, *arguments],
        env=env,
        capture_output=True,
        text=True,
        timeout=20,
        check=False,
    )
    assert bearer not in result.stdout + result.stderr
    return result


def test_actual_bounded_cli_documents_validate_against_paired_app_sdl():
    import config

    root = Path(config.__file__).resolve().parents[2]
    documents = []
    for filename, name in (
        ("workflow.go", "previewWorkflowManifestQuery"),
        ("workflow_execution_stages.go", "workflowExecutionStagesQuery"),
    ):
        source = (CLI_ROOT / "cmd" / filename).read_text()
        found = re.search(rf"const {name} = `([\s\S]*?)`", source)
        assert found
        documents.append(parse(found[1]))
    for filename in ("backend/schema.graphql", "frontend/schema.graphql"):
        graph = build_schema((root / filename).read_text())
        assert all(not validate(graph, document) for document in documents)


def test_actual_cli_templates_canonical_examples_and_server_preview_roundtrip(
    binary, tmp_path, asgi_endpoint
):
    org = Organization.objects.create(
        name="Bounded CLI authoring", slug="bounded-cli-authoring"
    )
    bearer, _ = caller(org)
    sources = []
    for pattern in ("single", "chained", "fan_out", "review_loop"):
        file = tmp_path / f"{pattern}.toml"
        created = invoke(
            binary,
            tmp_path,
            asgi_endpoint,
            bearer,
            "workflow",
            "init",
            "--pattern",
            pattern,
            "-o",
            str(file),
        )
        assert created.returncode == 0, created.stderr
        sources.append(file.read_text())
    docs = os.getenv("ASTROLIFT_BOUNDED_DOCS")
    assert docs, "set ASTROLIFT_BOUNDED_DOCS to the canonical docs checkout"
    guide = (Path(docs) / "docs/guides/bounded-workflows.md").read_text()
    examples = re.findall(r"```toml\s*\n(.*?)```", guide, flags=re.DOTALL)
    assert len(examples) == 2
    sources.extend(examples)
    for index, source in enumerate(sources):
        manifest = parse_workflow_manifest(source)
        exported = emit_workflow_manifest(manifest)
        assert parse_workflow_manifest(exported) == manifest
        file = tmp_path / f"roundtrip-{index}.toml"
        file.write_text(exported)
        local = invoke(
            binary,
            tmp_path,
            asgi_endpoint,
            bearer,
            "workflow",
            "validate",
            str(file),
            "--json",
        )
        assert local.returncode == 0, local.stderr
        remote = invoke(
            binary,
            tmp_path,
            asgi_endpoint,
            bearer,
            "workflow",
            "validate",
            str(file),
            "--server",
            "--json",
        )
        assert remote.returncode == 0, remote.stderr
        preview = json.loads(remote.stdout)
        assert preview["ok"] and len(preview["stages"]) == len(manifest.stages)
        for actual, stage in zip(preview["stages"], manifest.stages, strict=True):
            assert actual["maxAttempts"] == stage.max_attempts
            assert actual["backEdge"] == stage.back_edge
            assert actual["iteration"] == stage.iteration
            assert actual["workflow"] == stage.workflow
    bad = tmp_path / "invalid-bounded.toml"
    bad.write_text(examples[1].replace('"max_items":2', '"max_items":51'))
    refused = invoke(
        binary,
        tmp_path,
        asgi_endpoint,
        bearer,
        "workflow",
        "validate",
        str(bad),
        "--server",
        "--json",
    )
    assert refused.returncode != 0 and json.loads(refused.stdout)["ok"] is False


@pytest.mark.asyncio
async def test_actual_completed_serial_engine_metadata_reaches_cli_and_token_revocation_refuses(
    binary, tmp_path, asgi_endpoint, temporal_env, settings
):
    plan = await sync_to_async(create_plan)()
    org = await sync_to_async(
        lambda: WorkflowRun.objects.get(pk=plan[2]).organization
    )()
    bearer, token_pk = await sync_to_async(caller)(org)
    settings.ASTROLIFT_TEMPORAL_ENABLED = True
    settings.TEMPORAL_ADDRESS = temporal_env.client.service_client.config.target_host
    settings.TEMPORAL_NAMESPACE = temporal_env.client.namespace
    async with temporal_worker(
        temporal_env, workflows=[WorkflowDefinitionRunWorkflow], activities=REGISTERED
    ):
        handle = await start(
            temporal_env,
            plan,
            {"items": [{"text": "PRIVATE-CLI-RESULT"}, {"text": "second"}]},
        )
        result = await asyncio.wait_for(handle.result(), 30)
        assert result.ok
        await sync_to_async(
            lambda: WorkflowRun.objects.filter(pk=plan[2]).update(
                run_id=handle.first_execution_run_id
            )
        )()
        run = await sync_to_async(lambda: WorkflowRun.objects.get(pk=plan[2]))()
        received = await sync_to_async(invoke, thread_sensitive=False)(
            binary,
            tmp_path,
            asgi_endpoint,
            bearer,
            "--org",
            org.slug,
            "workflow",
            "execution-stages",
            str(run.guid),
            "--json",
        )
    assert received.returncode == 0, received.stderr
    output = json.loads(received.stdout)
    assert output["execution"]["guid"] == str(run.guid)
    assert output["execution"]["temporalRunId"] == handle.first_execution_run_id
    records = output["stages"]
    persisted = await rows(plan[2])
    assert len(records) == len(persisted) == 3
    by_guid = {record["guid"]: record for record in records}
    parent, *items = persisted
    parent_stage_guid = await sync_to_async(lambda: str(parent.stage.guid))()
    for index, item in enumerate(items):
        record = by_guid[str(item.guid)]
        assert record["collectionIndex"] == index
        assert record["collectionParentExecutionGuid"] == str(parent.guid)
        assert record["collectionStageId"] == parent_stage_guid
        assert record["roundNumber"] == record["attemptNumber"] == 1
        assert (
            record["fanoutIndex"] is None
            and record["fanoutParentExecutionGuid"] is None
        )
    assert "PRIVATE-CLI-RESULT" not in received.stdout + received.stderr
    await sync_to_async(
        lambda: ApiToken.objects.filter(pk=token_pk).update(is_revoked=True)
    )()
    denied = await sync_to_async(invoke, thread_sensitive=False)(
        binary,
        tmp_path,
        asgi_endpoint,
        bearer,
        "--org",
        org.slug,
        "workflow",
        "execution-stages",
        str(run.guid),
        "--json",
    )
    assert denied.returncode != 0 and not denied.stdout.strip()
