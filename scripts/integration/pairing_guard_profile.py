"""Opt-in aggregate guard timings; no arguments, responses or private data."""

import json
import os
from pathlib import Path
import stat
import threading
import time


def install(monkeypatch):
    selected = os.getenv("NVKE_TEST_PAIRING_GUARD_PROFILE")
    if not selected:
        return
    import management_plan_context_service as original
    import management_source_frame as frames
    import management_plan_pull_authority_service as pulls
    import management_image_binding_store as bindings
    from nvke.operator_agent_client import OperatorAgentClient

    path = Path(selected)
    if not path.is_absolute():
        raise ValueError("Use an absolute owned profile path")
    metrics, lock = {}, threading.Lock()

    def save(label, error):
        with lock:
            record = {
                "boundary": label,
                "metrics": {key: dict(value) for key, value in metrics.items()},
            }
        if error is not None:
            record["exceptionType"] = type(error).__name__
            code = getattr(error, "code", None)
            record["errorCode"] = (
                code
                if code
                in {
                    "CONTRACT_UNSUPPORTED",
                    "INVALID_INPUT",
                    "OPERATOR_CONTROL_UNSUPPORTED",
                    "OPERATOR_INVALID_RESPONSE",
                    "OPERATOR_UNAVAILABLE",
                    "PRECONDITION",
                    "SOURCE_CHANGED",
                }
                else "OTHER"
            )
        descriptor = os.open(
            path, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600
        )
        with os.fdopen(descriptor, "a") as output:
            info = os.fstat(output.fileno())
            if (
                not stat.S_ISREG(info.st_mode)
                or info.st_uid != os.getuid()
                or info.st_nlink != 1
                or stat.S_IMODE(info.st_mode) != 0o600
            ):
                raise ValueError("Use an owned private profile file")
            output.write(json.dumps(record, separators=(",", ":")) + "\n")

    def timed(label, function, publish=False):
        def measured(*args, **kwargs):
            start, error = time.monotonic(), None
            try:
                return function(*args, **kwargs)
            except BaseException as caught:
                error = caught
                raise
            finally:
                elapsed = time.monotonic() - start
                with lock:
                    row = metrics.setdefault(
                        label,
                        {
                            "calls": 0,
                            "seconds": 0.0,
                            "maximumSeconds": 0.0,
                            "failures": 0,
                        },
                    )
                    row["calls"] += 1
                    row["seconds"] += elapsed
                    row["maximumSeconds"] = max(row["maximumSeconds"], elapsed)
                    row["failures"] += int(error is not None)
                if publish:
                    save(label, error)

        return measured

    for owner, name, label, publish in [
        (original.OriginalSourceCheckpoint, "__call__", "original.check", False),
        (original.OriginalSourceCheckpoint, "validate", "original.validate", False),
        (frames.SourceFrame, "matches_current", "frame.current", False),
        (bindings.ImageBindings, "load", "binding.load", False),
        (pulls.store.PullAuthorities, "_source", "pull.source", False),
        (pulls.store.PullAuthorities, "prepare", "pull.prepare", True),
        (pulls, "perform", "pull.agent", True),
        (OperatorAgentClient, "prepare_install_plan_pull_authority", "pull.sdk", True),
    ]:
        monkeypatch.setattr(owner, name, timed(label, getattr(owner, name), publish))
