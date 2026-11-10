"""Owned browser/API peer for the authenticated verification integration fixture."""

import json
import os
import subprocess
from pathlib import Path
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanVerification


def run_verification_browser(
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
    captured,
    dropped,
):
    frontend = Path(os.environ["NVKE_TEST_VERIFICATION_FRONTEND"]).resolve()
    runner = frontend / "e2e/nvke-install-plan-verification-api-runner.mjs"
    assert runner.is_file()
    storybook = os.environ["NVKE_TEST_VERIFICATION_STORYBOOK"]
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
                "server": storybook + "/__nvke-verification-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-verification-browser-source",
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
                "tree": tree,
                "artifacts": captured,
                "retainedArtifactId": captured["id"],
                "retainedSelection": {
                    "item": captured["item"],
                    "ordinal": captured["ordinal"],
                },
                "canReview": True,
                "pending": False,
                "loading": False,
                "error": None,
            },
        },
    }
    config["bearer"] = bearer
    config_path = tmp_path / "verification-browser-private.json"
    with config_path.open("x") as output:
        config_path.chmod(0o600)
        json.dump(config, output)
    dropped["prepareNvkeOperatorInstallPlanVerification("] = 1
    log = tmp_path / "verification-browser-private.log"
    with log.open("x") as output:
        log.chmod(0o600)
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_VERIFICATION_BROWSER_CONFIG": str(config_path)},
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=900,
            check=False,
        )
    assert result.returncode == 0, (
        "Owned browser/API verification qualification failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert report["passed"] and report["pageErrors"] == []
    row = NvkeInstallPlanVerification.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    assert row.summary["verificationDigest"] == report["verificationDigest"]
    assert row.summary["captureDigest"] == report["captureDigest"]
    assert row.summary["continuationAuthorized"] is False
    return row
