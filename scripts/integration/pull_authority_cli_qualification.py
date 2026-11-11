"""Compiled CLI/App/mTLS admitted-Pod references and explicit cold recovery."""

import copy
import hashlib
import json
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanPullAuthority, NvkeOperation


class PullAuthorityCapture:
    def __init__(self, case, native, context_capture, image_capture):
        import test_management_workload_pull_authority as fixture

        self.case, self.context, self.images = case, context_capture, image_capture
        selected = fixture.PullAuthorityTests()
        selected.setUp()
        selected.cluster["metadata"]["uid"] = native.SITE
        for path, body in selected.paths.items():
            context_capture.kube.flow.http.responses[path] = (200, body)
        self.selected = copy.deepcopy(selected.selected)

    def body(self, plan, tree, binding, artifact_id):
        return {
            "format": 1,
            "artifactId": artifact_id,
            "planId": plan["id"],
            "planDigest": plan["plan"]["planDigest"],
            "treeId": tree["id"],
            "treeDigest": tree["treeDigest"],
            "bindingId": binding["id"],
            "bindingDigest": binding["bindingDigest"],
            "selectedPods": copy.deepcopy(self.selected),
            "maximumBytes": "16777216",
            "maximumCalls": 512,
            "timeoutSeconds": 300,
        }

    def qualify(
        self,
        run,
        private_file,
        dropped,
        job,
        tree_flags,
        plan,
        tree,
        workload,
        binding,
        tmp_path,
    ):
        import management_plan_pull_authority_service as service

        artifact_id = str(uuid4())
        body = self.body(plan, tree, binding, artifact_id)
        flags = (
            *tree_flags[:-2],
            "--tree",
            tree["id"],
            "--tree-version",
            tree["version"],
            "--tree-digest",
            tree["treeDigest"],
            "--workload",
            workload["id"],
            "--workload-version",
            workload["version"],
            "--workload-digest",
            workload["workloadDigest"],
            "--binding",
            binding["id"],
            "--binding-version",
            binding["version"],
            "--binding-digest",
            binding["bindingDigest"],
            "--artifact",
            artifact_id,
        )
        roots = (
            self.case.engine.install_plans.root,
            self.case.engine.install_chart_artifacts,
            self.case.engine.install_execution_trees,
            self.case.engine.install_render_contexts,
            self.case.engine.install_workload_inventories,
            self.case.engine.install_registry_images,
            self.case.engine.install_image_bindings,
        )
        before = [service.store.storage._scan(root, 1073741824) for root in roots]
        operation = NvkeOperation.all_objects.filter(guid=job).values().get()
        admissions = copy.deepcopy(self.case.engine._admissions())
        registry_reads = len(self.images.registry.requests)
        private = private_file("pull-authority.json", body)
        journal = tmp_path / "pull-authority-journal.json"
        dropped["prepareNvkeOperatorInstallPlanPullAuthority("] = 1
        run(
            "agent",
            "install-plan",
            "pull-authority",
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
        for private_value in [
            "selectedPods",
            *self.selected,
            *self.selected.values(),
        ]:
            assert private_value not in journal.read_text()
        row = NvkeInstallPlanPullAuthority.objects.get(guid=artifact_id)
        assert row.status == "prepared"
        retained = (
            NvkeInstallPlanPullAuthority.all_objects.filter(pk=row.pk).values().get()
        )
        artifact = self.case.engine.install_pull_authorities / artifact_id
        attempts = sorted(path.name for path in (artifact / "work").iterdir())
        assert len(attempts) == 1 and (artifact / "index.json").is_file()
        pod_reads = len(self.context.kube.flow.http.requests)
        self.images.restart()
        with (
            patch.object(
                service.store.pulls,
                "collect",
                side_effect=AssertionError("Cold Pod pull recollection"),
            ),
            patch.object(
                service.store.transport,
                "FrozenKubernetesRead",
                side_effect=AssertionError("Cold Kubernetes transport"),
            ),
        ):
            result = run(
                "agent", "install-plan", "pull-authority", "review", job, *flags
            )
            assert (
                run(
                    "agent",
                    "install-plan",
                    "pull-authority",
                    "retry-preparation",
                    job,
                    *flags,
                )
                == result
            )
        assert result["id"] == artifact_id and result["podCount"] == len(self.selected)
        assert result["serviceAccountCount"] == 1 and result["secretCount"] == 1
        assert not result["continuationAuthorized"]
        assert (
            NvkeInstallPlanPullAuthority.all_objects.filter(pk=row.pk).values().get()
            == retained
        )
        assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
        assert [
            service.store.storage._scan(root, 1073741824) for root in roots
        ] == before
        assert len(self.context.kube.flow.http.requests) == pod_reads
        assert len(self.images.registry.requests) == registry_reads
        assert NvkeOperation.all_objects.filter(guid=job).values().get() == operation
        assert self.case.engine._admissions() == admissions
        for private_value in [
            *self.selected,
            *self.selected.values(),
        ]:
            assert private_value not in json.dumps(result)
        return result
