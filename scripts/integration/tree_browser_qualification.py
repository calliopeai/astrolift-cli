"""Owned browser/API peer for the native tree integration fixture."""

import json
import os
import subprocess
from pathlib import Path
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanTree


def run_tree_browser(
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
    dropped,
):
    frontend = Path(os.environ["NVKE_TEST_TREE_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-tree-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_TREE_STORYBOOK"]
    artifact_id = str(uuid4())
    config = {
        "storybook": storybook,
        "endpoint": endpoint + "/app/gql/config/",
        "bearer": "",
        "artifactId": artifact_id,
        "fixture": {
            "scope": {
                "server": storybook + "/__nvke-tree-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-tree-browser-source",
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
                "artifacts": charts,
                "canReview": True,
                "pending": False,
                "loading": False,
                "error": None,
            },
        },
    }
    config["bearer"] = bearer
    config_path = tmp_path / "tree-browser-private.json"
    with config_path.open("x") as output:
        config_path.chmod(0o600)
        json.dump(config, output)
    dropped["prepareNvkeOperatorInstallPlanTree("] = 1
    log = tmp_path / "tree-browser-private.log"
    with log.open("x") as output:
        log.chmod(0o600)
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_TREE_BROWSER_CONFIG": str(config_path)},
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=600,
            check=False,
        )
    assert result.returncode == 0, (
        "Owned browser/API tree qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert report["passed"] and report["pageErrors"] == []
    row = NvkeInstallPlanTree.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["treeDigest"] == report["treeDigest"]
    assert row.summary["bytes"] == report["bytes"]
    assert row.summary["continuationAuthorized"] is False
    return row
