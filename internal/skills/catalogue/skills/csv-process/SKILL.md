---
name: csv-process
description: Read, filter, select, aggregate, and join CSV / tabular data without spinning up a full data stack.
version: "0.1.0"
---

# csv-process

Work with CSV and tabular data quickly. Use this for the everyday operations —
peek at a file, filter rows, pick columns, compute an aggregate, or join two
files on a key — without writing a one-off parser or pulling in a heavy library.

The helper at `scripts/csv_tool.py` (stdlib `csv`) handles all of these. For
large datasets or complex analytics, `pandas` is a better fit
(`pip install pandas`) — but reach for it only when the operations below aren't
enough.

## Operations

All subcommands read a CSV with a header row and write CSV (or a value) to
stdout.

```bash
# Peek: header + first N rows
python scripts/csv_tool.py head data.csv -n 5

# Filter rows by a column condition (==, !=, >, <, >=, <=, contains)
python scripts/csv_tool.py filter data.csv --where 'status == active'
python scripts/csv_tool.py filter data.csv --where 'amount > 100'

# Select / reorder columns
python scripts/csv_tool.py select data.csv --columns id,name,amount

# Aggregate a numeric column, optionally grouped
python scripts/csv_tool.py agg data.csv --column amount --op sum
python scripts/csv_tool.py agg data.csv --column amount --op sum --group-by region

# Inner-join two files on a shared key column
python scripts/csv_tool.py join orders.csv customers.csv --on customer_id
```

Supported `--op` for `agg`: `sum`, `mean`, `min`, `max`, `count`.

## Procedure

1. **Inspect first** with `head` — confirm the delimiter parsed, the header names
   are what you expect, and the data looks sane.
2. **Filter, then select** to shrink the data to what matters before aggregating
   or joining.
3. **Aggregate** with `--group-by` to get per-category totals; without it you get
   a single overall value.
4. **Join** on a key present in both files. The helper does an inner join (rows
   with a matching key in both); duplicate non-key column names from the right
   file are suffixed to avoid collisions.

## Cautions

- The helper assumes a **header row** and comma delimiter. For tab- or
  semicolon-delimited files, convert first or use `pandas`.
- Numeric ops coerce values to float and **skip cells that aren't numeric** —
  check your column is clean if a count looks low.
- Joins are in-memory; for very large files use a database or `pandas`.
- CSV from spreadsheets may carry a BOM or quoted newlines — the stdlib `csv`
  reader handles quoted fields, but eyeball the `head` output to be sure.
