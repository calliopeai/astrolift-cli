"""Opt-in App/PostgreSQL/browser against a fresh, held Linux native fixture.

The private handoff must come from the owned fixture runner. Original installation
state is controlled setup; parent registration and all subsequent reads use the
actual App/SDK/mTLS agent. This is not a physical installation qualification.
"""

import json
import os
from pathlib import Path
from types import SimpleNamespace

import pytest
import test_nvke_install_plan_tree as pipeline
from nvke.contract import upstream_revision
from pull_runtime_browser_qualification import run_pull_runtime_browser
from pull_runtime_parent_qualification import _public, register_pull_runtime_parents

from astrolift_clusters import nvke_install_plan_pull_runtime as correspondence
from astrolift_clusters import nvke_operator_authority as authorities
from astrolift_clusters.models import NvkeOperation, NvkeSite
from astrolift_clusters.schema.nvke_operator import authority_type
from astrolift_clusters.tests.test_nvke_operator import partial_install, source_review
from astrolift_clusters.tests.test_nvke_sites import context
from astrolift_identity.api_tokens import (
    mint_token,
    reset_current_api_token,
    set_current_api_token,
)
from astrolift_identity.models import ApiToken
from core.schema.audit import MutationAuditLog

pytestmark = pytest.mark.django_db(transaction=True)


@pytest.fixture
def world():
    return pipeline.world.__wrapped__()


@pytest.fixture
def binary():
    yield from pipeline.binary.__wrapped__()


@pytest.fixture
def dropped_cli_replies():
    return {}


@pytest.fixture
def asgi_endpoint(settings, dropped_cli_replies):
    yield from pipeline.asgi_endpoint.__wrapped__(settings, dropped_cli_replies)


