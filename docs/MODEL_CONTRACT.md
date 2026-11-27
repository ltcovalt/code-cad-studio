# Code CAD Studio — Model & Library Contract

This document defines the open contract between `build123d` model repositories and **Code CAD Studio**. It requires **no external PyPI package**—models may use plain Python `@dataclass` + `dataclasses.field(metadata=...)` directly, or copy Studio's zero-dependency `utils/params.py` helper (`worker/params.py`) for convenience.

---

## 1. Library Layout & Directory Discovery

A library is a directory registered in Studio's `config.json`. During discovery (`GET /models`):

- **Root-only skipped directories**: `utils/`, `exports/`, `tests/`, `test/` (helpers in `utils/` and reusable components in `components/` are added to `sys.path` and hot-reloaded on edit).
- **Always-skipped directories**: `.git/`, `.venv/`, `venv/`, `__pycache__/`, `node_modules/`, `.mypy_cache/`, `.pytest_cache/`, `.ruff_cache/`, `build/`, `dist/`, and any dot-prefixed directory.
- **Skipped Python files**: files starting with `_`, starting with `test_`, or named `setup.py`.
- **Canonical model & component layout**:
  - Preferred model layout: `models/<model_name>/<model_name>.py` (with optional `presets.json` in the same folder), discovered with `category: "model"`.
  - Preferred component layout: `components/<component_name>/<component_name>.py` (with optional `presets.json` and an `__init__.py` re-exporting public symbols), discovered with `category: "component"`.
  - Standalone script in a shared folder: `models/<shared>/<script_name>.py` or `components/<script_name>.py` (with optional `<script_name>.presets.json`).
  - Non-CAD helper modules under `components/` (containing no `create_*`/`build`/`generate`/`create` entrypoint and no `build123d`/`ocp_vscode`/`show` usage) are ignored during discovery.
  - When a file resides directly in `<name>/<name>.py`, the redundant `<name>` directory wrapper is collapsed from the sidebar tree.

---

## 2. Progressive 3-Tier Model Discovery

Every eligible `.py` file is inspected statically via Python's `ast` module (UTF-8 / UTF-8 BOM via `utf-8-sig`) and classified into one of three visible model kinds (or ignored if non-CAD):

| Tier | `kind` | Criteria | Schema & Preview Behavior |
|---|---|---|---|
| **Tier 3 (Full Parametric)** | `"parametric"` | Defines `<Model>Params` `@dataclass` + `create_<model>(params)` (or `create_*` / `build` / `generate` / `create`) | Full structured UI form, `@default_preset`, presets, and safe source `update-defaults`. |
| **Tier 2 (Function Parametric)** | `"parametric"` | Defines `create_*`, `build`, `generate`, or `create` with typed function parameters (no `@dataclass`) | Auto-generated UI controls from function signature; presets work; `update-defaults` reports unsupported. |
| **Tier 1 (Arbitrary Script)** | `"script"` | No `create_*`/`build`/`generate`/`create` entrypoint, but AST imports `build123d` / `ocp_vscode` / `ocp_viewer` or calls `show()` / `show_object()` | `GET /models/<id>/schema` returns empty `fields: []` without executing the script. `POST /models/<id>/render` executes the script in a `show()`/`show_object()` capture context, falling back to top-level `Part`/`Compound`/`BuildPart`/`Solid`/`Sketch` bindings. |
| **Syntax / Encoding Error** | `"error"` | File fails `ast.parse` or UTF-8 decoding | Remains visible in the library tree with `parse_error` populated; schema/render/export return HTTP `422` with the diagnostic message instead of silently hiding the file. |

Files that contain neither a CAD entrypoint nor `build123d`/`ocp_vscode`/`show` AST usage are ignored.

---

## 3. Canonical `modelID`

Every discovered model receives a collision-free, filesystem-safe canonical identifier:

```text
<library_id>/<rel_local_id>
```

Examples:
- `code-cad/container_lid` (from `models/container_lid/container_lid.py`, `category: "model"`)
- `code-cad/components/pull_tab` (from `components/pull_tab/pull_tab.py`, `category: "component"`)
- `demo/spool_cap` (from `models/spool_cap/spool_cap.py`)
- `my-lib/brackets/wall_mount` (from `models/brackets/wall_mount.py`)

