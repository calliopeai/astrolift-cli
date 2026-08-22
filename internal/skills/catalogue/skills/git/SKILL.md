---
name: git
description: Everyday git — clone, branch, stage, diff, commit, and push, with a safe, predictable workflow.
version: "0.1.0"
---

# git

Version-control basics done correctly. `git` is in the runtime; this skill is the
workflow that keeps your changes reviewable and avoids the common footguns
(committing the wrong files, pushing to the wrong place, mangling history).

## Standard workflow

1. **Know where you are.** Before touching anything:
   ```bash
   git status            # what's changed / staged / untracked
   git branch --show-current
   ```
2. **Branch off the base.** Never commit straight to `main` unless explicitly
   told to. Create a focused branch from an up-to-date base:
   ```bash
   git switch -c feature/short-description
   ```
3. **Review before you stage.** Read your own diff — it's the cheapest bug catch
   there is:
   ```bash
   git diff               # unstaged changes
   git diff --staged      # what a commit would include
   ```
4. **Stage deliberately.** Prefer naming paths over `git add -A`, so you don't
   sweep in unrelated or generated files:
   ```bash
   git add path/to/changed_file.py path/to/other.py
   ```
5. **Commit with a meaningful message.** One logical change per commit. First
   line ≤ ~72 chars, imperative mood, explaining the *why*:
   ```bash
   git commit -m "Fix off-by-one in pagination cursor"
   ```
6. **Push to your branch.** Set the upstream on the first push:
   ```bash
   git push -u origin feature/short-description
   ```

## Inspecting

```bash
git log --oneline -20            # recent history, compact
git log --oneline --stat -5       # with files changed
git show <sha>                    # a single commit's diff
git diff main...HEAD              # everything your branch adds vs main
```

## Cautions

- **Never rewrite shared history.** No `rebase` or `commit --amend` on commits
  you've already pushed or that others may have. New commits only.
- **Never force-push** a shared branch (especially `main`).
- **Check the remote and branch before pushing** (`git remote -v`,
  `git branch --show-current`) — pushing to the wrong place is hard to undo
  cleanly.
- **Don't commit secrets or build artifacts.** Verify with `git status` /
  `git diff --staged` that only intended files are staged; respect
  `.gitignore`.
- Configure identity locally for the repo if it isn't already set
  (`git config user.name` / `user.email`), rather than relying on a global
  default that may be wrong for this project.
