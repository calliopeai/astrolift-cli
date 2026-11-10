"""Owned browser/API peer for the authenticated image integration fixture."""

import json
import os
import subprocess
from contextlib import nullcontext
from unittest.mock import patch
from pathlib import Path
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanImage


def run_image_browser(
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
    reference,
    root_digest,
    dropped,
    workload=None,
    restart=None,
):
    frontend = Path(os.environ["NVKE_TEST_IMAGE_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-image-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_IMAGE_STORYBOOK"]
    artifact_id = str(uuid4())
    config = {
        "storybook": storybook,
        "endpoint": endpoint + "/app/gql/config/",
        "bearer": "",
        "artifactId": artifact_id,
        "reference": reference,
        "rootDigest": root_digest,
        "fixture": {
            "scope": {
                "server": storybook + "/__nvke-image-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-image-browser-source",
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
    if workload is not None:
        config["fixture"]["workload"] = workload
    config["bearer"] = bearer
    config_path = tmp_path / "image-browser-private.json"
    with os.fdopen(
        os.open(
            config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600
        ),
        "w",
    ) as output:
        json.dump(config, output)
    dropped["prepareNvkeOperatorInstallPlanImage("] = 1
    log = tmp_path / "image-browser-private.log"
    from astrolift_clusters import nvke_install_plan_images as images

    original_publish = images._publish
    restarted = []

    def published(*args, **kwargs):
        value = original_publish(*args, **kwargs)
        if not restarted and str(args[0]) == artifact_id:
            restart()
            restarted.append(True)
        return value

    publisher = (
        patch.object(images, "_publish", side_effect=published)
        if restart
        else nullcontext()
    )
    with (
        publisher,
        os.fdopen(
            os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600),
            "w",
        ) as output,
    ):
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_IMAGE_BROWSER_CONFIG": str(config_path)},
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=900,
            check=False,
        )
    if restart:
        assert restarted == [True]
    assert result.returncode == 0, (
        "Owned browser/API image qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert report["passed"] and report["pageErrors"] == []
    row = NvkeInstallPlanImage.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["artifactDigest"] == report["artifactDigest"]
    assert row.summary["captureDigest"] == report["captureDigest"]
    assert row.summary["rootDigest"] == root_digest
    assert row.summary["continuationAuthorized"] is False
    if workload is not None:
        assert report["workloadId"] == row.summary["workloadId"] == workload["id"]
        assert (
            report["workloadVersion"]
            == row.source_binding["workload_version"]
            == workload["version"]
        )
        assert (
            report["workloadDigest"]
            == row.summary["workloadDigest"]
            == workload["workloadDigest"]
        )
    return row
