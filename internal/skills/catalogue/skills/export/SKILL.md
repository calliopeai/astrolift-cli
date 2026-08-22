---
name: export
description: Write a dataset (array of records) to CSV, JSON, XLSX, or PDF through one interface — format chosen by the output extension.
version: "0.1.0"
---

# export

One way to hand a dataset off as a file. Use this at the end of a task when you
have rows of data (a list of records) and need to deliver them as CSV, JSON,
XLSX, or PDF — without remembering a different library for each.

The helper at `scripts/export.py` takes a JSON array of objects on stdin (or from
a file) and writes the format implied by the output filename's extension:

| Extension | Format | Dependency |
|---|---|---|
| `.csv` | CSV | stdlib |
| `.json` | pretty JSON | stdlib |
| `.xlsx` | Excel workbook | `openpyxl` (`pip install openpyxl`) |
| `.pdf` | table on a PDF page | `reportlab` (`pip install reportlab`) |

CSV and JSON need nothing installed. XLSX and PDF require their library — install
it before running, and only when you actually target that format.

## Input shape

A JSON array of flat objects (the records). The column order is taken from the
union of keys, first-seen order:

```json
[
  {"id": 1, "name": "Ada",  "score": 91},
  {"id": 2, "name": "Linus", "score": 88}
]
```

## Using the helper

```bash
# From a file, choosing format by extension
python scripts/export.py data.json --out report.csv
python scripts/export.py data.json --out report.xlsx
python scripts/export.py data.json --out report.pdf --title "Scores"

# From stdin (e.g. piped from another tool)
some-command | python scripts/export.py - --out out.json
```

## Procedure

1. **Assemble the records as a JSON array of objects.** Normalize each record to
   flat key/value pairs (nested objects don't map cleanly to a table — flatten or
   JSON-encode them into a single cell first).
2. **Pick the output format by naming the file** — `.csv`, `.json`, `.xlsx`, or
   `.pdf`. Install the dependency for xlsx/pdf if you're using them.
3. **Run the helper, then verify** the file exists and is non-empty
   (`ls -l report.xlsx`).

## Cautions

- Declare xlsx/pdf dependencies to the runner: `openpyxl` / `reportlab` are
  **not** stdlib.
- Inconsistent keys across records are fine — missing cells are written empty —
  but a wildly ragged dataset usually signals the records weren't normalized.
- PDF export renders a simple table; very wide datasets won't fit a page well —
  prefer CSV/XLSX for many columns.
