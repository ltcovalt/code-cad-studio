# Code CAD Studio

A local-first, model-agnostic desktop GUI for parametric [`build123d`](https://github.com/gumyr/build123d) Python scripts, built with **Wails v3 (Go) + Svelte 5 + TypeScript + Tailwind CSS v4 + daisyUI v5 + `three-cad-viewer`** and backed by a **persistent Python worker**.

> **Status: Pre-Release** — Until an initial release is published, expect breaking changes and refactors as the project structure, parameter schema, and APIs evolve toward their ideal design without backwards-compatibility shims.

## Features

- **Model-Agnostic Discovery**: Point the app at any folder of `build123d` scripts (`models/<name>/<name>.py` or standalone `.py` files) with shared `components/` and `utils/`.
- **Automatic Parameter UI**: Introspects `<Model>Params` `@dataclass` fields (`bool`, `int`, `float`, `str`, `Literal`, `Enum`, `Optional[T]` nullable `Auto` controls, nested dataclass docstrings, and structured `field.metadata` keys: `label`, `description`, `unit`, `min`, `max`, `step`, `when`). Nested `@dataclass` fields render as collapsible sections—and any nested dataclass with a non-nullable `enabled: bool` field promotes that toggle into its section header.
- **Persistent Python Worker**: Keeps `build123d`/`OCP` imported in memory for fast debounced slider/input updates without per-render Python startup overhead.
- **Interactive 3D Viewport**: Permanently mounted `three-cad-viewer` across all 3 resizable layouts (`Single Column`, `Side by Side`, `Right Parameters`) so WebGL state, camera angle, and zoom are preserved across parameter edits, presets, layout changes, and hot reloads.
- **Presets**: Synthesizes a `Default` preset from `<Model>Params()`, loads project `presets.json` + user-local `~/.config/code-cad-studio/presets/`, and saves sparse diffs.
- **Hot Reload**: Recursive `fsnotify` watcher restarts the Python worker on `.py` edits (including `components/` and `utils/`), refreshes schemas/presets while preserving active user edits, and re-renders in place via SSE.
- **Smart Native Exports**: Export `.step`, `.stl`, and `.3mf` via native OS Save-As dialogs (or browser downloads in web mode), pre-filling filenames from an optional `export_filename(params)` hook.
- **Web-Portable Core**: `internal/server` exposes a clean JSON REST API (`/api/*`) + SSE (`/events`) using standard Go `net/http` with zero Wails imports.

## Quick Start

### Development Mode

```bash
wails3 dev
```

### Typecheck & Production Build

```bash
cd frontend && npm run check && npm run build && cd ..
go test ./...
go build -o bin/code-cad-studio .
```

### Usage

1. Launch **Code CAD Studio**.
2. Click **Settings** in the top bar.
3. Choose your `build123d` project folder (e.g. your `code-cad` repo or `./examples/demo_project`).
4. Optionally select your Python interpreter (if left blank, the app auto-detects `<project>/.venv/bin/python`).
5. Pick a model from the sidebar, tweak parameters in real time, save presets, and export `.STEP`, `.STL`, or `.3MF`.
