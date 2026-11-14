"""Compiled CLI/App/mTLS node observations with all five native selectors."""

import copy
import hashlib
import json
from pathlib import Path
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanNodePlatforms, NvkeOperation


class NodePlatformsCapture:
    def __init__(self, case, native, context_capture, image_capture):
        import management_plan_node_platform_service as service
        import test_management_node_platforms as node_fixture
        from test_management_platform_content import selected_helpers

        self.case, self.context, self.images = case, context_capture, image_capture
        helpers = selected_helpers()
        assert set(helpers) == set(service.store.platforms.executor.PROFILES)
        self.profile = case.root / "platform-profile.json"
        profiles = {}
        for profile, filename in sorted(helpers.items()):
            binary = Path(filename).resolve()
            profiles[profile] = {
                "program": str(binary),
                "manifest": {
                    "format": 1,
                    "runtimeProfile": profile,
                    "platformLibraryVersion": service.store.platforms.executor.PROFILES[
                        profile
                    ],
                    "binarySha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                    "sourceSha256": service.store.platforms.executor.expected_sources(
                        profile
                    ),
                },
            }
        native.agent.private_write(
            self.profile, json.dumps({"format": 1, "profiles": profiles}).encode()
        )
        # The profile is part of the original plan's source, before preparation.
        case.server.shutdown()
        port, endpoint, instance = (
            case.server.server_port,
            case.url,
            case.engine.instance_id,
        )
        case.server.server_close()
        case.thread.join(timeout=5)
        case.engine.close()
        case.engine = case.make_engine(platform_profile=self.profile)
        case.start_server(port=port)
        assert (case.url, case.engine.instance_id) == (endpoint, instance)
        node = node_fixture.NodePlatformTests()
        node.setUp()
        self.selected = {}
        for index, profile in enumerate(sorted(helpers)):
            name = f"owned-platform-{index}"
            body = copy.deepcopy(node.node)
            body["metadata"].update(name=name, uid=f"owned-node-uid-{index}")
            body["status"]["nodeInfo"].update(
                operatingSystem="linux",
                architecture="arm64",
                containerRuntimeVersion="containerd://"
                + profile.removeprefix("containerd/v"),
            )
            context_capture.kube.flow.http.responses["/api/v1/nodes/" + name] = (
                200,
                body,
            )
            self.selected[name] = {
                "uid": body["metadata"]["uid"],
                "runtimeProfile": profile,
                "platform": {"os": "linux", "architecture": "arm64", "variant": "v8"},
            }
        context_capture.kube.flow.http.responses["/api/v1/namespaces/kube-system"] = (
            200,
            {
                **copy.deepcopy(node.namespace),
                "metadata": {**node.namespace["metadata"], "uid": native.SITE},
            },
        )

    def body(self, plan, tree, binding, artifact_id):
        import management_plan_node_platform_service as service

        return {
            "format": 1,
            "artifactId": artifact_id,
            "planId": plan["id"],
            "planDigest": plan["plan"]["planDigest"],
            "treeId": tree["id"],
            "treeDigest": tree["treeDigest"],
            "bindingId": binding["id"],
            "bindingDigest": binding["bindingDigest"],
            "nodePlatforms": copy.deepcopy(self.selected),
            **{
                key: "16777216"
                if budget == "maximum_image_bytes"
                else str(service.store.DEFAULTS[budget])
                if budget in service.store.BYTE_BUDGETS
                else service.store.DEFAULTS[budget]
                for key, budget in service.BUDGETS.items()
            },
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
        import management_plan_node_platform_service as service

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
        private = private_file("node-platforms.json", body)
        journal = tmp_path / "node-platforms-journal.json"
        dropped["prepareNvkeOperatorInstallPlanNodePlatforms("] = 1
        run(
            "agent",
            "install-plan",
            "node-platforms",
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
            "nodePlatforms",
            *self.selected,
            *(v["uid"] for v in self.selected.values()),
            *service.store.platforms.executor.PROFILES,
        ]:
            assert private_value not in journal.read_text()
        row = NvkeInstallPlanNodePlatforms.objects.get(guid=artifact_id)
        assert row.status == "prepared"
        retained = (
            NvkeInstallPlanNodePlatforms.all_objects.filter(pk=row.pk).values().get()
        )
        artifact = self.case.engine.install_node_platforms / artifact_id
        attempts = sorted(path.name for path in (artifact / "work").iterdir())
        assert len(attempts) == 1 and (artifact / "index.json").is_file()
        node_reads = len(self.context.kube.flow.http.requests)
        self.images.restart()
        with (
            patch.object(
                service.store.platforms.nodes,
                "collect",
                side_effect=AssertionError("Cold node recollection"),
            ),
            patch.object(
                service.store.platforms.transport,
                "FrozenKubernetesRead",
                side_effect=AssertionError("Cold Kubernetes transport"),
            ),
        ):
            result = run(
                "agent", "install-plan", "node-platforms", "review", job, *flags
            )
            assert (
                run(
                    "agent",
                    "install-plan",
                    "node-platforms",
                    "retry-preparation",
                    job,
                    *flags,
                )
                == result
            )
        assert result["id"] == artifact_id and result["nodeCount"] == 5
        assert result["pairCount"] == 5 * result["contentCount"]
        assert result["issueCount"] > 0 and not result["continuationAuthorized"]
        assert (
            NvkeInstallPlanNodePlatforms.all_objects.filter(pk=row.pk).values().get()
            == retained
        )
        assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
        assert [
            service.store.storage._scan(root, 1073741824) for root in roots
        ] == before
        assert len(self.context.kube.flow.http.requests) == node_reads
        assert len(self.images.registry.requests) == registry_reads
        assert NvkeOperation.all_objects.filter(guid=job).values().get() == operation
        assert self.case.engine._admissions() == admissions
        for private_value in [
            *self.selected,
            *(v["uid"] for v in self.selected.values()),
            *service.store.platforms.executor.PROFILES,
        ]:
            assert private_value not in json.dumps(result)
        return result
