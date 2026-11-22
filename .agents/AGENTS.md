# Workspace Instructions: code-cad-studio (Code CAD Studio)

> **Scope:** Applies to the `code-cad-studio` repository.

> **Pre-Release Policy (Active Until Further Notice):**
> Code CAD Studio is pre-release. Always favor the cleanest, most ideal architecture and project structure—even when that introduces breaking changes across schemas, APIs, worker contracts, or frontend state. Aggressively remove dead, redundant, or unnecessary code, and do **not** add backwards-compatibility shims, fallback parsers, migration layers, or deprecated aliases until an official release is cut.

## Architecture & Conventions

- **Web-Portable Core**: Keep `internal/server` free of Wails imports (standard Go `net/http` JSON REST API + SSE). Desktop-only bindings (`wailsDialogs`, `wailsWindow`) live in `main.go`.
- **Schema Contract**: `worker/worker.py` introspects `<Model>Params` `@dataclass` definitions strictly from runtime types, `dataclasses.Field.metadata` (`label`, `description`, `unit`, `min`, `max`, `step`, `when`), root class `__default_preset__`, and nested class `__doc__`—no brittle source-text or comment scraping.
- **Project Discovery**: Skip non-model directories (`utils/`, `components/`, `exports/`, caches, tests) during discovery while hot-reloading edits in `components/` and `utils/`.
- **Validation**: Before finishing changes in `code-cad-studio`, run from the `code-cad-studio` root:
  - `npm --prefix frontend run check` and `npm --prefix frontend run build`
  - `go test ./...`
- **Git & Commit Protocol**: Local commits do not require prior approval once validation passes. Never push to a remote without presenting a summary and receiving explicit user approval (invoking `/ship` counts as approval).
