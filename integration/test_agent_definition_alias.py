"""Compiled historical alias -> real bearer ASGI/PG/Temporal reviewed starts.

All resolvers, credential ceilings, durable request encryption, SDK clients,
workflows and activities are production implementations. The proxy only drops
an actual accepted HTTP response or sends an unknown field to the real schema.
"""

import asyncio
import json
import os
import re
from pathlib import Path
from types import SimpleNamespace
from uuid import uuid4

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip("requires compiled CLI and app backend", allow_module_level=True)

from asgiref.sync import sync_to_async
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken, Member, Organization
from astrolift_workflows.activities import workflow_stage_activities as activities
from astrolift_workflows.workflows.workflow_definition_run import (
    WorkflowDefinitionRunWorkflow,
)
from core.permissions import Permission
from core.run_input_contract import digest
from core.testing.temporal import temporal_test_env, temporal_worker
from core.tests.utils.scope_world import bind_role
from django.contrib.auth import get_user_model
from workflows.models import WorkflowDefinition, WorkflowDefinitionStart, WorkflowStage
from workflows.reviewed_starts import definition_revision

from test_pipeline_reviewed import forwarding_proxy, invoke, running_api

pytestmark = pytest.mark.django_db(transaction=True)


def create_definition_world():
    suffix = uuid4().hex[:12]
    org = Organization.objects.create(
        name="Disposable alias proof", slug=f"alias-{suffix}"
    )
    user = get_user_model().objects.create_user(username=f"alias-{suffix}")
    Member.objects.create(user=user, scope_kind="ORG", scope_id=org.pk)
    bind_role(
        user,
        permissions=[Permission.WORKFLOW_READ, Permission.WORKFLOW_TRIGGER],
        kind="ORG",
        scope_id=org.pk,
        slug=f"alias-{suffix}",
    )
    definition = WorkflowDefinition.objects.create(
        organization=org,
        name="Reviewed alias",
        slug=f"alias-{suffix}",
        model_label="",
        input_schema={
            "type": "object",
            "properties": {"message": {"type": "string"}},
            "required": ["message"],
            "additionalProperties": False,
        },
    )
    WorkflowStage.objects.create(
        definition=definition, slug=f"checkpoint-{suffix}", order=0, kind="checkpoint"
    )
    issued = mint_token()
    ApiToken.objects.create(
        user=user,
        organization=org,
        name="Alias acceptance",
        token_hash=issued.token_hash,
        scopes=["read:apps", "workflow:trigger"],
    )
    return SimpleNamespace(
        org=org,
        user=user,
        definition=definition,
        token=issued.plaintext,
        revision=definition_revision(definition),
        digest=digest(definition.input_schema),
    )


def test_reviewed_definition_and_alias_documents_match_actual_schema():
    from config.schema import schema
    from graphql import parse, validate

    constants = {}
    root = Path(__file__).resolve().parents[1]
    for filename in ["workflow_definition_reviewed.go", "agent_definition_reviewed.go"]:
        for name, expression in re.findall(
            r"^const (\w+) = (.+)$", (root / "cmd" / filename).read_text(), re.MULTILINE
        ):
            pieces = expression.split(" + ")
            constants[name] = "".join(
                piece[1:-1] if piece.startswith("`") else constants[piece]
                for piece in pieces
            )
    for name in [
        "reviewedDefinitionQuery",
        "reviewedDefinitionRecoveryQuery",
        "reviewedDefinitionStartMutation",
        "agentDefinitionExecutionQuery",
    ]:
        assert validate(schema._schema, parse(constants[name])) == [], name


