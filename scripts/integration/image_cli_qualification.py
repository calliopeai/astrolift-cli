"""Owned App/compiled-CLI registry image capture and offline recovery."""

import hashlib
import json
from unittest.mock import patch
from uuid import uuid4

from astrolift_clusters.models import NvkeInstallPlanImage


class ImageCapture:
    source_kind = "registry"

    def __init__(self, case, native):
        import test_management_registry_capture as registry_fixture

        self.case, self.native = case, native
        self.fixture, self.registry = registry_fixture, None
        self.class_ready = self.closed = False
        try:
            registry_fixture.RegistryCaptureTests.setUpClass()
            self.class_ready = True
            self.registry = registry_fixture.RegistryCaptureTests()
            self.registry.setUp()
            self.registry.mode, self.registry.redirect = "bearer", True
            self.policy = json.dumps(self.registry.policy)
            self.profile = case.root / "image-profile.json"
            native.agent.private_write(
                self.profile,
                json.dumps(
                    {
                        "format": 1,
                        "policies": {
                            "reviewed": {
                                "input": "registry-policy.json",
                                "sha256": hashlib.sha256(
                                    self.policy.encode()
                                ).hexdigest(),
                            }
                        },
                    }
                ).encode(),
            )
            self.restart()
        except BaseException:
            self.close()
            raise

    def close(self):
        if self.closed:
            return
        self.closed = True
        try:
            if self.registry is not None:
                self.registry.doCleanups()
        finally:
            if self.class_ready:
                self.fixture.RegistryCaptureTests.tearDownClass()

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
        case.engine = case.make_engine(
            **({"image_profile": self.profile} if self.profile else {})
        )
        case.start_server(port=port)
        assert (case.url, case.engine.instance_id) == (endpoint, instance)

    def materialize(self, destination):
        (destination / "images.txt").write_text(self.registry.reference + "\n")

    def qualify(
        self, run, private_file, dropped, job, tree_flags, plan, tree, charts, tmp_path
    ):
        import management_plan_image_service as service

        case = self.case
        artifact_id = str(uuid4())
        body = {
            "format": 1,
            "artifactId": artifact_id,
            "planId": plan["id"],
            "planDigest": plan["plan"]["planDigest"],
            "treeId": tree["id"],
            "treeDigest": tree["treeDigest"],
            "reference": self.registry.reference,
            "policy": "reviewed",
            "maximumBytes": "268435456",
            "maximumCalls": 512,
            "timeoutSeconds": 300,
        }
        if self.source_kind == "local":
            body.pop("policy")
            body.pop("maximumCalls")
            body["sourceKind"] = "local"
        flags = (
            "--source-kind",
            self.source_kind,
            *tree_flags[:-2],
            "--tree",
            tree["id"],
            "--tree-version",
            tree["version"],
            "--tree-digest",
            tree["treeDigest"],
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
        private = private_file("image.json", body)
        journal = tmp_path / "image-journal.json"
        dropped["prepareNvkeOperatorInstallPlanImage("] = 1
        run(
            "agent",
            "install-plan",
            "image",
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
        assert saved["artifactId"] == artifact_id and saved["treeId"] == tree["id"]
        assert saved["inputSha256"] == hashlib.sha256(private.read_bytes()).hexdigest()
        assert not any(
            key in saved
            for key in ("reference", "policy", "maximumBytes", "requestText")
        )
        row = NvkeInstallPlanImage.objects.get(guid=artifact_id)
        assert row.status == "prepared"
        retained = NvkeInstallPlanImage.objects.filter(pk=row.pk).values().get()
        artifact = case.engine.install_registry_images / artifact_id
        assert (artifact / "index.json").is_file()
        attempts = sorted(path.name for path in (artifact / "work").iterdir())
        assert len(attempts) == 1
        reads = len(self.registry.requests)
        assert reads > 0 if self.source_kind == "registry" else reads == 0
        self.restart()
        with patch.object(
            service.images.registry,
            "capture",
            side_effect=AssertionError("Recovery recollected image"),
        ):
            public = run("agent", "install-plan", "image", "review", job, *flags)
            assert (
                run("agent", "install-plan", "image", "retry-preparation", job, *flags)
                == public
            )
        assert public["sourceKind"] == self.source_kind
        assert public["id"] == artifact_id and public["treeId"] == tree["id"]
        assert public["chartArtifactDigest"] == charts["artifactDigest"]
        assert public["rootDigest"] == self.registry.descriptor["digest"]
        assert public["continuationAuthorized"] is False
        assert (
            public["executionState"]
            == "requires-workload-platform-credential-phase-use-and-linked-admission"
        )
        assert len(self.registry.requests) == reads
        assert sorted(path.name for path in (artifact / "work").iterdir()) == attempts
        assert NvkeInstallPlanImage.objects.filter(pk=row.pk).values().get() == retained
        for name, root in (
            ("plans", case.engine.install_plans.root),
            ("charts", case.engine.install_chart_artifacts),
            ("trees", case.engine.install_execution_trees),
        ):
            assert service.preparation.storage._scan(root, 1073741824) == parents[name]
        return public


class LocalImageCapture(ImageCapture):
    source_kind = "local"

    def __init__(self, case, native):
        from types import SimpleNamespace
        import test_management_oci_content as content

        self.case, self.native = case, native
        self.profile = None
        self.closed = self.class_ready = False
        self.fixture = None
        blobs = content.OCIContentTests()
        blobs.blobs = {}
        config = blobs.document(
            {"os": "linux", "architecture": "arm64"},
            "application/vnd.oci.image.config.v1+json",
        )
        layer = blobs.blob(
            b"PRIVATE_LOCAL_LAYER", "application/vnd.oci.image.layer.v1.tar"
        )
        descriptor = blobs.document(
            {"schemaVersion": 2, "config": config, "layers": [layer]},
            "application/vnd.oci.image.manifest.v1+json",
        )
        self.registry = SimpleNamespace(
            reference="example.test/local@" + descriptor["digest"],
            descriptor=descriptor,
            requests=[],
        )
        self.files = {
            "oci-layout": b'{"imageLayoutVersion":"1.0.0"}',
            "index.json": json.dumps(
                {"schemaVersion": 2, "manifests": [descriptor]}
            ).encode(),
            **{
                "blobs/" + digest.replace(":", "/"): raw
                for digest, raw in blobs.blobs.items()
            },
        }

    def close(self):
        self.closed = True

    def materialize(self, destination):
        self.native.agent.private_write(
            destination / "images.txt",
            (self.registry.reference + "\towned-layout\n").encode(),
        )
        for relative, raw in self.files.items():
            path = destination / "owned-layout" / relative
            path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            self.native.agent.private_write(path, raw)

    def qualify(self, *args, **kwargs):
        import management_plan_image_service as service

        with patch.object(
            service.images.registry,
            "capture",
            side_effect=AssertionError("Local proof contacted registry"),
        ):
            return super().qualify(*args, **kwargs)
