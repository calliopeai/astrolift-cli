"""Compiled CLI/App/Postgres/native workload inventory and cold recovery."""

import hashlib
import json
from uuid import uuid4
from unittest.mock import patch

from astrolift_clusters.models import NvkeInstallPlanWorkload


def qualify_workload(
    case,
    context_capture,
    context_summary,
    run,
    private_file,
    dropped,
    job,
    tree_flags,
    plan,
    tree,
    charts,
    tmp_path,
):
    import management_plan_workload_service as service

    artifact_id = str(uuid4())
    body = {
        "format": 1,
        "artifactId": artifact_id,
        "planId": plan["id"],
        "planDigest": plan["plan"]["planDigest"],
        "treeId": tree["id"],
        "treeDigest": tree["treeDigest"],
        "contextBindings": {
            context_summary["item"]: {
                "contextId": context_summary["id"],
                "contextDigest": context_summary["contextDigest"],
            }
        },
        "maximumBytes": "16777216",
        "maximumResources": 10000,
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
        "--artifact",
        artifact_id,
    )
    roots = (
        case.engine.install_plans.root,
        case.engine.install_chart_artifacts,
        case.engine.install_execution_trees,
        case.engine.install_render_contexts,
    )
    before = [service.preparation.storage._scan(root, 1073741824) for root in roots]
    private = private_file("workload.json", body)
    journal = tmp_path / "workload-journal.json"
    dropped["prepareNvkeOperatorInstallPlanWorkload("] = 1
    run(
        "agent",
        "install-plan",
        "workload",
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
    assert (
        saved["artifactId"] == artifact_id
        and saved["inputSha256"] == hashlib.sha256(private.read_bytes()).hexdigest()
    )
    assert "contextBindings" not in saved and "maximumBytes" not in saved
    row = NvkeInstallPlanWorkload.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    retained = NvkeInstallPlanWorkload.objects.filter(pk=row.pk).values().get()
    root = case.engine.install_workload_inventories / artifact_id
    assert (root / "index.json").is_file()
    attempts = sorted(p.name for p in (root / "work").iterdir())
    assert len(attempts) == 1
    context_capture.restart()
    with (
        patch.object(
            service.workloads.inventory,
            "from_context",
            side_effect=AssertionError("Cold Helm collection"),
        ),
        patch.object(
            service.workloads.inventory,
            "capture_native",
            side_effect=AssertionError("Cold native collection"),
        ),
    ):
        result = run("agent", "install-plan", "workload", "review", job, *flags)
        assert result["id"] == artifact_id and result["itemCount"] == plan["itemCount"]
        assert result["continuationAuthorized"] is False
        assert len(result["workloadDigest"]) == 64
        assert (
            run("agent", "install-plan", "workload", "retry-preparation", job, *flags)
            == result
        )
    assert NvkeInstallPlanWorkload.objects.filter(pk=row.pk).values().get() == retained
    assert sorted(p.name for p in (root / "work").iterdir()) == attempts
    assert [
        service.preparation.storage._scan(root, 1073741824) for root in roots
    ] == before
    assert not any(
        key in result
        for key in ("contextBindings", "documents", "credentials", "images")
    )
    return result
