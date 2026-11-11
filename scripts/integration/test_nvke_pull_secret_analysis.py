"""Actual CLI/App/PostgreSQL/SDK/mTLS/native-helper Secret-only analysis."""

import test_nvke_install_plan_tree as pipeline
import pytest

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


def test_compiled_cli_secret_analysis_recovery(
    world,
    binary,
    tmp_path,
    asgi_endpoint,
    dropped_cli_replies,
    monkeypatch,
):
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
