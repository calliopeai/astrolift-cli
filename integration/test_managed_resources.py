"""Compiled native CLI -> real ASGI/PostgreSQL scoped resource pages and exact targets.

Only disposable local records are mutated. No provider or engine is contacted.
"""

import json
import os
from uuid import uuid4

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip("requires compiled CLI and real app backend", allow_module_level=True)

from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken, Member
from astrolift_lifecycle.models import AppEnvironment
from astrolift_registry.models import RegisteredApp
from astrolift_services.models import ManagedService, ManagedServiceAttachment
from astrolift_services.tests import test_resource_reads_2207 as shared
from core.permissions import Permission
from core.tests.utils.scope_world import bind_role
from django.utils import timezone
from test_pipeline_reviewed import forwarding_proxy, invoke, running_api

pytestmark = pytest.mark.django_db(transaction=True)


@pytest.fixture(autouse=True)
def no_search(monkeypatch):
    monkeypatch.setattr(
        "core.documents.ProfileDocument.index_profile",
        classmethod(lambda cls, row: None),
    )
    monkeypatch.setattr(
        "core.documents.ProfileDocument.delete_profile",
        classmethod(lambda cls, row: None),
    )


def create_world():
    world = shared.world.__wrapped__()
    Member.objects.create(user=world.user, scope_kind="ORG", scope_id=world.org.pk)
    world.binding = bind_role(
        world.user,
        permissions=[
            Permission.PROJECT_READ,
            Permission.PROJECT_UPDATE,
            Permission.APP_READ,
        ],
        kind="ORG",
        scope_id=world.org.pk,
        slug=f"resource-cli-{uuid4().hex}",
    )
    token = mint_token()
    ApiToken.objects.create(
        user=world.user,
        organization=world.org,
        name="Disposable resource proof",
        token_hash=token.token_hash,
        scopes=["read:apps", "project:write"],
    )
    world.token = token.plaintext
    ManagedService.objects.bulk_create(
        [
            ManagedService(
                project=world.medops_project,
                tenant_cluster=world.cluster,
                name=f"resource-{index:03}",
                kind="redis",
                config={"credential": "PRIVATE_RESOURCE_MARKER"},
            )
            for index in range(250)
        ]
    )
    ManagedService.objects.filter(project=world.medops_project).update(
        created_at=timezone.now()
    )
    apps = RegisteredApp.objects.bulk_create(
        [
            RegisteredApp(
                organization=world.org,
                team=world.medops,
                project=world.medops_project,
                name=f"Consumer {index}",
                slug=f"consumer-{index}",
                k8s_namespace=f"consumer-{index}",
            )
            for index in range(251)
        ]
    )
    environments = AppEnvironment.objects.bulk_create(
        [
            AppEnvironment(
                registered_app=app, name="production", tenant_cluster=world.cluster
            )
            for app in apps
        ]
    )
    ManagedServiceAttachment.objects.bulk_create(
        [
            ManagedServiceAttachment(
                managed_service=world.service,
                app_environment=env,
                credential_ref="PRIVATE_RESOURCE_MARKER",
            )
            for env in environments
        ]
    )
    world.extra_env = AppEnvironment.objects.create(
        registered_app=world.medops_app, name="production", tenant_cluster=world.cluster
    )
    world.owned = ManagedService.objects.create(
        registered_app=world.medops_app,
        app_environment=world.extra_env,
        name="private-cache",
        kind="redis",
        config={"credential": "PRIVATE_RESOURCE_MARKER"},
    )
    return world


def call(world, proxy, tmp_path, args):
    result = invoke(
        world,
        proxy,
        tmp_path,
        ["--project", str(world.medops_project.guid), *args, "--json"],
    )
    assert "PRIVATE_RESOURCE_MARKER" not in result.stdout + result.stderr
    assert "RESOURCE_PRIVATE_MARKER" not in result.stdout + result.stderr
    return result