async def test_real_alias_exact_start_lost_response_recovery_and_engine_wait(
    settings, tmp_path
):
    async with temporal_test_env() as engine:
        settings.ASTROLIFT_TEMPORAL_ENABLED = True
        settings.TEMPORAL_ADDRESS = engine.client.service_client.config.target_host
        settings.TEMPORAL_NAMESPACE = "default"
        settings.TEMPORAL_TASK_QUEUE = f"cli-alias-{uuid4().hex}"
        world = await sync_to_async(create_definition_world)()
        marker = "PRIVATE_ALIAS_INPUT_NEVER_IN_RECEIPT"
        inputs = tmp_path / "inputs.json"
        inputs.write_text(json.dumps({"message": marker}))
        path = tmp_path / "request.json"
        args = [
            "agent",
            "run",
            str(world.definition.guid),
            "--request-file",
            str(path),
            "--input",
            f"@{inputs}",
            "--yes",
            "--json",
        ]
        with running_api() as port, forwarding_proxy(
            port,
            start_operation="StartWorkflowDefinition",
            start_field="startWorkflowDefinition",
        ) as proxy:
            proxy.drop_start = True
            lost = await asyncio.to_thread(invoke, world, proxy, tmp_path, args)
            assert lost.returncode != 0 and "unknown" in lost.stderr, lost.stderr
            original = path.read_bytes()
            saved = json.loads(original)
            assert saved["kind"] == "workflow-definition"
            assert saved["targetId"] == str(world.definition.guid)
            assert saved["organizationId"] == str(world.org.guid)
            assert saved["actorUserId"] == world.user.pk
            assert (
                saved["revision"] == world.revision
                and saved["inputSchemaDigest"] == world.digest
            )
            assert (
                marker not in original.decode() and world.token not in original.decode()
            )
            row = await sync_to_async(
                WorkflowDefinitionStart.objects.select_related("execution").get
            )(request_id=saved["requestId"])
            assert (
                row.actor_key == f"user:{world.user.pk}"
                and row.definition_id == world.definition.pk
            )
            assert row.dispatch_status == "submitted" and row.execution.run_id
            handle = engine.client.get_workflow_handle(
                row.execution.workflow_id, run_id=row.execution.run_id
            )
            try:
                assert (await handle.describe()).status.name == "RUNNING"
                # Saved metadata wins over changed definitions and unavailable old input files.
                inputs.unlink()
                await sync_to_async(
                    WorkflowDefinition.objects.filter(pk=world.definition.pk).update
                )(name="Renamed after dispatch", is_enabled=False)
                before = len(proxy.requests)
                recovered = await asyncio.to_thread(
                    invoke, world, proxy, tmp_path, args
                )
                assert recovered.returncode == 0, recovered.stderr
                receipt = json.loads(recovered.stdout)["start"]
                assert receipt["executionId"] == str(row.execution.guid)
                assert receipt["temporalRunId"] == row.execution.run_id
                assert receipt["requestId"] == saved["requestId"]
                assert path.read_bytes() == original
                assert not any(
                    "mutation" in q["query"] for q, _ in proxy.requests[before:]
                )
                # Restore executability; the frozen original plan runs through the real worker.
                await sync_to_async(
                    WorkflowDefinition.objects.filter(pk=world.definition.pk).update
                )(is_enabled=True)
                registered = [
                    activities.get_workflow_stages,
                    activities.create_stage_execution,
                    activities.update_stage_execution,
                    activities.snapshot_checkpoint,
                    activities.mark_workflow_run,
                    activities.record_human_gate_decision,
                ]
                async with temporal_worker(
                    engine,
                    task_queue=settings.TEMPORAL_TASK_QUEUE,
                    workflows=[WorkflowDefinitionRunWorkflow],
                    activities=registered,
                ):
                    result = await asyncio.wait_for(handle.result(), 15)
                    assert result["ok"] is True
                    waited = await asyncio.to_thread(
                        invoke, world, proxy, tmp_path, [*args, "--wait"]
                    )
                    assert waited.returncode == 0, waited.stderr
                    assert (
                        json.loads(waited.stdout)["execution"]["status"] == "completed"
                    )
                    assert (await handle.describe()).status.name == "COMPLETED"
                assert (
                    marker
                    not in waited.stdout
                    + waited.stderr
                    + recovered.stdout
                    + recovered.stderr
                )
                assert await sync_to_async(WorkflowDefinitionStart.objects.count)() == 1
                starts = [
                    q
                    for q, _ in proxy.requests
                    if "mutation StartWorkflowDefinition" in q["query"]
                ]
                assert len(starts) == 1
                sent = starts[0]["variables"]["input"]
                assert sent["definitionId"] == str(world.definition.guid)
                assert sent["expectedRevision"] == saved["revision"]
                assert sent["expectedInputSchemaDigest"] == saved["inputSchemaDigest"]
                assert sent["requestId"] == saved["requestId"] and sent["inputs"] == {
                    "message": marker
                }
                assert all(
                    org == str(world.org.guid)
                    for q, org in proxy.requests
                    if "Definition" in q["query"]
                )
                assert not any(
                    "runWorkflowDefinition(" in q["query"] for q, _ in proxy.requests
                )

                # New invalid requests are refused, including schema errors, without a legacy fallback.
                for name, extra, target in [
                    ("slug", [], world.definition.slug),
                    ("literal", ["--input", marker], str(world.definition.guid)),
                    ("ceiling", [], str(world.definition.guid)),
                    ("schema", [], str(world.definition.guid)),
                ]:
                    request = tmp_path / f"{name}.json"
                    token = world.token
                    if name == "ceiling":

                        def narrow():
                            issued = mint_token()
                            ApiToken.objects.create(
                                user=world.user,
                                organization=world.org,
                                name="Read-only alias",
                                token_hash=issued.token_hash,
                                scopes=["read:apps"],
                            )
                            return issued.plaintext

                        token = await sync_to_async(narrow)()
                    proxy.reject_schema = name == "schema"
                    inputs.write_text(json.dumps({"message": marker}))
                    refused = await asyncio.to_thread(
                        invoke,
                        world,
                        proxy,
                        tmp_path,
                        [
                            "agent",
                            "run",
                            target,
                            "--request-file",
                            str(request),
                            "--inputs-file",
                            str(inputs),
                            "--yes",
                            "--json",
                            *extra,
                        ],
                        token=token,
                    )
                    assert refused.returncode != 0, (
                        name,
                        refused.stdout,
                        refused.stderr,
                    )
                    assert marker not in refused.stdout + refused.stderr
                    assert (
                        await sync_to_async(WorkflowDefinitionStart.objects.count)()
                        == 1
                    )
                assert not any(
                    "runWorkflowDefinition(" in q["query"] for q, _ in proxy.requests
                )
            finally:
                if (await handle.describe()).status.name == "RUNNING":
                    await handle.terminate("Disposable alias acceptance cleanup")