@pytest.mark.skipif(
    not os.getenv("NVKE_TEST_PULL_RUNTIME_HOST_HANDOFF"),
    reason="Select a fresh owned native runtime fixture private handoff",
)
def test_browser_reads_owned_native_runtime_correspondence(
    world, binary, asgi_endpoint, tmp_path
):
    selected = os.getenv("NVKE_TEST_PULL_RUNTIME_HOST_HANDOFF")
    if not selected:
        pytest.skip("Select a fresh owned native runtime fixture private handoff")
    path = Path(selected)
    stat = path.lstat()
    assert (
        path.is_file()
        and not path.is_symlink()
        and stat.st_uid == os.getuid()
        and stat.st_nlink == 1
        and stat.st_mode & 0o777 == 0o600
    )
    handoff = json.loads(path.read_text())
    native = handoff["native"]
    site = NvkeSite.objects.create(
        guid=native["siteId"],
        upstream_revision=upstream_revision(),
        organization=world.org,
        name="Owned native correspondence",
        slug="owned-native-correspondence",
        organization_namespace="org-owned",
    )
    issued = mint_token()
    token = ApiToken.objects.create(
        user=world.user,
        organization=world.org,
        name="owned correspondence browser",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin", "read"],
    )
    mark = set_current_api_token(token)
    try:
        with context(world):
            authority = authorities.configure(
                site_id=site.guid,
                expected_actor_id=world.user.pk,
                expected_site_version=site.version,
                expected_version=0,
                endpoint=handoff["agentEndpoint"],
                instance_id=native["instanceId"],
                bundle_manifest_sha256=native["bundleManifestSha256"],
                timeout_seconds=300,
                credentials={
                    "ca_pem": native["ca"],
                    "client_certificate_pem": native["certificate"],
                    "client_key_pem": native["key"],
                },
            )
            site.refresh_from_db()
            authority = authorities.verify(
                site_id=site.guid,
                expected_actor_id=world.user.pk,
                expected_site_version=site.version,
                expected_version=authority.version,
            )
            assert authority.verification_code == "AGENT_VERIFIED"
            state = SimpleNamespace(world=world, site=site, authority=authority)
            # Match the controlled uncertain original already held by the native
            # fixture. No original dispatch or infrastructure execution occurs.
            original = partial_install(state)
            NvkeOperation.objects.filter(pk=original.pk).update(
                guid=native["jobId"], request_digest=native["intent"]
            )
            original.refresh_from_db()
            parents, source = register_pull_runtime_parents(
                history=native["history"], review=source_review(state, original)
            )
            read = correspondence.read(
                **source,
                analysis_id=parents["analysis"]["id"],
                expected_analysis_version=parents["analysis"]["version"],
                analysis_digest=parents["analysis"]["authorityDigest"],
                node_platform_id=parents["nodePlatforms"]["id"],
                expected_node_platform_version=parents["nodePlatforms"]["version"],
                node_platform_digest=parents["nodePlatforms"]["platformDigest"],
            )
            assert read["metadata"] == native["expected"]
        fixture = {
            "workload": parents["workload"],
            "binding": parents["binding"],
            "pullAuthority": parents["pullAuthority"],
            "analysis": parents["analysis"],
            "nodePlatforms": parents["nodePlatforms"],
            "scope": {
                "server": handoff["storybook"] + "/__nvke-pull-runtime-api",
                "organizationId": str(world.org.guid),
                "actorId": world.user.pk,
            },
            "source": {
                "scopeKey": "owned-native-runtime-correspondence",
                "site": {
                    "id": str(site.guid),
                    "version": site.version,
                    "upstreamRevision": site.upstream_revision,
                },
                "operation": {
                    "id": str(original.guid),
                    "version": original.version,
                    "siteId": str(site.guid),
                    "status": "UNCERTAIN",
                    "operationKey": "tooling.management.install",
                },
                "authority": _public(authority_type(authority)),
                "plan": parents["plan"],
                "charts": parents["charts"],
                "artifacts": parents["tree"],
                "canReview": True,
                "pending": False,
                "loading": False,
                "error": None,
            },
        }
        flags = {
            "site": str(site.guid),
            "site-version": site.version,
            "authority-version": authority.version,
            "version": original.version,
            "plan": parents["plan"]["id"],
            "plan-version": parents["plan"]["version"],
            "plan-digest": parents["plan"]["plan"]["planDigest"],
            "chart-artifact": parents["charts"]["id"],
            "chart-artifact-version": parents["charts"]["version"],
            "chart-artifact-digest": parents["charts"]["artifactDigest"],
            "tree": parents["tree"]["id"],
            "tree-version": parents["tree"]["version"],
            "tree-digest": parents["tree"]["treeDigest"],
            "workload": parents["workload"]["id"],
            "workload-version": parents["workload"]["version"],
            "workload-digest": parents["workload"]["workloadDigest"],
            "binding": parents["binding"]["id"],
            "binding-version": parents["binding"]["version"],
            "binding-digest": parents["binding"]["bindingDigest"],
            "pull-authority": parents["pullAuthority"]["id"],
            "pull-authority-version": parents["pullAuthority"]["version"],
            "pull-authority-digest": parents["pullAuthority"]["authorityDigest"],
            "artifact": parents["analysis"]["id"],
            "analysis-version": parents["analysis"]["version"],
            "analysis-digest": parents["analysis"]["authorityDigest"],
            "node-platform": parents["nodePlatforms"]["id"],
            "node-platform-version": parents["nodePlatforms"]["version"],
            "node-platform-digest": parents["nodePlatforms"]["platformDigest"],
        }
        cli = pipeline.paired.invoke(
            binary,
            tmp_path,
            asgi_endpoint,
            issued.plaintext,
            world.org.slug,
            "agent",
            "install-plan",
            "pull-runtime",
            "review",
            str(original.guid),
            *(part for name, value in flags.items() for part in ("--" + name, value)),
            process_timeout=8 * 360,
        )
        assert cli.returncode == 0, "Owned correspondence CLI failed"
        cli_result = json.loads(cli.stdout)
        assert (
            cli_result["correspondenceDigest"]
            == native["expected"]["correspondenceDigest"]
        )
        assert cli_result["continuationAuthorized"] is False
        assert cli_result["analysis"] == parents["analysis"]
        assert cli_result["nodePlatforms"] == parents["nodePlatforms"]
        before = MutationAuditLog.objects.filter(
            operation="nvke.operator_install_plan_pull_runtime_review", success=True
        ).count()
        run_pull_runtime_browser(
            frontend=handoff["frontend"],
            storybook=handoff["storybook"],
            endpoint=asgi_endpoint + "/app/gql/config/",
            bearer=issued.plaintext,
            fixture=fixture,
            working=tmp_path,
        )
        assert (
            MutationAuditLog.objects.filter(
                operation="nvke.operator_install_plan_pull_runtime_review", success=True
            ).count()
            == before + 2
        )
    finally:
        reset_current_api_token(mark)
