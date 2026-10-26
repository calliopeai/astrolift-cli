"""Validate the plan reader against the native control-plane schema."""
import os
import re
from pathlib import Path

import pytest

if not os.environ.get("ASTROLIFT_CLI_TEST_BINARY"):
    pytest.skip("requires the opt-in CLI/backend integration environment", allow_module_level=True)

from graphql import parse, validate


def test_plan_reader_uses_native_source_contract():
    from config.schema import schema

    source = (Path(__file__).resolve().parents[1] / "cmd/plan.go").read_text()
    for name in ("planSourceQuery", "planFileQuery"):
        document = re.search(name + r" = `([^`]+)`", source).group(1)
        assert validate(schema._schema, parse(document)) == []
