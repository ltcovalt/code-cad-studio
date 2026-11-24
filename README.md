# Code CAD Studio

A local-first, model-agnostic parametric 3D CAD GUI for [`build123d`](https://github.com/gumyr/build123d) Python scripts, built with **Wails v3 (Go) + Svelte 5 + TypeScript + Tailwind CSS v4 + daisyUI v5 + `three-cad-viewer`** and backed by a **persistent Python worker**.

> **Status: Pre-Release (`v0.1.0-alpha`)** — Under active development toward `v1.0.0`. See [`CHANGELOG.md`](CHANGELOG.md) for release notes and [`docs/MODEL_CONTRACT.md`](docs/MODEL_CONTRACT.md) for the model & library contract.

---

## Features

- **Multi-Library Explorer & Progressive Discovery**: Register multiple `build123d` model libraries in a nested Explorer tree with collision-free canonical IDs (`<library>/<rel>`). Automatically supports three tiers:
  1. **Full Parametric (`<Model>Params` `@dataclass`)**: Structured parameter forms with collapsible groups, section header `enabled: bool` toggles, conditional `when` visibility, `Literal`/`Enum` dropdowns, `Optional[T]` `Auto` fields, list parameters (`param_list`), `@default_preset`, and safe AST source default updates.
  2. **Function Parametric**: Auto-generated controls from typed `create_*`/`build`/`generate`/`create` function signatures.
  3. **Arbitrary `build123d` Scripts**: Zero-boilerplate preview capturing `show()`/`show_object()` or top-level shapes—plus inline diagnostics for files with syntax errors.
- **Persistent Supervised Python Worker**: Keeps `build123d`/`OCP` warm in memory for responsive live edits, with per-launch bearer-token IPC, live build timer, Cancel Build support, crash auto-recovery, and clean process-group teardown.
- **Interactive 3D CAD Viewport**: Lazy-loaded, permanently mounted `three-cad-viewer` across 3 resizable layouts (`Single Column`, `Side by Side`, `Right Parameters`) preserving camera pose, clipping planes, ortho/perspective mode, grid/axes, transparency, black edges, and part-tree visibility across parameter edits, presets, layout switches, and hot reloads.
- **Versioned Presets & Verification Notes**: Stores sparse-diff presets in versioned `presets.json` (`schema_version: 1`, or `<stem>.presets.json` in shared folders) plus user-local overrides, with print-verification badges (`unverified`, `unusable`, `usable`, `verified`) and notes.
- **Hot Reload**: Recursive `fsnotify` watcher restarts the worker on `.py` edits (including shared `components/` and `utils/`), refreshes schemas/presets while preserving unsaved tweaks, and re-renders in place over SSE.
- **Maker Workflow & Exports**: Export `.stl`, `.step`, and `.3mf` with `draft`/`standard`/`fine`/`custom` tessellation quality presets and optional `export_filename(params)` naming; quick desktop actions (**Open in Editor**, **Reveal in Folder**, **Open in Slicer**); built-in offline demo project; **New Model** scaffolding; first-launch Python/`uv` onboarding wizard; and 8 curated daisyUI themes.
- **Desktop & Web-Portable Server Modes**: Run as a native Wails v3 desktop app (Linux, macOS, Windows) or compile a headless HTTP/SSE server (`-tags server`, with included `Dockerfile.server`) serving browser downloads—backed by strict loopback/Origin/CSP security middleware.

---

## Quick Start

### Prerequisites

- **Go** `1.26+` and **Wails v3** (`wails3`) for desktop builds (Linux requires `libgtk-4-dev` and `libwebkitgtk-6.0-dev`)
- **Node.js** `22+` (for the Svelte 5 frontend)
- **Python** `3.10+` with `build123d` and `ocp_tessellate` (the first-launch onboarding wizard can detect existing venvs or provision an environment via `uv`)

### Development Mode

```bash
wails3 dev
```

### Build & Run

```bash
# Build frontend assets (embedded into Go binaries via embed.go)
npm --prefix frontend ci
npm --prefix frontend run build

# Native desktop binary
go build -o bin/code-cad-studio .
./bin/code-cad-studio

# Headless HTTP/SSE server binary (optional)
go build -tags server -o bin/code-cad-studio-server .
./bin/code-cad-studio-server
```

### Validation & Testing

```bash
# Frontend typecheck, unit tests, and production bundle
npm --prefix frontend run check && npm --prefix frontend run test && npm --prefix frontend run build

# Python worker unit tests and canonical params.py parity
pytest -v worker/tests && diff -u worker/params.py examples/demo_project/utils/params.py

# Go vet (both build tags) and race-checked unit tests
go vet ./... && go vet -tags server ./... && go test -race -count=1 ./...

# Opaque-box E2E suite (optional; set E2E_REQUIRE_CAD=1 for strict CAD geometry gates)
go test -tags e2e -v -count=1 ./tests/e2e/...
```

### First Launch

1. Launch **Code CAD Studio**—the onboarding wizard helps you configure a Python interpreter (or provision one via `uv`) and open either your own `build123d` repository or the built-in offline **Demo Project** (`examples/demo_project`).
2. Browse libraries and models from the left Explorer sidebar, or create a new parametric model with **New Model**.
3. Adjust parameters in real time, save and verify presets, or export `.STL`, `.STEP`, and `.3MF`.

---

## Documentation

- [**Model & Library Contract**](docs/MODEL_CONTRACT.md) — Discovery rules, `<Model>Params` metadata helpers (`utils/params.py`), `presets.json` v1 schema, safe `update-defaults`, and export formats.
- [**Contributing Guide**](CONTRIBUTING.md) — Development workflow, code conventions, Conventional Commits, SemVer, and pull request checklist.
- [**E2E Test Infrastructure**](TEST_INFRA.md) — Opaque-box E2E test suite architecture and strict CAD release gates.
- [**Security Policy**](SECURITY.md) — Trust & threat model, HTTP/IPC hardening, server-mode caveats, and vulnerability reporting.
- [**Changelog**](CHANGELOG.md) — Release history.

---

## License

Licensed under the **Apache License, Version 2.0**. See [`LICENSE`](LICENSE) and [`THIRD_PARTY_NOTICES.txt`](THIRD_PARTY_NOTICES.txt).
