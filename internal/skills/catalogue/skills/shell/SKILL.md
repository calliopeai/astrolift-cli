---
name: shell
description: Run shell commands safely — correct quoting, exit-code handling, output capture, and timeouts.
version: "0.1.0"
---

# shell

The runtime gives you a real shell. Use it well: most command failures and
data-loss accidents come from sloppy quoting, ignored exit codes, or unbounded
commands — not from the tool itself. This skill is the discipline, not a wrapper.

## Core rules

1. **Check the exit code.** `0` means success; anything else is a failure. Never
   assume a command worked because it printed something. In a script, fail fast:
   ```bash
   set -euo pipefail
   ```
   `-e` exits on the first error, `-u` errors on unset variables, `-o pipefail`
   makes a pipeline fail if any stage fails (not just the last).
2. **Quote every variable.** Use `"$var"`, not `$var`. Unquoted expansions split
   on whitespace and glob — the classic source of "it worked until a path had a
   space." For lists, use arrays: `args=(--flag "value"); cmd "${args[@]}"`.
3. **Capture output deliberately.**
   - `out=$(cmd)` captures stdout into a variable (trailing newlines stripped).
   - `cmd > out.txt 2> err.txt` splits streams to files.
   - `cmd > all.txt 2>&1` merges stderr into stdout.
   - `cmd 2>/dev/null` discards stderr only when you genuinely don't need it.
4. **Bound long-running commands** with a timeout so a hang can't stall the
   agent:
   ```bash
   timeout 30s some-slow-command
   ```
   `timeout` exits `124` when it kills the command — check for it.
5. **Inspect before you destroy.** For `rm`, `mv`, glob deletes, and anything
   with `--force`: print what will be affected first (`ls`, `echo`, `--dry-run`),
   confirm it's what you expect, then run. Never `rm -rf` a path built from an
   unvalidated variable.

## Composition

- **Pipes** `a | b` stream `a`'s stdout into `b`'s stdin. With `pipefail` set,
  the pipeline's exit code reflects any failed stage.
- **`&&` / `||`** chain on success / failure: `build && test` runs `test` only if
  `build` succeeded.
- **`xargs`** turns a list into arguments; prefer `-0` with `find … -print0` to be
  safe with odd filenames: `find . -name '*.tmp' -print0 | xargs -0 rm`.
- **Subshells** `( … )` isolate `cd` and environment changes so they don't leak
  into the rest of your script.

## Safety checklist before running a command

- Is every variable quoted?
- Will a non-zero exit be noticed (or is this in a `set -e` script)?
- Could this delete or overwrite something? Did I preview it?
- Could it hang? Does it need a `timeout`?
- Am I about to leak a secret into the process list or logs? (Pass secrets via
  environment variables or files, not as command-line arguments — argv is
  visible to other processes.)
