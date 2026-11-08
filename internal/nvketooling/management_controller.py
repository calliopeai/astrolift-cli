#!/usr/bin/env python3
"""Render a private, tenant-scoped native management-engine controller."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
from urllib.parse import urlsplit

import yaml

from management_api import (
    ProfileError,
    REVISION,
    fields,
    image,
    label_name,
    object_pairs,
)

SOURCES = {
    "management-engine/config/dev/manager.yaml": "47f8e8f622caa7d40a211ed7be4aac4fc037eefa73a8f9eff1f5eb654c710633",
    "management-engine/config/rbac/role.yaml": "a874617ae88b559671c2f34fc036ddd49a918ea759ed5972fe45a6ec438b9f44",
    "management-engine/config/rbac/role_binding.yaml": "5994dbcb95cd1a8d3c75d4bcf30b8ba31c00f622be6edd838322d0a2c672efe7",
    "management-engine/config/rbac/leader_election_role.yaml": "2c4ff36277be3b68b4c8148117eb3c52e00bae99f10578cd52f4e26def7b1719",
    "management-engine/config/rbac/leader_election_role_binding.yaml": "10db133a7e52bb96528b7745a3d5b95abc2576c21614fd1cecf59ce41a1fa65e",
    "management-engine/config/rbac/service_account.yaml": "e9e049f6467ad92ed945ada9ede61a6dd5a29635585277675cef6066292093e9",
}


def validate(profile):
    config = fields(
        profile,
        {
            "name",
            "namespace",
            "tenantNamespace",
            "image",
            "infraTemplateConfigMap",
            "controllerEndpoint",
            "controllerAuth",
        },
        {"imagePullPolicy"},
    )
    config = copy.deepcopy(config)
    for key in ("name", "namespace", "tenantNamespace", "infraTemplateConfigMap"):
        label_name(config[key], 32 if key == "name" else 63)
    image(config["image"])
    pull = config.setdefault("imagePullPolicy", "IfNotPresent")
    if not isinstance(pull, str) or pull not in {"Always", "IfNotPresent", "Never"}:
        raise ProfileError("Choose a Kubernetes image pull policy")
    endpoint = config["controllerEndpoint"]
    if not isinstance(endpoint, str) or len(endpoint) > 512:
        raise ProfileError("Bind the controller to an explicit internal service API")
    parsed = urlsplit(endpoint)
    host = parsed.hostname or ""
    parts = host.split(".")
    if (
        parsed.scheme not in {"http", "https"}
        or parsed.username is not None
        or parsed.password is not None
        or parsed.path != "/api"
        or parsed.query
        or parsed.fragment
        or len(parts) != 5
        or parts[2:] != ["svc", "cluster", "local"]
        or any(c.isspace() for c in endpoint)
        or parsed.port == 0
    ):
        raise ProfileError("Bind the controller to an explicit internal service API")
    label_name(parts[0])
    label_name(parts[1])
    auth = config["controllerAuth"]
    fields(auth, {"mode"}, {"audience"})
    if auth["mode"] == "projected":
        fields(auth, {"mode", "audience"})
        audience = auth["audience"]
        if not isinstance(audience, str) or not 1 <= len(audience) <= 512:
            raise ProfileError("Use an explicit HTTPS token audience")
        target = urlsplit(audience)
        if (
            target.scheme != "https"
            or not target.hostname
            or target.username is not None
            or target.password is not None
            or target.query
            or target.fragment
            or any(c.isspace() for c in audience)
        ):
            raise ProfileError("Use an explicit HTTPS token audience")
    elif auth["mode"] == "private-network":
        fields(auth, {"mode"})
    else:
        raise ProfileError(
            "Choose projected authentication or explicit private-network authority"
        )
    return config


def render(source: Path, profile: object) -> dict:
    config = validate(profile)
    docs = {}
    for relative, expected in SOURCES.items():
        path = source / relative
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 65536:
            raise ProfileError("Management controller source is unavailable or changed")
        content = path.read_bytes()
        if hashlib.sha256(content).hexdigest() != expected:
            raise ProfileError(
                "Management controller source differs from the supported pin"
            )
        docs[relative.rsplit("/", 1)[-1]] = yaml.safe_load(content)
    name = config["name"] + "-manager"
    namespace, tenant = config["namespace"], config["tenantNamespace"]
    items = [
        {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": value}}
        for value in sorted({namespace, tenant})
    ]
    for filename in (
        "service_account.yaml",
        "role.yaml",
        "role_binding.yaml",
        "leader_election_role.yaml",
        "leader_election_role_binding.yaml",
    ):
        row = copy.deepcopy(docs[filename])
        row["metadata"]["name"] = name + (
            "-leader" if filename.startswith("leader_") else ""
        )
        if row["kind"] in {"Role", "RoleBinding", "ServiceAccount"}:
            row["metadata"]["namespace"] = namespace
        if "subjects" in row:
            row["subjects"] = [
                {"kind": "ServiceAccount", "name": name, "namespace": namespace}
            ]
            row["roleRef"]["name"] = row["metadata"]["name"]
        if filename == "role.yaml":
            # Cluster-scoped release reads remain cluster scoped. ConfigMaps and
            # NVKECluster mutation/watch authority belong only to the watched tenant.
            scoped = copy.deepcopy(row)
            scoped.update(kind="Role")
            scoped["metadata"]["namespace"] = tenant
            scoped["rules"] = [
                r for r in row["rules"] if "clusterreleases" not in r["resources"]
            ]
            row["rules"] = [
                r for r in row["rules"] if "clusterreleases" in r["resources"]
            ]
            items.append(scoped)
        if filename == "role_binding.yaml":
            scoped = copy.deepcopy(row)
            scoped.update(kind="RoleBinding")
            scoped["metadata"]["namespace"] = tenant
            scoped["roleRef"]["kind"] = "Role"
            items.append(scoped)
        items.append(row)
    deployment = copy.deepcopy(docs["manager.yaml"])
    deployment["metadata"].update(name=name, namespace=namespace)
    labels = {"app.kubernetes.io/name": name}
    deployment["spec"]["selector"]["matchLabels"] = labels
    pod = deployment["spec"]["template"]
    pod["metadata"]["labels"] = labels
    pod["spec"]["serviceAccountName"] = name
    container = pod["spec"]["containers"][0]
    container.update(image=config["image"], imagePullPolicy=config["imagePullPolicy"])
    container["args"] = [
        "--leader-elect",
        "--leader-election-namespace=$(POD_NAMESPACE)",
        "--namespace=" + tenant,
        "--health-probe-bind-address=:8081",
        "--metrics-bind-address=0",
        "--infra-template-configmap=" + config["infraTemplateConfigMap"],
        "--nvke-controller-endpoint=" + config["controllerEndpoint"],
    ]
    container["ports"] = [p for p in container["ports"] if p["name"] == "health"]
    if config["controllerAuth"]["mode"] == "projected":
        container["args"].append(
            "--nvke-controller-token-file=/var/run/secrets/nvke-controller/token"
        )
        token = pod["spec"]["volumes"][0]["projected"]["sources"][0][
            "serviceAccountToken"
        ]
        token["audience"] = config["controllerAuth"]["audience"]
    else:
        pod["spec"].pop("volumes")
        container.pop("volumeMounts")
    items.append(deployment)
    items.append(
        {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {"name": name + "-private", "namespace": namespace},
            "spec": {
                "podSelector": {"matchLabels": labels},
                "policyTypes": ["Ingress"],
                "ingress": [],
            },
        }
    )
    return {"apiVersion": "v1", "kind": "List", "items": items}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("source", "profile", "output"):
        parser.add_argument("--" + flag, type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.profile.stat().st_size > 65536:
            raise ProfileError("Management controller profile exceeds its bound")
        profile = json.loads(args.profile.read_text(), object_pairs_hook=object_pairs)
        content = (
            json.dumps(
                render(args.source, profile), sort_keys=True, indent=2, allow_nan=False
            )
            + "\n"
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
                    "error": "Management controller rendering failed; inspect private profile and pinned source"
                }
            )
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