Validation rules:
- `library_id` must be a non-empty slug (`[A-Za-z0-9._-]+`, never `.` or `..`).
- `rel_local_id` uses forward slashes `/` with no extension, no empty segments (`//`), no leading/trailing `/`, no `.` or `..`, no `:`, no `\`, and no control characters.
- In HTTP URLs (`/api/models/{id}/...` and `/models/{id}/...`), `{id}` is URL-encoded (`encodeURIComponent(id)` -> `code-cad%2Fcontainer_lid`) and decoded via `unquote` before validation.

---

## 4. Parametric `@dataclass` Schema Contract

Studio introspects `<Model>Params` strictly from **runtime Python types** and `dataclasses.Field.metadata` (never scraping `#` comments):

### 4.1 `utils/params.py` Helpers

Studio embeds the canonical zero-dependency `params.py` (`worker/params.py`), identical to `examples/demo_project/utils/params.py` and `code-cad/utils/params.py`:

- **`@default_preset(label, *, desc=None)`**: Class decorator on the root `<Model>Params` dataclass setting `__default_preset__ = {"label": ..., "description": ...}` so the canonical code defaults display a meaningful label instead of `"Default"`.
- **`param(default, *, label=None, desc=None, item_label=None, unit=None, min=None, max=None, step=None, when=None)`**: Attaches standard `dataclasses.Field.metadata` keys:
  - `label` (`str`): Override auto-humanized field label.
  - `description` (`str`): Tooltip/help text (mapped from `desc`).
  - `unit` (`str`): Unit badge (`"mm"`, `"in"`, `"°"`, `"U"`, etc.).
  - `min`, `max`, `step` (`int | float`): Numeric bounds and step increment.
  - `item_label` (`str`): Label for items inside a list field.
  - `when` (`dict[str, Any]`): Conditional visibility rules (`{"mode": "beaded"}`, `{"core.shape": ["round", "oval"]}`). Bare keys resolve relative to the current nested dataclass group; dotted keys resolve from root `<Model>Params`.
