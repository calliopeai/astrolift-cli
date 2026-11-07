#!/usr/bin/env python3
"""Build and run the pinned native bootstrap/utilities in private operator workspaces."""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
from uuid import uuid4

REVISION = "92c39b8ad563ebe5ef705f781b3038b901aba10f"
UTILITIES = {
    ("bootstrap", "site"),
    ("generate", "sitespec"),
    ("generate", "oidc-kubeconfig"),
    ("validate", "sitespec"),
    ("inspect", "nvkr-release"),
    ("inspect", "sitespec"),
    ("inspect", "stack-config"),
    ("inspect", "values"),
    ("render", "deploy-path"),
    ("get", "bootstrap-manifest"),
}
BOOTSTRAP_VALUE_FLAGS = {
    "arch",
    "diagnostics-dir",
    "encryption-key-out",
    "kubeconfig-out",
    "nvke-controller-image",
    "otlp-endpoint",
    "provisioned-provider-image",
    "release",
    "site",
    "sitespec",
    "timeout",
}
BOOTSTRAP_BOOL_FLAGS = {
    "allow-control-plane-without-vip",
    "disable-mtls",
    "dry-run",
    "help",
    "json-logs",
    "keep-bootstrap-cluster",
    "otlp-insecure",
    "verbose",
}
MANAGEMENT_SCRIPTS = {
    "install": ("deploy/nvke-deploy.sh", ["install"]),
    "check": ("deploy/check-site.sh", []),
    "configure-access": ("controller/hack/apply-capi-spiffe-exec-auth.sh", []),
}
MANAGEMENT_VALUE_FLAGS = {
    "install": {
        "deploy-path",
        "sitespec",
        "stack-config",
        "arch",
        "release",
        "nvkr-run-dir",
        "config",
        "values-for",
        "kubeconfig",
        "timeout",
    },
    "check": {
        "sitespec",
        "stack-config",
        "kubeconfig",
        "release",
        "nvkr-run-dir",
    },
    "configure-access": {"kubeconfig"},
}


def source_digest(root: Path) -> str:
    result = hashlib.sha256()
    for path in sorted(root.rglob("*")):
        metadata = path.lstat()
        if stat.S_ISDIR(metadata.st_mode):
            continue
        if not stat.S_ISREG(metadata.st_mode):
            raise ToolingError(
                "Pinned source must contain only regular files and directories"
            )
        result.update(str(path.relative_to(root)).encode() + b"\0")
        result.update(str(stat.S_IMODE(metadata.st_mode)).encode() + b"\0")
        result.update(digest(path).encode() + b"\0")
    return result.hexdigest()


class ToolingError(Exception):
    """Only fixed diagnostics cross the command boundary."""


def digest(path: Path) -> str:
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def private_directory(path: Path, *, create: bool = False) -> Path:
    if path.is_symlink():
        raise ToolingError("Private directory cannot be a symlink")
    if create:
        path.mkdir(mode=0o700, parents=False, exist_ok=False)
    result = path.resolve(strict=True)
    metadata = result.stat()
    if (
        not stat.S_ISDIR(metadata.st_mode)
        or metadata.st_uid != os.getuid()
        or metadata.st_mode & 0o077
    ):
        raise ToolingError("Use an owner-only directory (mode 0700)")
    return result


