# Changelog

All notable changes to **Code CAD Studio** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **First-Class Component Management & Component Preset References:** Studio now
  discovers parametric components under `components/` into a dedicated
  **Components** sidebar section with full 3D preview, parameter editing,
  presets, verification badges, and `update-defaults`. Parent models that compose
  a component (or another model) via `group(...)` automatically link to it,
  rendering an inline component preset selector, jump-to-component link,
  nullable group toggles (`T | None = group(..., enabled=False)`), and
  preset-by-name references (`component_presets` in `presets.json`) that store
  only sparse per-field overrides instead of duplicating component parameters.
- **Auto-Bootstrapped `uv` & Managed Python 3.14 Setup:** First-run environment
  setup now automatically downloads a SHA-256-verified `uv` release when `uv` is
  not already installed and provisions a shared managed Python `3.14` virtual
  environment (`uv venv --python 3.14 --python-preference only-managed`) with
  `build123d` and `ocp_tessellate`—requiring no pre-installed system Python.
- **Viewport Bottom Status & Action Bar:** Added a dedicated status bar beneath
  the 3D viewport showing the active filename, quick desktop actions (Edit,
  OS-aware Reveal, Rebuild), live build timer & Cancel button, bounding box &
  timing telemetry, an expandable full-width build error/traceback drawer, and
  a single **`Export…`** action (`Ctrl/Cmd+E`).
- **Export Modal:** Consolidated `.STEP`, `.STL`, and `.3MF` export into a
  labeled modal with format cards, filename stem + extension preview, mesh
  quality presets (`Draft`, `Standard`, `Fine`, and `Custom` deflections), and
  post-export Reveal / Open in Slicer actions.
- **Explorer Right-Click Context Menu:** Added context menus on models (Open in
  Editor, Reveal, Copy Relative Path, Export…, Rebuild) and libraries (New
  Model…, Reveal Library Folder, Refresh Libraries).
- **Smart Default Preset Names:** Saving a preset from the base model defaults
  now pre-fills a humanized name derived from the model's
  `export_filename(params)` (e.g., `can_lid_54mm_tpu` → `54mm TPU`) with
  collision fallback (`(Modified)`, `(2)`, …) and select-all on focus. When
  editing an already-saved preset, overwrite-in-place remains the default.
- **Component-Aware Partial `update-defaults`:** Updating source defaults now
  allows applying local parameter changes in `<model>.py` even when imported
  shared component dataclasses (e.g., `components/pull_tab.py`) were also
  modified. Skipped external component fields are grouped by class/file with
  shared-impact context and an **Open in Editor** action for in-library files.

### Changed
- **Consolidated Default Library Directory:** Default managed downloads and
  built-in offline demo projects now live under `~/Documents/code-cad-studio/`
  (`code-cad` and `demo`).
- **Decluttered Parameters & Refined Highlights:** Stripped redundant model
  metadata and export controls from the Parameters pane, added subtle data-pill
  model counts, unified active Explorer & dropdown highlights to subtle base
  shading with a primary checkmark, and softened form-control focus rings.
- **Standardized Library Terminology:** Replaced mixed "model folder" /
  "repository" copy and removed unnecessary git references from setup prompts.

### Fixed
- **Frameless Window Resize Grip & Edge Strips:** Added a subtle bottom-right
  corner resize handle and thin right/bottom edge resize strips on unmaximized
  Linux/Windows desktop windows so resizing remains effortless even when a
  right-side Parameters scrollbar is visible.
- **Non-Destructive Offline Demo:** Adding the built-in offline demo from
  Onboarding or Settings now appends alongside existing/draft libraries instead
  of replacing them or skipping Step 1.
- **Silent Native Dialog Cancellation (Windows):** Closing or cancelling native
  file/folder pickers no longer surfaces a `"cancelled by user"` error banner.
- **Windows Case-Insensitive Path Deduplication:** Resolved canonical on-disk
  casing during local library detection and normalized Windows path comparisons
  so already-added libraries are not re-detected or duplicated with `(repos)`.

## [0.1.0-alpha] - 2026-11-23

Initial public pre-release of **Code CAD Studio**, a local-first parametric 3D
CAD GUI for [`build123d`](https://github.com/gumyr/build123d).

### Added
- **Desktop & Web-Portable Server Modes:** Wails v3 (`v3.1.0`) desktop shell
  with frameless/titlebar-inset native window support alongside a headless
  HTTP/SSE server build (`-tags server`) with browser file downloads.
- **Progressive Model Discovery & Parametric Inspector:** Automatic introspection
  of `<Model>Params` `@dataclass` schemas (`utils/params.py` metadata, nested
  collapsible cards, section toggles, conditional `when` fields, `Literal`
  dropdowns, `Optional[T]` auto fields, and `@default_preset`), plus zero-boilerplate
  preview for standard `build123d` scripts and inline error reporting for
  syntax-errored models.
- **Persistent Versioned Contracts (`docs/MODEL_CONTRACT.md`):** Versioned
  `config.json` (`schema_version: 1`), versioned `presets.json` (`schema_version: 1`)
  with per-model `<stem>.presets.json` scoping for shared directories, canonical
  `<library>/<rel>` model IDs, atomic writes, and corrupt-file backup preservation.
- **Interactive 3D CAD Viewport (`three-cad-viewer`):** Lazy-loaded WebGL CAD
  viewport preserving camera pose, clipping planes, ortho/perspective toggle,
  grid/axes, transparency, black edges, active tab, and part-tree visibility
  across live parameter edits, with WebGL context-loss recovery and offline HDRI
  blocking.
- **Maker Ergonomics & Export Workflow:** Live build timer and Cancel Build
  button, Open in Editor / Reveal in Folder / Open in Slicer desktop actions,
  `.stl`, `.step`, and `.3mf` export with `draft`/`standard`/`fine`/`custom`
  tessellation quality presets, built-in offline demo project & New Model
  scaffolding, onboarding Python/`uv` provisioning, and 8 curated DaisyUI themes.
- **Security & Worker Supervision:** Per-launch bearer-token worker IPC,
  loopback `Host`/`Origin`/`Sec-Fetch-Site`/`Content-Type` and 2 MiB body-size
  middleware, strict CSP headers, crash auto-recovery, unbounded stdout/stderr
  draining, process-group / Windows Job Object cleanup, and SHA-256 verification
  for source default updates and starter archive downloads.
- **Cross-Platform Packaging, Licensing & CI:** Apache-2.0 licensing (`LICENSE`,
  `THIRD_PARTY_NOTICES.txt`), `SECURITY.md`, custom Code CAD Studio icon assets,
  macOS `.app`/`.dmg` + Windows NSIS + Linux GTK4/WebKitGTK 6.0 packaging,
  runnable Python+`build123d` `Dockerfile.server`, and multi-OS GitHub Actions CI.
