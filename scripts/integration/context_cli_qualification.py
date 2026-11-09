"""Owned App/compiled-CLI context capture with real agent and Kubernetes mTLS."""

import hashlib
import json
import os
from pathlib import Path
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanContext


class ContextCapture:
    def __init__(self, case, native):
        import test_management_render_transport as transport_fixture

        self.case, self.native = case, native
        self.kube = transport_fixture.FrozenReadTests()
        self.kube.setUp()
        try:
            self.kube.flow.http.uid = native.SITE
            self.kube.flow.http.responses[self.kube.flow.http.namespace_path][1][
                "metadata"
            ]["uid"] = native.SITE
            self.kube.flow.http.responses[
                "/api/v1/namespaces/nktp/secrets?labelSelector=name%3Dnktp%2Cowner%3Dhelm&limit=200"
            ] = self.kube.flow.http.responses[self.kube.flow.history_path]
            self.profile = case.root / "render-profile.json"
            helper = Path(os.environ["NVKE_TEST_CHART_RENDER_HELPER"]).resolve()
            native.agent.private_write(
                self.profile,
                json.dumps(
                    {
                        "format": 1,
                        "helperProgram": str(helper),
                        "helperManifest": json.loads(
                            (helper.parent / "chart-helper.json").read_text()
                        ),
                        "helmVersion": self.kube.flow.helm,
                        "storageDriver": "secrets",
                        "schemas": {},
                    }
                ).encode(),
            )
            self.restart()
        except BaseException:
            self.close()
            raise

    def close(self):
        self.kube.doCleanups()

    def restart(self):
        case = self.case
        endpoint, port, instance = (
            case.url,
            case.server.server_port,
            case.engine.instance_id,
        )
        case.server.shutdown()
        case.server.server_close()
        case.thread.join(timeout=5)
        case.engine.close()
        case.engine = case.make_engine(render_profile=self.profile)
        case.start_server(port=port)
        assert (case.url, case.engine.instance_id) == (endpoint, instance)

    def plan_fixture(self):
        dumps = json.dumps

        def selected(value, *args, **kwargs):
            if (
                isinstance(value, dict)
                and value.get("kind") == "Config"
                and value.get("current-context") == "management"
            ):
                value = self.kube.config
            return dumps(value, *args, **kwargs)

        with patch.object(self.native.json, "dumps", side_effect=selected):
            return self.case.plan_fixture()

    def qualify(
        self, run, private_file, dropped, job, tree_flags, plan, tree, charts, tmp_path
    ):
        import management_plan_context_service as service

        case = self.case
        artifact_id = str(uuid4())
        body = {
            "format": 1,
            "artifactId": artifact_id,
            "planId": plan["id"],
            "planDigest": plan["plan"]["planDigest"],
            "treeId": tree["id"],
            "treeDigest": tree["treeDigest"],
            "item": "nktp",
            "revision": "1",
            "upgrade": False,
            "maximumBytes": "16777216",
            "maximumArtifactBytes": "1048576",
            "maximumCalls": 512,
            "maximumIterations": 64,
            "timeoutSeconds": 300,
        }
        flags = (
            *tree_flags[:-2],
            "--tree",
            tree["id"],
            "--tree-version",
            tree["version"],
            "--tree-digest",
            tree["treeDigest"],
            "--item",
            "nktp",
            "--ordinal",
            2,
            "--artifact",
            artifact_id,
        )
        parents = {
            name: service.preparation.storage._scan(root, 1073741824)
            for name, root in (
                ("plans", case.engine.install_plans.root),
                ("charts", case.engine.install_chart_artifacts),
                ("trees", case.engine.install_execution_trees),
            )
        }
        private = private_file("context.json", body)
        journal = tmp_path / "context-journal.json"
        dropped["prepareNvkeOperatorInstallPlanContext("] = 1
        run(
            "agent",
            "install-plan",
            "context",
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
        assert saved["artifactId"] == artifact_id
        assert saved["inputSha256"] == hashlib.sha256(private.read_bytes()).hexdigest()
        assert saved["treeId"] == tree["id"] and saved["ordinal"] == 2
        assert not any(
            key in saved for key in ("revision", "maximumBytes", "requestText")
        )
        row = NvkeInstallPlanContext.objects.get(guid=artifact_id)
        assert row.status == "prepared"
        retained_row = NvkeInstallPlanContext.objects.filter(pk=row.pk).values().get()
        artifact = case.engine.install_render_contexts / artifact_id
        assert (artifact / "index.json").is_file()
        attempts = sorted(path.name for path in (artifact / "work").iterdir())
        assert len(attempts) == 1
        reads = len(self.kube.flow.http.requests)
        assert reads > 0 and all(peer == "client" for peer in self.kube.peers)
        assert all(method == "GET" for method, _ in self.kube.flow.http.requests)
        self.restart()
        with (
            patch.object(
                service.authenticated.transport,
                "FrozenKubernetesRead",
                side_effect=AssertionError("Recovery created transport"),
            ),
            patch.object(
                service.authenticated.executor,
                "FrozenHelper",
                side_effect=AssertionError("Recovery created helper"),
            ),
        ):
            public = run("agent", "install-plan", "context", "review", job, *flags)
            assert (
                run(
                    "agent", "install-plan", "context", "retry-preparation", job, *flags
                )
                == public
            )
        assert public["id"] == artifact_id and public["treeId"] == tree["id"]
        assert public["chartArtifactDigest"] == charts["artifactDigest"]
        assert (
            public["documentCount"] == 1 and public["continuationAuthorized"] is False
        )
        assert (
            public["executionState"]
            == "requires-authenticated-phase-verification-images-and-linked-admission"
        )
        assert len(self.kube.flow.http.requests) == reads
        assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
        assert (
            NvkeInstallPlanContext.objects.filter(pk=row.pk).values().get()
            == retained_row
        )
        for name, root in (
            ("plans", case.engine.install_plans.root),
            ("charts", case.engine.install_chart_artifacts),
            ("trees", case.engine.install_execution_trees),
        ):
            assert service.preparation.storage._scan(root, 1073741824) == parents[name]
