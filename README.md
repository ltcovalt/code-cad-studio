# Code CAD Studio

A local-first, model-agnostic desktop GUI for parametric [`build123d`](https://github.com/gumyr/build123d) Python scripts, built with **Wails v3 (Go) + HTMX + Tailwind CSS v4 + daisyUI + `three-cad-viewer`** and backed by a **persistent Python worker**.

Based on the [`wails-htmx-tailwind-daisyui-template`](https://github.com/ltcovalt/wails-htmx-tailwind-daisyui-template) architecture, upgraded to Wails v3.

## Features

- **Model-Agnostic Discovery**: Point the app at any folder of `build123d` scripts (`models/<name>/<name>.py` or standalone `.py` files) with shared `components/`.
- **Automatic Parameter UI**: Introspects `<Model>Params` `@dataclass` fields (`bool`, `int`, `float`, `str`, `Literal`, `Enum`, inline comments, and `field(metadata={"min", "max", "step"})`). Nested `@dataclass` fields render as explicit collapsible sections—and any nested dataclass with an `enabled: bool` field automatically promotes that toggle into its section header. Ungrouped top-level fields render inside a collapsible **Core Parameters** section.
- **Persistent Python Worker**: Keeps `build123d`/`OCP` imported in memory for fast debounced slider/input updates without per-render Python startup overhead.
- **Interactive 3D Viewport**: `three-cad-viewer` mounted outside HTMX swap targets so camera angle and zoom are preserved across parameter edits.
- **Presets**: Synthesizes a `Default` preset from `<Model>Params()`, loads project `presets.json` + user-local `~/.config/code-cad-studio/presets/`, and saves sparse diffs.
- **Hot Reload**: Recursive `fsnotify` watcher restarts the Python worker on `.py` edits (including `components/`) and re-renders the active model in place via SSE.
- **Native Exports**: Export `.step`, `.stl`, and `.3mf` via native OS Save-As dialogs.
- **Web-Portable Core**: `internal/server` uses standard Go `net/http` with zero Wails imports.

## Quick Start

### Development Mode

```bash
wails3 dev
```

### Production Build

```bash
cd frontend && npm run build && cd ..
go build -o bin/code-cad-studio .
```

### Usage

1. Launch **Code CAD Studio**.
2. Click **Settings** in the top bar.
3. Choose your `build123d` project folder (e.g. your `code-cad` repo or `./examples/demo_project`).
4. Optionally select your Python interpreter (if left blank, the app auto-detects `<project>/.venv/bin/python`).
5. Pick a model from the sidebar, tweak parameters in real time, save presets, and export `.STEP`, `.STL`, or `.3MF`.
