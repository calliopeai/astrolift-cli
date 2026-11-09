"""Compiled CLI, actual ASGI/Postgres and owned mTLS/Helm 4 execution-tree recovery."""

import hashlib
import json
import os
import shutil
import subprocess
from pathlib import Path
from types import SimpleNamespace
from uuid import uuid4

import pytest
import test_nvke_agent as paired_agent
import test_nvke_contracts as paired
from django.db import transaction
from astrolift_clusters.models import (
    NvkeInstallPlan,
    NvkeInstallPlanCharts,
    NvkeInstallPlanTree,
    NvkeOperation,
    NvkeOperatorAuthority,
    NvkeSite,
)
from astrolift_clusters.tests.test_nvke_operator import partial_install
from astrolift_clusters.tests.test_nvke_sites import context
from astrolift_identity.api_tokens import (
    mint_token,
    reset_current_api_token,
    set_current_api_token,
)
from astrolift_identity.models import ApiToken

pytestmark = pytest.mark.django_db(transaction=True)


@pytest.fixture
def world():
    return paired_agent.world.__wrapped__()


@pytest.fixture
def binary():
    selected = paired.binary.__wrapped__()
    digest = hashlib.sha256(Path(selected).read_bytes()).hexdigest()
    yield selected
    assert hashlib.sha256(Path(selected).read_bytes()).hexdigest() == digest


@pytest.fixture
def dropped_cli_replies():
    return {}


@pytest.fixture
def asgi_endpoint(settings, dropped_cli_replies):
    yield from paired.asgi_endpoint.__wrapped__(settings, dropped_cli_replies)


