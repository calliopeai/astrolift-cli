"""Compiled CLI -> real ASGI/PostgreSQL exact preview identity proof.

Only the external pod/pricing/log-provider boundary is instrumented. GraphQL,
permissions, tenancy, persistent models, HTTP, and the native binary are real.
No Temporal/Kubernetes effects are dispatched by these read-only operations.
"""

import json
import os

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip("requires opt-in compiled CLI and backend", allow_module_level=True)

from test_pipeline_reviewed import create_world, forwarding_proxy, invoke, running_api
from astrolift_clusters.models import ProviderPlugin, TenantCluster
from astrolift_identity.models import Project, Team
from astrolift_lifecycle.models import AppEnvironment, PreviewEnvironment
from astrolift_registry.models import RegisteredApp
from core.permissions import Permission
from core.tests.utils.scope_world import bind_role

pytestmark = pytest.mark.django_db(transaction=True)


def test_native_exact_preview_reads_and_routes(settings, monkeypatch, tmp_path):
    monkeypatch.setattr(
        "core.documents.ProfileDocument.index_profile", classmethod(lambda *args: None)
    )
    settings.DEBUG = False
    world = create_world()
    bind_role(
        world.user,
        permissions=[Permission.APP_READ_LOGS],
        kind="ORG",
        scope_id=world.org.pk,
        slug="preview-logs",
    )
    team = Team.objects.create(
        organization=world.org, name="Fixture team", slug="fixture"
    )
    project = Project.objects.create(
        organization=world.org, team=team, name="Fixture project", slug="fixture"
    )
    app = RegisteredApp.objects.create(
        organization=world.org,
        team=team,
        project=project,
        name="Fixture app",
        slug="preview-app",
        provisioning_status="ready",
    )
    ProviderPlugin.objects.bulk_create(
        [
            ProviderPlugin(
                name="Preview fixture",
                slug="preview-fixture",
                plugin_version="1",
                config_schema={},
                capabilities_manifest={},
            )
        ]
    )
    cluster = TenantCluster.objects.create(
        organization=world.org,
        name="Preview cluster",
        slug="preview",
        provider_plugin=ProviderPlugin.objects.get(slug="preview-fixture"),
        lifecycle="managed",
        endpoint="https://fixture.invalid",
        auth_method="kubeconfig",
        auth_config={},
        provider_config={},
    )
    env = AppEnvironment.objects.create(
        registered_app=app,
        tenant_cluster=cluster,
        name="arbitrary-env",
        k8s_namespace="canonical-preview",
        url="https://reused.invalid",
    )
    preview = PreviewEnvironment.objects.create(
        registered_app=app,
        app_environment=env,
        namespace=env.k8s_namespace,
        pr_number=1,
        branch="old-preview",
        hostname="reused.invalid",
        status="running",
    )
    PreviewEnvironment.objects.bulk_create(
        [
            PreviewEnvironment(
                registered_app=app,
                app_environment=env,
                namespace=env.k8s_namespace,
                pr_number=i,
                branch=f"new-{i}",
                hostname="reused.invalid",
                status="running",
            )
            for i in range(2, 205)
        ]
    )
    pod_calls = []
    monkeypatch.setattr(
        "astrolift_lifecycle.schema.queries.list_app_pods",
        lambda **kw: pod_calls.append(kw) or [],
    )
    monkeypatch.setattr(
        "astrolift_lifecycle.preview_cost.estimate_daily_cost",
        lambda **kw: pytest.fail("pricing without resource data"),
    )
    log_calls = []
    monkeypatch.setattr(
        "core.cluster_log_query.query_app_logs",
        lambda **kw: log_calls.append(kw) or None,
    )
    with running_api() as port, forwarding_proxy(port) as proxy:
        base = [
            "app",
            "previews",
            "show",
            app.slug,
            "--id",
            str(preview.guid),
            "--json",
        ]
        exact = invoke(world, proxy, tmp_path, base)
        assert exact.returncode == 0, exact.stderr
        result = json.loads(exact.stdout)
        assert result["id"] == str(preview.guid) and result["environment"][
            "environmentId"
        ] == str(env.guid)
        assert result["environment"]["environmentName"] == "arbitrary-env"
        assert (
            result["runtimeStatus"] == "not_requested"
            and result["estimatedDailyCostUsd"] is None
        )
        assert not pod_calls
        assert (
            len(proxy.requests) == 1
            and "astroliftPreviewEnvironment(" in proxy.requests[0][0]["query"]
        )
        cost = invoke(world, proxy, tmp_path, base + ["--cost"])
        assert cost.returncode == 0, cost.stderr
        assert len(pod_calls) == 1 and pod_calls[0]["namespace"] == "canonical-preview"
        assert json.loads(cost.stdout)["estimatedDailyCostUsd"] is None
        logs = invoke(
            world,
            proxy,
            tmp_path,
            ["app", "previews", "logs", app.slug, "--id", str(preview.guid), "--json"],
        )
        assert logs.returncode == 0, logs.stderr
        assert (
            len(log_calls) == 1
            and log_calls[0]["namespace"] == env.k8s_namespace
            and log_calls[0]["cluster"].pk == cluster.pk
        )
        req = proxy.requests[-1][0]
        assert req["variables"]["expectedEnvironmentId"] == str(env.guid)
        assert req["variables"]["ifMatchEnvironmentVersion"] == env.version
        assert all(
            "astroliftEnvironments(" not in r[0]["query"] for r in proxy.requests
        )
        wrong_app = invoke(
            world,
            proxy,
            tmp_path,
            [
                "app",
                "previews",
                "show",
                "other-app",
                "--id",
                str(preview.guid),
                "--json",
            ],
        )
        assert wrong_app.returncode != 0 and "identity" in wrong_app.stderr
        foreign = create_world()
        hidden = invoke(foreign, proxy, tmp_path, base)
        assert (
            hidden.returncode != 0
            and str(env.guid) not in hidden.stdout + hidden.stderr
        )
        env.soft_delete()
        retired = invoke(world, proxy, tmp_path, base)
        assert (
            retired.returncode == 0
            and json.loads(retired.stdout)["environmentStatus"] == "retired"
        )
        for verb in ("open", "logs"):
            args = [
                "app",
                "previews",
                verb,
                app.slug,
                "--id",
                str(preview.guid),
                "--json",
            ]
            if verb == "open":
                args.append("--url")
            refused = invoke(world, proxy, tmp_path, args)
            assert refused.returncode != 0 and "retired" in refused.stderr
        assert len(log_calls) == 1
