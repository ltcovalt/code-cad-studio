# Workspace Instructions: code-cad-studio (Code CAD Studio)

> **Scope:** Applies to the `code-cad-studio` repository.

> **Pre-Release Policy (`0.y.z` Alpha — Active Until `1.0.0`):**
> Code CAD Studio is in pre-release (`0.y.z`). Favor the cleanest, most ideal architecture and project structure—even when that introduces breaking changes across internal APIs, worker IPC, or frontend state. Aggressively remove dead or redundant code, and do **not** add backwards-compatibility shims, fallback parsers, or deprecated aliases. On-disk contracts (`config.json`, `presets.json`) use explicit integer `schema_version` envelopes and fail closed on version mismatch; bump `schema_version` only when on-disk formats change incompatibly.

---

## Architecture & Invariants

- **Web-Portable Core & Build Tags**:
  - Keep `internal/server` completely free of Wails imports (standard Go `net/http` JSON REST API + SSE).
  - Desktop-only Wails v3 bindings (`wailsDialogs`, `wailsWindow`, window chrome) live in `desktop_wails.go` (`//go:build !server`).
  - Headless HTTP/SSE server wiring lives in `desktop_server.go` (`//go:build server`).
  - `main.go` and `embed.go` remain build-tag agnostic. Both default desktop and `-tags server` builds must always compile and pass `go vet`.
- **Model & Library Contract**:
  - Treat `docs/MODEL_CONTRACT.md` as the single source of truth for 3-tier progressive discovery (`parametric` / `script` / `error`), canonical `<library_id>/<rel_local_id>` model IDs, `<Model>Params` runtime introspection (no comment scraping), `presets.json` `schema_version: 1`, safe AST `update-defaults`, and export quality presets.
  - `worker/params.py` and `examples/demo_project/utils/params.py` (and `code-cad/utils/params.py`) must remain **byte-identical** and **stdlib-only**.
- **Security & Persistence Invariants** (see `SECURITY.md`):
  - Preserve loopback `Host`, `Origin`/`Sec-Fetch-Site`, `Content-Type`, 2 MiB body-size, and strict CSP middleware (`internal/server/security.go`).
  - Preserve per-launch 256-bit `CODE_CAD_WORKER_TOKEN` bearer auth on worker IPC and process-group / Job Object teardown.
  - Desktop helper actions (`open-editor`, `reveal-file`, `open-slicer`) must execute directly without a shell and validate symlink-resolved paths against configured library/export roots.
  - Use atomic file writes (`tmp` + `rename`) and preserve corrupt-file backups rather than silently overwriting user configs or presets.

---

## Code Conventions & Style

- **Go (`internal/`, `*.go`)**:
  - Format with `gofmt`; keep `go vet` (desktop + `-tags server`) and `go test -race` clean.
  - Wrap errors with `fmt.Errorf("...: %w", err)` and prefer the Go standard library over new dependencies.
  - Isolate platform-specific code in `*_linux.go`, `*_unix.go`, `*_windows.go`, `*_other.go`.
  - Unit tests must isolate filesystem/env state via `t.TempDir()` and never mutate host `~/.config/code-cad-studio`.
- **Frontend (`frontend/src/` — Svelte 5, TypeScript, Tailwind v4, daisyUI v5)**:
  - Use Svelte 5 runes (`$state`, `$derived`, `$effect`, `$props`); do not use legacy Svelte 4 `$:` or `export let`.
  - Maintain strict TypeScript (`strict: true`, avoid `any`); centralize shared API/domain types in `src/lib/types.ts`.
  - Keep pure logic and API clients in `src/lib/*.ts` with co-located Vitest suites (`*.test.ts`); keep `.svelte` components focused on presentation and interaction.
  - Use daisyUI v5 semantic tokens (`bg-base-100`, `text-base-content`, `btn-primary`, etc.) so all 8 curated themes render properly. Scope `three-cad-viewer` CSS overrides under `#cad-viewer-root` in `src/styles.css`.
  - Keep `CadViewport` permanently mounted and lazy-loaded (`three-cad-viewer` in its own chunk, no inline `<script>` in `index.html`) so WebGL state and CSP guarantees are preserved.
