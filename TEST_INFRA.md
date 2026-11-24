# Code CAD Studio — Opaque-Box E2E Test Infrastructure (`TEST_INFRA.md`)

## 1. Purpose & Design Principles

The E2E suite in `tests/e2e/` provides **strictly opaque-box, release-gating verification** for Code CAD Studio `v0.1.0-alpha` across Features **F1–F18** and **Tiers 1–4**:

- **Build-Tag Isolation**: Every Go file in `tests/e2e/` carries `//go:build e2e`. Standard unit/race runs (`go test -race ./...`) compile zero E2E files and experience zero side effects or slowdowns.
- **Zero `internal/*` Imports**: `tests/e2e/` belongs to `package e2e_test` and uses only the Go standard library. It interacts with the system solely through:
  1. Compiled binaries (`go build -tags server` and `go build .`)
  2. HTTP + SSE requests against the running server process
  3. Direct subprocess execution of `worker/worker.py` (`CODE_CAD_WORKER_TOKEN` + `READY <port>\n` + HTTP IPC)
  4. CLI runs of `npm`, `pytest`, `go`, and filesystem inspections on isolated temporary workspaces
- **Host Isolation**: `startServer` creates a per-test `t.TempDir()` containing isolated `home/`, `xdg_config/`, `xdg_data/`, `xdg_cache/`, `appdata/`, `localappdata/`, `exports/`, and `workspace/`, overriding `HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `APPDATA`, and `LOCALAPPDATA`. Host user config (`~/.config/code-cad-studio`) and `/home/luke/repos/code-cad` are never mutated.
- **Process-Group Teardown**: Both `startServer` and `startWorker` spawn child processes with `Setpgid: true` and register `t.Cleanup` handlers that send `SIGTERM`, wait for exit, escalate to `SIGKILL` on `-pgid`, and ensure zero orphaned Python worker processes remain.
- **Honest CAD Capability Gating (`E2E_REQUIRE_CAD=1`)**:
  - `hasRealCAD(pythonPath)` executes `python -c "import build123d, ocp_tessellate"` with a clean `PYTHONPATH` (no mocks).
  - **Control-plane tests** (IPC auth, AST discovery, PEP 604 typing, UTF-8 BOM, config/presets v1, watcher/SSE, cancel/respawn, security middleware) use `tests/e2e/fixtures/mock_cad/` on `PYTHONPATH` **only** when real `build123d` is unavailable.
  - **Real-CAD geometry tests** (`T1-F4-03`, `T1-F4-04`, `T1-F7-03`..`05`, `T4-01/RealCAD`, `T4-02/RealCAD`) call `requireRealCAD(t)`: they **never** load `mock_cad`, `t.Skip` when CAD libs are absent in non-strict mode, and **`t.Fatalf`** when `E2E_REQUIRE_CAD=1` is set so no release gate can silently skip.

---

## 2. Suite Layout

```text
tests/e2e/
├── harness_test.go            # TestMain, binary builder, isolated env, process-group reaper, SSE client, CAD probe
├── tier1_worker_test.go       # T1-F1-* .. T1-F7-*  (Worker IPC, source updates, typing/BOM, perf, discovery, params.py, modelID/export/pytest)
├── tier1_server_test.go       # T1-F8-* .. T1-F10-*, T1-F12-* .. T1-F14-* (Config/Presets v1, watcher, supervision, setup, security, SSE, -tags server, scaffold)
├── tier1_artifacts_test.go    # T1-F11-*, T1-F15-* .. T1-F18-* (code-cad migration, frontend check/vitest/build/CSS/chunks, viewport/HDRI/UX, packaging/Apache-2.0/CI)
├── tier2_boundaries_test.go   # T2-01 .. T2-28 (boundary, malformed JSON, >128KB pipe, SIGKILL, idle cancel, zip-bomb/symlink, Host/Origin/415/413, proto guards)
├── tier3_interactions_test.go # T3-01 .. T3-06 (pairwise cross-feature workflows)
├── tier4_scenarios_test.go    # T4-01 .. T4-03 (demo_project, code-cad 7 models, full build & smoke gates)
└── fixtures/
    ├── mock_cad/              # Pure-Python build123d / ocp_tessellate / OCP stubs (control-plane IPC only)
    │   ├── OCP/
    │   ├── build123d/
    │   └── ocp_tessellate/
    └── models/                # Synthetic opaque-box CAD test models
        ├── arbitrary_script.py
        ├── curved_export.py
        ├── infinite_loop.py
        ├── runtime_error.py
        ├── shared/{alpha.py,beta.py}
        ├── stdlib_dataclass.py
        ├── stdout_flood.py
        ├── sub/widget.py
        ├── syntax_error.py
        └── typing_bom.py      # Starts with UTF-8 BOM (\xef\xbb\xbf)
```

Runtime-generated security zip fixtures (`symlink.zip`, `traversal.zip`, `bomb.zip`) are created programmatically inside `t.TempDir()` during `TestT2_20_ArchiveExtractionSecurity` rather than committing binary blobs.

---

## 3. Environment Variables & Resolution Order

| Variable | Default | Purpose |
|---|---|---|
| `E2E_PYTHON_PATH` | Auto-resolved | Explicit Python 3.10+ interpreter path for worker/pytest tests |
| `E2E_REQUIRE_CAD` | `0` | When `"1"`, missing `build123d`/`ocp_tessellate`/`pytest`/`npm` fails loudly (`t.Fatalf`) instead of skipping |
| `E2E_SKIP_FRONTEND_BUILD` | `0` | Skip automatic `frontend/dist` build in `ensureFrontendDist` if prebuilt |
| `E2E_SKIP_SSE_KEEPALIVE` | `0` | Skip the 15s SSE `: keepalive` wait in `TestT1_F14_04_SSEIDsAndKeepalive` during rapid iteration |

Python resolution order in `resolvePython(t)`:
1. `E2E_PYTHON_PATH`
2. `<repo>/.venv/bin/python`
3. `/home/luke/repos/code-cad/.venv/bin/python`
4. `python3.14`, `python3.13`, `python3.12`, `python3.11`, `python3.10`, `python3` in `PATH`.

---

## 4. Runner Commands

- **Compile E2E suite (no execution)**:
  ```bash
  go test -tags e2e -c ./tests/e2e/... && rm -f e2e.test
  ```
- **Standard E2E (control-plane + repo/frontend/packaging; skips real-CAD geometry if `build123d` absent)**:
  ```bash
  go test -tags e2e -v -count=1 ./tests/e2e/...
  ```
- **Strict Full-CAD E2E (Release Gate — zero skips allowed)**:
  ```bash
  E2E_REQUIRE_CAD=1 E2E_PYTHON_PATH=/path/to/cad-venv/bin/python go test -tags e2e -v -count=1 ./tests/e2e/...
  ```
- **Verify unit/race isolation**:
  ```bash
  go test -race ./...
  ```
