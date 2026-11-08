#!/usr/bin/env python3
"""Render private management-engine APIs from pinned manifests and immutable images."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
import re
from pathlib import Path

import yaml

REVISION = "92c39b8ad563ebe5ef705f781b3038b901aba10f"
SOURCES = {
    "management-engine/config/crd/bases/internal.nvke.nvidia.com_clusterreleasechannels.yaml": "2f114f377032c4bbb7a54c5a9ce7112b848f06c84494484d0b0ce0e385a9b7d8",
    "management-engine/config/crd/bases/internal.nvke.nvidia.com_controlplaneclasses.yaml": "090ae26a5f73b0b31cff3e10bcb33d0eed036e2b72f2cc674f807d5462517910",
    "management-engine/config/crd/bases/internal.nvke.nvidia.com_nvkeclusters.yaml": "b4da5d51013a2625cbee727346b5c1651613c87d9570f4eb496a55ab7f4c13bd",
    "management-engine/config/dev/operator-api.yaml": "00d530f7f473ba306eed5b15aa83ac529de2efa9e3f02779474c78819229a1f6",
    "management-engine/config/dev/operator-api-rbac.yaml": "aedc6682516a8ec8945a94f44415ee88365cb8c5ab67e22d6af5a351694de8a6",
    "management-engine/config/dev/tenant-api.yaml": "c2855a63ef924c768331a2ab47ecd7f829ba9c40d66797751bfb686d0e94316d",
    "management-engine/config/dev/tenant-api-rbac.yaml": "c4a2fbb9b15087a9a309817f9bc2fcc418b212649ae5b22e1c59e92bdae816d7",
}
PART_OF = "astro-nvke-management-apis"


class ProfileError(ValueError):
    pass


def fields(value, required, optional=()):
    if (
        not isinstance(value, dict)
        or not set(required) <= value.keys()
        or value.keys() - set(required) - set(optional)
    ):
        raise ProfileError("Unexpected or missing management API profile fields")
    return value


def label_name(value, maximum=63):
    if (
        not isinstance(value, str)
        or len(value) > maximum
        or not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?", value)
    ):
        raise ProfileError("Use a valid bounded Kubernetes name")
    return value


def image(value):
    if not isinstance(value, str) or not re.fullmatch(
        r"[a-zA-Z0-9][a-zA-Z0-9./:_\[\]-]{0,450}@sha256:[0-9a-f]{64}", value
    ):
        raise ProfileError(
            "Use an explicit immutable image reference with a SHA-256 digest"
        )
    return value


def selector(value):
    if not isinstance(value, dict) or not 1 <= len(value) <= 16:
        raise ProfileError("Bind each API client to explicit pod labels")
    for key, item in value.items():
        if not isinstance(key, str) or len(key) > 317 or key.count("/") > 1:
            raise ProfileError("Use valid Kubernetes pod label keys")
        prefix, slash, name = key.rpartition("/")
        if slash:
            if len(prefix) > 253:
                raise ProfileError("Use valid Kubernetes pod label prefixes")
            for part in prefix.split("."):
                label_name(part)
        else:
            name = key
        if len(name) > 63 or not re.fullmatch(
            r"[A-Za-z0-9](?:[-A-Za-z0-9_.]*[A-Za-z0-9])?", name
        ):
            raise ProfileError("Use valid Kubernetes pod label keys")
        if (
            not isinstance(item, str)
            or len(item) > 63
            or (
                item
                and not re.fullmatch(
                    r"[A-Za-z0-9](?:[-A-Za-z0-9_.]*[A-Za-z0-9])?", item
                )
            )
        ):
            raise ProfileError("Use valid Kubernetes pod label values")
    return copy.deepcopy(value)


def validate(profile):
    config = fields(
        profile,
        {"name", "namespace", "tenantNamespace", "images"},
        {"clients", "imagePullPolicy"},
    )
    name = label_name(config["name"], 32)
    namespace = label_name(config["namespace"])
    tenant = label_name(config["tenantNamespace"])
    images = fields(config["images"], {"operator", "tenant"})
    images = {role: image(value) for role, value in images.items()}
    pull = config.get("imagePullPolicy", "IfNotPresent")
    if not isinstance(pull, str) or pull not in {"Always", "IfNotPresent", "Never"}:
        raise ProfileError("Choose a Kubernetes image pull policy")
    clients = config.get("clients", [])
    if not isinstance(clients, list) or len(clients) > 32:
        raise ProfileError("Use a bounded list of explicitly scoped API clients")
    peers = []
    for entry in clients:
        client = fields(entry, {"namespace", "podLabels"})
        peers.append(
            {
                "namespaceSelector": {
                    "matchLabels": {
                        "kubernetes.io/metadata.name": label_name(client["namespace"])
                    }
                },
                "podSelector": {"matchLabels": selector(client["podLabels"])},
            }
        )
    return name, namespace, tenant, images, pull, peers


def source_documents(source):
    documents = {}
    for relative, expected in SOURCES.items():
        path = source / relative
        if (
            path.is_symlink()
            or not path.is_file()
            or path.stat().st_size > 4 * 1024 * 1024
        ):
            raise ProfileError("Management API source is unavailable or changed")
        content = path.read_bytes()
        if hashlib.sha256(content).hexdigest() != expected:
            raise ProfileError("Management API source differs from the supported pin")
        documents[relative] = [
            row for row in yaml.safe_load_all(content) if row is not None
        ]
    return documents


def render(source: Path, profile: object) -> dict:
    name, namespace, tenant, images, pull, peers = validate(profile)
    docs = source_documents(source)
    items = [
        {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": value}}
        for value in sorted({namespace, tenant})
    ]
    for plural in ("clusterreleasechannels", "controlplaneclasses", "nvkeclusters"):
        items.extend(
            docs[
                f"management-engine/config/crd/bases/internal.nvke.nvidia.com_{plural}.yaml"
            ]
        )
    for role in ("operator", "tenant"):
        component = f"{name}-{role}-api"
        base = f"management-engine/config/dev/{role}-api"
        for original in docs[base + "-rbac.yaml"]:
            row = copy.deepcopy(original)
            row["metadata"]["name"] = component
            if row["kind"] in {"Role", "RoleBinding"}:
                row["metadata"]["namespace"] = tenant
            elif row["kind"] == "ServiceAccount":
                row["metadata"]["namespace"] = namespace
            if "roleRef" in row:
                row["roleRef"]["name"] = component
                for subject in row["subjects"]:
                    subject.update(name=component, namespace=namespace)
            items.append(row)
        deployment = copy.deepcopy(docs[base + ".yaml"][0])
        deployment["metadata"].update(name=component, namespace=namespace)
        labels = {"app.kubernetes.io/name": component}
        deployment["spec"]["selector"]["matchLabels"] = labels
        template = deployment["spec"]["template"]
        template["metadata"]["labels"] = {
            **labels,
            "app.kubernetes.io/instance": name,
            "app.kubernetes.io/part-of": PART_OF,
        }
        template["spec"]["serviceAccountName"] = component
        container = template["spec"]["containers"][0]
        container.update(image=images[role], imagePullPolicy=pull)
        if role == "tenant":
            container["args"] = ["--namespace=" + tenant]
        items.append(deployment)
        items.append(
            {
                "apiVersion": "v1",
                "kind": "Service",
                "metadata": {"name": component, "namespace": namespace},
                "spec": {
                    "type": "ClusterIP",
                    "selector": labels,
                    "ports": [
                        {
                            "name": "http",
                            "port": 8080,
                            "targetPort": "http",
                            "protocol": "TCP",
                        }
                    ],
                },
            }
        )
    items.append(
        {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {"name": name + "-api-private", "namespace": namespace},
            "spec": {
                "podSelector": {
                    "matchLabels": {
                        "app.kubernetes.io/instance": name,
                        "app.kubernetes.io/part-of": PART_OF,
                    }
                },
                "policyTypes": ["Ingress"],
                "ingress": [
                    {"from": peers, "ports": [{"protocol": "TCP", "port": 8080}]}
                ]
                if peers
                else [],
            },
        }
    )
    return {"apiVersion": "v1", "kind": "List", "items": items}


def object_pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ProfileError("Duplicate management API profile fields")
        result[key] = value
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.profile.stat().st_size > 65536:
            raise ProfileError("Management API profile exceeds its bound")
        profile = json.loads(args.profile.read_text(), object_pairs_hook=object_pairs)
        result = render(args.source, profile)
        content = (
            json.dumps(result, sort_keys=True, indent=2, allow_nan=False) + "\n"
        ).encode()
        descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as out:
            out.write(content)
            out.flush()
            os.fsync(out.fileno())
        print(
            json.dumps(
                {
                    "manifestSourceRevision": REVISION,
                    "sha256": hashlib.sha256(content).hexdigest(),
                }
            )
        )
        return 0
    except Exception:
        print(
            json.dumps(
                {
                    "error": "Management API rendering failed; inspect private profile and pinned source"
                }
            )
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
