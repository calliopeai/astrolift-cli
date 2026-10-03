import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


spec = importlib.util.spec_from_file_location(
    "vendor_permissions", Path(__file__).parents[1] / "vendor-permissions.py"
)
vendor = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vendor)


class PermissionCatalogueTests(unittest.TestCase):
    def test_static_source_is_sorted_without_execution_and_records_provenance(self):
        source = b'raise RuntimeError("must never execute")\nclass Permission(enum.StrEnum):\n B = "team.read"\n A = "app.read"\n'
        body = json.loads(vendor.catalogue(source, "a" * 40))
        self.assertEqual(body["permissions"], ["app.read", "team.read"])
        self.assertEqual(body["catalogueKind"], "bundled")
        self.assertTrue(body["requiresTargetCheck"])
        self.assertEqual(body["source"]["revision"], "a" * 40)

    def test_unknown_dynamic_or_duplicate_enum_members_fail_closed(self):
        for content in [
            "class Permission(enum.StrEnum):\n A = discover_permissions()\n",
            'class Permission(enum.StrEnum):\n A = "app.read"\n B = "app.read"\n',
            'class Permission(enum.StrEnum):\n A = "app.member.read"\n',
            "class Permission(enum.StrEnum):\n pass\n",
            'class Other(enum.StrEnum):\n A = "app.read"\n',
        ]:
            with self.subTest(source=content), self.assertRaises(ValueError):
                vendor.catalogue(content.encode(), "a" * 40)

    def test_moving_symbolic_revisions_are_refused(self):
        for revision in ["main", "HEAD", "a" * 7, "A" * 40]:
            with self.subTest(revision=revision), self.assertRaises(ValueError):
                vendor.read_source(Path("/does/not/exist"), revision)

    def test_source_read_uses_immutable_tree_not_dirty_or_later_checkout(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)

            def git(*args):
                return subprocess.run(
                    ["git", "-C", str(repo), *args], check=True, capture_output=True
                ).stdout

            git("init", "-q")
            path = repo / vendor.SOURCE_PATH
            path.parent.mkdir(parents=True)
            original = b'class Permission(enum.StrEnum):\n READ = "app.read"\n'
            path.write_bytes(original)
            git("add", ".")
            git(
                "-c",
                "user.name=Fixture",
                "-c",
                "user.email=fixture@example.invalid",
                "-c",
                "commit.gpgsign=false",
                "commit",
                "-qm",
                "permission fixture",
            )
            revision = git("rev-parse", "HEAD").decode().strip()
            path.write_bytes(
                b'class Permission(enum.StrEnum):\n NEW = "future.action"\n'
            )
            self.assertEqual(vendor.read_source(repo, revision), original)
            self.assertEqual(
                json.loads(
                    vendor.catalogue(vendor.read_source(repo, revision), revision)
                )["permissions"],
                ["app.read"],
            )


if __name__ == "__main__":
    unittest.main()
