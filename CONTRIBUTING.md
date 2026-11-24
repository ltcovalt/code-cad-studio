# Contributing to Code CAD Studio

Thanks for your interest in contributing to **Code CAD Studio**! This guide covers local development, code style, commit conventions, versioning, and pull request expectations.

---

## 1. Local Development Setup

### Prerequisites

- **Go** `1.26+` (see [`go.mod`](go.mod))
- **Node.js** `22+` and `npm`
- **Python** `3.10+` (`pytest` for worker unit tests; `build123d` + `ocp_tessellate` for live 3D rendering and strict E2E geometry tests)
- **Wails v3** (`wails3`) for desktop dev/packaging
  - **Linux**: `libgtk-4-dev`, `libwebkitgtk-6.0-dev`, `pkg-config`

### Common Commands

```bash
# Install frontend dependencies
npm --prefix frontend ci

# Run desktop dev mode (Vite + Wails v3 hot reload)
wails3 dev

# Build frontend & run headless server mode
npm --prefix frontend run build
go run -tags server .
```

---

## 2. Repository Architecture

```text
code-cad-studio/
├── main.go                  # Entrypoint (build-tag agnostic)
├── desktop_wails.go         # Wails v3 desktop bindings (//go:build !server)
├── desktop_server.go        # Headless HTTP/SSE server mode (//go:build server)
├── embed.go                 # Embedded frontend/dist, worker.py, params.py, demo project
├── internal/
│   ├── config/              # Versioned config.json persistence (schema_version: 1)
│   ├── presets/             # Versioned presets.json storage & sparse diffs
│   ├── server/              # Web-portable Go net/http REST API + SSE (zero Wails imports)
│   ├── setup/               # Python/uv detection, provisioning, starter downloads
│   ├── watcher/             # Recursive fsnotify hot-reload watcher
│   └── worker/              # Persistent Python worker supervisor & bearer-token IPC
├── worker/
│   ├── worker.py            # Persistent Python AST discovery, render, export & update-defaults
│   ├── params.py            # Canonical zero-dependency parameter metadata helper
│   └── tests/               # Pytest unit suite (runs without build123d)
├── frontend/                # Svelte 5 + TypeScript + Tailwind v4 + daisyUI v5 SPA
├── tests/e2e/               # Opaque-box E2E suite (//go:build e2e; see TEST_INFRA.md)
└── docs/MODEL_CONTRACT.md   # Open model & library contract
```

**Key architectural rules:**
- Keep `internal/server` free of Wails imports. Both default desktop and `-tags server` builds must compile and pass `go vet`.
- Keep `worker/params.py` and `examples/demo_project/utils/params.py` **byte-identical** and stdlib-only.
- Preserve all security invariants documented in [`SECURITY.md`](SECURITY.md) (loopback/Origin/CSP middleware, bearer-token worker IPC, shell-free desktop actions with symlink-resolved path checks, atomic file writes).

---

## 3. Code Style & Conventions

Shared whitespace/encoding defaults are defined in [`.editorconfig`](.editorconfig).

### Go
- Format all Go files with `gofmt`.
- Keep `go vet ./...`, `go vet -tags server ./...`, and `go test -race -count=1 ./...` completely clean.
- Wrap errors with `%w` and contextual messages; avoid adding third-party dependencies when the standard library suffices.
- Isolate OS-specific code using build-constrained files (`*_linux.go`, `*_unix.go`, `*_windows.go`, `*_other.go`).
- Isolate filesystem/environment state in tests with `t.TempDir()` and overridden env vars—never touch the developer's `~/.config/code-cad-studio`.

### Frontend (Svelte 5 + TypeScript + Tailwind v4 + daisyUI v5)
- Use **Svelte 5 runes** (`$state`, `$derived`, `$effect`, `$props`); avoid legacy Svelte 4 `$:` or `export let` patterns.
- Maintain strict TypeScript (`strict: true`); avoid `any` escapes; place shared API/domain types in `frontend/src/lib/types.ts`.
- Keep pure logic and API clients in `frontend/src/lib/*.ts` with co-located `*.test.ts` Vitest tests; keep `.svelte` components focused on UI.
- Use daisyUI v5 semantic theme tokens (`bg-base-100`, `text-base-content`, `btn-primary`, etc.) so all 8 curated themes work consistently. Scope `three-cad-viewer` overrides under `#cad-viewer-root` in `frontend/src/styles.css`.
- Keep `CadViewport` permanently mounted and lazy-loaded (no inline `<script>` in `index.html`) to preserve WebGL state, bundle budgets, and CSP compliance.

