"""Compiled CLI, actual ASGI/Postgres and owned mTLS/Helm 4 execution-tree recovery."""

from contextlib import closing
import hashlib
import json
import os
import re
import shutil
import subprocess
from urllib.error import HTTPError
from urllib.request import Request, urlopen
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
    with closing(
        paired.asgi_endpoint.__wrapped__(settings, dropped_cli_replies)
    ) as endpoints:
        for endpoint in endpoints:
            # ASGI startup alone does not load Django's lazy URL/schema graph.
            request = Request(
                endpoint + "/app/gql/config/",
                data=b'{"query":"{__typename}"}',
                headers={"Content-Type": "application/json"},
            )
            try:
                with urlopen(request, timeout=180) as response:
                    assert response.status == 200
            except HTTPError as error:
                assert error.code in {400, 401, 403}
            yield endpoint


@pytest.mark.parametrize(
    "client",
    [
        "cli",
        "browser",
        "context",
        "context-browser",
        "verification",
        "verification-browser",
        "image",
        "local-image",
        "local-image-browser",
        "image-browser",
        "workload",
        "workload-browser",
        "image-binding",
        "image-binding-browser",
        "immutable",
        "node-platforms",
        "workload-image",
        "workload-image-browser",
    ],
)
def test_compiled_cli_tree_prepare_lost_reply_cold_review_and_private_retry(
    world, binary, tmp_path, asgi_endpoint, dropped_cli_replies, monkeypatch, client
):
    if client == "browser" and (
        not os.getenv("NVKE_TEST_TREE_FRONTEND")
        or not os.getenv("NVKE_TEST_TREE_STORYBOOK")
    ):
        pytest.skip("Select the owned tree browser frontend and Storybook endpoint")
    if client == "context-browser" and (
        not os.getenv("NVKE_TEST_CONTEXT_FRONTEND")
        or not os.getenv("NVKE_TEST_CONTEXT_STORYBOOK")
    ):
        pytest.skip("Select the owned context frontend and Storybook endpoint")
    if client == "verification-browser" and (
        not os.getenv("NVKE_TEST_VERIFICATION_FRONTEND")
        or not os.getenv("NVKE_TEST_VERIFICATION_STORYBOOK")
    ):
        pytest.skip("Select the owned verification frontend and Storybook endpoint")
    if client in {"image-browser", "workload-image-browser"} and (
        not os.getenv("NVKE_TEST_IMAGE_FRONTEND")
        or not os.getenv("NVKE_TEST_IMAGE_STORYBOOK")
    ):
        pytest.skip("Select the owned image frontend and Storybook endpoint")
    if client == "local-image-browser" and (
        not os.getenv("NVKE_TEST_LOCAL_IMAGE_FRONTEND")
        or not os.getenv("NVKE_TEST_LOCAL_IMAGE_STORYBOOK")
    ):
        pytest.skip("Select the owned local image frontend and Storybook endpoint")
    if client == "workload-browser" and (
        not os.getenv("NVKE_TEST_WORKLOAD_FRONTEND")
        or not os.getenv("NVKE_TEST_WORKLOAD_STORYBOOK")
    ):
        pytest.skip("Select the owned workload frontend and Storybook endpoint")
    selected = os.getenv("NVKE_TEST_AGENT_OPSCODE")
    if (
        not selected
        or not os.getenv("NVKE_TEST_HELM4")
        or not os.getenv("NVKE_TEST_CHART_HELPER")
    ):
        pytest.skip("Select the owned pinned runtime, Helm 4 and snapshot-built helper")
    source = Path(selected)
    revision = (
        "58b643a0a50f71d822deade7fb633b98acded4f9"
        if client
        in {
            "workload",
            "workload-browser",
            "image-binding",
            "image-binding-browser",
            "immutable",
            "node-platforms",
            "workload-image",
            "workload-image-browser",
        }
        else "a0f0d0dd0df6aea24e1698e7ccc3ce4491fc2f22"
        if client in {"image", "image-browser", "local-image", "local-image-browser"}
        else (
            "c93fe5f5dcafd870dd6d34211a4a034b600b1b70"
            if client in {"verification", "verification-browser"}
            else "8ad214a1a71f6b082707da5317e20a612a54ae7e"
        )
    )
    if client in {
        "image-binding",
        "image-binding-browser",
        "immutable",
        "node-platforms",
    }:
        revision = "c67a6fe84b4653624de1862d5dc27fe3eb35f285"
    if client == "immutable":
        revision = "65a0833"
    if client == "node-platforms":
        revision = "ac7ab2e0f2048d5e4c10bcd6b3a886998cdca687"
    for name in (
        "operator_agent.py",
        "operator_contract.py",
        "management_recovery.py",
        "management_plan.py",
        "management_plan_store.py",
        "management_plan_preparation.py",
        "management_dependencies.py",
        "management_chart_archive.py",
        "management_chart_materialization.py",
        "management_plan_chart_service.py",
        "management_execution_release.py",
        "management_execution_arguments.py",
        "management_execution_tree.py",
        "management_template_rendering.py",
        "management_render_inputs.py",
        "management_render_discovery.py",
        "management_render_release.py",
        "management_render_lookup.py",
        "management_render_capture.py",
        "management_render_context_store.py",
        "management_render_authenticated.py",
        "management_render_transport.py",
        "management_render_executor.py",
        "management_plan_context_service.py",
        "management_frozen_chart.py",
        "management_plan_tree_service.py",
        "qualify_bootstrap.py",
        "tooling.py",
        "test_operator_agent.py",
        "test_management_render_transport.py",
        "test_management_render_capture.py",
        "chart-helper/main.go",
        "chart-helper/render.go",
        "chart-helper/release.go",
        "chart-helper/go.mod",
        "chart-helper/go.sum",
    ) + (
        (
            "management_plan_verification_service.py",
            "management_render_revalidation.py",
            "management_render_verification_store.py",
        )
        if client in {"verification", "verification-browser"}
        else ()
    ):
        relative = "kubernetes/nvke/" + name
        assert (source / relative).read_bytes() == subprocess.check_output(
            ["git", "show", revision + ":" + relative], cwd=source
        )
    if client in {
        "image",
        "image-browser",
        "local-image",
        "local-image-browser",
        "workload",
        "workload-browser",
        "image-binding",
        "image-binding-browser",
        "immutable",
        "node-platforms",
        "workload-image",
        "workload-image-browser",
    }:
        paths = (
            subprocess.check_output(
                ["git", "ls-tree", "-r", "--name-only", revision, "kubernetes/nvke"],
                cwd=source,
            )
            .decode()
            .splitlines()
        )
        expected = {
            name
            for name in paths
            if name.endswith((".py", ".go")) or Path(name).name in {"go.mod", "go.sum"}
        }
        actual = {
            str(path.relative_to(source))
            for path in (source / "kubernetes/nvke").rglob("*")
            if path.is_file()
            and (path.suffix in {".py", ".go"} or path.name in {"go.mod", "go.sum"})
        }
        assert actual == expected
        for name in sorted(expected):
            path = source / name
            if path.is_symlink():
                target = os.readlink(path)
                assert not Path(target).is_absolute()
                assert path.resolve(strict=True).is_relative_to(
                    source.resolve() / "kubernetes/nvke"
                )
                actual_bytes = os.fsencode(target)
            else:
                actual_bytes = path.read_bytes()
            assert actual_bytes == subprocess.check_output(
                ["git", "show", revision + ":" + name], cwd=source
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
            # Artifact commands review five parent records before their final
            # private request. Each private HTTP call keeps its existing 360s
            # budget; do not accidentally cap the whole serial command as one
            # request. Four ordinary metadata calls and local journal work have
            # separate bounded allowance. Native preparation still has 300s.
            process_timeout=(6 * 360 + 4 * 60 + 20)
            if arguments[:2] == ("agent", "install-plan")
            else 380,
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
            image_capture = None
            context_capture = None
            if client in {
                "image-binding",
                "image-binding-browser",
                "immutable",
                "node-platforms",
                "workload-image",
                "workload-image-browser",
            }:
                install_plan = native.agent.OperatorAgent.install_plan

                def observed_install_plan(engine, *args, **kwargs):
                    try:
                        return install_plan(engine, *args, **kwargs)
                    except native.agent.AgentError as error:
                        if re.fullmatch(r"[A-Z_]{1,128}", error.code):
                            print("Owned native plan refusal:", error.code)
                        cause = error.__context__
                        if cause is not None:
                            frames = []
                            trace = cause.__traceback__
                            while trace:
                                frames.append(
                                    (
                                        Path(trace.tb_frame.f_code.co_filename).name,
                                        trace.tb_lineno,
                                    )
                                )
                                trace = trace.tb_next
                            print(
                                "Owned native exception location:",
                                type(cause).__name__,
                                frames,
                            )
                        raise

                monkeypatch.setattr(
                    native.agent.OperatorAgent, "install_plan", observed_install_plan
                )
                make_engine = case.make_engine
                profiles = {}

                def combined_engine(**options):
                    for key in ("render_profile", "image_profile", "platform_profile"):
                        if key in options:
                            profiles[key] = options[key]
                    return make_engine(**{**profiles, **options})

                monkeypatch.setattr(case, "make_engine", combined_engine)
            if client in {
                "image",
                "image-browser",
                "local-image",
                "local-image-browser",
                "image-binding",
                "image-binding-browser",
                "immutable",
                "node-platforms",
                "workload-image",
                "workload-image-browser",
            }:
                from image_cli_qualification import ImageCapture, LocalImageCapture

                image_capture = (
                    LocalImageCapture
                    if client in {"local-image", "local-image-browser"}
                    else ImageCapture
                )(case, native)
            if client in {
                "context",
                "context-browser",
                "verification",
                "verification-browser",
                "workload",
                "workload-browser",
                "image-binding",
                "image-binding-browser",
                "immutable",
                "node-platforms",
                "workload-image",
                "workload-image-browser",
            }:
                from context_cli_qualification import ContextCapture

                context_capture = ContextCapture(case, native)
            node_capture = None
            if client == "node-platforms":
                from node_platform_cli_qualification import NodePlatformsCapture

                node_capture = NodePlatformsCapture(
                    case, native, context_capture, image_capture
                )
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
            job, intent, body = (
                context_capture.plan_fixture()
                if context_capture
                else case.plan_fixture()
            )
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
            if image_capture and image_capture.source_kind == "registry":
                body["overrides"]["files"]["registry-policy.json"] = (
                    image_capture.policy
                )
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
                if image_capture and client not in {
                    "image-binding",
                    "image-binding-browser",
                    "immutable",
                    "node-platforms",
                    "workload-image",
                    "workload-image-browser",
                }:
                    image_capture.materialize(destination)
                else:
                    (destination / "images.txt").unlink(missing_ok=True)
                chart = destination / "owned-local-chart"
                chart.mkdir(mode=0o700)
                (chart / "templates").mkdir(mode=0o700)
                native.agent.private_write(
                    chart / "Chart.yaml",
                    b"apiVersion: v2\nname: nktp\nversion: 1.0.0\n",
                )
                document = (
                    b"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: owned\n"
                )
                if client in {
                    "image-binding",
                    "image-binding-browser",
                    "immutable",
                    "node-platforms",
                    "workload-image",
                    "workload-image-browser",
                }:
                    document = (
                        "apiVersion: v1\nkind: Pod\nmetadata:\n  name: owned\nspec:\n  containers:\n    - name: owned\n      image: "
                        + image_capture.registry.reference
                        + "\n"
                    ).encode()
                native.agent.private_write(chart / "templates/config.yaml", document)
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
            case.engine = case.make_engine(
                **(
                    {"render_profile": context_capture.profile}
                    if context_capture
                    else {}
                ),
                **(
                    {"image_profile": image_capture.profile}
                    if image_capture and image_capture.profile
                    else {}
                ),
            )
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
            if context_capture:
                context_summary = context_capture.qualify(
                    run,
                    private_file,
                    dropped_cli_replies,
                    job,
                    tree_flags,
                    plan,
                    tree,
                    data,
                    tmp_path,
                )
            if client in {
                "workload",
                "workload-browser",
                "image-binding",
                "image-binding-browser",
                "immutable",
                "node-platforms",
                "workload-image",
                "workload-image-browser",
            }:
                from workload_cli_qualification import qualify_workload

                workload_summary = qualify_workload(
                    case,
                    context_capture,
                    context_summary,
                    run,
                    private_file,
                    dropped_cli_replies,
                    job,
                    tree_flags,
                    plan,
                    tree,
                    data,
                    tmp_path,
                )
            if client == "workload-browser":
                from workload_browser_qualification import run_workload_browser

                browser_row = run_workload_browser(
                    bearer=issued.plaintext,
                    tmp_path=tmp_path,
                    endpoint=asgi_endpoint,
                    world=world,
                    site=state.site,
                    authority=authority,
                    operation=original,
                    plan=plan,
                    charts=data,
                    tree=tree,
                    context_summary=context_summary,
                    restart=context_capture.restart,
                    dropped=dropped_cli_replies,
                )
                browser_artifact = case.engine.install_workload_inventories / str(
                    browser_row.guid
                )
                assert (browser_artifact / "index.json").is_file()
                assert len(list((browser_artifact / "work").iterdir())) == 1
            if image_capture:
                image_summary = image_capture.qualify(
                    run,
                    private_file,
                    dropped_cli_replies,
                    job,
                    tree_flags,
                    plan,
                    tree,
                    data,
                    tmp_path,
                    workload=workload_summary
                    if client
                    in {
                        "image-binding",
                        "image-binding-browser",
                        "immutable",
                        "node-platforms",
                        "workload-image",
                        "workload-image-browser",
                    }
                    else None,
                )
            if client in {
                "image-binding",
                "image-binding-browser",
                "immutable",
                "node-platforms",
            }:
                from image_binding_cli_qualification import qualify_image_binding

                binding_summary = qualify_image_binding(
                    case,
                    image_capture,
                    run,
                    private_file,
                    dropped_cli_replies,
                    job,
                    tree_flags,
                    plan,
                    tree,
                    data,
                    workload_summary,
                    image_summary,
                    tmp_path,
                )
            if node_capture:
                node_capture.qualify(
                    run,
                    private_file,
                    dropped_cli_replies,
                    job,
                    tree_flags,
                    plan,
                    tree,
                    workload_summary,
                    binding_summary,
                    tmp_path,
                )
            if client == "immutable":
                from immutable_cli_qualification import qualify_immutable_artifacts

                qualify_immutable_artifacts(
                    case,
                    image_capture,
                    run,
                    private_file,
                    dropped_cli_replies,
                    job,
                    tree_flags,
                    plan,
                    tree,
                    workload_summary,
                    binding_summary,
                    tmp_path,
                )
            if client == "image-binding-browser":
                from image_binding_browser_qualification import (
                    run_image_binding_browser,
                )

                reads_before_binding_browser = len(image_capture.registry.requests)
                browser_binding = run_image_binding_browser(
                    bearer=issued.plaintext,
                    tmp_path=tmp_path,
                    endpoint=asgi_endpoint,
                    world=world,
                    site=state.site,
                    authority=authority,
                    operation=original,
                    plan=plan,
                    charts=data,
                    tree=tree,
                    reference=image_capture.registry.reference,
                    image=image_summary,
                    dropped=dropped_cli_replies,
                    workload=workload_summary,
                    restart=image_capture.restart,
                )
                browser_artifact = case.engine.install_image_bindings / str(
                    browser_binding.guid
                )
                assert (browser_artifact / "index.json").is_file()
                assert len(list((browser_artifact / "work").iterdir())) == 1
                assert (
                    len(image_capture.registry.requests) == reads_before_binding_browser
                )
            if client in {
                "image-browser",
                "local-image-browser",
                "workload-image-browser",
            }:
                browser_tree_row = (
                    NvkeInstallPlanTree.objects.filter(guid=tree_id).values().get()
                )
                browser_parent_roots = (
                    case.engine.install_plans.root,
                    case.engine.install_chart_artifacts,
                    case.engine.install_execution_trees,
                )
                browser_parents = [
                    preparation.storage._scan(root, 1073741824)
                    for root in browser_parent_roots
                ]
                if client == "local-image-browser":
                    from local_image_browser_qualification import (
                        run_local_image_browser as run_image_browser,
                    )
                else:
                    from image_browser_qualification import run_image_browser

                browser_row = run_image_browser(
                    bearer=issued.plaintext,
                    tmp_path=tmp_path,
                    endpoint=asgi_endpoint,
                    world=world,
                    site=state.site,
                    authority=authority,
                    operation=original,
                    plan=plan,
                    charts=data,
                    tree=tree,
                    reference=image_capture.registry.reference,
                    root_digest=image_capture.registry.descriptor["digest"],
                    dropped=dropped_cli_replies,
                    **(
                        {"workload": workload_summary, "restart": image_capture.restart}
                        if client == "workload-image-browser"
                        else {}
                    ),
                )
                browser_artifact = case.engine.install_registry_images / str(
                    browser_row.guid
                )
                assert (browser_artifact / "index.json").is_file()
                assert len(list((browser_artifact / "work").iterdir())) == 1
                assert (
                    NvkeInstallPlanTree.objects.filter(guid=tree_id).values().get()
                    == browser_tree_row
                )
                assert [
                    preparation.storage._scan(root, 1073741824)
                    for root in browser_parent_roots
                ] == browser_parents
            if client == "verification":
                from verification_cli_qualification import qualify

                qualify(
                    context_capture,
                    run,
                    private_file,
                    dropped_cli_replies,
                    job,
                    tree_flags,
                    plan,
                    tree,
                    data,
                    tmp_path,
                )
            if client == "verification-browser":
                from verification_browser_qualification import run_verification_browser

                browser_row = run_verification_browser(
                    bearer=issued.plaintext,
                    tmp_path=tmp_path,
                    endpoint=asgi_endpoint,
                    world=world,
                    site=state.site,
                    authority=authority,
                    operation=original,
                    plan=plan,
                    charts=data,
                    tree=tree,
                    captured=context_summary,
                    dropped=dropped_cli_replies,
                )
                browser_artifact = case.engine.install_render_verifications / str(
                    browser_row.guid
                )
                assert (browser_artifact / "index.json").is_file()
                assert len(list((browser_artifact / "work").iterdir())) == 1
            if client == "context-browser":
                from context_browser_qualification import run_context_browser

                browser_row = run_context_browser(
                    bearer=issued.plaintext,
                    tmp_path=tmp_path,
                    endpoint=asgi_endpoint,
                    world=world,
                    site=state.site,
                    authority=authority,
                    operation=original,
                    plan=plan,
                    charts=data,
                    tree=tree,
                    dropped=dropped_cli_replies,
                )
                browser_artifact = case.engine.install_render_contexts / str(
                    browser_row.guid
                )
                assert (browser_artifact / "index.json").is_file()
                assert len(list((browser_artifact / "work").iterdir())) == 1
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
            if image_capture:
                image_capture.close()
            if context_capture:
                context_capture.close()
            case.tearDown()
    finally:
        native.NativeAgentTests.tearDownClass()
