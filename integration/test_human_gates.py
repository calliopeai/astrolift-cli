"""Compiled CLI, real bearer GraphQL, PostgreSQL, Temporal, and response loss."""

import asyncio
import json
import os
from uuid import uuid4

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip("requires compiled CLI and the recoverable gate backend", allow_module_level=True)

from asgiref.sync import sync_to_async
from constance import settings as constance_settings
from astrolift_workflows.tests.test_serial_collections_temporal_2156 import REGISTERED
from astrolift_workflows.workflows.workflow_definition_run import WorkflowDefinitionRunWorkflow
from core.testing.temporal import temporal_test_env, temporal_worker
from workflows.models import WorkflowDefinition, WorkflowStage, WorkflowStageExecution

from test_definition_reviewed import (
    clear_disposable_engine_connection_cache,
    create_definition_world,
)
from test_pipeline_reviewed import forwarding_proxy, invoke, running_api

pytestmark = pytest.mark.django_db(transaction=True)


def create_gate_world(kind):
    world = create_definition_world()
    world.user.email = "gate-approver@example.test"
    world.user.save()
    definition = world.definition
    definition.input_schema = {"type": "object", "properties": {"items": {"type": "array"}}, "additionalProperties": False}
    definition.pattern_kind = "chained"
    definition.save()
    stage = definition.stages.get()
    stage.kind = "human_gate"
    stage.timeout_seconds = 3600
    stage.approvers = [world.user.email]
    if kind != "root":
        stage.kind = "collection"
        stage.output_key = "each"
        stage.iteration = {"max_items": 3, "items_path": "items", "body_end": "body"}
        body = WorkflowStage.objects.create(definition=definition, order=1, kind="human_gate", output_key="body",
            timeout_seconds=3600, approvers=[world.user.email])
        if kind == "nested":
            child = WorkflowDefinition.objects.create(organization=world.org, name="Nested gate", slug="nested-gate", model_label="")
            WorkflowStage.objects.create(definition=child, order=0, kind="human_gate", timeout_seconds=3600, approvers=[world.user.email])
            body.kind = "workflow"
            body.workflow_ref = child.slug
            body.save()
    stage.save()
    return world


@pytest.mark.parametrize("kind", ["root", "collection", "nested"])
async def test_cli_recovers_dropped_decision_response_without_another_write(settings, monkeypatch, tmp_path, kind):
    async with temporal_test_env() as engine:
        settings.ASTROLIFT_TEMPORAL_ENABLED = True
        settings.TEMPORAL_ADDRESS = engine.client.service_client.config.target_host
        settings.TEMPORAL_NAMESPACE = engine.client.namespace
        settings.TEMPORAL_TASK_QUEUE = f"cli-gate-{uuid4().hex}"
        settings.CACHES = {"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}}
        # Constance snapshots Django settings on import. Disable its optional
        # shared cache while this test isolates permission caches in memory.
        monkeypatch.setattr(constance_settings, "DATABASE_CACHE_BACKEND", None)
        world = await sync_to_async(create_gate_world)(kind)
        inputs = tmp_path / "inputs.json"
        inputs.write_text(json.dumps({"items": [{"text": "one"}]}))
        async with temporal_worker(engine, task_queue=settings.TEMPORAL_TASK_QUEUE,
            workflows=[WorkflowDefinitionRunWorkflow], activities=REGISTERED):
            with running_api() as port, forwarding_proxy(port, start_operation="DecideHumanGate", start_field="decideHumanGate") as proxy:
                started = await asyncio.to_thread(invoke, world, proxy, tmp_path, ["workflow", "definition-start", str(world.definition.guid),
                    "--request-file", str(tmp_path / "request.json"), "--inputs-file", str(inputs), "--yes", "--json"])
                assert started.returncode == 0, started.stderr
                start = json.loads(started.stdout)["start"]
                root = engine.client.get_workflow_handle(start["temporalWorkflowId"], run_id=start["temporalRunId"])
                try:
                    for _ in range(50):
                        listed = await asyncio.to_thread(invoke, world, proxy, tmp_path, ["workflow", "gates", "--json"])
                        assert listed.returncode == 0, listed.stderr
                        gates = json.loads(listed.stdout)
                        if gates:
                            break
                        await asyncio.sleep(.05)
                    else:
                        pytest.fail("No actionable gate appeared")
                    gate = gates[0]
                    assert len(gates) == 1
                    exact = ["--run", gate["runGuid"], "--stage", gate["executionGuid"]]
                    temporal_id = gate["temporalExecution"]["runId"]
                    if kind != "root":
                        assert temporal_id != start["temporalRunId"]
                    status_args = ["workflow", "gate-status", *exact, "--json"]
                    before = await asyncio.to_thread(invoke, world, proxy, tmp_path, status_args)
                    assert before.returncode == 0 and json.loads(before.stdout)["gate"]["requestState"] == "not_requested"
                    decision = ["workflow", "gate", *exact, "--temporal-run", temporal_id,
                        "--decision", "approve", "--note", "Explicit user choice", "--yes", "--json"]
                    proxy.drop_start = True
                    lost = await asyncio.to_thread(invoke, world, proxy, tmp_path, decision)
                    assert lost.returncode != 0
                    unknown = json.loads(lost.stdout)
                    assert unknown["ok"] is False and unknown["target"]["stageExecutionGuid"] == gate["executionGuid"]
                    assert "gate-status" in lost.stderr
                    await asyncio.wait_for(root.result(), 20)
                    position = len(proxy.requests)
                    recovered = await asyncio.to_thread(invoke, world, proxy, tmp_path, status_args)
                    assert recovered.returncode == 0, recovered.stderr
                    final = json.loads(recovered.stdout)
                    assert final["gate"]["requestState"] == "recorded"
                    assert final["gate"]["recordedDecision"] == "approved" and final["gate"]["decidedByMe"]
                    assert all("mutation" not in query["query"] for query, _org in proxy.requests[position:])
                    repeated = await asyncio.to_thread(invoke, world, proxy, tmp_path, decision)
                    assert repeated.returncode == 0 and json.loads(repeated.stdout)["gate"]["requestState"] == "recorded"
                    conflict_args = list(decision)
                    conflict_args[conflict_args.index("approve")] = "reject"
                    conflict = await asyncio.to_thread(invoke, world, proxy, tmp_path, conflict_args)
                    assert conflict.returncode != 0
                    receipt = json.loads(conflict.stdout)
                    assert receipt["gate"]["recordedDecision"] == "approved" and receipt["target"]["temporalRunId"] == temporal_id
                    row = await sync_to_async(WorkflowStageExecution.objects.get)(guid=gate["executionGuid"])
                    assert row.output["human_gate"]["decided_by_user_id"] == world.user.pk
                    assert all("signalWorkflowInstance" not in query["query"] for query, _org in proxy.requests)
                finally:
                    if (await root.describe()).status.name == "RUNNING":
                        await root.terminate(reason="Disposable CLI gate test cleanup")
