"""Opt-in CLI/API/PostgreSQL/Kind receipts; no HTTP, auth or provider substitutes.

Run using the app repository's Django pytest settings, with its backend on
PYTHONPATH and ASTROLIFT_CLI_TEST_BINARY pointing to this checkout's Go binary.
"""

import json
import os
import select
import socket
import subprocess
import threading
import time
from types import SimpleNamespace

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip(
        "requires the opt-in compiled CLI and app test environment",
        allow_module_level=True,
    )

from core.tests import test_exec_environment_contracts_2209 as shared

pytestmark = [
    pytest.mark.django_db(transaction=True),
    pytest.mark.timeout(60, method="thread"),
]


@pytest.fixture
def world():
    return shared.world.__wrapped__()


@pytest.fixture
def kind_targets(world):
    yield from shared.kind_targets.__wrapped__(world)


@pytest.fixture
def exec_targets(world, kind_targets):
    yield from shared.exec_targets.__wrapped__(world, kind_targets)


@pytest.fixture
def api_server(exec_targets):
    import uvicorn
    from config.asgi import application

    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    sock.listen(128)
    server = uvicorn.Server(
        uvicorn.Config(application, lifespan="off", log_level="critical")
    )
    thread = threading.Thread(
        target=server.run, kwargs={"sockets": [sock]}, daemon=True
    )
    thread.start()
    deadline = time.monotonic() + 5
    while not server.started:
        assert thread.is_alive() and time.monotonic() < deadline
        time.sleep(0.01)
    yield SimpleNamespace(
        url=f"http://127.0.0.1:{sock.getsockname()[1]}", server=server
    )
    server.should_exit = True
    thread.join(5)
    assert not thread.is_alive()
    sock.close()


def invoke(world, server, tmp_path, args, *, stdin=None, scopes=None):
    _, headers = shared.credentials(world, scopes=scopes)
    env = {
        **os.environ,
        "XDG_CONFIG_HOME": str(tmp_path),
        "ASTROLIFT_TOKEN": headers["Authorization"][7:],
    }
    return subprocess.run(
        [
            os.environ["ASTROLIFT_CLI_TEST_BINARY"],
            "--api-url",
            server.url,
            "--org",
            str(world.org.guid),
            "--app",
            world.medops_app.slug,
            "--no-prompt",
            *args,
        ],
        input=stdin,
        text=True,
        capture_output=True,
        env=env,
        timeout=25,
    )


@pytest.mark.parametrize("selection", ["selected", "remote"])
def test_cli_reviews_and_patches_only_the_selected_environment(
    world, exec_targets, api_server, tmp_path, selection
):
    environment = getattr(world, selection)
    reviewed = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "workload",
            "review",
            world.workload.slug,
            "--environment",
            str(environment.guid),
        ],
    )
    assert reviewed.returncode == 0, reviewed.stderr
    review = json.loads(reviewed.stdout)
    assert review["target"]["environmentId"] == str(environment.guid)
    assert review["actorId"] == world.user.pk
    path = tmp_path / "review.json"
    path.write_text(reviewed.stdout)
    bindings = {env.pk: api for env, api in exec_targets.bindings}
    before = {
        env.pk: bindings[env.pk]
        .apps.read_namespaced_deployment("web", env.k8s_namespace)
        .spec.replicas
        for env in (world.primary, world.selected, world.remote)
    }
    scaled = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "workload",
            "scale",
            world.workload.slug,
            "1",
            "--environment",
            str(environment.guid),
            "--review",
            str(path),
            "--yes",
        ],
    )
    assert scaled.returncode == 0, scaled.stderr
    receipt = json.loads(scaled.stdout)
    assert receipt["accepted"] is True and receipt["completed"] is None
    assert receipt["operationId"] and receipt["target"]["environmentId"] == str(
        environment.guid
    )
    for env in (world.primary, world.selected, world.remote):
        count = (
            bindings[env.pk]
            .apps.read_namespaced_deployment("web", env.k8s_namespace)
            .spec.replicas
        )
        assert count == (1 if env.pk == environment.pk else before[env.pk])
    stale = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "workload",
            "restart",
            world.workload.slug,
            "--environment",
            str(environment.guid),
            "--review",
            str(path),
            "--yes",
        ],
    )
    assert stale.returncode != 0 and "VERSION_MISMATCH" in stale.stderr
    refreshed = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "workload",
            "review",
            world.workload.slug,
            "--environment",
            str(environment.guid),
        ],
    )
    assert refreshed.returncode == 0, refreshed.stderr
    path.write_text(refreshed.stdout)
    restarted = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "workload",
            "restart",
            world.workload.slug,
            "--environment",
            str(environment.guid),
            "--review",
            str(path),
            "--yes",
        ],
    )
    assert restarted.returncode == 0, restarted.stderr
    for env in (world.primary, world.selected, world.remote):
        annotations = (
            bindings[env.pk]
            .apps.read_namespaced_deployment("web", env.k8s_namespace)
            .spec.template.metadata.annotations
            or {}
        )
        assert ("kubectl.kubernetes.io/restartedAt" in annotations) == (
            env.pk == environment.pk
        )


