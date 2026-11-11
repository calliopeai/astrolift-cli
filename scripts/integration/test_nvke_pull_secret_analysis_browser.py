"""Compiled CLI and production browser through actual App/database/mTLS agent."""

import http.client
import json
import os
from pathlib import Path
from urllib.parse import urlsplit

import pytest
import test_nvke_install_plan_tree as pipeline
from astrolift_clusters.models import NvkeOperation, NvkeSite
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken
from pull_secret_analysis_browser_qualification import run_pull_secret_analysis_browser
from pull_secret_analysis_cli_qualification import PullSecretAnalysisCapture

pytestmark = pytest.mark.django_db(transaction=True)


@pytest.fixture
def world():
    return pipeline.world.__wrapped__()


@pytest.fixture
def binary():
    yield from pipeline.binary.__wrapped__()


@pytest.fixture
def dropped_cli_replies():
    return pipeline.dropped_cli_replies.__wrapped__()


@pytest.fixture
def asgi_endpoint(settings, dropped_cli_replies):
    yield from pipeline.asgi_endpoint.__wrapped__(settings, dropped_cli_replies)


def test_compiled_cli_and_browser_pull_secret_analysis_recovery(
    world,
    binary,
    tmp_path,
    asgi_endpoint,
    dropped_cli_replies,
    monkeypatch,
):
    frontend = os.getenv("NVKE_TEST_PULL_SECRET_ANALYSIS_FRONTEND")
    storybook = os.getenv("NVKE_TEST_PULL_SECRET_ANALYSIS_STORYBOOK")
    if not frontend or not storybook:
        pytest.skip("Select owned Pod pull frontend and Storybook endpoint")
    assert (
        Path(frontend) / "e2e/nvke-install-plan-pull-secret-analysis-api-runner.mjs"
    ).is_file()
    url = urlsplit(storybook)
    assert (
        url.scheme == "http"
        and url.hostname == "127.0.0.1"
        and not url.username
        and not url.password
    )
    connection = http.client.HTTPConnection(url.hostname, url.port, timeout=10)
    try:
        connection.request("GET", "/index.json")
        response = connection.getresponse()
        assert response.status == 200
        index = json.loads(response.read(8 * 1024 * 1024))
        assert (
            "screens-nvke-owned-api-secret-analysis-recovery--recovery"
            in index["entries"]
        )
    finally:
        connection.close()
    original_qualify, reports = PullSecretAnalysisCapture.qualify, []
    issued = mint_token()
    ApiToken.objects.create(
        user=world.user,
        organization=world.org,
        name="owned Pod pull browser",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin", "read"],
    )

    def qualify(
        capture,
        run,
        private_file,
        dropped,
        job,
        tree_flags,
        plan,
        tree,
        workload,
        binding,
        working,
    ):
        result = original_qualify(
            capture,
            run,
            private_file,
            dropped,
            job,
            tree_flags,
            plan,
            tree,
            workload,
            binding,
            working,
        )
        assert not result["continuationAuthorized"]
        site = NvkeSite.objects.get(guid=plan["siteId"])
        operation = NvkeOperation.objects.get(guid=job)
        authority = run("agent", "get", "--site", str(site.guid))
        charts = run(
            "agent",
            "install-plan",
            "charts",
            "review",
            job,
            "--site",
            str(site.guid),
            "--site-version",
            plan["siteVersion"],
            "--authority-version",
            plan["authorityVersion"],
            "--version",
            plan["operationVersion"],
            "--plan",
            plan["id"],
            "--plan-version",
            plan["version"],
            "--plan-digest",
            plan["plan"]["planDigest"],
            "--artifact",
            tree["chartArtifactId"],
        )
        reports.append(
            run_pull_secret_analysis_browser(
                capture=capture,
                bearer=issued.plaintext,
                tmp_path=working,
                endpoint=asgi_endpoint,
                world=world,
                site=site,
                authority=authority,
                operation=operation,
                plan=plan,
                charts=charts,
                tree=tree,
                workload=workload,
                binding=binding,
                dropped=dropped,
                pull=capture.pull,
            )
        )
        return result

    monkeypatch.setattr(PullSecretAnalysisCapture, "qualify", qualify)
    pipeline.test_compiled_cli_tree_prepare_lost_reply_cold_review_and_private_retry(
        world,
        binary,
        tmp_path,
        asgi_endpoint,
        dropped_cli_replies,
        monkeypatch,
        "pull-authority",
        secret_analysis=True,
    )
    assert len(reports) == 1
