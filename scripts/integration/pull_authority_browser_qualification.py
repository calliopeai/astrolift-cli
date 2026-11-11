"""Owned production browser controller against App/Postgres/mTLS Pod pull service."""

import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanPullAuthority


def run_pull_authority_browser(
    *,
    capture,
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
    import management_plan_pull_authority_service as service
    from astrolift_clusters import nvke_install_plan_pull_authority as artifacts

    frontend = Path(os.environ["NVKE_TEST_PULL_AUTHORITY_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-pull-authority-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_PULL_AUTHORITY_STORYBOOK"]
    artifact_id = str(uuid4())
    config = {
        "storybook": storybook,
        "endpoint": endpoint + "/app/gql/config/",
        "bearer": bearer,
        "artifactId": artifact_id,
        "body": capture.body(plan, tree, binding, artifact_id),
        "fixture": {
            "binding": binding,
            "workload": workload,
            "scope": {
                "server": storybook + "/__nvke-pull-authority-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-pull-authority-browser-source",
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
    config_path = tmp_path / "pull-authority-browser-private.json"
    with os.fdopen(
        os.open(
            config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600
        ),
        "w",
    ) as output:
        json.dump(config, output)
    config_digest = hashlib.sha256(config_path.read_bytes()).hexdigest()
    log = tmp_path / "pull-authority-browser-private.log"
    case = capture.case
    roots = [
        case.engine.install_plans.root,
        case.engine.install_chart_artifacts,
        case.engine.install_execution_trees,
        case.engine.install_render_contexts,
        case.engine.install_workload_inventories,
        case.engine.install_registry_images,
        case.engine.install_image_bindings,
    ]
    before = [service.store.storage._scan(root, 1073741824) for root in roots]
    original = type(operation).all_objects.filter(pk=operation.pk).values().get()
    admissions = copy.deepcopy(case.engine._admissions())
    registry_reads = len(capture.images.registry.requests)
    dropped["prepareNvkeOperatorInstallPlanPullAuthority("] = 1
    original_publish, restarted = artifacts._publish, []
    original_collect = service.store.pulls.collect
    original_transport = service.store.transport.FrozenKubernetesRead
    reads_after_prepare = []

    def no_cold_collect(*args, **kwargs):
        assert not restarted, "Cold browser recovery repeated Pod pull collection"
        return original_collect(*args, **kwargs)

    def no_cold_transport(*args, **kwargs):
        assert not restarted, "Cold browser recovery opened Kubernetes transport"
        return original_transport(*args, **kwargs)

    def published(*args, **kwargs):
        value = original_publish(*args, **kwargs)
        if not restarted and str(args[0]) == artifact_id:
            reads_after_prepare.append(len(capture.context.kube.flow.http.requests))
            capture.images.restart()
            restarted.append(True)
        return value

    with (
        patch.object(artifacts, "_publish", side_effect=published),
        patch.object(service.store.pulls, "collect", side_effect=no_cold_collect),
        patch.object(
            service.store.transport,
            "FrozenKubernetesRead",
            side_effect=no_cold_transport,
        ),
        os.fdopen(
            os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600),
            "w",
        ) as output,
    ):
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_PULL_AUTHORITY_BROWSER_CONFIG": str(config_path)},
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=3 * 360 + 120,
            check=False,
        )
    assert restarted == [True]
    assert result.returncode == 0, (
        "Owned Pod pull browser qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert (
        report["passed"]
        and report["podCount"] == len(capture.selected)
        and report["pageErrors"] == []
    )
    row = NvkeInstallPlanPullAuthority.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["artifactDigest"] == report["artifactDigest"]
    assert row.summary["captureDigest"] == report["captureDigest"]
    assert row.summary["authorityDigest"] == report["rawDigest"]
    assert row.summary["podCount"] == len(capture.selected)
    assert row.summary["serviceAccountCount"] == 1 and row.summary["secretCount"] == 1
    assert row.summary["continuationAuthorized"] is False
    artifact = case.engine.install_pull_authorities / artifact_id
    assert (artifact / "index.json").is_file()
    assert len(list((artifact / "work").iterdir())) == 1
    assert [service.store.storage._scan(root, 1073741824) for root in roots] == before
    assert (
        type(operation).all_objects.filter(pk=operation.pk).values().get() == original
    )
    assert len(capture.images.registry.requests) == registry_reads
    assert reads_after_prepare == [len(capture.context.kube.flow.http.requests)]
    assert case.engine._admissions() == admissions
    assert hashlib.sha256(config_path.read_bytes()).hexdigest() == config_digest
    return row
