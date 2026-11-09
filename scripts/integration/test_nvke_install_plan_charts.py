"""Compiled CLI, actual ASGI/Postgres and owned mTLS/Helm 4 chart recovery."""

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
    return paired.binary.__wrapped__()


@pytest.fixture
def dropped_cli_replies():
    return {}


@pytest.fixture
def asgi_endpoint(settings, dropped_cli_replies):
    yield from paired.asgi_endpoint.__wrapped__(settings, dropped_cli_replies)


def test_compiled_cli_chart_prepare_lost_reply_review_and_private_retry(
    world, binary, tmp_path, asgi_endpoint, dropped_cli_replies, monkeypatch
):
    selected = os.getenv("NVKE_TEST_AGENT_OPSCODE")
    if (
        not selected
        or not os.getenv("NVKE_TEST_HELM4")
        or not os.getenv("NVKE_TEST_CHART_HELPER")
    ):
        pytest.skip("Select the owned pinned runtime, Helm 4 and snapshot-built helper")
    source = Path(selected)
    revision = "5200851616150efb6d121923f9e5dd2185f23474"
    for name in (
        "operator_agent.py",
        "management_chart_materialization.py",
        "management_plan_chart_service.py",
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
        for private in ("PRIVATE_CHART_CONTENT", "BEGIN PRIVATE KEY", "file://"):
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
            materializer = native.agent.management_plan_chart_service.artifacts
            real_deployment = preparation._deployment

            def owned_charts(engine, argv, inputs, destination, budget):
                result = real_deployment(engine, argv, inputs, destination, budget)
                for name in ("local-parent", "local-child"):
                    path = destination / name
                    path.mkdir(mode=0o700)
                    text = "apiVersion: v2\nname: " + name + "\nversion: 1.0.0\n"
                    if name == "local-parent":
                        text += "dependencies:\n- {name: local-child, version: '>=1.0.0', repository: 'file://../local-child'}\n"
                    native.agent.private_write(path / "Chart.yaml", text.encode())
                    native.agent.private_write(
                        path / "values.yaml", b"private: PRIVATE_CHART_CONTENT\n"
                    )
                return result

            def bound_inventory(**values):
                inputs = [
                    {
                        "id": name,
                        "root": "deployment",
                        "path": name,
                        "contentSha256": materializer._hash(
                            preparation.storage._content(
                                preparation.storage._scan(
                                    values["deployment"] / name, 1048576, private=True
                                )
                            )
                        ),
                    }
                    for name in ("local-parent", "local-child")
                ]
                graph = {
                    "charts": [
                        {
                            "item": "nktp",
                            "name": "local-parent",
                            "version": "1.0.0",
                            "repository": "",
                            "reference": "local-parent",
                        }
                    ],
                    "inputs": inputs,
                    "localChartDependencies": [
                        {
                            "chart": "local-parent",
                            "dependencyIndex": 0,
                            "repository": "file://../local-child",
                            "reference": "local-child",
                            "item": "nktp",
                        }
                    ],
                    "continuationAuthorized": False,
                }
                graph["inventoryDigest"] = materializer._hash(graph)
                return graph

            monkeypatch.setattr(preparation, "_deployment", owned_charts)
            monkeypatch.setattr(preparation.dependencies, "inventory", bound_inventory)
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
