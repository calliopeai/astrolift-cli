"""Owned correspondence browser read against actual App and retained parent rows."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
from urllib.parse import urlsplit

from astrolift_clusters.models import (
    NvkeInstallPlan,
    NvkeInstallPlanCharts,
    NvkeInstallPlanImageBinding,
    NvkeInstallPlanNodePlatforms,
    NvkeInstallPlanPullAuthority,
    NvkeInstallPlanPullSecretAnalysis,
    NvkeInstallPlanTree,
    NvkeInstallPlanWorkload,
    NvkeOperation,
)


def run_pull_runtime_browser(
    *, frontend, storybook, endpoint, bearer, fixture, working
):
    """Require a supplied real source; never prepare or synthesize parent rows."""
    frontend = Path(frontend).resolve()
    working = Path(working).resolve()
    runner = frontend / "e2e/nvke-install-plan-pull-runtime-api-runner.mjs"
    assert runner.is_file() and working.is_dir()
    for selected in (storybook, endpoint):
        url = urlsplit(selected)
        assert (
            url.scheme == "http"
            and url.hostname == "127.0.0.1"
            and not url.username
            and not url.password
        )
    source = fixture["source"]
    parents = [
        (NvkeOperation, source["operation"]),
        (NvkeInstallPlan, source["plan"]),
        (NvkeInstallPlanCharts, source["charts"]),
        (NvkeInstallPlanTree, source["artifacts"]),
        (NvkeInstallPlanWorkload, fixture["workload"]),
        (NvkeInstallPlanImageBinding, fixture["binding"]),
        (NvkeInstallPlanPullAuthority, fixture["pullAuthority"]),
        (NvkeInstallPlanPullSecretAnalysis, fixture["analysis"]),
        (NvkeInstallPlanNodePlatforms, fixture["nodePlatforms"]),
    ]
    snapshots = []
    for model, selected in parents:
        row = model.all_objects.filter(guid=selected["id"]).values().get()
        assert row["version"] == selected["version"]
        assert row["deleted_at"] is None
        if model is not NvkeOperation:
            assert row["status"] == "prepared"
        snapshots.append(row)
    # The browser receives only the public fixture. Its Node forwarding process
    # alone receives this ephemeral bearer; its output remains private.
    config = working / "pull-runtime-browser-private.json"
    with os.fdopen(
        os.open(config, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600),
        "w",
    ) as stream:
        json.dump(
            {
                "storybook": storybook,
                "endpoint": endpoint,
                "bearer": bearer,
                "fixture": fixture,
            },
            stream,
        )
    config_digest = hashlib.sha256(config.read_bytes()).hexdigest()
    log = working / "pull-runtime-browser-private.log"
    with os.fdopen(
        os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600),
        "w",
    ) as stream:
        result = subprocess.run(
            ["node", str(runner)],
            cwd=frontend,
            env={**os.environ, "NVKE_PULL_RUNTIME_BROWSER_CONFIG": str(config)},
            stdout=stream,
            stderr=subprocess.STDOUT,
            timeout=2 * 360 + 360,
            check=False,
        )
    assert result.returncode == 0, (
        "Owned correspondence browser failed; inspect its private log"
    )
    report = json.loads(log.read_text().splitlines()[-1])
    assert report == {
        "readCount": 2,
        "deliberatelyLostReplies": 1,
        "stableCorrespondenceDigest": True,
        "noAutomaticReadOrRetry": True,
        "noResultOrCredentialsInStorage": True,
        "errors": [],
    }
    assert hashlib.sha256(config.read_bytes()).hexdigest() == config_digest
    for (model, selected), before in zip(parents, snapshots, strict=True):
        assert model.all_objects.filter(guid=selected["id"]).values().get() == before
    return report
