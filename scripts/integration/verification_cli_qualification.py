"""Actual compiled CLI/App/agent verification and historical recovery acceptance."""

import hashlib
import json
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import (
    NvkeInstallPlanContext,
    NvkeInstallPlanVerification,
)


def qualify(
    capture, run, private_file, dropped, job, tree_flags, plan, tree, charts, tmp_path
):
    import management_plan_verification_service as service

    case = capture.case
    context = NvkeInstallPlanContext.objects.get(
        tree__guid=tree["id"], status="prepared"
    )
    retained_context = (
        NvkeInstallPlanContext.objects.filter(pk=context.pk).values().get()
    )
    artifact_id = str(uuid4())
    body = {
        "format": 1,
        "artifactId": artifact_id,
        "contextId": str(context.guid),
        "contextDigest": context.summary["contextDigest"],
        "planId": plan["id"],
        "planDigest": plan["plan"]["planDigest"],
        "treeId": tree["id"],
        "treeDigest": tree["treeDigest"],
        "item": "nktp",
        "maximumBytes": "16777216",
        "maximumCaptureAgeSeconds": 3600,
        "maximumCalls": 512,
        "timeoutSeconds": 300,
    }
    flags = (
        *tree_flags[: tree_flags.index("--artifact")],
        "--tree",
        tree["id"],
        "--tree-version",
        tree["version"],
        "--tree-digest",
        tree["treeDigest"],
        "--context",
        str(context.guid),
        "--context-version",
        context.version,
        "--context-digest",
        context.summary["contextDigest"],
        "--item",
        "nktp",
        "--ordinal",
        2,
        "--artifact",
        artifact_id,
    )
    roots = {
        "plans": case.engine.install_plans.root,
        "charts": case.engine.install_chart_artifacts,
        "trees": case.engine.install_execution_trees,
        "contexts": case.engine.install_render_contexts,
    }
    snapshots = {
        name: service.preparation.storage._scan(
            root, 1073741824, private=name == "contexts"
        )
        for name, root in roots.items()
    }
    private = private_file("verification.json", body)
    journal = tmp_path / "verification-journal.json"
    dropped["prepareNvkeOperatorInstallPlanVerification("] = 1
    run(
        "agent",
        "install-plan",
        "verification",
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
        saved["kind"] == "nvke-install-plan-verification"
        and saved["artifactId"] == artifact_id
    )
    assert (
        saved["contextId"] == str(context.guid)
        and saved["contextVersion"] == context.version
    )
    assert saved["contextDigest"] == context.summary["contextDigest"]
    assert saved["inputSha256"] == hashlib.sha256(private.read_bytes()).hexdigest()
    assert not any(
        key in saved
        for key in ("maximumCaptureAgeSeconds", "maximumBytes", "requestText")
    )
    row = NvkeInstallPlanVerification.objects.get(guid=artifact_id)
    assert row.status == "prepared"
    retained_row = NvkeInstallPlanVerification.objects.filter(pk=row.pk).values().get()
    artifact = case.engine.install_render_verifications / artifact_id
    assert (artifact / "index.json").is_file()
    attempts = sorted(path.name for path in (artifact / "work").iterdir())
    assert len(attempts) == 1
    reads = len(capture.kube.flow.http.requests)
    assert reads > 0 and all(peer == "client" for peer in capture.kube.peers)
    assert all(method == "GET" for method, _ in capture.kube.flow.http.requests)
    capture.restart()
    with patch.object(
        service.verifications.authenticated,
        "revalidate",
        side_effect=AssertionError("Historical recovery recollected"),
    ):
        public = run("agent", "install-plan", "verification", "review", job, *flags)
        assert (
            run(
                "agent",
                "install-plan",
                "verification",
                "retry-preparation",
                job,
                *flags,
            )
            == public
        )
    assert public["id"] == artifact_id and public["contextId"] == str(context.guid)
    assert public["contextDigest"] == context.summary["contextDigest"]
    assert public["captureDigest"] == context.summary["captureDigest"]
    assert public["frozenChartSha256"] == context.summary["frozenChartSha256"]
    assert public["chartArtifactDigest"] == charts["artifactDigest"]
    assert public["continuationAuthorized"] is False
    assert (
        public["executionState"]
        == "requires-phase-use-resource-credential-schema-images-and-linked-admission"
    )
    assert 1 <= public["requests"] <= body["maximumCalls"] and 1 <= int(
        public["bytes"]
    ) <= int(body["maximumBytes"])
    assert capture.native.PRIVATE not in json.dumps(public)
    assert str(case.root) not in json.dumps(public)
    index = capture.native.agent.read_private(artifact / "index.json")["body"]
    assert public["artifactDigest"] == service.verifications._hash(index)
    assert len(capture.kube.flow.http.requests) == reads
    assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
    assert (
        NvkeInstallPlanVerification.objects.filter(pk=row.pk).values().get()
        == retained_row
    )
    assert (
        NvkeInstallPlanContext.objects.filter(pk=context.pk).values().get()
        == retained_context
    )
    for name, root in roots.items():
        assert (
            service.preparation.storage._scan(
                root, 1073741824, private=name == "contexts"
            )
            == snapshots[name]
        )
