#!/usr/bin/env python3
"""jq-style select / filter / reshape over JSON and YAML (stdlib JSON).

A tiny path language addresses into a document:

* ``a.b`` nested key, ``a[0]`` index, ``a[]`` map-over-list, ``.`` whole doc.

Plus two reshaping ops: ``--where key=value`` (filter list elements) and
``--pick a,b`` (project object keys). Reads stdin, writes stdout.

YAML input/output requires PyYAML (``pip install pyyaml``); JSON is
stdlib-only.

Examples::

    python transform.py 'data.total' < response.json
    python transform.py 'items[]' --where status=active --pick id,name < x.json
"""

from __future__ import annotations

import argparse
import json
import re
import sys

_MISSING = object()

# One path step: a bare key, key[index], or key[] (map).
_STEP = re.compile(r"^(?P<key>[^.\[\]]*)(?P<idx>\[\d*\])?$")


def _load(text: str, fmt: str):
    if fmt == "json":
        return json.loads(text)
    if fmt == "yaml":
        try:
            import yaml
        except ImportError:  # pragma: no cover - exercised only without PyYAML
            raise SystemExit("yaml input requires PyYAML — run: pip install pyyaml")
        return yaml.safe_load(text)
    raise SystemExit(f"unknown format {fmt!r}")


def _dump(value, fmt: str) -> str:
    if fmt == "yaml":
        try:
            import yaml
        except ImportError:  # pragma: no cover
            raise SystemExit("yaml output requires PyYAML — run: pip install pyyaml")
        return yaml.safe_dump(value, sort_keys=False, default_flow_style=False)
    return json.dumps(value, indent=2, ensure_ascii=False) + "\n"


def _parse_path(path: str) -> list[tuple[str, object]]:
    """Parse a dotted/bracket path into (key, index-or-MAP-or-None) steps."""
    if path.strip() in ("", "."):
        return []
    steps: list[tuple[str, object]] = []
    for raw in path.split("."):
        m = _STEP.match(raw)
        if not m:
            raise SystemExit(f"bad path segment: {raw!r}")
        key = m.group("key")
        idx_token = m.group("idx")
        index: object = None
        if idx_token is not None:
            inner = idx_token[1:-1]
            index = "MAP" if inner == "" else int(inner)
        steps.append((key, index))
    return steps


def _step_into(value, key: str, index):
    """Apply one parsed step to a value, returning the navigated result."""
    if key:
        if not isinstance(value, dict):
            return _MISSING
        value = value.get(key, _MISSING)
        if value is _MISSING:
            return _MISSING
    if index is None:
        return value
    if index == "MAP":
        return value if isinstance(value, list) else _MISSING
    if isinstance(value, list) and -len(value) <= index < len(value):
        return value[index]
    return _MISSING


def select(doc, path: str):
    """Navigate ``doc`` by ``path``. ``[]`` maps the remaining path over a list."""
    steps = _parse_path(path)
    current = [doc]  # list of "cursors" so a [] step can fan out
    mapping = False
    for key, index in steps:
        nxt = []
        for cur in current:
            result = _step_into(cur, key, index)
            if result is _MISSING:
                nxt.append(None)
            elif index == "MAP" and isinstance(result, list):
                nxt.extend(result)
                mapping = True
            else:
                nxt.append(result)
        current = nxt
    return current if mapping else current[0]


def _matches(obj, conditions: list[tuple[str, str]]) -> bool:
    if not isinstance(obj, dict):
        return False
    return all(str(obj.get(k)) == v for k, v in conditions)


def _pick(obj, keys: list[str]):
    if isinstance(obj, dict):
        return {k: obj[k] for k in keys if k in obj}
    return obj


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="jq-style JSON/YAML transform.")
    parser.add_argument("path", nargs="?", default=".", help="selection path")
    parser.add_argument("--where", action="append", default=[], help="key=value list filter")
    parser.add_argument("--pick", help="comma-separated keys to project")
    parser.add_argument("--input", default="json", choices=("json", "yaml"))
    parser.add_argument("--output", default="json", choices=("json", "yaml"))
    args = parser.parse_args(argv)

    doc = _load(sys.stdin.read(), args.input)
    result = select(doc, args.path)

    conditions = []
    for cond in args.where:
        if "=" not in cond:
            raise SystemExit(f"bad --where {cond!r} (expected key=value)")
        k, _, v = cond.partition("=")
        conditions.append((k, v))

    if conditions:
        if not isinstance(result, list):
            raise SystemExit("--where requires a list selection (use a '[]' path)")
        result = [item for item in result if _matches(item, conditions)]

    if args.pick:
        keys = [k.strip() for k in args.pick.split(",") if k.strip()]
        result = [_pick(i, keys) for i in result] if isinstance(result, list) else _pick(result, keys)

    sys.stdout.write(_dump(result, args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
