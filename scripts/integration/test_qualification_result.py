"""A misleading wrapper exit flag cannot qualify a failed or incomplete run."""

import unittest

from qualification_result import qualification_exit_code


class QualificationResultTests(unittest.TestCase):
    def result(self, **changes):
        return qualification_exit_code(
            **{
                "attempted_exit_code": 0,
                "checks": {"native": True, "app": True, "sources": True},
                "required_checks": ("native", "app", "sources"),
                "cleanup_codes": [0, 0],
                **changes,
            }
        )

    def test_complete_success(self):
        self.assertEqual(self.result(), 0)

    def test_reported_failure_overrides_zero_exit(self):
        for key in ("failureStage", "failureType", "failureContext"):
            self.assertEqual(
                self.result(
                    checks={
                        "native": True,
                        "app": True,
                        "sources": True,
                        key: None,
                    }
                ),
                1,
            )

    def test_missing_partial_or_truthy_evidence_refuses(self):
        for value in (None, False, 1, "true", {}):
            self.assertEqual(
                self.result(
                    checks={
                        "native": True,
                        "app": value,
                        "sources": True,
                    }
                ),
                1,
            )
        for required in ((), 1, "native", (None,), ("",), ("native", "native")):
            self.assertEqual(self.result(required_checks=required), 1)

    def test_bad_exit_types_and_failed_attempt_never_pass(self):
        for value in (None, False, True, "0", -1, 256):
            self.assertEqual(self.result(attempted_exit_code=value), 1)
        self.assertEqual(self.result(attempted_exit_code=3), 3)

    def test_failed_missing_or_untyped_cleanup_refuses(self):
        for values in ([], [0, 1], [False], [None], ["0"], 0, 1, "0"):
            self.assertEqual(self.result(cleanup_codes=values), 1)
