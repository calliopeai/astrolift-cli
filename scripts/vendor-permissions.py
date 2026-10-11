#!/usr/bin/env python3
"""Generate an offline permission catalogue from one immutable backend Git tree.

This parses source without importing Django or executing any backend code.
It never fetches a repository or contacts an installation.
"""

import argparse
import ast
import hashlib
import json
from pathlib import Path
import re
import subprocess

SOURCE_PATH = "backend/core/permissions.py"
DESTINATION = (
    Path(__file__).resolve().parents[1] / "internal/permissioncatalog/catalogue.json"
)


def catalogue(source: bytes, revision: str) -> bytes:
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("revision must be a full lowercase Git commit SHA")
    if len(source) > 1024 * 1024:
        raise ValueError("permission source exceeds the bounded source size")
    classes = [
        node
        for node in ast.parse(source).body
        if isinstance(node, ast.ClassDef) and node.name == "Permission"
    ]
    if len(classes) != 1:
        raise ValueError("source must contain exactly one Permission enum")
    permissions = []
    for node in classes[0].body:
        if (
            isinstance(node, ast.Expr)
            and isinstance(node.value, ast.Constant)
            and isinstance(node.value.value, str)
        ):
            continue  # A class docstring carries no permission.
        if (
            not isinstance(node, ast.Assign)
            or len(node.targets) != 1
            or not isinstance(node.targets[0], ast.Name)
        ):
            raise ValueError("Permission members must be static string assignments")
        if not isinstance(node.value, ast.Constant) or not isinstance(
            node.value.value, str
        ):
            raise ValueError("Permission members must be static string assignments")
        slug = node.value.value
        if (
            not re.fullmatch(r"[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*", slug)
            or slug in permissions
        ):
            raise ValueError("permission slugs must be unique resource.action strings")
        permissions.append(slug)
    if not permissions:
        raise ValueError("permission catalogue must not be empty")
    result = {
        "catalogueKind": "bundled",
        "requiresTargetCheck": True,
        "interpretation": "Permission definitions from a bundled backend revision; not the selected server catalogue, account grants or credential/target authority.",
        "source": {
            "repository": "calliopeai/astrolift-app",
            "revision": revision,
            "path": SOURCE_PATH,
            "sha256": hashlib.sha256(source).hexdigest(),
        },
        "permissions": sorted(permissions),
    }
    return (json.dumps(result, indent=2) + "\n").encode()


def read_source(repository: Path, revision: str) -> bytes:
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("revision must be a full lowercase Git commit SHA")
    return subprocess.run(
        ["git", "-C", str(repository), "show", f"{revision}:{SOURCE_PATH}"],
        check=True,
        capture_output=True,
    ).stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=["refresh", "check"])
    parser.add_argument(
        "--source",
        type=Path,
        required=True,
        help="existing backend repository; no network fetch",
    )
    parser.add_argument(
        "--revision", required=True, help="full immutable backend commit SHA"
    )
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.revision):
        parser.error("--revision must be a full lowercase Git commit SHA")
    source = read_source(args.source, args.revision)
    rendered = catalogue(source, args.revision)
    if args.operation == "refresh":
        DESTINATION.parent.mkdir(parents=True, exist_ok=True)
        DESTINATION.write_bytes(rendered)
    elif DESTINATION.read_bytes() != rendered:
        raise SystemExit(
            "bundled permission catalogue differs from the selected immutable source"
        )
    print(f"{args.operation}: bundled catalogue matches {args.revision}")


if __name__ == "__main__":
    main()
