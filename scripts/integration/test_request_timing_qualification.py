import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from request_timing_qualification import record_request_timing


class RequestTimingPrivacyTests(unittest.TestCase):
    def test_records_only_allowlisted_field_and_timing_without_variables_or_aliases(
        self,
    ):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / "timing.jsonl"
            with patch.dict(os.environ, {"NVKE_TEST_REQUEST_TIMINGS": str(target)}):
                record_request_timing(
                    json.dumps(
                        {
                            "query": "mutation($input:Input!){PRIVATE_ALIAS:prepareNvkeOperatorInstallPlanContext(input:$input){ok}}",
                            "variables": {
                                "input": {
                                    "requestText": "PRIVATE_BODY",
                                    "credentials": "PRIVATE_TOKEN",
                                }
                            },
                        }
                    ).encode(),
                    12.3456,
                    200,
                    True,
                    True,
                )
                record_request_timing(
                    b'{"query":"query{PRIVATE_ALIAS:unapprovedField{value}}"}',
                    1,
                    403,
                    True,
                    False,
                )
                record_request_timing(b"invalid PRIVATE_BODY", 1, None, False, False)
            raw = target.read_text()
            self.assertNotIn("PRIVATE", raw)
            records = [json.loads(line) for line in raw.splitlines()]
            self.assertEqual(
                records[0],
                {
                    "field": "prepareNvkeOperatorInstallPlanContext",
                    "seconds": 12.346,
                    "status": 200,
                    "completed": True,
                    "dropped": True,
                },
            )
            self.assertEqual([r["field"] for r in records[1:]], ["other", "other"])
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)

    def test_refuses_a_symlink_or_shared_log(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / "target"
            target.write_text("")
            target.chmod(0o644)
            link = Path(folder) / "link"
            link.symlink_to(target)
            for path in (target, link):
                with (
                    patch.dict(os.environ, {"NVKE_TEST_REQUEST_TIMINGS": str(path)}),
                    self.assertRaises((OSError, RuntimeError)),
                ):
                    record_request_timing(b"{}", 1, 200, True, False)