@pytest.mark.parametrize("client", ["cli", "browser"])
def test_compiled_cli_tree_prepare_lost_reply_cold_review_and_private_retry(
    world, binary, tmp_path, asgi_endpoint, dropped_cli_replies, monkeypatch, client
):
    if client == "browser" and (
        not os.getenv("NVKE_TEST_TREE_FRONTEND")
        or not os.getenv("NVKE_TEST_TREE_STORYBOOK")
    ):
        pytest.skip("Select the owned tree browser frontend and Storybook endpoint")
    selected = os.getenv("NVKE_TEST_AGENT_OPSCODE")
    if (
        not selected
        or not os.getenv("NVKE_TEST_HELM4")
        or not os.getenv("NVKE_TEST_CHART_HELPER")
    ):
        pytest.skip("Select the owned pinned runtime, Helm 4 and snapshot-built helper")
    source = Path(selected)
    revision = "0d17f53ec998698df88b2ab86f3d0359fbedbbfc"
    for name in (
        "operator_agent.py",
        "management_chart_materialization.py",
        "management_plan_chart_service.py",
        "management_plan_tree_service.py",
        "management_execution_tree.py",
        "management_template_rendering.py",
        "management_execution_release.py",
        "management_execution_arguments.py",
        "management_dependencies.py",
        "test_operator_agent.py",
        "chart-helper/main.go",
        "chart-helper/go.mod",
        "chart-helper/go.sum",
    ):
        relative = "kubernetes/nvke/" + name
        assert (source / relative).read_bytes() == subprocess.check_output(
            ["git", "show", revision + ":" + relative], cwd=source
        )
    monkeypatch.syspath_prepend(str(source / "kubernetes/nvke"))
    import test_operator_agent as native

    issued = mint_token()
    token = ApiToken.objects.create(
        user=world.user,
        organization=world.org,
        name="owned chart CLI",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin", "read"],
    )

    def run(*arguments, ok=True):
        result = paired.invoke(
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            world.org.slug,
            *arguments,
            # The product permits 300s chart work with a 360s private API timeout.
            process_timeout=380,
        )
        for private in (
            "PRIVATE_CHART_CONTENT",
            "PRIVATE_TREE_CONTENT",
            "BEGIN PRIVATE KEY",
            "file://",
        ):
            assert private not in result.stdout + result.stderr
        if not ok:
            assert result.returncode != 0
            return result
        assert result.returncode == 0, result.stderr
        return json.loads(result.stdout)

    def private_file(name, value):
        path = tmp_path / name
        path.write_text(json.dumps(value))
        path.chmod(0o600)
        return path

    site = run(
        "site",
        "create",
        "--name",
        "Chart CLI",
        "--slug",
        "chart-cli",
        "--namespace",
        "org-owned",
    )
    monkeypatch.setattr(native, "SITE", site["id"])
    native.NativeAgentTests.setUpClass()
    case = native.NativeAgentTests()
    try:
        case.setUp()
        try:
            credentials = private_file(
                "mtls.json",
                {
                    "caPem": (case.certs / "ca.pem").read_text(),
                    "clientCertificatePem": (case.certs / "client.pem").read_text(),
                    "clientKeyPem": (case.certs / "client.key").read_text(),
                },
            )
            authority = run(
                "agent",
                "configure",
                "--site",
                site["id"],
                "--site-version",
                site["version"],
                "--endpoint",
                case.url,
                "--instance",
                case.engine.instance_id,
                "--bundle-sha256",
                case.bundle_digest,
                "--credentials-file",
                credentials,
                "--connection-timeout",
                300,
            )
            authority = run(
                "agent",
                "verify",
                "--site",
                site["id"],
                "--site-version",
                authority["siteVersion"],
                "--authority-version",
                authority["version"],
            )
            job, intent, body = case.plan_fixture()
            body["overrides"] = {
                "options": {"config": "input:config.json"},
                "files": {
                    "config.json": json.dumps(
                        {
                            "Nico": {
                                "Endpoint": "https://nico.example.test",
                                "OrgID": "owned",
                                "APIName": "owned",
                                "Token": "PRIVATE_TREE_CONTENT",
                            },
                            "Tenant": {
                                "User": "owned",
                                "Password": "PRIVATE_TREE_CONTENT",
                            },
                            "ClusterName": "owned-browser-tree",
                        }
                    ),
                    "secret.txt": "PRIVATE_TREE_CONTENT",
                },
            }
            state = SimpleNamespace(
                world=world,
                site=NvkeSite.objects.get(guid=site["id"]),
                authority=NvkeOperatorAuthority.objects.get(guid=authority["id"]),
            )
            mark = set_current_api_token(token)
            try:
                # Controlled failed-receipt setup ends before the captured rows.
                with transaction.atomic(), context(world):
                    original = partial_install(state)
                    NvkeOperation.objects.filter(pk=original.pk).update(
                        guid=job, request_digest=intent
                    )
                    original.refresh_from_db()
            finally:
                reset_current_api_token(mark)
            original_row = NvkeOperation.objects.filter(pk=original.pk).values().get()
            original_native = (case.engine._job(job) / "state.json").read_bytes()
            original_guard = (
                case.engine.workspace / "management-install-state.json"
            ).read_bytes()
            runs = sorted(
                path.name for path in (case.engine.workspace / "runs").iterdir()
            )
            preparation = native.agent.management_plan_preparation
            real_deployment = preparation._deployment

            def owned_deployment(engine, argv, inputs, destination, budget):
                provenance = real_deployment(engine, argv, inputs, destination, budget)
                (destination / "nvke-deploy.yaml").write_text(
                    "items:\n"
                    "  - {name: nvke-base-config, type: template}\n"
                    "  - {name: spire-tpm-pubhashes, type: manifest}\n"
                    "  - {name: nktp, type: helm}\n"
                )
                (destination / "images.txt").unlink(missing_ok=True)
                chart = destination / "owned-local-chart"
                chart.mkdir(mode=0o700)
                (chart / "templates").mkdir(mode=0o700)
                native.agent.private_write(
                    chart / "Chart.yaml",
                    b"apiVersion: v2\nname: nktp\nversion: 1.0.0\n",
                )
                native.agent.private_write(
                    chart / "templates/config.yaml",
                    b"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: owned\n",
                )
                (destination / "nktp/release.yaml").write_text(
                    "chart:\n  name: nktp\n  path: owned-local-chart\n  setFile:\n    - password="
                    + str(inputs / "secret.txt")
                    + "\nrelease:\n  name: nktp\n  namespace: nktp\nvalues:\n  - values.yaml\n"
                )
                return provenance

            monkeypatch.setattr(preparation, "_deployment", owned_deployment)
            monkeypatch.setattr(preparation.qualification, "kubectl", case.plan_kubectl)
            flags = (
                "--site",
                site["id"],
                "--site-version",
                state.site.version,
                "--authority-version",
                state.authority.version,
                "--version",
                original.version,
            )
            plan = run(
                "agent",
                "install-plan",
                "prepare",
                job,
                *flags,
                "--plan",
                body["planId"],
                "--file",
                private_file("plan.json", body),
                "--request-file",
                tmp_path / "plan-journal.json",
            )
            parent = NvkeInstallPlan.objects.filter(guid=body["planId"]).values().get()
            artifact_id = str(uuid4())
            request = {
                "format": 1,
                "artifactId": artifact_id,
                "planId": body["planId"],
                "planDigest": plan["plan"]["planDigest"],
                "maximumBytes": "1048576",
                "maximumExpandedBytes": "1048576",
            }
            private = private_file("charts.json", request)
            journal = tmp_path / "charts-journal.json"
            which = shutil.which

            def selected_tool(name):
                return {
                    "helm": os.environ["NVKE_TEST_HELM4"],
                    "astro-nvke-chart-helper": os.environ["NVKE_TEST_CHART_HELPER"],
                }.get(name) or which(name)

            monkeypatch.setattr(
                native.agent.management_plan_chart_service.shutil,
                "which",
                selected_tool,
            )
            chart_flags = (
                *flags,
                "--plan",
                body["planId"],
                "--plan-version",
                plan["version"],
                "--plan-digest",
                plan["plan"]["planDigest"],
                "--artifact",
                artifact_id,
            )
            dropped_cli_replies["prepareNvkeOperatorInstallPlanCharts("] = 1
            run(
                "agent",
                "install-plan",
                "charts",
                "prepare",
                job,
                *chart_flags,
                "--file",
                private,
                "--request-file",
                journal,
                ok=False,
            )
            saved = json.loads(journal.read_text())
            assert (
                saved["artifactId"] == artifact_id
                and saved["inputSha256"]
                == hashlib.sha256(private.read_bytes()).hexdigest()
            )
            assert (
                "maximumBytes" not in journal.read_text()
                and "PRIVATE" not in journal.read_text()
            )
            assert (
                NvkeInstallPlanCharts.objects.get(guid=artifact_id).status == "prepared"
            )
            attempts = sorted(
                path.name
                for path in (
                    case.engine.install_chart_artifacts / artifact_id / "work"
                ).iterdir()
            )
            data = run("agent", "install-plan", "charts", "review", job, *chart_flags)
            assert (
                data["id"] == artifact_id
                and data["chartCount"] == 1
                and not data["continuationAuthorized"]
            )
            assert isinstance(data["bytes"], str) and int(data["bytes"]) > 0
            assert (
                run(
                    "agent",
                    "install-plan",
                    "charts",
                    "retry-preparation",
                    job,
                    *chart_flags,
                )
                == data
            )
            assert (
                sorted(
                    path.name
                    for path in (
                        case.engine.install_chart_artifacts / artifact_id / "work"
                    ).iterdir()
                )
                == attempts
            )
            chart_row = (
                NvkeInstallPlanCharts.objects.filter(guid=artifact_id).values().get()
            )
            tree_id = str(uuid4())
            tree_body = {
                "format": 1,
                "artifactId": tree_id,
                "planId": body["planId"],
                "planDigest": plan["plan"]["planDigest"],
                "chartArtifactId": artifact_id,
                "chartArtifactDigest": data["artifactDigest"],
                "maximumBytes": "16777216",
            }
            tree_flags = (
                *flags,
                "--plan",
                body["planId"],
                "--plan-version",
                plan["version"],
                "--plan-digest",
                plan["plan"]["planDigest"],
                "--chart-artifact",
                artifact_id,
                "--chart-artifact-version",
                data["version"],
                "--chart-artifact-digest",
                data["artifactDigest"],
                "--artifact",
                tree_id,
            )
            tree_private = private_file("tree.json", tree_body)
            tree_journal = tmp_path / "tree-journal.json"
            dropped_cli_replies["prepareNvkeOperatorInstallPlanTree("] = 1
            run(
                "agent",
                "install-plan",
                "tree",
                "prepare",
                job,
                *tree_flags,
                "--file",
                tree_private,
                "--request-file",
                tree_journal,
                ok=False,
            )
            saved_tree = json.loads(tree_journal.read_text())
            assert saved_tree["artifactId"] == tree_id
            assert saved_tree["chartArtifactId"] == artifact_id
            assert saved_tree["chartArtifactVersion"] == data["version"]
            assert saved_tree["chartArtifactDigest"] == data["artifactDigest"]
            assert (
                saved_tree["inputSha256"]
                == hashlib.sha256(tree_private.read_bytes()).hexdigest()
            )
            assert "maximumBytes" not in tree_journal.read_text()
            assert NvkeInstallPlanTree.objects.get(guid=tree_id).status == "prepared"
            sealed = native.agent.management_plan_tree_service.trees.ExecutionTrees(
                case.engine.install_execution_trees, plans=case.engine.install_plans
            ).load(tree_id)
            assert len(sealed["templateRendering"]) == 1
            frozen = Path(sealed["treeLocation"])
            assert (
                "PRIVATE_TREE_CONTENT"
                in (
                    frozen / "deployment/nvke-base-config/20-nico-credentials.yaml"
                ).read_text()
            )
            assert (
                "type: template"
                not in (frozen / "deployment/nvke-deploy.yaml").read_text()
            )
            assert (
                frozen
                / "template-sources/nvke-base-config/20-nico-credentials.yaml.tmpl"
            ).is_file()

            artifact = case.engine.install_execution_trees / tree_id
            tree_attempts = sorted(path.name for path in (artifact / "work").iterdir())
            endpoint, port, instance = (
                case.url,
                case.server.server_port,
                case.engine.instance_id,
            )
            case.server.shutdown()
            case.server.server_close()
            case.thread.join(timeout=5)
            case.engine.close()
            case.engine = case.make_engine()
            case.start_server(port=port)
            assert case.url == endpoint and case.engine.instance_id == instance
            tree = run("agent", "install-plan", "tree", "review", job, *tree_flags)
            assert tree["id"] == tree_id and tree["chartArtifactId"] == artifact_id
            assert tree["fileCount"] >= data["chartCount"]
            assert isinstance(tree["bytes"], str) and int(tree["bytes"]) >= int(
                data["bytes"]
            )
            assert (
                tree["executionState"]
                == "requires-native-rendering-images-and-live-verification"
            )
            assert tree["continuationAuthorized"] is False
            assert (
                run(
                    "agent",
                    "install-plan",
                    "tree",
                    "retry-preparation",
                    job,
                    *tree_flags,
                )
                == tree
            )
            assert (
                sorted(path.name for path in (artifact / "work").iterdir())
                == tree_attempts
            )
            if client == "browser":
                from tree_browser_qualification import run_tree_browser

                browser_row = run_tree_browser(
                    bearer=issued.plaintext,
                    tmp_path=tmp_path,
                    endpoint=asgi_endpoint,
                    world=world,
                    site=state.site,
                    authority=authority,
                    operation=original,
                    plan=plan,
                    charts=data,
                    dropped=dropped_cli_replies,
                )
                browser_artifact = case.engine.install_execution_trees / str(
                    browser_row.guid
                )
                assert (browser_artifact / "index.json").is_file()
                assert len(list((browser_artifact / "work").iterdir())) == 1
            assert (
                NvkeInstallPlanCharts.objects.filter(guid=artifact_id).values().get()
                == chart_row
            )
            assert (
                NvkeOperation.objects.filter(pk=original.pk).values().get()
                == original_row
            )
            assert (
                NvkeInstallPlan.objects.filter(guid=body["planId"]).values().get()
                == parent
            )
            assert (
                case.engine._job(job) / "state.json"
            ).read_bytes() == original_native
            assert (
                case.engine.workspace / "management-install-state.json"
            ).read_bytes() == original_guard
            assert (
                sorted(path.name for path in (case.engine.workspace / "runs").iterdir())
                == runs
            )
            assert list(case.engine.install_plans.links.iterdir()) == []
        finally:
            case.tearDown()
    finally:
        native.NativeAgentTests.tearDownClass()
