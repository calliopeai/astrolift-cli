"""Compiled CLI version-check against real public install discovery (#178).

The CLI reads `astroliftServerInfo` from `/app/gql/config/public/` without a
credential. A current install answers through the app's real ASGI routing,
schema and resolver. An install that predates that route is the same app with
only that path removed from its URL configuration, so Django serves its own
404. No response, resolver or other route is replaced.
"""

import json
import os
import re
import subprocess
import types
from pathlib import Path

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip(
        "requires the opt-in compiled CLI and app backend", allow_module_level=True
    )

from django.conf import settings
from django.test import override_settings
from django.urls import URLResolver, include, path
from graphql import parse, validate

from test_pipeline_reviewed import running_api

pytestmark = pytest.mark.django_db(transaction=True)


def version_check(port, tmp_path):
    return subprocess.run(
        [
            os.environ["ASTROLIFT_CLI_TEST_BINARY"],
            "--api-url",
            f"http://127.0.0.1:{port}",
            "version-check",
            "--json",
        ],
        env={
            **os.environ,
            "XDG_CONFIG_HOME": str(tmp_path),
            "ASTROLIFT_NO_UPDATE_CHECK": "1",
            "ASTROLIFT_TOKEN": "",
            "ASTROLIFT_DEPLOY_TOKEN": "",
        },
        capture_output=True,
        text=True,
        timeout=30,
    )


def install_without_public_discovery():
    """The live URL configuration minus the public discovery route only."""
    import config.urls as live

    app_urls = [p for p in live.urls if str(p.pattern) != "gql/config/public/"]
    assert len(app_urls) == len(live.urls) - 1
    urlconf = types.ModuleType("install_without_public_discovery")
    urlconf.urlpatterns = [
        path(live.base, include(app_urls))
        if isinstance(p, URLResolver) and p.urlconf_name is live.urls
        else p
        for p in live.urlpatterns
    ]
    return urlconf


def test_version_check_document_validates_against_public_discovery_schema():
    from config.schema_public import schema_public

    source = (Path(__file__).resolve().parents[1] / "cmd/version_check.go").read_text()
    document = re.search(r"const versionCheckQuery = `([^`]+)`", source).group(1)
    assert validate(schema_public._schema, parse(document)) == []


def test_compiled_cli_reads_current_install_without_credentials(tmp_path):
    with running_api() as port:
        result = version_check(port, tmp_path)
    assert result.returncode == 0, result.stderr
    assert result.stderr == ""
    report = json.loads(result.stdout)
    assert report["compatible"] is True
    assert report["requiredCapability"] == "server_info.handshake"
    assert report["serverVersion"] == settings.VERSION
    assert report["apiVersion"].startswith(settings.VERSION + "+")


def test_compiled_cli_reports_install_without_public_discovery_unsupported(tmp_path):
    with (
        override_settings(ROOT_URLCONF=install_without_public_discovery()),
        running_api() as port,
    ):
        result = version_check(port, tmp_path)
    assert result.returncode == 1, result.stderr
    assert result.stderr == ""
    report = json.loads(result.stdout)
    assert report["compatible"] is False
    assert report["requiredCapability"] == "server_info.handshake"
    assert "HTTP 404" in report["reason"]
    assert "<" not in result.stdout
