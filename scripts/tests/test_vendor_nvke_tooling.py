"""Prove immutable source selection and offline native-runner drift detection."""

from contextlib import redirect_stdout
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[1] / "vendor-nvke-tooling.py"
spec = importlib.util.spec_from_file_location("vendor_nvke_tooling", MODULE)
vendor = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vendor)


class NativeRunnerVendorTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "source"
        self.source.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.test")
        self.git("config", "commit.gpgsign", "false")
        self.runner = self.source / vendor.SOURCE
        self.runner.parent.mkdir(parents=True)
        self.original = (
            'REVISION = "'
            + "c" * 40
            + '"\nUTILITIES = {("generate", "sitespec")}\nMANAGEMENT_SCRIPTS = {"install": ("deploy/script.sh", ["install"])}\n'
        ).encode()
        self.runner.write_bytes(self.original)
        self.backend = self.source / vendor.BACKEND_SOURCE
        self.backend.write_text('REVISION = "' + "c" * 40 + '"\n')
        self.management_api = self.source / vendor.MANAGEMENT_API_SOURCE
        self.management_api.write_text('REVISION = "' + "c" * 40 + '"\n')
        self.management_controller = self.source / vendor.MANAGEMENT_CONTROLLER_SOURCE
        self.management_controller.write_text("from management_api import REVISION\n")
        self.git("add", ".")
        self.git("commit", "-qm", "native runner fixture")
        self.revision = self.git("rev-parse", "HEAD").decode().strip()
        catalog = self.root / "internal/nvkecatalog/catalog.json"
        catalog.parent.mkdir(parents=True)
        catalog.write_text(json.dumps({"revision": "c" * 40}))
        self.destination = self.root / "internal/nvketooling"
        self.root_patch = patch.object(vendor, "ROOT", self.root)
        self.destination_patch = patch.object(vendor, "DESTINATION", self.destination)
        self.root_patch.start()
        self.destination_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.addCleanup(self.destination_patch.stop)

    def git(self, *arguments):
        return subprocess.check_output(
            ["git", "-C", str(self.source), *arguments], stderr=subprocess.DEVNULL
        )

    def run_vendor(self, *arguments):
        with (
            patch.object(sys, "argv", [str(MODULE), *arguments]),
            redirect_stdout(io.StringIO()),
        ):
            vendor.main()

    def refresh(self):
        self.run_vendor("--source", str(self.source), "--revision", self.revision)

    def test_dirty_checkout_does_not_enter_the_embedded_runner(self):
        self.runner.write_bytes(b"uncommitted code must not enter the release")
        self.backend.write_bytes(b"uncommitted renderer must not enter the release")
        self.management_api.write_bytes(
            b"uncommitted API renderer must not enter the release"
        )
        self.management_controller.write_bytes(b"uncommitted controller renderer")
        self.refresh()
        self.assertEqual((self.destination / "tooling.py").read_bytes(), self.original)
        self.assertEqual(
            (self.destination / "nktp_backend.py").read_text(),
            'REVISION = "' + "c" * 40 + '"\n',
        )
        metadata = json.loads((self.destination / "provenance.json").read_text())
        self.assertEqual(
            (self.destination / "management_api.py").read_text(),
            'REVISION = "' + "c" * 40 + '"\n',
        )
        self.assertEqual(metadata["sourceRevision"], self.revision)
        self.assertEqual(
            (self.destination / "management_controller.py").read_text(),
            "from management_api import REVISION\n",
        )
        self.run_vendor("--check", "--source", str(self.source))

    def test_offline_check_refuses_changed_runner_bytes(self):
        self.refresh()
        self.run_vendor("--check")
        with (self.destination / "tooling.py").open("ab") as output:
            output.write(b"\n# changed\n")
        with self.assertRaisesRegex(ValueError, "provenance"):
            self.run_vendor("--check")

    def test_catalog_pin_mismatch_is_refused(self):
        (self.root / "internal/nvkecatalog/catalog.json").write_text(
            json.dumps({"revision": "d" * 40})
        )
        with self.assertRaisesRegex(ValueError, "revisions differ"):
            self.refresh()
        self.assertFalse(self.destination.exists())

    def test_offline_check_refuses_changed_backend_renderer(self):
        self.refresh()
        with (self.destination / "nktp_backend.py").open("ab") as output:
            output.write(b"\n# changed\n")
        with self.assertRaisesRegex(ValueError, "provenance"):
            self.run_vendor("--check")

    def test_mutable_revision_name_is_not_accepted(self):
        with (
            redirect_stdout(io.StringIO()),
            patch("sys.stderr", io.StringIO()),
            self.assertRaises(SystemExit),
        ):
            self.run_vendor("--source", str(self.source), "--revision", "HEAD")

    def test_offline_check_refuses_changed_management_api_renderer(self):
        self.refresh()
        with (self.destination / "management_api.py").open("ab") as output:
            output.write(b"\n# changed\n")
        with self.assertRaisesRegex(ValueError, "provenance"):
            self.run_vendor("--check")

    def test_management_api_upstream_pin_mismatch_is_refused(self):
        self.management_api.write_text('REVISION = "' + "d" * 40 + '"\n')
        self.git("add", ".")
        self.git("commit", "-qm", "mismatched renderer fixture")
        self.revision = self.git("rev-parse", "HEAD").decode().strip()
        with self.assertRaisesRegex(ValueError, "API renderer.*revisions differ"):
            self.refresh()
        self.assertFalse(self.destination.exists())

    def test_offline_check_refuses_changed_management_controller_renderer(self):
        self.refresh()
        with (self.destination / "management_controller.py").open("ab") as output:
            output.write(b"\n# changed\n")
        with self.assertRaisesRegex(ValueError, "provenance"):
            self.run_vendor("--check")

    def test_controller_revision_dependency_must_be_bound_to_the_pinned_api(self):
        self.management_controller.write_text("from other_module import REVISION\n")
        self.git("add", ".")
        self.git("commit", "-qm", "changed controller dependency")
        self.revision = self.git("rev-parse", "HEAD").decode().strip()
        with self.assertRaisesRegex(ValueError, "pinned management API revision"):
            self.refresh()
        self.assertFalse(self.destination.exists())


if __name__ == "__main__":
    unittest.main()
