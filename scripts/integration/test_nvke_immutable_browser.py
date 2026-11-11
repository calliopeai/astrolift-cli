"""Compiled CLI and browser against the actual App, database and native agent."""

import http.client
import json
import os
from pathlib import Path
from urllib.parse import urlsplit


import pytest
import test_nvke_install_plan_tree as pipeline
from astrolift_clusters import nvke_install_plan_immutable as artifacts
from astrolift_clusters.models import (
    NvkeInstallPlanImageBinding,
    NvkeInstallPlanImagePayload,
    NvkeOperation,
    NvkeSite,
)
from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken
from immutable_browser_qualification import run_immutable_browser
import immutable_cli_qualification as leaf


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


def test_compiled_cli_and_browser_immutable_recovery(
    world,
    binary,
    tmp_path,
    asgi_endpoint,
    dropped_cli_replies,
    monkeypatch,
):
    frontend = os.getenv("NVKE_TEST_IMMUTABLE_FRONTEND")
    storybook = os.getenv("NVKE_TEST_IMMUTABLE_STORYBOOK")
    if not frontend or not storybook:
        pytest.skip(
            "Select the owned immutable browser frontend and Storybook endpoint"
        )
    assert (Path(frontend) / "e2e/nvke-install-plan-immutable-api-runner.mjs").is_file()
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
        assert response.status == 200, (
            "Start the owned Storybook server before native qualification"
        )
        index = json.loads(response.read(8 * 1024 * 1024))
        assert (
            "screens-nvke-owned-api-immutable-artifact-recovery--recovery"
            in index["entries"]
        )
    finally:
        connection.close()
    original_leaf, reports = leaf.qualify_immutable_artifacts, []
    issued = mint_token()
    ApiToken.objects.create(
        user=world.user,
        organization=world.org,
        name="owned immutable browser",
        token_hash=issued.token_hash,
        token_last_4=issued.last4,
        scopes=["admin", "read"],
    )

    def qualify(
        case,
        image_capture,
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
        result = original_leaf(
            case,
            image_capture,
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
        binding_row = NvkeInstallPlanImageBinding.objects.get(guid=binding["id"])
        selected = {}
        for row in NvkeInstallPlanImagePayload.objects.filter(
            binding=binding_row, status="prepared"
        ):
            intent = artifacts._body(
                row,
                binding_row.tree.charts.plan,
                binding_row.tree.charts,
                binding_row.tree,
                binding_row,
                tree=False,
            )
            assert intent["bindingId"] == binding["id"]
            assert intent["planDigest"] == plan["plan"]["planDigest"]
            assert intent["item"] not in selected
            selected[intent["item"]] = {
                "artifactId": str(row.guid),
                "artifactDigest": row.summary["artifactDigest"],
            }
        assert len(selected) == plan["itemCount"]
        native_plan = case.engine.install_plans.load(
            plan["id"], expected_digest=plan["plan"]["planDigest"]
        )
        phase = native_plan["plan"]["items"][0]["name"]
        common = dict(
            case=case,
            image_capture=image_capture,
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
        )
        payload = run_immutable_browser(mode="payload", selection=phase, **common)
        reports.append(payload)
        selected[phase] = {
            "artifactId": str(payload.guid),
            "artifactDigest": payload.summary["artifactDigest"],
        }
        execution = run_immutable_browser(
            mode="execution", selection=selected, **common
        )
        reports.append(execution)
        return result

    monkeypatch.setattr(leaf, "qualify_immutable_artifacts", qualify)
    pipeline.test_compiled_cli_tree_prepare_lost_reply_cold_review_and_private_retry(
        world,
        binary,
        tmp_path,
        asgi_endpoint,
        dropped_cli_replies,
        monkeypatch,
        "immutable",
    )
    assert len(reports) == 2
