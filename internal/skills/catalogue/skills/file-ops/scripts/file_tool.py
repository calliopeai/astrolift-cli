#!/usr/bin/env python3
"""Safe file edit + search helpers for agents (stdlib only).

Two subcommands covering the error-prone file operations:

* ``edit`` — replace an exact string in a file. Refuses to run unless the
  old string is present, and (without ``--all``) unless it is *unique* —
  so an ambiguous replacement can't silently corrupt the file. Writes
  atomically (temp file + rename).
* ``search`` — recursively grep file contents for a regex, optionally
  filtered by a filename glob. Skips non-UTF-8 (binary) files.

Examples::

    python file_tool.py edit config.py --old 'DEBUG = True' --new 'DEBUG = False'
    python file_tool.py search 'TODO|FIXME' src/ --glob '*.py'
"""

from __future__ import annotations

import argparse
import fnmatch
import os
import re
import sys
import tempfile
from pathlib import Path


def atomic_write(path: Path, text: str) -> None:
    """Write ``text`` to ``path`` atomically (temp file in same dir + rename)."""
    directory = path.parent
    fd, tmp = tempfile.mkstemp(dir=directory, prefix=f".{path.name}.", suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(text)
        os.replace(tmp, path)
    except BaseException:
        # Don't leave a stray temp file behind on failure.
        if os.path.exists(tmp):
            os.unlink(tmp)
        raise


def edit_file(path: Path, old: str, new: str, *, replace_all: bool) -> int:
    """Replace ``old`` with ``new`` in ``path``; return number of replacements.

    Raises ``ValueError`` if ``old`` is absent, or present more than once
    when ``replace_all`` is False (an ambiguous edit).
    """
    text = path.read_text(encoding="utf-8")
    count = text.count(old)
    if count == 0:
        raise ValueError(f"old string not found in {path}")
    if count > 1 and not replace_all:
        raise ValueError(
            f"old string appears {count} times in {path}; pass --all to replace every occurrence"
        )
    new_text = text.replace(old, new) if replace_all else text.replace(old, new, 1)
    atomic_write(path, new_text)
    return count if replace_all else 1


def _iter_files(root: Path, glob: str | None):
    if root.is_file():
        yield root
        return
    for dirpath, dirnames, filenames in os.walk(root):
        # Skip common noise directories.
        dirnames[:] = [d for d in dirnames if d not in {".git", "node_modules", "__pycache__"}]
        for name in filenames:
            if glob and not fnmatch.fnmatch(name, glob):
                continue
            yield Path(dirpath) / name


def search(pattern: str, root: Path, glob: str | None) -> list[str]:
    """Return ``path:line:text`` matches for ``pattern`` under ``root``."""
    regex = re.compile(pattern)
    hits: list[str] = []
    for file in _iter_files(root, glob):
        try:
            with file.open("r", encoding="utf-8") as fh:
                for lineno, line in enumerate(fh, start=1):
                    if regex.search(line):
                        hits.append(f"{file}:{lineno}:{line.rstrip()}")
        except (UnicodeDecodeError, OSError):
            # Binary or unreadable file — skip it.
            continue
    return hits


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Safe file edit + search.")
    sub = parser.add_subparsers(dest="action", required=True)

    edit = sub.add_parser("edit", help="replace an exact string in a file")
    edit.add_argument("path", type=Path)
    edit.add_argument("--old", required=True, help="exact string to replace")
    edit.add_argument("--new", required=True, help="replacement string")
    edit.add_argument("--all", action="store_true", help="replace every occurrence")

    srch = sub.add_parser("search", help="recursively grep file contents")
    srch.add_argument("pattern", help="regular expression")
    srch.add_argument("root", type=Path, nargs="?", default=Path("."))
    srch.add_argument("--glob", help="filename glob filter, e.g. '*.py'")

    args = parser.parse_args(argv)

    if args.action == "edit":
        try:
            n = edit_file(args.path, args.old, args.new, replace_all=args.all)
        except (OSError, ValueError) as exc:
            print(f"edit failed: {exc}", file=sys.stderr)
            return 1
        print(f"replaced {n} occurrence(s) in {args.path}")
        return 0

    hits = search(args.pattern, args.root, args.glob)
    for hit in hits:
        print(hit)
    return 0 if hits else 1


if __name__ == "__main__":
    raise SystemExit(main())
