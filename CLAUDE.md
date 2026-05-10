# CLAUDE.md — Astrolift CLI

Technical reference for AI agents working in this repo lives in
**[`bootstrap.md`](./bootstrap.md)**. Read it first.

The full command tree and behavioral spec live in `specs/10-cli.md` in
the Astrolift metarepo; this repo implements that surface.

## Top-of-mind rules

These also live in `bootstrap.md` § Conventions; surfacing here so a
quick-glance agent sees them:

1. **No rebases.** New commits only. No `git rebase`.
2. **No AI / co-author attribution** in commits or PR bodies.
3. **Push submodules before the parent metarepo.** This repo is a
   submodule of `astrolift`.
4. **`go fmt ./...` + `go vet ./...` + `make lint` before commit.**
   `make test` must pass too.
5. **Don't add a command without updating `README.md` + tests.** The
   help-text surface is the doc surface.
6. **Don't push --force to main** without explicit human approval.
7. **No cross-project references** — reference repos are learning
   sources only; don't link or cite them from this repo.

## Conventions quick-glance

- Cobra commands: one resource group per file under `cmd/`
- All platform calls go through `internal/api`; never hand-roll HTTP
  in command bodies
- Errors wrap with `%w`; return from `RunE`, don't `os.Exit` inside
  a command (see `cmd/ci.go` `configErr` for the documented exception)
- Exit codes: `0` ok, `1` operational failure, `2` config / usage error
- Every command supports `--json` via `internal/output`
- Credentials live at `~/.config/astrolift/credentials/<server>.yaml`
  mode `0600` — refuse looser perms

For everything else, see `bootstrap.md`.