def test_native_resource_pages_exact_context_and_reviewed_attachment(tmp_path):
    world = create_world()
    with running_api() as port, forwarding_proxy(port) as proxy:
        seen = set()
        cursor = None
        first_cursor = None
        while True:
            args = ["project", "resources", "list", "--page", "--limit", "50"]
            if cursor:
                args += ["--after", cursor]
            result = call(world, proxy, tmp_path, args)
            assert result.returncode == 0, result.stderr
            page = json.loads(result.stdout)
            assert page["totalCount"] == 251
            assert len(page["items"]) <= 50
            ids = {row["id"] for row in page["items"]}
            assert not ids & seen
            seen |= ids
            cursor = page["nextCursor"]
            if not first_cursor:
                first_cursor = cursor
            if not cursor:
                break
        assert len(seen) == 251
        default = call(world, proxy, tmp_path, ["project", "resources", "list"])
        assert default.returncode == 0, default.stderr
        assert len(json.loads(default.stdout)) == 251
        detail = call(
            world,
            proxy,
            tmp_path,
            ["project", "resources", "show", str(world.service.guid)],
        )
        assert detail.returncode == 0, detail.stderr
        revision = json.loads(detail.stdout)["contextRevision"]
        seen = set()
        cursor = None
        while True:
            args = [
                "project",
                "resources",
                "attachments",
                str(world.service.guid),
                "--expected-context-revision",
                revision,
                "--limit",
                "50",
            ]
            if cursor:
                args += ["--after", cursor]
            result = call(world, proxy, tmp_path, args)
            assert result.returncode == 0, result.stderr
            page = json.loads(result.stdout)
            assert page["totalCount"] == 251
            assert len(page["items"]) <= 50
            ids = {row["id"] for row in page["items"]}
            assert not ids & seen
            seen |= ids
            cursor = page["nextCursor"]
            if not cursor:
                break
        assert len(seen) == 251
        basic_requests = list(proxy.requests)
        assert not any(
            "CostPreview" in request[0]["query"] for request in basic_requests
        )
        assert not any(
            any(
                word in request[0]["query"]
                for word in [
                    "statusError",
                    "appliedConfig",
                    "secretGrants",
                    "credentialRef",
                    "backendRef",
                ]
            )
            for request in basic_requests
        )
        owned = invoke(
            world,
            proxy,
            tmp_path,
            [
                "--app",
                world.medops_app.slug,
                "app",
                "services",
                "show",
                str(world.owned.guid),
                "--json",
            ],
        )
        assert owned.returncode == 0, owned.stderr
        assert json.loads(owned.stdout)["registeredAppId"] == str(world.medops_app.guid)
        attached = call(
            world,
            proxy,
            tmp_path,
            [
                "project",
                "resources",
                "attach",
                str(world.service.guid),
                "--expected-context-revision",
                revision,
                "--app-env",
                str(world.extra_env.guid),
            ],
        )
        assert attached.returncode == 0, attached.stderr
        attachment = json.loads(attached.stdout)
        assert ManagedServiceAttachment.objects.filter(
            guid=attachment["id"],
            managed_service=world.service,
            app_environment=world.extra_env,
        ).exists()
        proof = [
            request[0]["variables"]["input"]
            for request in proxy.requests
            if "attachProjectManagedService(input:" in request[0]["query"]
        ][-1]
        assert proof["expectedContextRevision"] == revision
        detached = call(world, proxy, tmp_path, ["project", "resources", "detach", attachment["id"]])
        assert detached.returncode == 0, detached.stderr
        assert not ManagedServiceAttachment.objects.filter(guid=attachment["id"]).exists()
        assert any("astroliftProjectManagedServiceAttachmentOwner(" in request[0]["query"] for request in proxy.requests)
        missing_detach = call(world, proxy, tmp_path, ["project", "resources", "detach", attachment["id"]])
        assert missing_detach.returncode != 0 and missing_detach.stdout == ""
        world.cluster.region = "changed-region"
        world.cluster.save()
        before = ManagedServiceAttachment.objects.filter(
            managed_service=world.service
        ).count()
        refused = call(
            world,
            proxy,
            tmp_path,
            [
                "project",
                "resources",
                "attach",
                str(world.service.guid),
                "--expected-context-revision",
                revision,
                "--app-env",
                str(world.extra_env.guid),
            ],
        )
        assert refused.returncode != 0 and not refused.stdout
        assert (
            ManagedServiceAttachment.objects.filter(
                managed_service=world.service
            ).count()
            == before
        )
        original = world.service.guid
        world.service.delete()
        replacement = ManagedService.objects.create(
            project=world.medops_project,
            tenant_cluster=world.cluster,
            name="cache",
            kind="redis",
        )
        missing = call(
            world, proxy, tmp_path, ["project", "resources", "show", str(original)]
        )
        assert missing.returncode != 0 and str(replacement.guid) not in missing.stdout

        foreign_cursor = invoke(
            world,
            proxy,
            tmp_path,
            [
                "--project",
                str(world.platform_project.guid),
                "project",
                "resources",
                "list",
                "--page",
                "--after",
                first_cursor,
                "--json",
            ],
        )
        assert foreign_cursor.returncode != 0 and not foreign_cursor.stdout
        malformed = call(
            world,
            proxy,
            tmp_path,
            [
                "project",
                "resources",
                "list",
                "--page",
                "--after",
                "malformed-private-marker",
            ],
        )
        assert (
            malformed.returncode != 0
            and not malformed.stdout
            and "malformed-private-marker" not in malformed.stderr
        )
        world.binding.delete()
        revoked = call(
            world, proxy, tmp_path, ["project", "resources", "list", "--page"]
        )
        assert revoked.returncode != 0 and not revoked.stdout
