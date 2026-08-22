---
name: data-transform
description: jq-style select, filter, and reshape over JSON and YAML — extract paths, map arrays, and filter records without writing throwaway code.
version: "0.1.0"
---

# data-transform

Pull values out of JSON/YAML and reshape them without writing a one-off script
each time. Use this when you have a structured blob and need a field, a filtered
subset, or a remapped shape.

The helper at `scripts/transform.py` (stdlib `json`) gives you a small
path-and-filter language. YAML input/output works if `pyyaml` is installed
(`pip install pyyaml`); JSON needs nothing.

## Path language

Paths address into a document with dots and brackets:

- `user.name` — nested key access.
- `items[0]` — list index.
- `items[]` — map over every element of a list (returns a list of results).
- `items[].id` — pluck `id` from every element.
- `.` — the whole document.

Examples:

```bash
# Extract a nested value
python scripts/transform.py 'data.total' < response.json

# Pluck a field from every element of a list
python scripts/transform.py 'results[].email' < users.json

# Filter a list of objects, then project two fields each
python scripts/transform.py 'items[]' --where 'status=active' \
  --pick id,name < orders.json
```

## Operations

- **select** (positional path) — navigate to a value or map over a list.
- **`--where key=value`** — keep only list elements where `key` equals `value`
  (string compare). Repeatable; all conditions must match (AND).
- **`--pick a,b,c`** — for objects (or a list of objects), keep only those keys.
- **`--input` / `--output`** — `json` (default) or `yaml`. Reads stdin, writes
  stdout.

## Procedure

1. **Look at the shape first.** Print the top level (`python transform.py '.'`)
   or the keys before assuming a path exists.
2. **Build the path incrementally.** Start broad (`data`), then narrow
   (`data.items[]`) — that's how you discover the real structure of an unfamiliar
   payload.
3. **Filter, then project.** Narrow the set with `--where`, then reduce each
   record with `--pick`. Order matters: filtering first keeps the output small.
4. **Convert formats** by setting `--input`/`--output` (e.g. read YAML config,
   emit JSON for an API).

## Cautions

- A missing path yields `null` rather than crashing — check for `null` before
  using the result.
- `--where` compares as strings; `status=active` matches the string `"active"`.
- For genuinely complex transforms (grouping, arithmetic, joins across
  documents), write a small Python script with the `json` module instead of
  forcing it through this path language.
