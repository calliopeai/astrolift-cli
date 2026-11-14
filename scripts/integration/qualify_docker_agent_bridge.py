"""Manual host/owned-Linux-agent mTLS qualification; no installation admissions."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import selectors
import shutil
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from uuid import uuid4

from docker_agent_bridge import DockerAgentBridge

PROGRAM = """import os,sys,json,shutil
from pathlib import Path
os.umask(0o077)
shutil.copytree('/qualification-tools/source-snapshots/SNAPSHOT_NAME','/tmp/runtime')
os.chdir('/tmp/runtime');sys.path.insert(0,'/tmp/runtime')
shutil.copytree('/qualification-tools/bundle','/tmp/agent-bundle')
Path('/tmp/agent-bundle').chmod(0o700)
os.environ['NVKE_TEST_AGENT_BUNDLE']='/tmp/agent-bundle'
import test_operator_agent as fixture
original=fixture.agent.serve
def serve(engine,**values):
 values.update(host='127.0.0.1',port=9443)
 return original(engine,**values)
fixture.agent.serve=serve
fixture.NativeAgentTests.setUpClass()
case=fixture.NativeAgentTests();case.setUp()
try:
 data={
  'siteId':case.engine.site_id,'instanceId':case.engine.instance_id,
  'bundleManifestSha256':case.engine.bundle_digest,
  'ca':(case.certs/'ca.pem').read_text(),
  'certificate':(case.certs/'client.pem').read_text(),
  'key':(case.certs/'client.key').read_text(),
 }
 Path('/tmp/bridge-private.json').write_text(json.dumps(data))
 Path('/tmp/bridge-private.json').chmod(0o600)
 print('BRIDGE_READY',flush=True)
 assert sys.stdin.readline().strip()=='CLOSE'
 assert not case.engine._admissions()
