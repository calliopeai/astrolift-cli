"""Exercise drift refusal and canonical Git ancestry without remote credentials."""

import importlib.util
import json
from pathlib import Path
import subprocess
import shutil
import sys
import tempfile
import unittest

MODULE_PATH = Path(__file__).resolve().parents[1] / "vendor-charts.py"
spec = importlib.util.spec_from_file_location("vendor_charts", MODULE_PATH)
vendor = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vendor)


class VendorChartTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.source = self.root / "source"
        self.source.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.test")
        self.git("config", "commit.gpgsign", "false")
        self.git(
            "remote", "add", "origin", f"https://github.com/{vendor.REPOSITORY}.git"
        )
        chart = self.source / vendor.SOURCE_PATH
        chart.mkdir(parents=True)
        (chart / "Chart.yaml").write_text(
            "apiVersion: v2\nname: astrolift-prereqs\nversion: 0.1.1\n"
        )
        (chart / "Chart.lock").write_text("dependencies: []\n")
        (chart / "values.yaml").write_text("enabled: false\n")
        self.git("add", ".")
        self.git("commit", "-qm", "canonical fixture")
        self.revision = self.git("rev-parse", "HEAD").decode().strip()
        self.git("update-ref", "refs/remotes/origin/main", self.revision)
        self.canonical = vendor.published_source(self.source, self.revision)
        self.destination = self.root / "embedded"
        self.destination.mkdir()
        for name, data in self.canonical.items():
            (self.destination / name).write_bytes(data)
        (self.destination / "charts").mkdir()
        (self.destination / "charts/dependency-1.0.0.tgz").write_bytes(
            b"archive fixture"
        )
        self.inventory = self.root / "source.json"
        self.metadata = vendor.source_inventory(
            self.destination, self.revision, self.canonical
        )
        self.inventory.write_text(json.dumps(self.metadata))

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.source), *args])

    def test_exact_offline_file_set_passes_without_a_sibling(self):
        self.assertEqual(
            vendor.check(self.destination, self.inventory)["revision"], self.revision
        )

    def test_edit_removal_extra_and_archive_corruption_all_refuse(self):
        for mutation in ("edit", "remove", "extra", "archive"):
            with self.subTest(mutation=mutation):
                target = self.destination / "values.yaml"
                original = target.read_bytes()
                archive = self.destination / "charts/dependency-1.0.0.tgz"
                if mutation == "edit":
                    target.write_text("enabled: true\n")
                if mutation == "remove":
                    target.unlink()
                if mutation == "extra":
                    (self.destination / "unexpected.yaml").write_text("extra")
                if mutation == "archive":
                    archive.write_bytes(b"corrupted archive")
                with self.assertRaisesRegex(ValueError, "differs"):
                    vendor.check(self.destination, self.inventory)
                target.write_bytes(original)
                archive.write_bytes(b"archive fixture")
                (self.destination / "unexpected.yaml").unlink(missing_ok=True)

    def test_short_revision_or_symlink_refuses_offline_check(self):
        self.metadata["revision"] = self.revision[:7]
        self.inventory.write_text(json.dumps(self.metadata))
        with self.assertRaisesRegex(ValueError, "provenance"):
            vendor.check(self.destination, self.inventory)
        self.metadata["revision"] = self.revision
        self.inventory.write_text(json.dumps(self.metadata))
        (self.destination / "symlink").symlink_to(self.destination / "values.yaml")
        with self.assertRaisesRegex(ValueError, "symlinks"):
            vendor.check(self.destination, self.inventory)

    def test_dirty_old_and_unpublished_sources_refuse_before_any_overwrite(self):
        original = (self.destination / "values.yaml").read_bytes()
        (self.source / vendor.SOURCE_PATH / "values.yaml").write_text("dirty: true\n")
        with self.assertRaisesRegex(ValueError, "dirty"):
            vendor.refresh(
                self.source,
                self.revision,
                directory=self.destination,
                inventory_path=self.inventory,
            )
        self.git("restore", ".")
        (self.source / "new-file").write_text("next revision")
        self.git("add", ".")
        self.git("commit", "-qm", "unpublished fixture")
        unpublished = self.git("rev-parse", "HEAD").decode().strip()
        with self.assertRaisesRegex(ValueError, "published"):
            vendor.refresh(
                self.source,
                unpublished,
                directory=self.destination,
                inventory_path=self.inventory,
            )
        self.git("update-ref", "refs/remotes/origin/main", unpublished)
        self.git("checkout", "-q", self.revision)
        with self.assertRaisesRegex(ValueError, "selected checkout history"):
            vendor.refresh(
                self.source,
                unpublished,
                directory=self.destination,
                inventory_path=self.inventory,
            )
        self.assertEqual((self.destination / "values.yaml").read_bytes(), original)
        self.assertEqual(
            vendor.check(self.destination, self.inventory)["revision"], self.revision
        )

    def test_successful_refresh_uses_published_git_blobs_and_replaces_inventory(self):
        chart = self.source / vendor.SOURCE_PATH
        (chart / "values.yaml").write_text("enabled: true\n")
        self.git("add", ".")
        self.git("commit", "-qm", "published update fixture")
        published = self.git("rev-parse", "HEAD").decode().strip()
        self.git("update-ref", "refs/remotes/origin/main", published)
        fake_helm = self.root / "helm"
        fake_helm.write_text(
            '#!/bin/sh\nmkdir -p "$3/charts"\nprintf "archive fixture" > "$3/charts/dependency-1.0.0.tgz"\n'
        )
        fake_helm.chmod(0o755)
        vendor.refresh(
            self.source,
            published,
            helm=str(fake_helm),
            directory=self.destination,
            inventory_path=self.inventory,
        )
        self.assertEqual(
            (self.destination / "values.yaml").read_text(), "enabled: true\n"
        )
        self.assertEqual(
            vendor.check(self.destination, self.inventory)["revision"], published
        )

    def test_executable_check_exits_nonzero_on_tampering(self):
        scripts = self.root / "scripts"
        scripts.mkdir()
        helper = scripts / "vendor-charts.py"
        shutil.copyfile(MODULE_PATH, helper)
        embedded = self.root / "internal/charts/astrolift-prereqs"
        shutil.copytree(self.destination, embedded)
        shutil.copyfile(self.inventory, embedded.parent / "source.json")
        good = subprocess.run(
            [sys.executable, str(helper), "check"], text=True, capture_output=True
        )
        self.assertEqual(good.returncode, 0, good.stderr)
        self.assertIn(self.revision, good.stdout)
        (embedded / "values.yaml").write_text("tampered: true\n")
        rejected = subprocess.run(
            [sys.executable, str(helper), "check"], text=True, capture_output=True
        )
        self.assertNotEqual(rejected.returncode, 0)
        self.assertIn("values.yaml", rejected.stderr)
        self.assertIn("Chart parity failed", rejected.stderr)
        self.assertEqual(rejected.stdout, "")

    def test_dependency_failure_leaves_original_chart_and_inventory(self):
        with self.assertRaises(subprocess.CalledProcessError):
            vendor.refresh(
                self.source,
                self.revision,
                helm="false",
                directory=self.destination,
                inventory_path=self.inventory,
            )
        self.assertEqual(
            vendor.check(self.destination, self.inventory)["revision"], self.revision
        )


if __name__ == "__main__":
    unittest.main()
