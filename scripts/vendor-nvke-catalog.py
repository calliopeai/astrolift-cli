#!/usr/bin/env python3
"""Refresh/check command vocabulary from an explicitly selected paired provider.

Run with that App provider's Python environment. This is a maintainer operation;
normal CLI builds never import a sibling checkout, download upstream, or execute
provider logic. The committed vocabulary remains available offline.
"""

import argparse
import json
import sys
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--providers", type=Path, required=True)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    providers = args.providers.resolve(strict=True)
    if not (providers / "nvke/upstream/contract.json").is_file():
        parser.error("select the paired App backend/providers directory")
    sys.path.insert(0, str(providers))
    from nvke.contract import upstream_revision
    from nvke.operations import actions

    document = {
        "revision": upstream_revision(),
        "actions": [
            {
                "key": action.key,
                "surface": action.surface,
                "verb": action.verb,
                "mutates": action.mutates,
                "permission": action.permission,
            }
            for action in actions()
        ],
    }
    output = Path(__file__).resolve().parents[1] / "internal/nvkecatalog/catalog.json"
    expected = json.dumps(document, indent=2) + "\n"
    if args.check:
        if output.read_text() != expected:
            parser.exit(
                1,
                "NVKE command catalog differs from the paired provider; regenerate and review\n",
            )
        print("NVKE command catalog matches the paired provider")
    else:
        output.write_text(expected)
        print(f"Wrote {output}")


if __name__ == "__main__":
    main()
