"""Exact environment review beyond the legacy inventory cap; real ASGI/PG/RBAC."""

import json
import os
import re
from pathlib import Path

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip("requires compiled CLI and real app backend", allow_module_level=True)

from astrolift_identity.api_tokens import mint_token
from astrolift_identity.models import ApiToken
from astrolift_registry.models import Workload
from config.schema import schema
from core.tests import test_workload_environment_contracts_2217 as shared
from django.db import IntegrityError, transaction
from graphql import parse, validate

from test_pipeline_reviewed import forwarding_proxy, running_api

pytestmark = pytest.mark.django_db(transaction=True)


def test_environment_documents_validate_against_actual_schema():
    root = Path(__file__).resolve().parents[1]
    source = (root / "cmd/environment_actions.go").read_text()
    for name in [
        "environmentWorkloadsQuery",
        "environmentActionReviewQuery",
        "restartReviewedWorkloadMutation",
        "scaleReviewedWorkloadMutation",
    ]:
        document = re.search(rf"const {name} = `([^`]+)`", source).group(1)
        assert validate(schema._schema, parse(document)) == [], name


def test_compiled_cli_exact_review_reaches_workload_older_than_200_newer_siblings(
    tmp_path,
):
    import subprocess

    world = shared.world.__wrapped__()
    target = world.workload
    Workload.objects.bulk_create(
        [
            Workload(
                registered_app=target.registered_app,
                slug=f"newer-{index}",
                name=f"Newer {index}",
                kind="deployment",
            )
            for index in range(201)
        ]
    )
    assert (
        Workload.objects.filter(
            registered_app=target.registered_app, created_at__gt=target.created_at
        ).count()
        == 201
    )
    # Exact app+slug is a database-enforced active identity, independent of inventory size.
    with pytest.raises(IntegrityError), transaction.atomic():
        Workload.objects.create(
            registered_app=target.registered_app,
            slug=target.slug,
            name="Ambiguous active slug",
            kind="deployment",
        )
    issued = mint_token()
    ApiToken.objects.create(
        user=world.user,
        organization=world.org,
        name="Exact lookup proof",
        token_hash=issued.token_hash,
        scopes=["read:apps", "write:apps"],
    )
    with running_api() as port, forwarding_proxy(port) as proxy:
        result = subprocess.run(
            [
                os.environ["ASTROLIFT_CLI_TEST_BINARY"],
                "--api-url",
                proxy.url,
                "--org",
                str(world.org.guid),
                "--app",
                target.registered_app.slug,
                "--no-prompt",
                "app",
                "workload",
                "review",
                target.slug,
                "--environment",
                str(world.selected.guid),
            ],
            env={
                **os.environ,
                "XDG_CONFIG_HOME": str(tmp_path),
                "ASTROLIFT_TOKEN": issued.plaintext,
            },
            capture_output=True,
            text=True,
            timeout=25,
        )
        assert result.returncode == 0, result.stderr
        review = json.loads(result.stdout)
        assert review["actorId"] == world.user.pk
        assert review["organizationId"] == str(world.org.guid)
        assert review["target"]["appId"] == str(target.registered_app.guid)
        assert review["target"]["workloadId"] == str(target.guid)
        assert review["target"]["environmentId"] == str(world.selected.guid)
        assert review["target"]["namespace"] == world.selected.k8s_namespace
        selectors = [
            q
            for q, _ in proxy.requests
            if "query EnvironmentActionWorkload" in q["query"]
        ]
        assert len(selectors) == 1
        assert selectors[0]["variables"] == {
            "app": target.registered_app.slug,
            "workload": target.slug,
        }
        assert not any(
            "astroliftWorkloads(" in q["query"]
            or "astroliftWorkloadsPage(" in q["query"]
            or "mutation" in q["query"]
            for q, _ in proxy.requests
        )
        assert all(
            org == str(world.org.guid)
            for q, org in proxy.requests
            if "EnvironmentActionWorkload" in q["query"]
            or "astroliftWorkloadActionTarget" in q["query"]
        )
