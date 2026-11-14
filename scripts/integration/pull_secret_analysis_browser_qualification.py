"""Owned browser against App/Postgres/mTLS and native Secret-only analysis."""

import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanPullSecretAnalysis
from pull_authority_cli_qualification import require_offline_replay


def run_pull_secret_analysis_browser(
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
    pull,
):
    import management_plan_pull_secret_analysis_service as service
    from astrolift_clusters import nvke_install_plan_pull_secret_analysis as artifacts

    frontend = Path(os.environ["NVKE_TEST_PULL_SECRET_ANALYSIS_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-pull-secret-analysis-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_PULL_SECRET_ANALYSIS_STORYBOOK"]
    artifact_id = str(uuid4())
    config = {
        "storybook": storybook,
        "endpoint": endpoint + "/app/gql/config/",
        "bearer": bearer,
        "artifactId": artifact_id,
        "body": capture.analysis_body(plan, tree, binding, pull, artifact_id),
        "fixture": {
            "pullAuthority": pull,
            "binding": binding,
            "workload": workload,
            "scope": {
                "server": storybook + "/__nvke-pull-secret-analysis-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-pull-secret-analysis-browser-source",
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
    config_path = tmp_path / "pull-secret-analysis-browser-private.json"
    with os.fdopen(
        os.open(
            config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600
        ),
        "w",
    ) as output:
        json.dump(config, output)
    config_digest = hashlib.sha256(config_path.read_bytes()).hexdigest()
    log = tmp_path / "pull-secret-analysis-browser-private.log"
    case = capture.case
    roots = [
        case.engine.install_plans.root,
        case.engine.install_chart_artifacts,
        case.engine.install_execution_trees,
        case.engine.install_render_contexts,
        case.engine.install_workload_inventories,
        case.engine.install_registry_images,
        case.engine.install_image_bindings,
        case.engine.install_pull_authorities,
    ]
    before = [service.store.storage._scan(root, 1073741824) for root in roots]
    original = type(operation).all_objects.filter(pk=operation.pk).values().get()
    admissions = copy.deepcopy(case.engine._admissions())
    registry_reads = len(capture.images.registry.requests)
    dropped["prepareNvkeOperatorInstallPlanPullSecretAnalysis("] = 1
    original_publish, restarted = artifacts._publish, []
    original_collect = service.store.captures.pulls.collect
    original_transport = service.captures.store.transport.FrozenKubernetesRead
    reads_after_prepare = []
    original_analyze = service.store.executor.FrozenKeyringHelper.analyze

    def no_cold_analyze(helper, *args, **kwargs):
        assert not restarted, "Cold browser recovery reran native helper"
        return original_analyze(helper, *args, **kwargs)

    def no_cold_collect(*args, **kwargs):
        if restarted:
            require_offline_replay(kwargs.get("read_response"))
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
        patch.object(
            service.store.executor.FrozenKeyringHelper, "analyze", new=no_cold_analyze
        ),
        patch.object(
            service.store.captures.pulls, "collect", side_effect=no_cold_collect
        ),
        patch.object(
            service.captures.store.transport,
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
            env={
                **os.environ,
                "NVKE_PULL_SECRET_ANALYSIS_BROWSER_CONFIG": str(config_path),
            },
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=3 * 360 + 120,
            check=False,
        )
    assert restarted == [True]
    assert result.returncode == 0, (
        "Owned Secret analysis browser qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert (
        report["passed"]
        and report["imageUseCount"] == len(capture.selected)
        and report["pageErrors"] == []
    )
    row = NvkeInstallPlanPullSecretAnalysis.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["artifactDigest"] == report["artifactDigest"]
    assert row.summary["captureDigest"] == report["captureDigest"]
    assert row.summary["authorityDigest"] == report["rawDigest"]
    assert row.summary["imageUseCount"] == len(capture.selected)
    assert row.summary["candidateCount"] == len(capture.selected)
    assert row.summary["missingReferenceCount"] == 0
    assert row.source_binding["pull_authority"] == pull["id"]
    assert row.source_binding["pull_authority_version"] == pull["version"]
    assert row.source_binding["pull_authority_digest"] == pull["authorityDigest"]
    assert row.summary["analyzedAt"] == report["analyzedAt"]
    assert row.summary["sourceObservedAt"] == report["sourceObservedAt"]
    assert row.summary["continuationAuthorized"] is False
    artifact = case.engine.install_pull_secret_analyses / artifact_id
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
