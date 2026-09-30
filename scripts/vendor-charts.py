#!/usr/bin/env python3
"""Refresh from published canonical Git objects; verify the offline snapshot."""

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tempfile

REPOSITORY = "calliopeai/astrolift-opscode"
SOURCE_PATH = "helm/astrolift-prereqs"
ROOT = Path(__file__).resolve().parents[1]
DESTINATION = ROOT / "internal/charts/astrolift-prereqs"
INVENTORY = ROOT / "internal/charts/source.json"


def git(source, *args):
    result = subprocess.run(
        ["git", "-C", str(source), *args], capture_output=True, check=True
    )
    return result.stdout


def published_source(source, revision):
    """The caller fetches origin/main first; also refuse dirty or old siblings."""
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("source revision must be a full 40-character Git commit")
    if git(source, "status", "--porcelain", "--untracked-files=all").strip():
        raise ValueError(
            "canonical source checkout is dirty; commit and publish its changes first"
        )
    remote = git(source, "remote", "get-url", "origin").decode().strip()
    if remote not in (
        f"https://github.com/{REPOSITORY}.git",
        f"git@github.com:{REPOSITORY}.git",
    ):
        raise ValueError("source origin must be the canonical opscode repository")
    if git(source, "rev-parse", f"{revision}^{{commit}}").decode().strip() != revision:
        raise ValueError("source revision is not the requested commit")
    for descendant in ("refs/remotes/origin/main", "HEAD"):
        result = subprocess.run(
            [
                "git",
                "-C",
                str(source),
                "merge-base",
                "--is-ancestor",
                revision,
                descendant,
            ],
            capture_output=True,
        )
        if result.returncode:
            raise ValueError(
                "source revision must be published on origin/main and present in the selected checkout history"
            )
    files = {}
    for record in git(source, "ls-tree", "-rz", f"{revision}:{SOURCE_PATH}").split(
        b"\0"
    ):
        if not record:
            continue
        header, name = record.split(b"\t", 1)
        mode, kind, _ = header.split()
        if kind != b"blob" or mode not in (b"100644", b"100755"):
            raise ValueError("canonical chart must contain regular files only")
        filename = name.decode()
        if (
            PurePosixPath(filename).is_absolute()
            or ".." in PurePosixPath(filename).parts
        ):
            raise ValueError("invalid canonical chart path")
        files[filename] = git(source, "show", f"{revision}:{SOURCE_PATH}/{filename}")
    if "Chart.yaml" not in files or "Chart.lock" not in files:
        raise ValueError("canonical chart is missing Chart.yaml or Chart.lock")
    return files


def digest(data):
    return hashlib.sha256(data).hexdigest()


def chart_files(directory):
    files = {}
    for path in directory.rglob("*"):
        if path.is_symlink():
            raise ValueError("vendored chart must not contain symlinks")
        if path.is_file():
            files[path.relative_to(directory).as_posix()] = digest(path.read_bytes())
    return files


def source_inventory(directory, revision, canonical):
    hashes = chart_files(directory)
    canonical_hashes = {name: digest(data) for name, data in canonical.items()}
    if any(hashes.get(name) != checksum for name, checksum in canonical_hashes.items()):
        raise ValueError("dependency build changed canonical source files")
    extras = set(hashes) - set(canonical_hashes)
    if not extras or any(
        not re.fullmatch(r"charts/[^/]+\.tgz", name) for name in extras
    ):
        raise ValueError("only built subchart archives may supplement canonical files")
    return {
        "schemaVersion": 1,
        "repository": REPOSITORY,
        "revision": revision,
        "path": SOURCE_PATH,
        "canonicalFiles": canonical_hashes,
        "dependencyArchives": {name: hashes[name] for name in sorted(extras)},
    }


def check(directory=DESTINATION, inventory_path=INVENTORY):
    metadata = json.loads(inventory_path.read_text())
    if not isinstance(metadata, dict) or not isinstance(metadata.get("revision"), str):
        raise ValueError("invalid canonical chart provenance")
    if (
        metadata.get("schemaVersion") != 1
        or metadata.get("repository") != REPOSITORY
        or metadata.get("path") != SOURCE_PATH
        or not re.fullmatch(r"[0-9a-f]{40}", metadata.get("revision", ""))
    ):
        raise ValueError("invalid canonical chart provenance")
    canonical = metadata["canonicalFiles"]
    dependencies = metadata["dependencyArchives"]
    if not isinstance(canonical, dict) or not isinstance(dependencies, dict):
        raise ValueError("invalid source inventory")
    if not {"Chart.yaml", "Chart.lock"} <= canonical.keys() or not dependencies:
        raise ValueError("source inventory is incomplete")
    expected = dict(canonical)
    if set(canonical) & set(dependencies):
        raise ValueError("source and dependency inventory overlap")
    expected.update(dependencies)
    for name, checksum in expected.items():
        if (
            not isinstance(name, str)
            or not isinstance(checksum, str)
            or PurePosixPath(name).is_absolute()
            or ".." in PurePosixPath(name).parts
            or not re.fullmatch(r"[0-9a-f]{64}", checksum)
        ):
            raise ValueError("invalid source inventory entry")
    actual = chart_files(directory)
    if expected != actual:
        changed = sorted(
            name
            for name in expected.keys() | actual.keys()
            if expected.get(name) != actual.get(name)
        )
        raise ValueError(
            "vendored chart differs from pinned source inventory: " + ", ".join(changed)
        )
    return metadata


def refresh(
    source, revision, helm="helm", directory=DESTINATION, inventory_path=INVENTORY
):
    # Verify before touching the embedded chart. Files come from Git blobs,
    # never from an unrelated sibling's working directory.
    canonical = published_source(source, revision)
    directory.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(
        prefix=".chart-refresh-", dir=directory.parent
    ) as tmp:
        stage = Path(tmp) / "chart"
        stage.mkdir()
        for name, content in canonical.items():
            target = stage / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
        subprocess.run([helm, "dependency", "build", str(stage)], check=True)
        metadata = source_inventory(stage, revision, canonical)
        staged_inventory = Path(tmp) / "source.json"
        staged_inventory.write_text(
            json.dumps(metadata, indent=2, sort_keys=True) + "\n"
        )
        check(stage, staged_inventory)
        backup = Path(tmp) / "previous"
        existed = directory.exists()
        if existed:
            directory.rename(backup)
        try:
            stage.rename(directory)
            inventory_path.parent.mkdir(parents=True, exist_ok=True)
            os.replace(staged_inventory, inventory_path)
        except BaseException:
            if directory.exists():
                shutil.rmtree(directory)
            if existed:
                backup.rename(directory)
            raise
    return metadata


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("check")
    update = sub.add_parser("refresh")
    update.add_argument("--source", type=Path, required=True)
    update.add_argument("--revision", required=True)
    update.add_argument("--helm", default="helm")
    args = parser.parse_args()
    try:
        if args.command == "check":
            metadata = check()
        else:
            # Private-repository authentication is the developer's existing Git
            # credential configuration, never a token committed to this project.
            subprocess.run(
                [
                    "git",
                    "-C",
                    str(args.source),
                    "fetch",
                    "--no-tags",
                    "origin",
                    "main:refs/remotes/origin/main",
                ],
                check=True,
            )
            metadata = refresh(args.source, args.revision, args.helm)
        print(
            f"Prerequisite chart matches {metadata['repository']}@{metadata['revision']}"
        )
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Chart parity failed: {error}\n")


if __name__ == "__main__":
    main()
