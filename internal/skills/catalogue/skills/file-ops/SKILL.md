---
name: file-ops
description: Read, write, search, and edit files in the workspace safely — atomic writes, exact-match edits, and recursive content search.
version: "0.1.0"
---

# file-ops

Work with files in the workspace without corrupting them. Reads and searches are
low-risk; **writes and edits are where things go wrong** (clobbering a file,
ambiguous replacements, partial writes). This skill keeps those safe.

The helper at `scripts/file_tool.py` (stdlib only) covers the two error-prone
operations: an **exact-string edit** with a uniqueness guard, and a **recursive
search**. Plain reads/writes you can do with the shell or your built-in tools.

## Reading

- Read the whole file when it's small. For large files, read a line range so you
  don't flood context.
- Always assume a file may not exist — handle that case rather than crashing.

## Writing

- **Write atomically.** Write to a temp file in the same directory, then rename
  it over the target. A half-written file is worse than no write. The helper's
  `write` does this for you.
- **Don't clobber blindly.** Before overwriting a file you didn't create, read it
  first so you know what you're replacing.
- Preserve the file's existing newline at EOF and indentation style; gratuitous
  whitespace churn makes diffs unreadable.

## Editing (in place)

Prefer a **targeted replacement of an exact, unique string** over rewriting the
whole file — it's safer and produces a clean diff.

```bash
# Replace an exact string; fails if the old string isn't found exactly once
python scripts/file_tool.py edit path/to/file.py \
  --old 'DEBUG = True' --new 'DEBUG = False'

# Replace every occurrence on purpose
python scripts/file_tool.py edit path/to/file.py \
  --old 'old_name' --new 'new_name' --all
```

The edit **refuses to run** if `--old` appears zero times (nothing to do) or more
than once without `--all` (ambiguous) — that guard prevents the most common
edit mistakes.

## Searching

```bash
# Recursively find files whose contents match a regex; prints file:line:text
python scripts/file_tool.py search 'TODO|FIXME' src/

# Restrict by filename glob
python scripts/file_tool.py search 'import requests' . --glob '*.py'
```

## Cautions

- Never write outside the workspace directory.
- Make a copy before a risky bulk edit you might need to undo.
- Binary files: don't treat them as text — the search helper skips files it can't
  decode as UTF-8.