def write_json(path: Path, value: dict) -> None:
    with path.open("x", encoding="utf-8") as target:
        json.dump(value, target, sort_keys=True)
        target.write("\n")
        target.flush()
        os.fsync(target.fileno())
    directory = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def prepare(checkout: Path, output: Path) -> dict:
    output = private_directory(output, create=True)
    with tempfile.TemporaryDirectory(prefix="source-", dir=output) as temporary:
        archive = Path(temporary) / "source.tar"
        with archive.open("xb") as stream:
            subprocess.run(
                [
                    "git",
                    "-C",
                    str(checkout.resolve()),
                    "archive",
                    "--format=tar",
                    REVISION,
                ],
                stdout=stream,
                stderr=subprocess.DEVNULL,
                check=True,
            )
        source = output / "source"
        source.mkdir(mode=0o700)
        with tarfile.open(archive) as content:
            content.extractall(source, filter="data")
        env = os.environ.copy()
        for key in ("GOOS", "GOARCH"):
            env.pop(key, None)
        env.update(
            GOWORK="off", GOFLAGS="", GOENV="off", GOTOOLCHAIN="auto", CGO_ENABLED="0"
        )
        versions = (source / "make/tool-versions.mk").read_text(encoding="utf-8")
        capi = re.search(
            r"^CAPI_VERSION\s+\?=\s+(v\d+\.\d+\.\d+)\s*$", versions, re.MULTILINE
        )
        if capi is None:
            raise ToolingError(
                "Pinned source does not declare a supported CAPI version"
            )
        binary = output / "nvkectl"
        timestamp = subprocess.check_output(
            [
                "git",
                "-C",
                str(checkout.resolve()),
                "show",
                "-s",
                "--format=%cI",
                REVISION,
            ],
            stderr=subprocess.DEVNULL,
            text=True,
        ).strip()
        flags = f"-X main.version=astro-{REVISION[:12]} -X main.gitCommit={REVISION} -X main.gitTreeState=clean -X main.buildDate={timestamp} -X github.com/dsx-ai-factory/nvke/cli/internal/bootstrap/controllerdeploy.capiVersion={capi[1]}"
        with (output / "build.log").open("xb") as log:
            subprocess.run(
                [
                    "go",
                    "build",
                    "-mod=readonly",
                    "-buildvcs=false",
                    "-trimpath",
                    "-ldflags",
                    flags,
                    "-o",
                    str(binary),
                    "./cmd/nvkectl",
                ],
                cwd=source / "cli",
                env=env,
                stdout=log,
                stderr=log,
                check=True,
            )
        binary.chmod(0o700)
        result = {
            "format": 1,
            "revision": REVISION,
            "sourceArchiveSha256": digest(archive),
            "binarySha256": digest(binary),
            "capiVersion": capi[1],
            "sourceTreeSha256": source_digest(source),
        }
        write_json(output / "bundle.json", result)
        return result


def validate_command(arguments: list[str]) -> bool:
    if arguments == ["version"]:
        return False
    if (
        len(arguments) >= 2
        and arguments[0] == "management"
        and arguments[1] in MANAGEMENT_SCRIPTS
    ):
        if arguments[2:] in (["--help"], ["-h"]):
            return False
        options, dry_run, _ = management_options(arguments)
        kubeconfig = options.get("kubeconfig", [])
        if len(kubeconfig) != 1 or not kubeconfig[0]:
            raise ToolingError(
                "Supply exactly one explicit private --kubeconfig before passthrough arguments"
            )
        return arguments[1] != "check" and not dry_run
    if len(arguments) < 2 or tuple(arguments[:2]) not in UTILITIES:
        raise ToolingError(
            "Use a supported native bootstrap or utility command; managed API operations use Astro"
        )
    return tuple(arguments[:2]) == ("bootstrap", "site") and not bootstrap_dry_run(
        arguments[2:]
    )


def bootstrap_dry_run(flags: list[str]) -> bool:
    dry_run = False
    index = 0
    while index < len(flags):
        token = flags[index]
        if token == "--":
            return dry_run if index == len(flags) - 1 else False
        if token == "-f":
            index += 2
            continue
        if token.startswith("-f") or token in {"-h", "-v"}:
            index += 1
            continue
        if not token.startswith("--"):
            return False
        name, equals, value = token[2:].partition("=")
        if name in BOOTSTRAP_VALUE_FLAGS:
            index += 1 if equals else 2
            continue
        if name not in BOOTSTRAP_BOOL_FLAGS:
            return False
        if equals and value not in {
            "true",
            "True",
            "TRUE",
            "t",
            "T",
            "1",
            "false",
            "False",
            "FALSE",
            "f",
            "F",
            "0",
        }:
            return False
        if name == "dry-run":
            dry_run = not equals or value in {"true", "True", "TRUE", "t", "T", "1"}
        index += 1
    return dry_run


