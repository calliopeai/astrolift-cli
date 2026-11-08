"""Drive the compiled CLI and real pinned local tools without provisioning hosts."""

import hashlib
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit

CLI = os.environ.get("ASTROLIFT_NVKE_CLI")
BUNDLE = os.environ.get("NVKE_TEST_NATIVE_BUNDLE")
REVISION = "92c39b8ad563ebe5ef705f781b3038b901aba10f"


@unittest.skipUnless(CLI and BUNDLE, "Select a compiled CLI and prepared native bundle")
class CompiledNativeToolingTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="astro-cli-native-")
        self.addCleanup(self.temporary.cleanup)
        self.workspace = Path(self.temporary.name)
        self.bundle = BUNDLE

    def invoke(self, arguments, timeout=45, input_text=None):
        return subprocess.run(
            [CLI, "nvke", "tooling", *arguments],
            capture_output=True,
            text=True,
            input=input_text,
            timeout=timeout,
            check=False,
        )

    def native(self, arguments, input_text=None):
        response = self.invoke(
            [
                "run",
                "--bundle",
                self.bundle,
                "--workspace",
                str(self.workspace),
                "--json",
                "--",
                *arguments,
            ],
            input_text=input_text,
        )
        self.assertEqual(
            response.returncode,
            0,
            "compiled native command failed; inspect private artifacts",
        )
        result = json.loads(response.stdout)
        self.assertEqual(result["revision"], REVISION)
        self.assertEqual(result["exitCode"], 0)
        return result

    def test_compiled_native_csv_stdin_is_forwarded_without_console_disclosure(self):
        result = self.native(
            ["generate", "sitespec", "astro-cli-stdin", "--nodes", "-"],
            input_text="name,ip\ncp-stdin,10.10.50.3\n",
        )
        self.assertIn("cp-stdin", Path(result["stdout"]).read_text())
        self.assertNotIn("10.10.50.3", json.dumps(result))
        self.native(
            ["validate", "sitespec", "-f", result["stdout"], "--bootstrap-only"]
        )

    def test_real_compiled_native_spec_lifecycle(self):
        catalog = self.invoke(["catalog", "--json"])
        self.assertEqual(catalog.returncode, 0)
        self.assertEqual(json.loads(catalog.stdout)["upstreamRevision"], REVISION)
        (self.workspace / "hosts.csv").write_text("name,ip\ncp-01,10.10.50.2\n")
        version = self.native(["version"])
        self.assertIn(REVISION, Path(version["stdout"]).read_text())
        generated = self.native(
            [
                "generate",
                "sitespec",
                "astro-cli-native-test",
                "--nodes",
                "hosts.csv",
                "--set",
                "spire.trustDomain=astro-cli.test",
            ]
        )
        self.native(
            ["validate", "sitespec", "-f", generated["stdout"], "--bootstrap-only"]
        )
        self.native(["inspect", "sitespec", generated["stdout"]])
        self.native(["get", "bootstrap-manifest"])
        self.native(
            ["bootstrap", "site", "--sitespec", generated["stdout"], "--dry-run"]
        )
        self.assertFalse((self.workspace / "bootstrap-state.json").exists())

    def test_compiled_nktp_backend_profiles_are_private_and_source_pinned(self):
        profile = self.workspace / "backend-profile.json"
        for protocol, endpoint in (
            ("grpc", "telemetry.example.test:4317"),
            ("http", "https://telemetry.example.test/otlp"),
        ):
            profile.write_text(
                json.dumps(
                    {
                        "protocol": protocol,
                        "endpoint": endpoint,
                        "authorization": {
                            "secret": "astro-telemetry",
                            "key": "authorization",
                        },
                        "tls": {
                            "secret": "astro-tls",
                            "caKey": "ca.crt",
                            "certKey": "tls.crt",
                            "keyKey": "tls.key",
                        },
                    }
                )
            )
            output = self.workspace / (protocol + ".values.json")
            arguments = [
                "nktp-backend",
                "--bundle",
                self.bundle,
                "--profile",
                str(profile),
                "--output",
                str(output),
                "--json",
            ]
            result = self.invoke(arguments)
            self.assertEqual(
                result.returncode, 0, "compiled NKTP profile rendering failed"
            )
            metadata = json.loads(result.stdout)
            self.assertEqual(metadata["revision"], REVISION)
            self.assertEqual(
                metadata["sha256"], hashlib.sha256(output.read_bytes()).hexdigest()
            )
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)
            self.assertNotIn(endpoint, result.stdout + result.stderr)
            original = output.read_bytes()
            duplicate = self.invoke(arguments)
            self.assertNotEqual(duplicate.returncode, 0)
            self.assertEqual(output.read_bytes(), original)
            self.assertNotIn(endpoint, duplicate.stdout + duplicate.stderr)
        profile.write_text(
            json.dumps(
                {
                    "protocol": "grpc",
                    "endpoint": "PRIVATE_TOKEN@telemetry.example.test:4317",
                }
            )
        )
        output = self.workspace / "invalid.values.json"
        failed = self.invoke(
            [
                "nktp-backend",
                "--bundle",
                self.bundle,
                "--profile",
                str(profile),
                "--output",
                str(output),
            ]
        )
        self.assertNotEqual(failed.returncode, 0)
        self.assertFalse(output.exists())
        self.assertNotIn("PRIVATE_TOKEN", failed.stdout + failed.stderr)

    def test_compiled_management_apis_keep_scope_private_and_output_exclusive(self):
        profile = self.workspace / "management-profile.json"
        config = {
            "name": "native-apis",
            "namespace": "native-api-system",
            "tenantNamespace": "native-org",
            "images": {
                "operator": "registry.example/operator@sha256:" + "a" * 64,
                "tenant": "registry.example/tenant@sha256:" + "b" * 64,
            },
            "clients": [],
        }
        profile.write_text(json.dumps(config))
        output = self.workspace / "management-apis.json"
        arguments = [
            "management-apis",
            "--bundle",
            self.bundle,
            "--profile",
            str(profile),
            "--output",
            str(output),
            "--json",
        ]
        if os.environ.get("NVKE_TEST_TOOLING_PYTHON"):
            arguments.extend(["--python", os.environ["NVKE_TEST_TOOLING_PYTHON"]])
        response = self.invoke(arguments)
        self.assertEqual(
            response.returncode, 0, "compiled management API rendering failed"
        )
        metadata = json.loads(response.stdout)
        self.assertEqual(metadata["manifestSourceRevision"], REVISION)
        self.assertEqual(
            metadata["sha256"], hashlib.sha256(output.read_bytes()).hexdigest()
        )
        self.assertNotIn("registry.example", response.stdout + response.stderr)
        self.assertEqual(output.stat().st_mode & 0o777, 0o600)
        rows = json.loads(output.read_text())["items"]
        self.assertTrue(
            all(
                r["spec"]["type"] == "ClusterIP" for r in rows if r["kind"] == "Service"
            )
        )
        self.assertEqual(
            next(r for r in rows if r["kind"] == "NetworkPolicy")["spec"]["ingress"], []
        )
        self.assertTrue(
            all(
                r["metadata"]["namespace"] == "native-org"
                for r in rows
                if r["kind"] in {"Role", "RoleBinding"}
            )
        )
        original = output.read_bytes()
        self.assertNotEqual(self.invoke(arguments).returncode, 0)
        self.assertEqual(output.read_bytes(), original)
        config["clients"] = [{"namespace": "other-org", "podLabels": {}}]
        profile.write_text(json.dumps(config))
        invalid = self.workspace / "invalid-management-apis.json"
        arguments[arguments.index(str(output))] = str(invalid)
        failed = self.invoke(arguments)
        self.assertNotEqual(failed.returncode, 0)
        self.assertFalse(invalid.exists())
        self.assertNotIn("other-org", failed.stdout + failed.stderr)
        config["clients"] = []
        config["credentials"] = "PRIVATE_TOKEN"
        profile.write_text(json.dumps(config))
        invalid_credentials = self.workspace / "invalid-credentials-apis.json"
        arguments[arguments.index(str(invalid))] = str(invalid_credentials)
        failed = self.invoke(arguments)
        self.assertNotEqual(failed.returncode, 0)
        self.assertFalse(invalid_credentials.exists())
        self.assertNotIn("PRIVATE_TOKEN", failed.stdout + failed.stderr)

    @unittest.skipUnless(
        os.environ.get("NVKE_TEST_UPSTREAM"),
        "Select a pinned upstream checkout for native build proof",
    )
    def test_compiled_prepare_builds_a_pristine_pinned_bundle(self):
        output = self.workspace / "prepared"
        response = self.invoke(
            [
                "prepare",
                "--checkout",
                os.environ["NVKE_TEST_UPSTREAM"],
                "--output",
                str(output),
                "--json",
            ],
            timeout=180,
        )
        self.assertEqual(
            response.returncode,
            0,
            "native bundle preparation failed; inspect its private build log",
        )
        manifest = json.loads(response.stdout)
        self.assertEqual(manifest, json.loads((output / "bundle.json").read_text()))
        self.assertEqual(manifest["revision"], REVISION)
        self.assertEqual(
            manifest["binarySha256"],
            hashlib.sha256((output / "nvkectl").read_bytes()).hexdigest(),
        )
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)
        self.bundle = str(output)
        self.native(["version"])

    @unittest.skipUnless(
        os.environ.get("NVKE_TEST_KUBECONFIG") and os.environ.get("NVKE_TEST_OPSCODE"),
        "Select local Kubernetes and pinned operator test helpers",
    )
    def test_compiled_management_install_dry_run_only_reads_kubernetes(self):
        config = json.loads(
            subprocess.check_output(
                [
                    "kubectl",
                    "--kubeconfig",
                    os.environ["NVKE_TEST_KUBECONFIG"],
                    "config",
                    "view",
                    "--minify",
                    "-o",
                    "json",
                ]
            )
        )
        host = urlsplit(config["clusters"][0]["cluster"]["server"]).hostname
        try:
            local = host == "localhost" or ipaddress.ip_address(host).is_loopback
        except ValueError:
            local = False
        self.assertTrue(
            local, "compiled native qualification requires a loopback Kubernetes API"
        )
        helpers_path = (
            Path(os.environ["NVKE_TEST_OPSCODE"]) / "kubernetes/nvke/test_tooling.py"
        )
        spec = importlib.util.spec_from_file_location(
            "native_operator_test_helpers", helpers_path
        )
        helpers = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(helpers)
        (self.workspace / "hosts.csv").write_text("name,ip\ncp-01,10.10.50.2\n")
        generated = self.native(
            [
                "generate",
                "sitespec",
                "astro-cli-management-test",
                "--nodes",
                "hosts.csv",
                "--set",
                "spire.trustDomain=astro-cli.test",
            ]
        )
        stack = self.workspace / "stack.json"
        stack.write_text(
            json.dumps(
                {
                    "nico": {
                        "endpoint": "https://nico.example.test",
                        "orgID": "test-org",
                        "apiName": "forge",
                    },
                    "images": {
                        "controller": "registry.example.test/nvke/controller@sha256:"
                        + "2" * 64,
                        "profiles": "registry.example.test/nvke/profiles@sha256:"
                        + "1" * 64,
                    },
                    "mirror": "",
                }
            )
        )
        credentials = {
            "NVKECTL_NICO_TOKEN": "synthetic-test-nico-token",
            "NVKECTL_NODE_USER": "test-user",
            "NVKECTL_NODE_PASSWORD": "synthetic-test-node-password",
            "NVKECTL_DATABASE_PASSWORD": "synthetic-test-db-password",
        }
        with (
            helpers.read_only_kubernetes_proxy(
                Path(os.environ["NVKE_TEST_KUBECONFIG"]), self.workspace
            ) as (kubeconfig, observed),
            patch.dict(os.environ, credentials),
        ):
            result = self.native(
                [
                    "management",
                    "install",
                    "--kubeconfig",
                    str(kubeconfig),
                    "--sitespec",
                    generated["stdout"],
                    "--stack-config",
                    str(stack),
                    "--arch",
                    "linux/arm64",
                    "--dry-run",
                ]
            )
            self.assertTrue(observed)
            self.assertEqual({method for method, _path in observed}, {"GET"})
        planned = Path(result["stdout"]).read_text()
        for name in (
            "nktp",
            "postgres",
            "spire-crds",
            "temporal",
            "spire",
            "nvke-controller",
        ):
            self.assertIn("helm upgrade --install " + name + " ", planned)
        self.assertNotIn("synthetic-test-db-password", planned)
        self.assertFalse((self.workspace / "management-install-state.json").exists())

    def test_real_sigint_stops_native_children_and_preserves_the_attempt(self):
        bundle = self.workspace / "fake-bundle"
        bundle.mkdir(mode=0o700)
        python = shutil.which("python3.12") or shutil.which("python3")
        binary = bundle / "nvkectl"
        binary.write_text(
            "#!"
            + python
            + "\n"
            + """import os, time
from pathlib import Path
count = Path("effect-count")
count.write_text(str(int(count.read_text()) + 1) if count.exists() else "1")
Path("native-pid").write_text(str(os.getpid()))
time.sleep(5)
"""
        )
        binary.chmod(0o700)
        manifest = bundle / "bundle.json"
        manifest.write_text(
            json.dumps(
                {
                    "format": 1,
                    "revision": REVISION,
                    "binarySha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                }
            )
        )
        manifest.chmod(0o600)
        arguments = [
            CLI,
            "nvke",
            "tooling",
            "run",
            "--bundle",
            str(bundle),
            "--workspace",
            str(self.workspace),
            "--json",
            "--",
            "bootstrap",
            "site",
            "--sitespec",
            "synthetic.yaml",
        ]
        process = subprocess.Popen(
            arguments, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
        )
        try:
            deadline = time.monotonic() + 5
            marker = self.workspace / "native-pid"
            while not marker.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(marker.exists(), "native child did not start")
            pid = marker.read_text()
            process.send_signal(signal.SIGINT)
            output, diagnostic = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 1)
            self.assertIn("interrupted", diagnostic)
            self.assertEqual(output, "")
            status = subprocess.run(
                ["ps", "-p", pid, "-o", "stat="],
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertTrue(
                status.returncode != 0
                or not status.stdout.strip()
                or status.stdout.strip().startswith("Z")
            )
            self.assertTrue((self.workspace / "bootstrap-state.json").exists())
            retry = subprocess.run(
                arguments, capture_output=True, text=True, timeout=10, check=False
            )
            self.assertEqual(retry.returncode, 1)
            self.assertEqual((self.workspace / "effect-count").read_text(), "1")
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate(timeout=10)


if __name__ == "__main__":
    unittest.main()
