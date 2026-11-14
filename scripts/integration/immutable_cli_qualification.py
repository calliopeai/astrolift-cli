"""Actual compiled CLI/App/Postgres/mTLS immutable payload/tree cold recovery."""

import hashlib
import json
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import (
    NvkeInstallPlanImagePayload,
    NvkeInstallPlanImmutableExecution,
)


def qualify_immutable_artifacts(
    case,
    image_capture,
    run,
    private_file,
    dropped,
    job,
    tree_flags,
    plan,
    tree,
    workload,
    binding,
    tmp_path,
):
    import management_plan_image_payload_service as payloads
    import management_plan_immutable_execution_service as executions

    source = case.engine.install_plans.load(
        plan["id"], expected_digest=plan["plan"]["planDigest"]
    )
    phases = source["plan"]["items"]
    common = {
        "format": 1,
        "planId": plan["id"],
        "planDigest": plan["plan"]["planDigest"],
        "treeId": tree["id"],
        "treeDigest": tree["treeDigest"],
        "bindingId": binding["id"],
        "bindingDigest": binding["bindingDigest"],
        "maximumBytes": "16777216",
        "timeoutSeconds": 300,
    }
    flags = (
        *tree_flags[:-2],
        "--tree",
        tree["id"],
        "--tree-version",
        tree["version"],
        "--tree-digest",
        tree["treeDigest"],
        "--workload",
        workload["id"],
        "--workload-version",
        workload["version"],
        "--workload-digest",
        workload["workloadDigest"],
        "--binding",
        binding["id"],
        "--binding-version",
        binding["version"],
        "--binding-digest",
        binding["bindingDigest"],
    )
    roots = (
        case.engine.install_plans.root,
        case.engine.install_chart_artifacts,
        case.engine.install_execution_trees,
        case.engine.install_render_contexts,
        case.engine.install_workload_inventories,
        case.engine.install_registry_images,
        case.engine.install_image_bindings,
    )
    before = [payloads.preparation.storage._scan(root, 1073741824) for root in roots]
    reads = len(image_capture.registry.requests)
    selected = {}
    for number, phase in enumerate(phases):
        artifact_id = str(uuid4())
        body = {
            **common,
            "artifactId": artifact_id,
            "item": phase["name"],
            "maximumResources": 1000,
        }
        private = private_file(f"phase-payload-{number}.json", body)
        journal = tmp_path / f"phase-payload-{number}-journal.json"
        arguments = (*flags, "--artifact", artifact_id)
        if number == 0:
            dropped["prepareNvkeOperatorInstallPlanImagePayload("] = 1
        result = run(
            "agent",
            "install-plan",
            "image-payload",
            "prepare",
            job,
            *arguments,
            "--file",
            private,
            "--request-file",
            journal,
            ok=number != 0,
        )
        saved = json.loads(journal.read_text())
        assert (
            saved["artifactId"] == artifact_id and saved["bindingId"] == binding["id"]
        )
        assert saved["inputSha256"] == hashlib.sha256(private.read_bytes()).hexdigest()
        assert "item" not in saved and "payloadBindings" not in saved
        row = NvkeInstallPlanImagePayload.objects.get(guid=artifact_id)
        assert row.status == "prepared"
        artifact = case.engine.install_image_payloads / artifact_id
        attempts = sorted(path.name for path in (artifact / "work").iterdir())
        assert len(attempts) == 1 and (artifact / "index.json").is_file()
        if number == 0:
            retained = (
                NvkeInstallPlanImagePayload.objects.filter(pk=row.pk).values().get()
            )
            image_capture.restart()
            with patch.object(
                payloads.store.rewrite,
                "materialize",
                side_effect=AssertionError("Cold payload rewrite"),
            ):
                result = run(
                    "agent", "install-plan", "image-payload", "review", job, *arguments
                )
                assert (
                    run(
                        "agent",
                        "install-plan",
                        "image-payload",
                        "retry-preparation",
                        job,
                        *arguments,
                    )
                    == result
                )
            assert (
                NvkeInstallPlanImagePayload.objects.filter(pk=row.pk).values().get()
                == retained
            )
        assert result["id"] == artifact_id and not result["continuationAuthorized"]
        assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
        selected[phase["name"]] = {
            "artifactId": artifact_id,
            "artifactDigest": result["artifactDigest"],
        }
        assert not case.engine._admissions().get(artifact_id)
    artifact_id = str(uuid4())
    body = {**common, "artifactId": artifact_id, "payloadBindings": selected}
    private = private_file("immutable-execution.json", body)
    journal = tmp_path / "immutable-execution-journal.json"
    arguments = (*flags, "--artifact", artifact_id)
    dropped["prepareNvkeOperatorInstallPlanImmutableExecutionTree("] = 1
    run(
        "agent",
        "install-plan",
        "immutable-execution-tree",
        "prepare",
        job,
        *arguments,
        "--file",
        private,
        "--request-file",
        journal,
        ok=False,
    )
    saved = json.loads(journal.read_text())
    assert saved["inputSha256"] == hashlib.sha256(private.read_bytes()).hexdigest()
    assert "payloadBindings" not in saved and not any(
        c["artifactId"] in journal.read_text() for c in selected.values()
    )
    row = NvkeInstallPlanImmutableExecution.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    retained = (
        NvkeInstallPlanImmutableExecution.objects.filter(pk=row.pk).values().get()
    )
    artifact = case.engine.install_immutable_execution_trees / artifact_id
    attempts = sorted(path.name for path in (artifact / "work").iterdir())
    assert len(attempts) == 1 and (artifact / "index.json").is_file()
    image_capture.restart()
    with (
        patch.object(
            payloads.store.rewrite,
            "materialize",
            side_effect=AssertionError("Cold phase rewrite"),
        ),
        patch.object(
            executions.store.execution,
            "assemble",
            side_effect=AssertionError("Cold execution assembly"),
        ),
    ):
        result = run(
            "agent",
            "install-plan",
            "immutable-execution-tree",
            "review",
            job,
            *arguments,
        )
        assert (
            run(
                "agent",
                "install-plan",
                "immutable-execution-tree",
                "retry-preparation",
                job,
                *arguments,
            )
            == result
        )
    assert result["id"] == artifact_id and not result["continuationAuthorized"]
    assert result["phaseCount"] == sum(p["proposedAction"] == "execute" for p in phases)
    assert result["skippedPhaseCount"] == sum(
        p["proposedAction"] == "skip" for p in phases
    )
    assert (
        NvkeInstallPlanImmutableExecution.objects.filter(pk=row.pk).values().get()
        == retained
    )
    assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
    assert [
        payloads.preparation.storage._scan(root, 1073741824) for root in roots
    ] == before
    assert len(image_capture.registry.requests) == reads
    assert not case.engine._admissions().get(artifact_id)
    for private_field in (
        "treeLocation",
        "arguments",
        "payloadBindings",
        image_capture.registry.reference,
    ):
        assert private_field not in json.dumps(result)
    return result