@pytest.mark.parametrize("selection", ["selected", "remote"])
def test_cli_exec_reads_exact_target_and_streams_after_admission(
    world, exec_targets, api_server, tmp_path, selection
):
    environment = getattr(world, selection)
    result = invoke(
        world,
        api_server,
        tmp_path,
        [
            "exec",
            "--environment",
            str(environment.guid),
            "--pod",
            "reviewed-pod",
            "--container",
            "main",
            "--no-tty",
            "--",
            "sh",
            "-c",
            'echo "$PROOF_ENV"; cat',
        ],
        stdin="cli-input-once\n",
    )
    assert result.returncode == 0, result.stderr
    assert (
        environment.name in result.stdout and result.stdout.count("cli-input-once") == 1
    )
    assert (
        environment.k8s_namespace in result.stderr
        and str(environment.tenant_cluster.guid) in result.stderr
    )


def test_cli_read_only_and_missing_pod_refuse_without_opening(
    world, exec_targets, api_server, tmp_path
):
    args = [
        "exec",
        "--environment",
        str(world.selected.guid),
        "--pod",
        "reviewed-pod",
        "--no-tty",
        "--",
        "true",
    ]
    denied = invoke(world, api_server, tmp_path, args, scopes=["read:apps"])
    assert denied.returncode != 0 and "Exec target:" not in denied.stderr
    absent = invoke(
        world,
        api_server,
        tmp_path,
        [
            "exec",
            "--environment",
            str(world.selected.guid),
            "--pod",
            "gone-pod",
            "--",
            "true",
        ],
    )
    assert absent.returncode != 0 and "no primary fallback" in absent.stderr


def test_cli_review_cannot_be_reused_by_another_actor(
    world, exec_targets, api_server, tmp_path
):
    result = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "workload",
            "review",
            world.workload.slug,
            "--environment",
            str(world.selected.guid),
        ],
    )
    assert result.returncode == 0, result.stderr
    review = json.loads(result.stdout)
    review["actorId"] += 1
    path = tmp_path / "review.json"
    path.write_text(json.dumps(review))
    denied = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "workload",
            "scale",
            world.workload.slug,
            "1",
            "--environment",
            str(world.selected.guid),
            "--review",
            str(path),
            "--yes",
        ],
    )
    assert denied.returncode != 0 and "another actor" in denied.stderr
    for environment, api in exec_targets.bindings:
        assert (
            api.apps.read_namespaced_deployment(
                "web", environment.k8s_namespace
            ).spec.replicas
            == 0
        )


def test_cli_disconnect_reports_unknown_outcome_without_reconnecting(
    world, exec_targets, api_server, tmp_path
):
    from uvicorn.protocols.websockets.websockets_impl import WebSocketProtocol

    def active_sockets():
        return [
            connection
            for connection in api_server.server.server_state.connections
            if isinstance(connection, WebSocketProtocol)
        ]

    _, headers = shared.credentials(world)
    env = {
        **os.environ,
        "XDG_CONFIG_HOME": str(tmp_path),
        "ASTROLIFT_TOKEN": headers["Authorization"][7:],
    }
    process = subprocess.Popen(
        [
            os.environ["ASTROLIFT_CLI_TEST_BINARY"],
            "--api-url",
            api_server.url,
            "--org",
            str(world.org.guid),
            "--app",
            world.medops_app.slug,
            "--no-prompt",
            "exec",
            "--environment",
            str(world.selected.guid),
            "--pod",
            "reviewed-pod",
            "--container",
            "main",
            "--no-tty",
            "--",
            "cat",
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
    )
    try:
        process.stdin.write(b"applied-once\n")
        process.stdin.flush()
        ready, _, _ = select.select([process.stdout], [], [], 10)
        assert ready, "actual admitted shell did not produce input output"
        assert process.stdout.readline() == b"applied-once\n"
        connections = active_sockets()
        assert len(connections) == 1
        # Close the real accepted TCP/WebSocket transport as a server restart
        # would; no application or authentication handler is substituted.
        for connection in connections:
            connection.loop.call_soon_threadsafe(connection.shutdown)
        assert process.wait(timeout=5) != 0
        _, errors = process.communicate(timeout=5)
        assert (
            b"outcome may be unknown" in errors
            and b"no reconnect or input replay" in errors
        )
        deadline = time.monotonic() + 3
        while active_sockets() and time.monotonic() < deadline:
            time.sleep(0.01)
        assert not active_sockets()
    finally:
        if process.poll() is None:
            process.kill()
        process.communicate(timeout=5)


def test_cli_complete_explicit_exec_target_needs_no_inventory_grant(
    world, exec_targets, api_server, tmp_path
):
    from core.permissions import Permission
    from core.tests.utils.scope_world import bind_role

    world.exec_binding.delete()
    bind_role(
        world.user,
        permissions=[Permission.APP_EXEC_POD],
        kind="APP",
        scope_id=world.medops_app.pk,
        slug="explicit-exec-without-inventory",
    )
    result = invoke(
        world,
        api_server,
        tmp_path,
        [
            "app",
            "exec",
            world.workload.slug,
            "--environment",
            str(world.selected.guid),
            "--pod",
            "reviewed-pod",
            "--container",
            "main",
            "--no-tty",
            "--",
            "sh",
            "-c",
            'echo "$PROOF_ENV"',
        ],
        stdin="",
        scopes=["write:apps"],
    )
    assert result.returncode == 0, result.stderr
    assert (
        world.selected.name in result.stdout
        and world.selected.k8s_namespace in result.stderr
    )