### Python Worker (`worker/`)
- Target Python `3.10+` (`from __future__ import annotations`, PEP 604 `X | Y` unions), PEP 8 formatting, and explicit type annotations.
- Keep `worker/worker.py` stdlib-only outside optional `build123d`/`ocp_tessellate` imports.
- Ensure `worker/tests/` passes with plain `pytest` (no `build123d` dependency).

---

## 4. Commit Message Format

This repository follows [**Conventional Commits v1.0.0**](https://www.conventionalcommits.org/):

```text
<type>(<scope>): <imperative lowercase subject>

[optional body explaining why]

[optional footer(s), e.g. BREAKING CHANGE: ...]
```

- Keep the subject line ≤ 72 characters, imperative mood ("add", "fix", "harden", not "added"), lowercase, and without a trailing period.
- **Allowed types**: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `style`, `chore`, `build`, `ci`.
- **Canonical scopes**: `frontend`, `ui`, `viewport`, `server`, `worker`, `presets`, `storage`, `watcher`, `setup`, `e2e`, `packaging`, `ci`, `docs`, `deps`.
- Indicate breaking changes with `!` after the type/scope (e.g., `feat(storage)!: ...`) and/or a `BREAKING CHANGE:` footer.

---

## 5. Branching & Pull Requests

- Create a short-lived branch from `main` named `<type>/<short-kebab-slug>` (e.g., `feat/preset-tags`, `fix/watcher-shutdown`).
- Keep each PR focused on a single feature, fix, or cohesive refactor.
- Use a **Conventional Commit** title for the PR (squash-merges reuse the PR title).
- Fill out the PR template (`.github/pull_request_template.md`) with what changed, why, and how you verified it.
- For user-visible changes, add a concise bullet under `## [Unreleased]` in [`CHANGELOG.md`](CHANGELOG.md).
- Ensure all CI checks pass before requesting review.

---

## 6. Validation Checklist

Run the full local suite matching [`.github/workflows/ci.yml`](.github/workflows/ci.yml) before pushing or opening a PR:

```bash
# Frontend
npm --prefix frontend run check && npm --prefix frontend run test && npm --prefix frontend run build

# Python worker & params.py parity
pytest -v worker/tests && diff -u worker/params.py examples/demo_project/utils/params.py

# Go (both build tags + race detector)
go vet ./... && go vet -tags server ./... && go test -race -count=1 ./...

# Binary build verification
go build -o bin/code-cad-studio . && go build -tags server -o bin/code-cad-studio-server .
```

For changes touching worker IPC, HTTP/SSE endpoints, storage schemas, file watching, security middleware, or packaging, also run the opaque-box E2E suite (see [`TEST_INFRA.md`](TEST_INFRA.md)):

```bash
go test -tags e2e -v -count=1 ./tests/e2e/...
```

---

## 7. Semantic Versioning & Releases

Code CAD Studio adheres to [**Semantic Versioning 2.0.0**](https://semver.org/) and [**Keep a Changelog 1.1.0**](https://keepachangelog.com/en/1.1.0/):

- **`0.y.z` (Pre-Release)**: Minor bumps (`0.2.0`) may include breaking changes. Pre-releases use `-alpha[.N]`, `-beta[.N]`, or `-rc[.N]` tags (e.g., `v0.1.0-alpha`).
- **`1.0.0+`**: `MAJOR` for breaking API/contract changes, `MINOR` for backward-compatible features, `PATCH` for fixes.
- **On-disk schemas** (`config.json`, `presets.json`) carry an independent integer `schema_version` (currently `1`), incremented only on breaking storage changes.

When cutting a release, keep versions synchronized across:
1. `build/config.yml` (`info.version`) and platform packaging manifests (`build/darwin/Info*.plist`, `build/linux/nfpm/nfpm.yaml`, `build/windows/info.json`, `build/windows/wails.exe.manifest`, `build/windows/nsis/wails_tools.nsh`)
2. `frontend/package.json` (`version`)
3. `CHANGELOG.md` (move `[Unreleased]` entries into `## [X.Y.Z] - YYYY-MM-DD`)
4. `SECURITY.md` (update Supported Versions if a minor/major line changes)
5. Annotated git tag `vX.Y.Z[-prerelease]`

---

## 8. Security & Licensing

- Please report security vulnerabilities privately as described in [`SECURITY.md`](SECURITY.md).
- By contributing to Code CAD Studio, you agree that your contributions will be licensed under the **Apache License, Version 2.0** ([`LICENSE`](LICENSE)).
