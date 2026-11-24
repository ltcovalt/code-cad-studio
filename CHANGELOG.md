# Changelog

All notable changes to **Code CAD Studio** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
