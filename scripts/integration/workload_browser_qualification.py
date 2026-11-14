"""Owned browser/API peer for the authenticated workload integration fixture."""

import json
import os
import subprocess
from unittest.mock import patch
from pathlib import Path
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanWorkload


def run_workload_browser(
    *,
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
    context_summary,
    restart,
    dropped,
):
    frontend = Path(os.environ["NVKE_TEST_WORKLOAD_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-workload-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_WORKLOAD_STORYBOOK"]
    artifact_id = str(uuid4())
    config = {
        "storybook": storybook,
        "endpoint": endpoint + "/app/gql/config/",
        "bearer": "",
        "artifactId": artifact_id,
        "contextBindings": {
            context_summary["item"]: {
                "contextId": context_summary["id"],
                "contextDigest": context_summary["contextDigest"],
            }
        },
        "fixture": {
            "scope": {
                "server": storybook + "/__nvke-workload-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-workload-browser-source",
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
    config["bearer"] = bearer
    config_path = tmp_path / "workload-browser-private.json"
    with os.fdopen(
        os.open(
            config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600
        ),
        "w",
    ) as output:
        json.dump(config, output)
    dropped["prepareNvkeOperatorInstallPlanWorkload("] = 1
    log = tmp_path / "workload-browser-private.log"
    from astrolift_clusters import nvke_install_plan_workloads as workloads

    original_publish = workloads._publish
    restarted = []

    def published(*args, **kwargs):
        value = original_publish(*args, **kwargs)
        if not restarted and str(args[0]) == artifact_id:
            restart()
            restarted.append(True)
        return value

    with (
        patch.object(workloads, "_publish", side_effect=published),
        os.fdopen(
            os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600),
            "w",
        ) as output,
    ):
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_WORKLOAD_BROWSER_CONFIG": str(config_path)},
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=900,
            check=False,
        )
    assert restarted == [True]
    assert result.returncode == 0, (
        "Owned browser/API workload qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert report["passed"] and report["pageErrors"] == []
    assert report["kind"] == "workload"
    row = NvkeInstallPlanWorkload.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["artifactDigest"] == report["artifactDigest"]
    assert row.summary["captureDigest"] == report["captureDigest"]
    assert row.summary["workloadDigest"] == report["workloadDigest"]
    assert row.summary["continuationAuthorized"] is False
    return row
