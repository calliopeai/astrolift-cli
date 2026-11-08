#!/usr/bin/env python3
"""Render private, source-pinned NKTP backend overrides without reading credentials."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit

REVISION = "92c39b8ad563ebe5ef705f781b3038b901aba10f"
SOURCES = {
    "nktp/deploy/charts/nktp/values.yaml": "d4c2d21bfe6b787e9f7a78edab388e997e32fd9f88135df103b64fa6c47ee8a6",
    "deploy/nico/nktp/values.yaml": "33c6ad2b2650cda75677faadced0a2f5df2d7e7ff9228f0700a924b3df869195",
    "nktp/deploy/charts/nktp/templates/_helpers.tpl": "a64a20d7f24bcffb29288ada372e4cba65cfa616f4284b0c4b9fa87065d8b7e5",
}
MOUNT = "/etc/nktp/astro-backend-tls"


class ProfileError(ValueError):
    pass


def fields(value: object, required: set[str], optional: set[str]) -> dict:
    if (
        not isinstance(value, dict)
        or not required <= value.keys()
        or value.keys() - required - optional
    ):
        raise ProfileError("Unexpected or missing backend profile fields")
    return value


def name(value: object, *, key: bool = False) -> str:
    pattern = (
        r"[A-Za-z0-9._-]{1,253}" if key else r"[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?"
    )
    if not isinstance(value, str) or not re.fullmatch(pattern, value):
        raise ProfileError("Use a valid Kubernetes Secret name and data key")
    if not key and any(
        not part or len(part) > 63 or part.startswith("-") or part.endswith("-")
        for part in value.split(".")
    ):
        raise ProfileError("Use a valid Kubernetes Secret name and data key")
    return value


def endpoint(value: object, protocol: str) -> str:
    if (
        not isinstance(value, str)
        or not value
        or len(value) > 2048
        or any(ord(c) < 33 or ord(c) > 126 for c in value)
        or any(c in value for c in '$\\"')
    ):
        raise ProfileError("Use an explicit TLS backend endpoint without interpolation")
    parsed = urlsplit(value if protocol == "http" else "//" + value)
    try:
        port = parsed.port
    except ValueError:
        raise ProfileError("Use a valid backend port") from None
    if (
        not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        raise ProfileError(
            "Backend endpoints cannot contain credentials, queries or fragments"
        )
    if protocol == "http":
        if parsed.scheme != "https":
            raise ProfileError("OTLP/HTTP requires HTTPS")
    elif parsed.scheme or parsed.path or port is None:
        raise ProfileError("OTLP/gRPC requires host:port with TLS")
    if port == 0:
        raise ProfileError("Use a valid backend port")
    return value


def render(source: Path, profile: object) -> dict:
    for relative, expected in SOURCES.items():
        if hashlib.sha256((source / relative).read_bytes()).hexdigest() != expected:
            raise ProfileError("NKTP source differs from the supported pin")
    config = fields(profile, {"protocol", "endpoint"}, {"authorization", "tls"})
    protocol = config["protocol"]
    if not isinstance(protocol, str) or protocol not in ("grpc", "http"):
        raise ProfileError("Choose grpc or http")
    target = endpoint(config["endpoint"], protocol)
    auth = config.get("authorization")
    if auth is not None:
        auth = fields(auth, {"secret", "key"}, set())
        name(auth["secret"])
        name(auth["key"], key=True)
    tls = config.get("tls")
    tls_settings = {"insecure": False, "insecure_skip_verify": False}
    backend_volume = None
    if tls is not None:
        tls = fields(tls, {"secret", "caKey"}, {"certKey", "keyKey"})
        name(tls["secret"])
        if ("certKey" in tls) != ("keyKey" in tls):
            raise ProfileError("Client certificate and key must be configured together")
        items = []
        for field, path, option in (
            ("caKey", "ca.crt", "ca_file"),
            ("certKey", "tls.crt", "cert_file"),
            ("keyKey", "tls.key", "key_file"),
        ):
            if field in tls:
                items.append({"key": name(tls[field], key=True), "path": path})
                tls_settings[option] = MOUNT + "/" + path
        tls_settings["reload_interval"] = "1m"
        backend_volume = {
            "name": "astro-backend-tls",
            "secret": {
                "secretName": tls["secret"],
                "defaultMode": 0o440,
                "items": items,
            },
        }
    result = {"otel-lgtm": {"enabled": False}}
    cluster_env = {
        "name": "K8S_CLUSTER_NAME",
        "valueFrom": {"configMapKeyRef": {"name": "nktp-cluster", "key": "name"}},
    }
    pod_env = {
        "name": "POD_NAME",
        "valueFrom": {"fieldRef": {"fieldPath": "metadata.name"}},
    }
    for component in ("gateway_logs", "gateway_metrics", "gateway_traces", "watchdog"):
        signal = component.removeprefix("gateway_")
        signals = (
            ("logs", "metrics", "traces") if component == "watchdog" else (signal,)
        )
        for sidecar in (False, True) if component == "watchdog" else (False,):
            component_values = result.setdefault(component, {})
            values = (
                component_values.setdefault("sidecar", {})
                if sidecar
                else component_values
            )
            exporter = {"endpoint": target, "tls": dict(tls_settings)}
            if protocol == "http":
                exporter["retry_on_failure"] = {"max_elapsed_time": "30m"}
                exporter["sending_queue"] = {
                    "block_on_overflow": not sidecar,
                    "sizer": "bytes",
                    "queue_size": 16777216
                    if sidecar
                    else 8388608
                    if component == "watchdog"
                    else 128849018,
                    "num_consumers": 2 if sidecar else 32,
                    "batch": {
                        "sizer": "bytes",
                        "min_size": 65536 if sidecar else 1048576,
                        "max_size": 262144 if sidecar else 2097152,
                        "flush_timeout": "1s",
                    },
                }
            exporter_name = "otlp" if protocol == "grpc" else "otlp_http"
            if auth:
                exporter["headers"] = {
                    "Authorization": "${env:ASTRO_NKTP_AUTHORIZATION}"
                }
            exporters = {exporter_name: exporter}
            if protocol == "http":
                exporters["otlp"] = None
            values["otelcol"] = {"config": {"exporters": exporters}}
            if protocol == "http":
                values["otelcol"]["config"]["service"] = {
                    "pipelines": {s: {"exporters": [exporter_name]} for s in signals}
                }
            memory = (
                "192MiB"
                if sidecar
                else "448MiB"
                if component == "watchdog"
                else "384MiB"
            )
            env = (
                [pod_env, {"name": "GOMEMLIMIT", "value": memory}, cluster_env]
                if sidecar
                else [
                    {"name": "GOMEMLIMIT", "value": memory},
                    {"name": "GOGC", "value": "50"},
                    cluster_env,
                ]
            )
            if auth:
                env.append(
                    {
                        "name": "ASTRO_NKTP_AUTHORIZATION",
                        "valueFrom": {
                            "secretKeyRef": {"name": auth["secret"], "key": auth["key"]}
                        },
                    }
                )
            kubernetes = values.setdefault("kubernetes", {})
            kubernetes["env"] = env
            if backend_volume:
                internal = []
                if not sidecar:
                    for kind in (
                        ("server",) if component == "watchdog" else ("server", "client")
                    ):
                        internal.append(
                            {
                                "name": f"internal-tls-{kind}",
                                "secret": {
                                    "secretName": f"nktp-{component.replace('_', '-')}-{kind}-tls",
                                    "items": [
                                        {"key": key, "path": key}
                                        for key in ("tls.crt", "tls.key", "ca.crt")
                                    ],
                                },
                            }
                        )
                    kubernetes["volumes"] = internal + [backend_volume]
                    kubernetes["podSecurityContext"] = {"fsGroup": 10001}
                kubernetes["volumeMounts"] = (
                    []
                    if sidecar
                    else [
                        {
                            "name": "internal-tls-server",
                            "mountPath": "/etc/nktp/internal-tls/server",
                            "readOnly": True,
                        }
                    ]
                ) + [
                    {"name": "astro-backend-tls", "mountPath": MOUNT, "readOnly": True}
                ]
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--profile", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        values = render(args.source, json.loads(args.profile.read_text()))
        payload = (json.dumps(values, sort_keys=True, indent=2) + "\n").encode()
        descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as output:
            output.write(payload)
            output.flush()
            os.fsync(output.fileno())
        print(
            json.dumps(
                {"revision": REVISION, "sha256": hashlib.sha256(payload).hexdigest()}
            )
        )
        return 0
    except (OSError, ValueError, TypeError):
        print(
            "NKTP profile rendering failed; check source pin, profile and new output path",
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
