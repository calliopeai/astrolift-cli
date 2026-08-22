#!/usr/bin/env python3
"""Read / filter / select / aggregate / join CSV data (stdlib ``csv``).

Subcommands (all expect a header row, all write CSV or a value to stdout):

* ``head``   — header + first N rows.
* ``filter`` — keep rows matching ``--where 'col OP value'``.
* ``select`` — keep/reorder ``--columns``.
* ``agg``    — sum/mean/min/max/count a numeric ``--column``, optional
  ``--group-by``.
* ``join``   — inner-join two files ``--on`` a shared key column.

For large data or heavy analytics use pandas instead; this is the
no-install everyday tool.
"""

from __future__ import annotations

import argparse
import csv
import sys
from collections import OrderedDict

_OPS = {
    "==": lambda a, b: a == b,
    "!=": lambda a, b: a != b,
    ">": lambda a, b: _num(a) > _num(b),
    "<": lambda a, b: _num(a) < _num(b),
    ">=": lambda a, b: _num(a) >= _num(b),
    "<=": lambda a, b: _num(a) <= _num(b),
    "contains": lambda a, b: b in a,
}


def _num(value) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return float("nan")


def _read(path: str) -> tuple[list[str], list[dict]]:
    with open(path, newline="", encoding="utf-8-sig") as fh:
        reader = csv.DictReader(fh)
        fields = reader.fieldnames or []
        rows = list(reader)
    return fields, rows


def _write(fields: list[str], rows: list[dict]) -> None:
    writer = csv.DictWriter(sys.stdout, fieldnames=fields)
    writer.writeheader()
    for row in rows:
        writer.writerow({k: row.get(k, "") for k in fields})


def _parse_where(expr: str) -> tuple[str, str, str]:
    """Parse 'col OP value' into (column, op_token, value)."""
    # Try multi-char ops first so '>=' isn't read as '>'.
    for op in ("contains", ">=", "<=", "==", "!=", ">", "<"):
        token = f" {op} " if op == "contains" else op
        if token in expr:
            col, _, value = expr.partition(token)
            return col.strip(), op, value.strip()
    raise SystemExit(f"bad --where {expr!r} (expected 'col OP value')")


def cmd_head(args) -> int:
    fields, rows = _read(args.path)
    _write(fields, rows[: args.n])
    return 0


def cmd_filter(args) -> int:
    fields, rows = _read(args.path)
    col, op, value = _parse_where(args.where)
    if col not in fields:
        raise SystemExit(f"no such column {col!r}; have {fields}")
    test = _OPS[op]
    kept = [r for r in rows if test(r.get(col, ""), value)]
    _write(fields, kept)
    return 0


def cmd_select(args) -> int:
    fields, rows = _read(args.path)
    cols = [c.strip() for c in args.columns.split(",") if c.strip()]
    missing = [c for c in cols if c not in fields]
    if missing:
        raise SystemExit(f"no such column(s): {missing}; have {fields}")
    _write(cols, rows)
    return 0


def cmd_agg(args) -> int:
    _, rows = _read(args.path)
    groups: OrderedDict[str, list[float]] = OrderedDict()
    for row in rows:
        key = row.get(args.group_by, "") if args.group_by else ""
        val = _num(row.get(args.column, ""))
        if val != val:  # NaN: non-numeric cell, skip
            continue
        groups.setdefault(key, []).append(val)

    def reduce(values: list[float]) -> float:
        if args.op == "sum":
            return sum(values)
        if args.op == "mean":
            return sum(values) / len(values) if values else 0.0
        if args.op == "min":
            return min(values) if values else 0.0
        if args.op == "max":
            return max(values) if values else 0.0
        return float(len(values))  # count

    if args.group_by:
        out_fields = [args.group_by, f"{args.op}_{args.column}"]
        out_rows = [{args.group_by: k, out_fields[1]: reduce(v)} for k, v in groups.items()]
        _write(out_fields, out_rows)
    else:
        print(reduce(groups.get("", [])))
    return 0


def cmd_join(args) -> int:
    left_fields, left_rows = _read(args.left)
    right_fields, right_rows = _read(args.right)
    if args.on not in left_fields or args.on not in right_fields:
        raise SystemExit(f"join key {args.on!r} must be in both files")

    index: dict[str, list[dict]] = {}
    for row in right_rows:
        index.setdefault(row[args.on], []).append(row)

    # Right columns (minus key) get a suffix where they'd collide with left.
    right_only = [c for c in right_fields if c != args.on]
    renames = {c: (f"{c}_right" if c in left_fields else c) for c in right_only}
    out_fields = left_fields + [renames[c] for c in right_only]

    out_rows: list[dict] = []
    for lrow in left_rows:
        for rrow in index.get(lrow[args.on], []):
            merged = dict(lrow)
            for c in right_only:
                merged[renames[c]] = rrow.get(c, "")
            out_rows.append(merged)

    _write(out_fields, out_rows)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="CSV read/filter/agg/join.")
    sub = parser.add_subparsers(dest="action", required=True)

    p = sub.add_parser("head")
    p.add_argument("path")
    p.add_argument("-n", type=int, default=10)
    p.set_defaults(func=cmd_head)

    p = sub.add_parser("filter")
    p.add_argument("path")
    p.add_argument("--where", required=True, help="'col OP value'")
    p.set_defaults(func=cmd_filter)

    p = sub.add_parser("select")
    p.add_argument("path")
    p.add_argument("--columns", required=True, help="comma-separated column names")
    p.set_defaults(func=cmd_select)

    p = sub.add_parser("agg")
    p.add_argument("path")
    p.add_argument("--column", required=True)
    p.add_argument("--op", default="sum", choices=("sum", "mean", "min", "max", "count"))
    p.add_argument("--group-by")
    p.set_defaults(func=cmd_agg)

    p = sub.add_parser("join")
    p.add_argument("left")
    p.add_argument("right")
    p.add_argument("--on", required=True, help="shared key column")
    p.set_defaults(func=cmd_join)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