- **Python Worker (`worker/`)**:
  - Target Python 3.10+ (`from __future__ import annotations`, PEP 604 `X | Y` types), PEP 8 style, explicit type annotations.
  - Keep `worker/worker.py` stdlib-only outside optional `build123d`/`ocp_tessellate` imports.
  - Keep `worker/tests/` runnable via `pytest` without `build123d` installed.

---

## Git, Commits, Versioning & Pull Requests

- **Commit Format (Conventional Commits v1.0.0)**:
  - `<type>(<scope>): <imperative lowercase subject>` (≤ 72 chars, no trailing period; use `!` or a `BREAKING CHANGE:` footer for breaking changes).
  - **Types**: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `style`, `chore`, `build`, `ci`.
  - **Canonical scopes**: `frontend`, `ui`, `viewport`, `server`, `worker`, `presets`, `storage`, `watcher`, `setup`, `e2e`, `packaging`, `ci`, `docs`, `deps`.
  - Keep commits atomic and cleanly scoped; never commit `bin/` outputs, `.venv/`, or local scratch files.
- **Commit & Push Policy**:
  - Local commits do not require prior approval once validation passes.
  - Never push to a remote without presenting a summary and receiving explicit user approval (invoking `/ship` counts as approval).
- **Semantic Versioning & Changelog**:
  - Follow SemVer 2.0.0 and Keep a Changelog 1.1.0 (`CHANGELOG.md`). Record user-visible changes under `## [Unreleased]` during development.
  - Pre-release tags use `v0.Y.Z-alpha[.N]` / `-beta[.N]` / `-rc[.N]`. On release, sync version across:
    1. `build/config.yml` (`info.version`) and generated build assets (`build/darwin/Info*.plist`, `build/linux/nfpm/nfpm.yaml`, `build/windows/info.json`, `build/windows/wails.exe.manifest`, `build/windows/nsis/wails_tools.nsh`)
    2. `frontend/package.json` (`version`)
    3. `CHANGELOG.md` (cut dated release section from `[Unreleased]`)
    4. `SECURITY.md` (Supported Versions, when a minor/major line changes)
    5. Annotated git tag (`vX.Y.Z[-prerelease]`)
- **Branching & Pull Requests**:
  - Direct commits on `main` are fine for solo pre-release work. When using branches/PRs (see `CONTRIBUTING.md`): branch as `<type>/<short-slug>`, use a Conventional Commit PR title, keep scope focused, update `CHANGELOG.md` (`[Unreleased]`) for user-visible changes, and ensure CI passes before merge.

---

## Validation Gate

Run relevant checks from the `code-cad-studio` root before finishing changes (matching `.github/workflows/ci.yml`):

```bash
# Frontend (check + unit tests + production bundle)
npm --prefix frontend run check && npm --prefix frontend run test && npm --prefix frontend run build

# Python worker (unit tests + canonical params.py parity)
pytest -v worker/tests && diff -u worker/params.py examples/demo_project/utils/params.py

# Go (vet both build tags + race-enabled unit tests)
go vet ./... && go vet -tags server ./... && go test -race -count=1 ./...
```

- When modifying `main.go`, `desktop_*.go`, `embed.go`, or packaging: verify both binaries build (`go build -o bin/code-cad-studio . && go build -tags server -o bin/code-cad-studio-server .`).
- When modifying worker IPC, HTTP/SSE server, storage schemas, watcher, security middleware, or before cutting a release: run the opaque-box E2E suite (`go test -tags e2e -v -count=1 ./tests/e2e/...`; set `E2E_REQUIRE_CAD=1` for strict release gating—see `TEST_INFRA.md`).

---

## Key Reference Docs

- [`docs/MODEL_CONTRACT.md`](docs/MODEL_CONTRACT.md) — Library discovery, `@dataclass` schema contract, `presets.json` v1, `update-defaults`, exports
- [`SECURITY.md`](SECURITY.md) — Threat model, HTTP/IPC hardening, server-mode caveats
- [`TEST_INFRA.md`](TEST_INFRA.md) — Opaque-box E2E suite architecture and runner commands
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — Contributor workflow, style guide, and PR expectations
- [`CHANGELOG.md`](CHANGELOG.md) — Release history and `[Unreleased]` entries
