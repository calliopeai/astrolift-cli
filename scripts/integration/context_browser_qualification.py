"""Owned browser/API peer for the authenticated context integration fixture."""

import json
import os
import subprocess
from pathlib import Path
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanContext


def run_context_browser(
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
    dropped,
):
    frontend = Path(os.environ["NVKE_TEST_CONTEXT_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-context-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_CONTEXT_STORYBOOK"]
    artifact_id = str(uuid4())
    config = {
        "storybook": storybook,
        "endpoint": endpoint + "/app/gql/config/",
        "bearer": "",
        "artifactId": artifact_id,
        "item": "nktp",
        "ordinal": 2,
        "fixture": {
            "scope": {
                "server": storybook + "/__nvke-context-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-context-browser-source",
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
    config_path = tmp_path / "context-browser-private.json"
    with config_path.open("x") as output:
        config_path.chmod(0o600)
        json.dump(config, output)
    dropped["prepareNvkeOperatorInstallPlanContext("] = 1
    log = tmp_path / "context-browser-private.log"
    with log.open("x") as output:
        log.chmod(0o600)
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_CONTEXT_BROWSER_CONFIG": str(config_path)},
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=900,
            check=False,
        )
    assert result.returncode == 0, (
        "Owned browser/API context qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert report["passed"] and report["pageErrors"] == []
    row = NvkeInstallPlanContext.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["contextDigest"] == report["contextDigest"]
    assert row.summary["captureDigest"] == report["captureDigest"]
    assert row.summary["continuationAuthorized"] is False
    return row
