# AGENTS.md — Astrolift CLI

Generic shim for AI coding agents (covers tools that don't have their
own `<TOOL>.md` shim). All shims point to **[`bootstrap.md`](./bootstrap.md)**
as the single source of truth.

Read `bootstrap.md` first.

## Top-of-mind rules

1. **No rebases.** New commits only.
2. **No AI / co-author attribution** in commits.
3. **Push submodules before parent metarepo.**
4. **`go fmt ./...` + `go vet ./...` + `make lint` before commit;**
   `make test` must pass.
5. **Don't add a command without updating `README.md` + tests.**
6. **Don't push --force to main** without explicit human approval.
7. **No cross-project references** in code or docs.

## Conventions quick-glance

- Cobra commands grouped per file under `cmd/`
- All platform I/O through `internal/api`
- Exit codes: `0` ok, `1` operational, `2` config / usage
- Every command supports `--json` via `internal/output`
- Credentials at `~/.config/astrolift/credentials/<server>.yaml` mode `0600`

Full conventions, build commands, and directory layout in `bootstrap.md`.
