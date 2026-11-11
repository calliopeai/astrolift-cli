"""Owned browser/App/Postgres/mTLS immutable artifact recovery qualification."""

import hashlib
import json
import os
import subprocess
from pathlib import Path
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import (
    NvkeInstallPlanImagePayload,
    NvkeInstallPlanImmutableExecution,
)


def run_immutable_browser(
    *,
    case,
    image_capture,
    mode,
    selection,
    bearer,
    tmp_path,
    endpoint,
    world,
    site,
    authority,
    operation,
    plan,
    charts,
    tree,
    workload,
    binding,
    dropped,
):
    if mode not in {"payload", "execution"}:
        raise ValueError("Select the owned immutable artifact kind")
    frontend = Path(os.environ["NVKE_TEST_IMMUTABLE_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-immutable-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_IMMUTABLE_STORYBOOK"]
    artifact_id = str(uuid4())
    body = {
        "format": 1,
        "artifactId": artifact_id,
        "planId": plan["id"],
        "planDigest": plan["plan"]["planDigest"],
        "treeId": tree["id"],
        "treeDigest": tree["treeDigest"],
        "bindingId": binding["id"],
        "bindingDigest": binding["bindingDigest"],
        "maximumBytes": "16777216",
        "timeoutSeconds": 300,
    }
    if mode == "payload":
        body.update({"item": selection, "maximumResources": 1000})
    else:
        body["payloadBindings"] = selection
    config = {
        "storybook": storybook,
        "endpoint": endpoint + "/app/gql/config/",
        "bearer": bearer,
        "artifactId": artifact_id,
        "body": body,
        "fixture": {
            "mode": mode,
            "binding": binding,
            "workload": workload,
            "scope": {
                "server": storybook + "/__nvke-immutable-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-immutable-browser-source",
                "site": {
                    "id": str(site.guid),
                    "version": site.version,
                    "upstreamRevision": site.upstream_revision,
                },
                "operation": {
                    "id": str(operation.guid),
                    "version": operation.version,
                    "siteId": str(site.guid),
                    "status": "UNCERTAIN",
                    "operationKey": "tooling.management.install",
                },
                "authority": authority,
                "plan": plan,
                "charts": charts,
                "artifacts": tree,
                "canReview": True,
                "pending": False,
                "loading": False,
                "error": None,
            },
        },
    }
    config_path = tmp_path / f"immutable-{mode}-browser-private.json"
    with os.fdopen(
        os.open(
            config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600
        ),
        "w",
    ) as output:
        json.dump(config, output)
    config_digest = hashlib.sha256(config_path.read_bytes()).hexdigest()
    log = tmp_path / f"immutable-{mode}-browser-private.log"
    from astrolift_clusters import nvke_install_plan_immutable as artifacts
    import management_plan_image_payload_service as payloads
    import management_plan_immutable_execution_service as executions

    roots = [
        case.engine.install_plans.root,
        case.engine.install_chart_artifacts,
        case.engine.install_execution_trees,
        case.engine.install_render_contexts,
        case.engine.install_workload_inventories,
        case.engine.install_registry_images,
        case.engine.install_image_bindings,
    ]
    if mode == "execution":
        roots.append(case.engine.install_image_payloads)
    before = [payloads.preparation.storage._scan(root, 1073741824) for root in roots]
    original = type(operation).all_objects.filter(pk=operation.pk).values().get()
    reads = len(image_capture.registry.requests)
    suffix = "ImagePayload" if mode == "payload" else "ImmutableExecutionTree"
    dropped["prepareNvkeOperatorInstallPlan" + suffix + "("] = 1
    original_publish, restarted = artifacts._publish, []
    materialize, assemble = (
        payloads.store.rewrite.materialize,
        executions.store.execution.assemble,
    )

    def no_cold_rewrite(*args, **kwargs):
        assert not restarted, "Cold browser recovery repeated phase rewriting"
        return materialize(*args, **kwargs)

    def no_cold_assembly(*args, **kwargs):
        assert not restarted, "Cold browser recovery repeated tree assembly"
        return assemble(*args, **kwargs)

    def published(*args, **kwargs):
        value = original_publish(*args, **kwargs)
        if not restarted and str(args[0]) == artifact_id:
            image_capture.restart()
            restarted.append(True)
        return value

    with (
        patch.object(artifacts, "_publish", side_effect=published),
        patch.object(
            payloads.store.rewrite, "materialize", side_effect=no_cold_rewrite
        ),
        patch.object(
            executions.store.execution, "assemble", side_effect=no_cold_assembly
        ),
        os.fdopen(
            os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600),
            "w",
        ) as output,
    ):
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_IMMUTABLE_BROWSER_CONFIG": str(config_path)},
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=1200,
            check=False,
        )
    assert restarted == [True]
    assert result.returncode == 0, (
        "Owned immutable browser qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert report["passed"] and report["mode"] == mode and report["pageErrors"] == []
    model = (
        NvkeInstallPlanImagePayload
        if mode == "payload"
        else NvkeInstallPlanImmutableExecution
    )
    row = model.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["artifactDigest"] == report["artifactDigest"]
    assert row.summary["captureDigest"] == report["captureDigest"]
    assert (
        row.summary["payloadDigest" if mode == "payload" else "executionDigest"]
        == report["rawDigest"]
    )
    assert row.summary["continuationAuthorized"] is False
    artifact_root = (
        case.engine.install_image_payloads
        if mode == "payload"
        else case.engine.install_immutable_execution_trees
    )
    assert (artifact_root / artifact_id / "index.json").is_file()
    assert len(list((artifact_root / artifact_id / "work").iterdir())) == 1
    assert [
        payloads.preparation.storage._scan(root, 1073741824) for root in roots
    ] == before
    assert (
        type(operation).all_objects.filter(pk=operation.pk).values().get() == original
    )
    assert len(image_capture.registry.requests) == reads
    assert not case.engine._admissions().get(artifact_id)
    assert hashlib.sha256(config_path.read_bytes()).hexdigest() == config_digest
    return row