finally:
 case.tearDown();fixture.NativeAgentTests.tearDownClass()
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--opscode", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    base = args.output.resolve()
    for suffix in (
        ".pid",
        ".log",
        ".json",
        ".exit",
        ".cid",
        ".sources.json",
        ".proxy.log",
        ".wrong-run.log",
    ):
        if Path(str(base) + suffix).exists():
            raise FileExistsError("Use a fresh qualification output prefix")
    with base.with_suffix(".pid").open("x") as marker:
        marker.write(str(os.getpid()))
    started = time.monotonic()
    cfg = json.loads(args.profile.read_text())
    docker = cfg["dockerCommand"]
    run_id = str(uuid4())
    snapshot = (
        Path(cfg["toolsRoot"]) / "source-snapshots" / ("host-agent-bridge-" + run_id)
    )
    snapshot.mkdir(mode=0o700)
    hashes = {}
    paths = subprocess.check_output(
        ["git", "ls-files", "kubernetes/nvke"], cwd=args.opscode, text=True
    ).splitlines()
    for name in paths:
        path = args.opscode / name
        if path.suffix not in {".py", ".go", ".mod", ".sum"}:
            continue
        relative = path.relative_to(args.opscode / "kubernetes/nvke")
        target = snapshot / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, target)
        hashes[str(relative)] = hashlib.sha256(target.read_bytes()).hexdigest()
    Path(str(base) + ".sources.json").write_text(json.dumps(hashes, indent=2) + "\n")
    program = PROGRAM.replace("SNAPSHOT_NAME", snapshot.name)
    compile(program, "owned-agent-bridge", "exec")
    helper = Path(__file__).with_name("docker_agent_bridge.py")
    source_hashes = {
        path.name: hashlib.sha256(path.read_bytes()).hexdigest()
        for path in (Path(__file__), helper)
    }
    cid = child = bridge = None
    code, failure, stage = 1, None, "create-container"
    checks, cleanup = {}, []
    with base.with_suffix(".log").open("x") as log:

        def command(arguments):
            return subprocess.run(
                [*docker, *arguments],
                stdout=subprocess.PIPE,
                stderr=log,
                text=True,
                check=True,
            )

        try:
            result = command(
                [
                    "run",
                    "-d",
                    "--rm",
                    "--pull=never",
                    "--name",
                    "nvke-agent-bridge-" + run_id,
                    "--label",
                    "astrolift.qualification.run=" + run_id,
                    "--network",
                    "none",
                    "--user",
                    "0:0",
                    "--cpus",
                    "1",
                    "--memory",
                    "1g",
                    "--pids-limit",
                    "128",
                    "--cap-drop",
                    "ALL",
                    "--security-opt",
                    "no-new-privileges",
                    "--read-only",
                    "--tmpfs",
                    "/tmp:rw,exec,nosuid,nodev,size=536870912,mode=1777",
                    "-v",
                    cfg["toolsRoot"] + ":/qualification-tools:ro",
                    cfg["runtimeImage"],
                    "sleep",
                    "600",
                ]
            )
            cid = result.stdout.strip()
            Path(str(base) + ".cid").write_text(cid)
            stage = "agent-readiness"
            child = subprocess.Popen(
                [*docker, "exec", "-i", cid, "python", "-B", "-c", program],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=log,
                text=True,
            )
            with selectors.DefaultSelector() as ready:
                ready.register(child.stdout, selectors.EVENT_READ)
                if (
                    not ready.select(120)
                    or child.stdout.readline().strip() != "BRIDGE_READY"
                ):
                    raise RuntimeError("Owned agent did not signal readiness")
            private = json.loads(
                command(["exec", cid, "cat", "/tmp/bridge-private.json"]).stdout
            )
            stage = "wrong-run-refusal"
            try:
                with DockerAgentBridge(
                    docker,
                    cid,
                    str(uuid4()),
                    port=9443,
                    log=Path(str(base) + ".wrong-run.log"),
                ):
                    raise AssertionError("Wrong qualification run accepted")
            except RuntimeError:
                checks["wrongRunNonceRefusedBeforeProxy"] = True
            stage = "authenticated-host-read"
            bridge = DockerAgentBridge(
                docker, cid, run_id, port=9443, log=Path(str(base) + ".proxy.log")
            )
            bridge.__enter__()
            bridge.docker, bridge.container_id, bridge.run_id, bridge.port = (
                ("false",),
                "0" * 64,
                str(uuid4()),
                1,
            )
            with tempfile.TemporaryDirectory(
                prefix="nvke-owned-bridge-client-"
            ) as folder:
                root = Path(folder)
                for key, name in (("certificate", "client.pem"), ("key", "client.key")):
                    (root / name).write_text(private[key])
                    (root / name).chmod(0o600)
                verified = ssl.create_default_context(cadata=private["ca"])
                verified.load_cert_chain(root / "client.pem", root / "client.key")
                with urllib.request.urlopen(
                    bridge.endpoint + "/v1/identity", context=verified, timeout=30
                ) as response:
                    identity = json.load(response)
                for key in ("siteId", "instanceId", "bundleManifestSha256"):
                    assert identity[key] == private[key]
                checks["hostToActualAgentVerifiedMTLSIdentity"] = True
                checks["targetSnapshotUnaffectedByCallerFieldChanges"] = True
                stage = "missing-client-refusal"
                try:
                    urllib.request.urlopen(
                        bridge.endpoint + "/v1/identity",
                        context=ssl.create_default_context(cadata=private["ca"]),
                        timeout=10,
                    )
                except (OSError, urllib.error.URLError):
                    checks["clientCertificateRequired"] = True
                else:
                    raise AssertionError("Missing client certificate accepted")
            checks["proxySessions"] = bridge.session_count
            assert bridge.session_count == 2
            stage = "agent-close"
            child.stdin.write("CLOSE\n")
            child.stdin.flush()
            child.stdin.close()
            assert child.wait(timeout=30) == 0
            checks["agentClosedWithoutAdmissions"] = True
            code = 0
        except Exception as error:
            failure = {"stage": stage, "type": type(error).__name__}
        finally:
            if bridge is not None:
                try:
                    bridge.close()
                    cleanup.append({"resource": "owned-proxy", "exitCode": 0})
                except Exception as error:
                    cleanup.append({"resource": "owned-proxy", "exitCode": 1})
                    failure = {"stage": "proxy-close", "type": type(error).__name__}
            if cid:
                result = subprocess.run(
                    [*docker, "rm", "-f", cid], stdout=log, stderr=log
                )
                cleanup.append(
                    {"resource": "owned-container", "exitCode": result.returncode}
                )
            if child is not None and child.poll() is None:
                DockerAgentBridge._finish(child)
    unchanged = all(
        hashlib.sha256((snapshot / name).read_bytes()).hexdigest() == digest
        for name, digest in hashes.items()
    )
    helper_unchanged = all(
        hashlib.sha256(Path(__file__).with_name(name).read_bytes()).hexdigest()
        == digest
        for name, digest in source_hashes.items()
    )
    code = code or int(
        not unchanged or not helper_unchanged or any(row["exitCode"] for row in cleanup)
    )
    report = {
        "exitCode": code,
        "elapsedSeconds": time.monotonic() - started,
        "checks": checks,
        "failure": failure,
        "cleanup": cleanup,
        "sourceManifest": str(base) + ".sources.json",
        "sourceFiles": len(hashes),
        "allFrozenSourcesUnchanged": unchanged,
        "harnessSourceSha256": source_hashes,
        "harnessSourcesUnchanged": helper_unchanged,
        "network": "none-with-owned-loopback-opaque-stdio-proxy",
        "privileged": False,
        "readOnlyRoot": True,
        "capabilities": "all-dropped",
        "cpus": 1,
        "memoryGiB": 1,
        "scope": "Actual host/owned Linux agent mTLS identity and client certificate refusal; no App/database/artifact/lifecycle proof",
        "protectedOriginalsChanged": False,
        "log": str(base.with_suffix(".log")),
    }
    base.with_suffix(".json").write_text(json.dumps(report, indent=2) + "\n")
    base.with_suffix(".exit").write_text(str(code) + "\n")
    return code


if __name__ == "__main__":
    raise SystemExit(main())
