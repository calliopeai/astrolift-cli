#!/usr/bin/env python3
"""Export a dataset (JSON array of objects) to CSV / JSON / XLSX / PDF.

The output format is chosen by the ``--out`` file extension. CSV and
JSON are stdlib-only; XLSX needs ``openpyxl`` and PDF needs ``reportlab``
(install only the one you use). The optional libraries are imported
lazily so this script byte-compiles and runs for the stdlib formats even
when they aren't installed.

Examples::

    python export.py data.json --out report.csv
    python export.py data.json --out report.xlsx
    cat data.json | python export.py - --out report.pdf --title "Scores"
"""

from __future__ import annotations

import argparse
import csv
import json
import sys
from pathlib import Path


def _read_records(path: str) -> list[dict]:
    raw = sys.stdin.read() if path == "-" else Path(path).read_text(encoding="utf-8")
    data = json.loads(raw)
    if not isinstance(data, list) or not all(isinstance(r, dict) for r in data):
        raise SystemExit("input must be a JSON array of objects")
    return data


def _columns(records: list[dict]) -> list[str]:
    """Union of keys across records, in first-seen order."""
    cols: list[str] = []
    seen: set[str] = set()
    for rec in records:
        for key in rec:
            if key not in seen:
                seen.add(key)
                cols.append(key)
    return cols


def _cell(value) -> str:
    """Render a cell value as text (JSON-encode nested structures)."""
    if value is None:
        return ""
    if isinstance(value, (dict, list)):
        return json.dumps(value, ensure_ascii=False)
    return str(value)


def to_csv(records: list[dict], cols: list[str], out: Path) -> None:
    with out.open("w", newline="", encoding="utf-8") as fh:
        writer = csv.writer(fh)
        writer.writerow(cols)
        for rec in records:
            writer.writerow([_cell(rec.get(c)) for c in cols])


def to_json(records: list[dict], cols: list[str], out: Path) -> None:
    out.write_text(json.dumps(records, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def to_xlsx(records: list[dict], cols: list[str], out: Path) -> None:
    try:
        from openpyxl import Workbook
    except ImportError:  # pragma: no cover - exercised only without openpyxl
        raise SystemExit("xlsx export requires openpyxl — run: pip install openpyxl")
    wb = Workbook()
    ws = wb.active
    ws.append(cols)
    for rec in records:
        ws.append([_cell(rec.get(c)) for c in cols])
    wb.save(out)


def to_pdf(records: list[dict], cols: list[str], out: Path, *, title: str | None) -> None:
    try:
        from reportlab.lib import colors
        from reportlab.lib.pagesizes import letter
        from reportlab.lib.styles import getSampleStyleSheet
        from reportlab.platypus import Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle
    except ImportError:  # pragma: no cover - exercised only without reportlab
        raise SystemExit("pdf export requires reportlab — run: pip install reportlab")

    data = [cols] + [[_cell(rec.get(c)) for c in cols] for rec in records]
    table = Table(data, repeatRows=1)
    table.setStyle(
        TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#2d3748")),
                ("TEXTCOLOR", (0, 0), (-1, 0), colors.white),
                ("FONTSIZE", (0, 0), (-1, -1), 9),
                ("GRID", (0, 0), (-1, -1), 0.5, colors.grey),
                ("ROWBACKGROUNDS", (0, 1), (-1, -1), [colors.white, colors.HexColor("#f7fafc")]),
                ("VALIGN", (0, 0), (-1, -1), "TOP"),
            ]
        )
    )
    doc = SimpleDocTemplate(str(out), pagesize=letter, topMargin=54, bottomMargin=54)
    flowables = []
    if title:
        flowables.append(Paragraph(title, getSampleStyleSheet()["Title"]))
        flowables.append(Spacer(1, 12))
    flowables.append(table)
    doc.build(flowables)


_WRITERS = {
    ".csv": lambda r, c, o, t: to_csv(r, c, o),
    ".json": lambda r, c, o, t: to_json(r, c, o),
    ".xlsx": lambda r, c, o, t: to_xlsx(r, c, o),
    ".pdf": lambda r, c, o, t: to_pdf(r, c, o, title=t),
}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Export a dataset to CSV/JSON/XLSX/PDF.")
    parser.add_argument("input", help="JSON array file, or '-' for stdin")
    parser.add_argument("--out", required=True, help="output path; format from extension")
    parser.add_argument("--title", help="title for PDF output")
    args = parser.parse_args(argv)

    out = Path(args.out)
    writer = _WRITERS.get(out.suffix.lower())
    if writer is None:
        raise SystemExit(f"unsupported output extension {out.suffix!r}; use {sorted(_WRITERS)}")

    records = _read_records(args.input)
    cols = _columns(records)
    writer(records, cols, out, args.title)
    print(f"wrote {out} ({len(records)} records, {len(cols)} columns)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
