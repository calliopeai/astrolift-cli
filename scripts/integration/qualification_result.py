"""Require explicit success evidence before a qualification can exit green."""


def qualification_exit_code(
    *, attempted_exit_code, checks, required_checks, cleanup_codes
):
    if type(attempted_exit_code) is not int or not 0 <= attempted_exit_code <= 255:
        return 1
    if attempted_exit_code:
        return attempted_exit_code
    if not isinstance(checks, dict) or any(
        name in checks for name in ("failureStage", "failureType", "failureContext")
    ):
        return 1
    if (
        not isinstance(required_checks, (tuple, list))
        or not required_checks
        or any(not isinstance(name, str) or not name for name in required_checks)
        or len(set(required_checks)) != len(required_checks)
    ):
        return 1
    if any(checks.get(name) is not True for name in required_checks):
        return 1
    if not isinstance(cleanup_codes, (tuple, list)) or not cleanup_codes:
        return 1
    if any(type(code) is not int or code != 0 for code in cleanup_codes):
        return 1
    return 0
