"""Optional owned-fixture timings; never retain GraphQL variables or credentials."""

import json
import os
import re
import stat

FIELDS = {
    "prepareNvkeOperatorInstallPlanPullAuthority",
    "reviewNvkeOperatorInstallPlanPullAuthority",
    "recoverNvkeOperatorInstallPlanPullAuthorityPreparation",
    "prepareNvkeOperatorInstallPlanPullSecretAnalysis",
    "reviewNvkeOperatorInstallPlanPullSecretAnalysis",
    "recoverNvkeOperatorInstallPlanPullSecretAnalysisPreparation",
    "createNvkeSite",
    "configureNvkeOperatorAuthority",
    "verifyNvkeOperatorAuthority",
    "astroliftNvkeOperation",
    "astroliftNvkeSite",
    "astroliftNvkeOperatorAuthority",
    "prepareNvkeOperatorInstallPlan",
    "reviewNvkeOperatorInstallPlan",
    "astroliftNvkeOperatorInstallPlanItemPage",
    "prepareNvkeOperatorInstallPlanCharts",
    "reviewNvkeOperatorInstallPlanCharts",
    "recoverNvkeOperatorInstallPlanChartsPreparation",
    "prepareNvkeOperatorInstallPlanTree",
    "reviewNvkeOperatorInstallPlanTree",
    "recoverNvkeOperatorInstallPlanTreePreparation",
    "prepareNvkeOperatorInstallPlanContext",
    "reviewNvkeOperatorInstallPlanContext",
    "recoverNvkeOperatorInstallPlanContextPreparation",
    "prepareNvkeOperatorInstallPlanVerification",
    "reviewNvkeOperatorInstallPlanVerification",
    "recoverNvkeOperatorInstallPlanVerificationPreparation",
    "prepareNvkeOperatorInstallPlanImage",
    "reviewNvkeOperatorInstallPlanImage",
    "recoverNvkeOperatorInstallPlanImagePreparation",
    "prepareNvkeOperatorInstallPlanWorkload",
    "reviewNvkeOperatorInstallPlanWorkload",
    "recoverNvkeOperatorInstallPlanWorkloadPreparation",
    "prepareNvkeOperatorInstallPlanImageBinding",
    "reviewNvkeOperatorInstallPlanImageBinding",
    "recoverNvkeOperatorInstallPlanImageBindingPreparation",
    "prepareNvkeOperatorInstallPlanImagePayload",
    "reviewNvkeOperatorInstallPlanImagePayload",
    "recoverNvkeOperatorInstallPlanImagePayloadPreparation",
    "prepareNvkeOperatorInstallPlanImmutableExecutionTree",
    "reviewNvkeOperatorInstallPlanImmutableExecutionTree",
    "recoverNvkeOperatorInstallPlanImmutableExecutionTreePreparation",
}


def record_request_timing(body, seconds, status, completed, dropped):
    path = os.getenv("NVKE_TEST_REQUEST_TIMINGS")
    if not path:
        return
    field = "other"
    try:
        query = json.loads(body).get("query")
        match = (
            re.search(r"\{\s*(?:\w+\s*:\s*)?(\w+)\s*(?:\(|\{)", query)
            if isinstance(query, str)
            else None
        )
        if match and match[1] in FIELDS:
            field = match[1]
    except (ValueError, TypeError, AttributeError):
        pass
    record = {
        "field": field,
        "seconds": round(seconds, 3),
        "status": status,
        "completed": bool(completed),
        "dropped": bool(dropped),
    }
    descriptor = os.open(
        path, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600
    )
    with os.fdopen(descriptor, "a") as output:
        opened = os.fstat(output.fileno())
        if (
            not stat.S_ISREG(opened.st_mode)
            or opened.st_mode & 0o077
            or opened.st_nlink != 1
            or opened.st_uid != os.getuid()
        ):
            raise RuntimeError("Use an owned private timing file")
        output.write(json.dumps(record, separators=(",", ":")) + "\n")