- **`param_list(factory, ...)`**: Declares `default_factory` list fields with the same UI metadata.
- **`group(factory, *, enabled=True, preset=None, params=None, label=None, desc=None, when=None)`**: Declares nested `@dataclass` parameter groups. Pass `enabled=False` on a nullable group (`SubParams | None`) to make the section optional and disabled (`None`) by default. Optionally pass `preset="<key>"` (a component preset key from the component's `presets.json`) and/or `params={...}` (sparse per-model overrides) to customize a shared component's defaults.

### 4.2 Supported Runtime Types

- Primitives: `bool`, `int`, `float`, `str`
- Constrained choices (dropdowns): `typing.Literal[...]` and `enum.Enum` subclasses
- Nullable / auto-computed parameters: `Optional[T]` or PEP 604 `T | None` (empty string `""` for numeric/boolean/list fields coerces to `None` when nullable, else falls back to the field default)
- Collections: `list[T]`, `list[SubDataclass]`, bare `list`, and `tuple` / `tuple[T, ...]`
- Nested groups: `@dataclass` fields (nested class docstrings provide section descriptions; nullable groups `SubParams | None` or groups with an `enabled: bool` field render a toggle switch in the collapsible section header). When a nested group's `@dataclass` is defined in another discovered parametric component or model in the same library, Studio automatically links it (`component_id`, `component_name`, `group_defaults`) and renders an inline component preset picker.

---

## 5. Generator Function, Exports & Side-Effect-Free Imports

- **Generator signature**: `def create_<model>(params: <Model>Params) -> Part | Compound | Solid` (returning geometry without writing files, printing to stdout, or launching viewers).
- **Side-effect-free imports**: Never import `ocp_viewer` / `ocp_vscode`, call `set_defaults()`, or construct geometry at module top-level in parametric models. Keep standalone execution inside `if __name__ == "__main__":`.
- **Standalone export flags**: Top-level `EXPORT_STL: bool = False` and `EXPORT_STEP: bool = False` must default to `False`.
- **`export_filename(params)` hook (optional)**: Returns a lowercase `snake_case` stem (without extension) reused by standalone runs and Studio exports (sanitized via `_sanitize_filename_stem`).

---

## 6. `presets.json` Schema v1 & Per-Model Scoping

Project-level presets use an explicit versioned JSON envelope (`schema_version: 1`):

```json
{
  "schema_version": 1,
  "default": {
    "verification": {
      "status": "verified",
      "note": "Printed in PETG, clean fit"
    }
  },
  "presets": {
    "prusa_spool_holder": {
      "label": "Prusa Spool Holder (12.7mm)",
      "description": "Press-fit cap for 1/2-inch rod",
      "verification": {
        "status": "usable",
        "note": "Slightly tight on painted rods"
      },
      "component_presets": {
        "pull_tab": "heavy_duty"
      },
      "params": {
        "core": {
          "shaft_diameter": 12.7
        }
      }
    }
  }
}
```

- **Component preset references (`component_presets`)**: Optional map from nested group path (e.g. `"pull_tab"`) to component preset key (e.g. `"heavy_duty"`). When resolving a preset, Studio layers `model defaults → referenced component preset → sparse params overrides`. Saving a parent preset stores only the component preset key plus any per-field overrides beyond that component preset (omitting unchanged component fields).
- **File scoping**:
  - Canonical model directory (`parent.name == file.stem`): `<dir>/presets.json`.
  - Shared-directory standalone script (`parent.name != file.stem`): `<dir>/<stem>.presets.json`.
- **Verification statuses**: `"unverified"`, `"unusable"`, `"usable"`, `"verified"`.
- **Fail-closed parsing**: Malformed or wrong-version preset files raise an explicit error and are never silently overwritten.

---

## 7. Safe Source Default Updates (`update-defaults`)

Studio can write modified parameter defaults and Base preset metadata (`@default_preset`) back into `<model>.py` via `POST /models/<id>/update-defaults`:

- **UTF-8 & BOM safe**: Preserves UTF-8 BOM (`\xef\xbb\xbf`) if present and computes AST byte spans accurately.
- **Literal-only local edits with partial apply**: Only simple literal expressions (constants, signed ints/floats, flat literal lists/tuples) defined inside the model's own `.py` file are eligible, plus `group(...)` calls on the model's `@dataclass`:
  - Nullable local groups using `group(...)` can toggle `enabled=True` ↔ `enabled=False` alongside local subfield default updates in the same pass.
  - External shared component groups using `group(...)` can update `enabled=True`/`False`, `preset="<key>"`, and sparse `params={...}` overrides directly in the model's `.py` file without mutating the shared component file.
  - Non-`group(...)` external shared component dataclasses, computed expressions (`a + b`), `Enum` defaults, and `param_list` factories are reported under `unsupported` with structured metadata (`reason_code`, `owner_class`, `in_library`, and optional `source_rel_path` / `source_file` for in-library components). When both local and unsupported fields are changed, applying updates writes only the local literals and leaves external/unsupported fields untouched; when zero local fields (and no `@default_preset` change) can be updated, apply fails closed without touching disk.
- **Optional `@default_preset` metadata**: Passing `default_preset: { "label": "...", "description": "..." }` safely adds, updates, or removes `@default_preset(...)` on the root `<Model>Params` dataclass (inserting `default_preset` into `utils.params` imports when needed). Metadata-only updates (`updated == 0`) preserve Base's print verification status.
- **Optimistic locking**: Non-dry-run applies (`dry_run: false`) require a non-empty `expected_sha256` matching the current file SHA-256 on disk; otherwise HTTP `400` or stale-file conflict is returned.
- **In-memory syntax verification & atomic replace**: Re-parses the patched Python source in memory before writing via temporary file + `os.replace` retry.

---

## 8. Export Formats & Quality Presets

`POST /models/<id>/export` accepts:

- **Formats**: `"stl"`, `"step"`, `"stp"`, `"3mf"` (anything else returns HTTP `400`).
- **Quality presets** (applied to `export_stl` and `Mesher.add_shape`):
  - `"draft"`: `linear_deflection = 0.02`, `angular_deflection = 0.3`
  - `"standard"` (default): `linear_deflection = 0.001`, `angular_deflection = 0.1`
  - `"fine"`: `linear_deflection = 0.0005`, `angular_deflection = 0.05`
  - `"custom"`: explicit `linear_deflection` (`1e-5 <= x <= 10.0`) and `angular_deflection` (`1e-3 <= x <= 3.15`).
