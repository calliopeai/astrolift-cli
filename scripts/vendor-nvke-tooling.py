#!/usr/bin/env python3
"""Refresh/check the immutable native operator runner used by astro nvke tooling."""

import argparse
import ast
import hashlib
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
DESTINATION = ROOT / "internal/nvketooling"
SOURCE = "kubernetes/nvke/tooling.py"
BACKEND_SOURCE = "kubernetes/nvke/nktp_backend.py"
MANAGEMENT_API_SOURCE = "kubernetes/nvke/management_api.py"
MANAGEMENT_CONTROLLER_SOURCE = "kubernetes/nvke/management_controller.py"


def describe(source):
    constants = {}
    for node in ast.parse(source).body:
        if (
            isinstance(node, ast.Assign)
            and len(node.targets) == 1
            and isinstance(node.targets[0], ast.Name)
        ):
            if node.targets[0].id in {"REVISION", "UTILITIES", "MANAGEMENT_SCRIPTS"}:
                constants[node.targets[0].id] = ast.literal_eval(node.value)
    expected = json.loads((ROOT / "internal/nvkecatalog/catalog.json").read_text())[
        "revision"
    ]
    if constants["REVISION"] != expected:
        raise ValueError(
            "native tooling and managed operation catalog revisions differ"
        )
    commands = {" ".join(command) for command in constants["UTILITIES"]}
    commands.update(
        "management " + command for command in constants["MANAGEMENT_SCRIPTS"]
    )
    commands.add("version")
    return expected, sorted(commands)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path)
    parser.add_argument("--revision")
    parser.add_argument("--check", action="store_true")
    options = parser.parse_args()
    if options.check:
        metadata = json.loads((DESTINATION / "provenance.json").read_text())
        source = (DESTINATION / "tooling.py").read_bytes()
        backend = (DESTINATION / "nktp_backend.py").read_bytes()
        management_api = (DESTINATION / "management_api.py").read_bytes()
        management_controller = (DESTINATION / "management_controller.py").read_bytes()
        revision = metadata["sourceRevision"]
    else:
        if (
            not options.source
            or not options.revision
            or not re.fullmatch("[0-9a-f]{40}", options.revision)
        ):
            parser.error("refresh requires --source and an exact full --revision")
        revision = options.revision
        source = subprocess.check_output(
            ["git", "-C", str(options.source), "show", f"{revision}:{SOURCE}"]
        )
        backend = subprocess.check_output(
            ["git", "-C", str(options.source), "show", f"{revision}:{BACKEND_SOURCE}"]
        )
        management_api = subprocess.check_output(
            [
                "git",
                "-C",
                str(options.source),
                "show",
                f"{revision}:{MANAGEMENT_API_SOURCE}",
            ]
        )
        management_controller = subprocess.check_output(
            [
                "git",
                "-C",
                str(options.source),
                "show",
                f"{revision}:{MANAGEMENT_CONTROLLER_SOURCE}",
            ]
        )
    if not re.fullmatch("[0-9a-f]{40}", revision):
        raise ValueError("source revision must be a full immutable commit")
    upstream, commands = describe(source)
    backend_revision = next(
        ast.literal_eval(node.value)
        for node in ast.parse(backend).body
        if isinstance(node, ast.Assign)
        and any(
            isinstance(target, ast.Name) and target.id == "REVISION"
            for target in node.targets
        )
    )
    if backend_revision != upstream:
        raise ValueError("NKTP renderer and native tooling revisions differ")
    api_revision = next(
        ast.literal_eval(node.value)
        for node in ast.parse(management_api).body
        if isinstance(node, ast.Assign)
        and any(
            isinstance(target, ast.Name) and target.id == "REVISION"
            for target in node.targets
        )
    )
    if api_revision != upstream:
        raise ValueError("Management API renderer and native tooling revisions differ")
    revision_imports = [
        node
        for node in ast.parse(management_controller).body
        if isinstance(node, ast.ImportFrom)
        and node.module == "management_api"
        and node.level == 0
        and any(item.name == "REVISION" and item.asname is None for item in node.names)
    ]
    if len(revision_imports) != 1:
        raise ValueError(
            "Management controller must use the pinned management API revision dependency"
        )
    expected = {
        "format": 1,
        "sourceRevision": revision,
        "upstreamRevision": upstream,
        "runnerSha256": hashlib.sha256(source).hexdigest(),
        "backendRendererSha256": hashlib.sha256(backend).hexdigest(),
        "managementApiRendererSha256": hashlib.sha256(management_api).hexdigest(),
        "managementControllerRendererSha256": hashlib.sha256(
            management_controller
        ).hexdigest(),
        "commands": sorted(
            [
                *commands,
                "render nktp-backend",
                "render management-apis",
                "render management-controller",
            ]
        ),
    }
    if options.check:
        if metadata != expected:
            raise ValueError(
                "native runner provenance differs from the vendored bytes/catalog"
            )
        if options.source:
            canonical = subprocess.check_output(
                ["git", "-C", str(options.source), "show", f"{revision}:{SOURCE}"]
            )
            if canonical != source:
                raise ValueError("vendored runner differs from its immutable source")
            canonical_backend = subprocess.check_output(
                [
                    "git",
                    "-C",
                    str(options.source),
                    "show",
                    f"{revision}:{BACKEND_SOURCE}",
                ]
            )
            if canonical_backend != backend:
                raise ValueError(
                    "vendored NKTP renderer differs from its immutable source"
                )
            canonical_api = subprocess.check_output(
                [
                    "git",
                    "-C",
                    str(options.source),
                    "show",
                    f"{revision}:{MANAGEMENT_API_SOURCE}",
                ]
            )
            if canonical_api != management_api:
                raise ValueError(
                    "Vendored management API renderer differs from its immutable source"
                )
            canonical_controller = subprocess.check_output(
                [
                    "git",
                    "-C",
                    str(options.source),
                    "show",
                    f"{revision}:{MANAGEMENT_CONTROLLER_SOURCE}",
                ]
            )
            if canonical_controller != management_controller:
                raise ValueError(
                    "Vendored management controller renderer differs from its immutable source"
                )
        print("NVKE native runner provenance verified")
    else:
        DESTINATION.mkdir(parents=True, exist_ok=True)
        (DESTINATION / "tooling.py").write_bytes(source)
        (DESTINATION / "nktp_backend.py").write_bytes(backend)
        (DESTINATION / "management_api.py").write_bytes(management_api)
        (DESTINATION / "management_controller.py").write_bytes(management_controller)
        (DESTINATION / "provenance.json").write_text(
            json.dumps(expected, indent=2) + "\n"
        )
        print("NVKE native runner pinned at " + revision)


if __name__ == "__main__":
    main()