def management_options(arguments: list[str]) -> tuple[dict[str, list[str]], bool, int]:
    values: dict[str, list[str]] = {}
    dry_run = False
    index = 2
    command = arguments[1]
    kubeconfig_index = -1
    while index < len(arguments):
        token = arguments[index]
        if token == "--" and command == "install":
            if any(arg.startswith("--kube") for arg in arguments[index + 1 :]):
                raise ToolingError(
                    "Helm passthrough cannot override the management connection"
                )
            break
        if token == "--dry-run" and command == "install":
            dry_run = True
            index += 1
            continue
        if token == "--diagnostics" and command == "check":
            index += 1
            continue
        if not token.startswith("--"):
            raise ToolingError("Use the pinned management command's named options")
        name, equals, value = token[2:].partition("=")
        if name not in MANAGEMENT_VALUE_FLAGS[command]:
            raise ToolingError("Unsupported pinned management option")
        if name == "kubeconfig":
            kubeconfig_index = index
        if not equals:
            index += 1
            if index >= len(arguments):
                raise ToolingError("Management option lacks its value")
            value = arguments[index]
        if equals and command != "configure-access":
            raise ToolingError("Use separate management option names and values")
        values.setdefault(name, []).append(value)
        index += 1
    return values, dry_run, kubeconfig_index


def management_execution(
    bundle: Path, workspace: Path, run_dir: Path, manifest: dict, arguments: list[str]
) -> list[str]:
    help_only = arguments[2:] in (["--help"], ["-h"])
    if not help_only:
        options, _, kubeconfig_index = management_options(arguments)
        path = Path(options["kubeconfig"][0])
        if not path.is_absolute():
            path = workspace / path
        info = path.lstat()
        if (
            not stat.S_ISREG(info.st_mode)
            or info.st_uid != os.getuid()
            or stat.S_IMODE(info.st_mode) != 0o600
        ):
            raise ToolingError(
                "Management kubeconfig must be an owner-owned regular file with mode 0600"
            )
    source = bundle / "source"
    expected = manifest.get("sourceTreeSha256")
    if (
        not expected
        or not source.is_dir()
        or source.is_symlink()
        or source_digest(source) != expected
    ):
        raise ToolingError(
            "Pinned management source changed or lacks integrity metadata; prepare a new bundle"
        )
    copied = run_dir / "source"
    shutil.copytree(source, copied)
    if source_digest(copied) != expected:
        raise ToolingError("Management source changed while preparing execution")
    script, prefix = MANAGEMENT_SCRIPTS[arguments[1]]
    if help_only:
        return ["bash", str(copied / script), *prefix, *arguments[2:]]
    snapshot = run_dir / "management.kubeconfig"
    flatten_kubeconfig(path, snapshot, run_dir / "credential-preflight.stderr")
    native = arguments[2:].copy()
    relative_index = kubeconfig_index - 2
    if "=" in native[relative_index]:
        native[relative_index] = "--kubeconfig=" + str(snapshot)
    else:
        native[relative_index + 1] = str(snapshot)
    if arguments[1] == "install" and "deploy-path" not in options:
        native = ["--deploy-path", str(copied / "deploy/nico"), *native]
    return ["bash", str(copied / script), *prefix, *native]


def flatten_kubeconfig(source: Path, destination: Path, diagnostics: Path) -> None:
    with (
        os.fdopen(
            os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb"
        ) as output,
        diagnostics.open("xb") as errors,
    ):
        subprocess.run(
            [
                "kubectl",
                "--kubeconfig",
                str(source),
                "config",
                "view",
                "--flatten",
                "--minify",
                "--raw",
            ],
            stdout=output,
            stderr=errors,
            check=True,
        )
        output.flush()
        os.fsync(output.fileno())


