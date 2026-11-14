"""Actual compiled CLI/App/Postgres/mTLS signed image binding and recovery."""

import hashlib
import json
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanImageBinding


def qualify_image_binding(
    case,
    image_capture,
    run,
    private_file,
    dropped,
    job,
    tree_flags,
    plan,
    tree,
    charts,
    workload,
    image,
    tmp_path,
):
    import management_plan_image_binding_service as service

    artifact_id = str(uuid4())
    body = {
        "format": 1,
        "artifactId": artifact_id,
        "planId": plan["id"],
        "planDigest": plan["plan"]["planDigest"],
        "treeId": tree["id"],
        "treeDigest": tree["treeDigest"],
        "workloadId": workload["id"],
        "workloadDigest": workload["workloadDigest"],
        "imageBindings": {
            image_capture.registry.reference: {
                "sourceKind": "registry",
                "artifactId": image["id"],
                "artifactDigest": image["artifactDigest"],
            }
        },
        "maximumBytes": "16777216",
        "maximumImageBytes": "268435456",
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
        "--artifact",
        artifact_id,
    )
    roots = (
        case.engine.install_plans.root,
        case.engine.install_chart_artifacts,
        case.engine.install_execution_trees,
        case.engine.install_render_contexts,
        case.engine.install_workload_inventories,
        case.engine.install_registry_images,
    )
    before = [service.preparation.storage._scan(root, 1073741824) for root in roots]
    private = private_file("image-binding.json", body)
    journal = tmp_path / "image-binding-journal.json"
    reads = len(image_capture.registry.requests)
    dropped["prepareNvkeOperatorInstallPlanImageBinding("] = 1
    run(
        "agent",
        "install-plan",
        "image-binding",
        "prepare",
        job,
        *flags,
        "--file",
        private,
        "--request-file",
        journal,
        ok=False,
    )
    saved = json.loads(journal.read_text())
    assert saved["artifactId"] == artifact_id and saved["workloadId"] == workload["id"]
    assert saved["workloadVersion"] == workload["version"]
    assert saved["workloadDigest"] == workload["workloadDigest"]
    assert saved["inputSha256"] == hashlib.sha256(private.read_bytes()).hexdigest()
    assert not any(
        key in saved
        for key in ("imageBindings", "reference", "maximumImageBytes", "requestText")
    )
    assert image_capture.registry.reference not in journal.read_text()
    assert image["id"] not in journal.read_text()
    row = NvkeInstallPlanImageBinding.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    retained = NvkeInstallPlanImageBinding.objects.filter(pk=row.pk).values().get()
    root = case.engine.install_image_bindings / artifact_id
    assert (root / "index.json").is_file()
    attempts = sorted(path.name for path in (root / "work").iterdir())
    assert len(attempts) == 1
    image_capture.restart()
    with (
        patch.object(
            service.registry_images.registry,
            "capture",
            side_effect=AssertionError("Cold acquisition"),
        ),
        patch.object(
            service.store.bindings.workloads.inventory,
            "from_context",
            side_effect=AssertionError("Cold render"),
        ),
        patch.object(
            service.store.bindings.workloads.inventory,
            "capture_native",
            side_effect=AssertionError("Cold native collection"),
        ),
    ):
        result = run("agent", "install-plan", "image-binding", "review", job, *flags)
        assert (
            run(
                "agent",
                "install-plan",
                "image-binding",
                "retry-preparation",
                job,
                *flags,
            )
            == result
        )
    assert result["id"] == artifact_id and result["workloadId"] == workload["id"]
    assert result["workloadVersion"] == workload["version"]
    assert result["workloadDigest"] == workload["workloadDigest"]
    assert (
        result["imageCount"] == 1
        and result["imageUseCount"] == workload["imageUseCount"]
    )
    assert result["verifiedImageBytes"] == image["bytes"]
    assert result["continuationAuthorized"] is False
    assert image_capture.registry.reference not in json.dumps(result)
    assert len(image_capture.registry.requests) == reads
    assert sorted(path.name for path in (root / "work").iterdir()) == attempts
    assert (
        NvkeInstallPlanImageBinding.objects.filter(pk=row.pk).values().get() == retained
    )
    assert [
        service.preparation.storage._scan(root, 1073741824) for root in roots
    ] == before
    assert not case.engine._admissions().get(artifact_id)
    return result
