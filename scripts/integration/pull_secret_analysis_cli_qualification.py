"""Actual compiled-client Secret analysis with retained private cold recovery."""

import copy
import hashlib
import json
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanPullSecretAnalysis, NvkeOperation
from pull_authority_cli_qualification import (
    PullAuthorityCapture,
    require_offline_replay,
)


class PullSecretAnalysisCapture(PullAuthorityCapture):
    def analysis_body(self, plan, tree, binding, pull, artifact_id):
        return {
            "format": 1,
            "artifactId": artifact_id,
            "planId": plan["id"],
            "planDigest": plan["plan"]["planDigest"],
            "treeId": tree["id"],
            "treeDigest": tree["treeDigest"],
            "bindingId": binding["id"],
            "bindingDigest": binding["bindingDigest"],
            "pullAuthorityId": pull["id"],
            "pullAuthorityDigest": pull["authorityDigest"],
            "imageSelections": [
                {
                    "podKey": key,
                    "imagePointer": "/spec/containers/0/image",
                    "ensureSecretPulledImages": True,
                }
                for key in self.selected
            ],
            "maximumBytes": "16777216",
            "maximumHelperBytes": "1048576",
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
        import management_plan_pull_secret_analysis_service as service

        pull = super().qualify(
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
        )
        self.pull = pull
        artifact_id = str(uuid4())
        body = self.analysis_body(plan, tree, binding, pull, artifact_id)
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
            "--pull-authority",
            pull["id"],
            "--pull-authority-version",
            pull["version"],
            "--pull-authority-digest",
            pull["authorityDigest"],
            "--artifact",
            artifact_id,
        )
        roots = [
            self.case.engine.install_plans.root,
            self.case.engine.install_chart_artifacts,
            self.case.engine.install_execution_trees,
            self.case.engine.install_render_contexts,
            self.case.engine.install_workload_inventories,
            self.case.engine.install_registry_images,
            self.case.engine.install_image_bindings,
            self.case.engine.install_pull_authorities,
        ]
        storage = service.store.captures.storage
        before = [storage._scan(root, 1073741824) for root in roots]
        operation = NvkeOperation.all_objects.filter(guid=job).values().get()
        admissions = copy.deepcopy(self.case.engine._admissions())
        pod_reads = len(self.context.kube.flow.http.requests)
        registry_reads = len(self.images.registry.requests)
        private = private_file("pull-secret-analysis.json", body)
        journal = tmp_path / "pull-secret-analysis-journal.json"
        dropped["prepareNvkeOperatorInstallPlanPullSecretAnalysis("] = 1
        run(
            "agent",
            "install-plan",
            "pull-secret-analysis",
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
        assert saved["pullAuthorityId"] == pull["id"]
        assert saved["pullAuthorityVersion"] == pull["version"]
        assert saved["pullAuthorityDigest"] == pull["authorityDigest"]
        for value in [
            "imageSelections",
            "ensureSecretPulledImages",
            "PRIVATE_PULL_PASSWORD",
            *self.selected,
        ]:
            assert value not in journal.read_text()
        row = NvkeInstallPlanPullSecretAnalysis.objects.get(guid=artifact_id)
        assert row.status == "prepared"
        retained = (
            NvkeInstallPlanPullSecretAnalysis.all_objects.filter(pk=row.pk)
            .values()
            .get()
        )
        artifact = self.case.engine.install_pull_secret_analyses / artifact_id
        attempts = sorted(path.name for path in (artifact / "work").iterdir())
        assert len(attempts) == 1 and (artifact / "index.json").is_file()
        self.images.restart()
        original_collect = service.store.captures.pulls.collect

        def replay_only(*args, **kwargs):
            require_offline_replay(kwargs.get("read_response"))
            return original_collect(*args, **kwargs)

        with (
            patch.object(
                service.store.captures.pulls, "collect", side_effect=replay_only
            ),
            patch.object(
                service.captures.store.transport,
                "FrozenKubernetesRead",
                side_effect=AssertionError("Cold Kubernetes transport"),
            ),
            patch.object(
                service.store.executor.FrozenKeyringHelper,
                "analyze",
                side_effect=AssertionError("Cold native helper execution"),
            ),
        ):
            result = run(
                "agent", "install-plan", "pull-secret-analysis", "review", job, *flags
            )
            assert (
                run(
                    "agent",
                    "install-plan",
                    "pull-secret-analysis",
                    "retry-preparation",
                    job,
                    *flags,
                )
                == result
            )
        assert result["id"] == artifact_id
        assert result["imageUseCount"] == len(self.selected)
        assert result["candidateCount"] == len(self.selected)
        assert result["missingReferenceCount"] == 0
        assert result["pullAuthorityId"] == pull["id"]
        assert result["continuationAuthorized"] is False
        assert (
            NvkeInstallPlanPullSecretAnalysis.all_objects.filter(pk=row.pk)
            .values()
            .get()
            == retained
        )
        assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
        assert [storage._scan(root, 1073741824) for root in roots] == before
        assert len(self.context.kube.flow.http.requests) == pod_reads
        assert len(self.images.registry.requests) == registry_reads
        assert NvkeOperation.all_objects.filter(guid=job).values().get() == operation
        assert self.case.engine._admissions() == admissions
        for value in ["PRIVATE_PULL_PASSWORD", *self.selected]:
            assert value not in json.dumps(result)
        return result