def run(bundle: Path, workspace: Path, arguments: list[str]) -> dict:
    bundle = private_directory(bundle)
    workspace = private_directory(workspace)
    manifest = json.loads((bundle / "bundle.json").read_text(encoding="utf-8"))
    binary = bundle / "nvkectl"
    if (
        not isinstance(manifest, dict)
        or manifest.get("format") != 1
        or manifest.get("revision") != REVISION
        or binary.is_symlink()
        or digest(binary) != manifest.get("binarySha256")
    ):
        raise ToolingError("Native binary does not match its pinned bundle")
    effectful = validate_command(arguments)
    management = arguments[:1] == ["management"]
    kind = "management-" + arguments[1] if management else "bootstrap"
    with (workspace / ".native.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ToolingError(
                "Another native operation holds this workspace"
            ) from None
        journal = workspace / (kind + "-state.json")
        if effectful and journal.exists():
            raise ToolingError(
                "Operation was already attempted; inspect its retained state and artifacts before recovery"
            )
        identifier = str(uuid4())
        records = workspace / "runs"
        records.mkdir(mode=0o700, exist_ok=True)
        records = private_directory(records)
        run_dir = private_directory(records / identifier, create=True)
        result = {
            "format": 1,
            "revision": REVISION,
            "runId": identifier,
            "startedAt": datetime.now(timezone.utc).isoformat(),
            "phase": "started",
            "kind": kind if effectful or management else "utility",
            "argumentsDigest": hashlib.sha256(
                json.dumps(arguments).encode()
            ).hexdigest(),
        }
        write_json(run_dir / "started.json", result)
        config = workspace / ".config"
        config.mkdir(mode=0o700, exist_ok=True)
        config = private_directory(config)
        env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("OTEL_", "HELM_KUBE"))
            and key not in {"KUBECONFIG", "NVKECTL_SITE", "XDG_CONFIG_HOME"}
        }
        env["XDG_CONFIG_HOME"] = str(config)
        env.update(
            NVKECTL=str(binary),
            NVKE_STATE_DIR=str(run_dir / "state"),
            PYTHONDONTWRITEBYTECODE="1",
        )
        command = (
            management_execution(bundle, workspace, run_dir, manifest, arguments)
            if management
            else [str(binary), *arguments]
        )
        if management:
            env["KUBECONFIG"] = str(run_dir / "management.kubeconfig")
        with (
            (run_dir / "stdout").open("xb") as stdout,
            (run_dir / "stderr").open("xb") as stderr,
        ):
            if effectful:
                write_json(journal, result)
            process = subprocess.run(
                command,
                cwd=workspace,
                env=env,
                stdout=stdout,
                stderr=stderr,
                check=False,
            )
        result.update(
            phase="completed"
            if process.returncode == 0
            else "uncertain"
            if effectful
            else "failed",
            exitCode=process.returncode,
            stdout=str(run_dir / "stdout"),
            stderr=str(run_dir / "stderr"),
        )
        write_json(run_dir / "finished.json", result)
        return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    build = commands.add_parser("prepare")
    build.add_argument("--checkout", type=Path, required=True)
    build.add_argument("--output", type=Path, required=True)
    execute = commands.add_parser("run")
    execute.add_argument("--bundle", type=Path, required=True)
    execute.add_argument("--workspace", type=Path, required=True)
    execute.add_argument("arguments", nargs=argparse.REMAINDER)
    options = parser.parse_args()
    os.umask(0o077)
    try:
        if options.command == "prepare":
            result = prepare(options.checkout, options.output)
        else:
            arguments = (
                options.arguments[1:]
                if options.arguments[:1] == ["--"]
                else options.arguments
            )
            result = run(options.bundle, options.workspace, arguments)
        print(json.dumps(result, sort_keys=True))
        return result.get("exitCode", 0)
    except (
        ToolingError,
        OSError,
        ValueError,
        subprocess.SubprocessError,
        tarfile.TarError,
    ) as error:
        print(
            str(error)
            if isinstance(error, ToolingError)
            else "Native tooling failed; inspect private build/run artifacts",
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
