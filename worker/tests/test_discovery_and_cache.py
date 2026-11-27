from __future__ import annotations

import ast
import json
import os
from pathlib import Path
import sys
import types
import pytest

from conftest import http_request, running_worker_server
import worker


class _DummyWrapped:
    pass


class _DummyShape:
    def __init__(self, label: str = "") -> None:
        self.wrapped = _DummyWrapped()
        self.label = label


def test_progressive_3tier_discovery_and_canonical_ids(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    (lib_dir / "models" / "box_part").mkdir(parents=True)
    (lib_dir / "models" / "shared").mkdir(parents=True)
    (lib_dir / "utils").mkdir(parents=True)

    # Tier 3 canonical folder <name>/<name>.py -> presets.json
    (lib_dir / "models" / "box_part" / "box_part.py").write_text(
        '"""Parametric box."""\n'
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class BoxPartParams:\n"
        "    w: float = 10.0\n"
        "def create_box_part(params: BoxPartParams):\n"
        "    return None\n",
        encoding="utf-8",
    )

    # Shared folder standalone scripts -> <stem>.presets.json
    (lib_dir / "models" / "shared" / "script_show.py").write_text(
        '"""Arbitrary script using show()."""\n'
        "from build123d import Box\n"
        "from ocp_vscode import show\n"
        "b = Box(1, 2, 3)\n"
        'show(b, names=["CustomBox"], colors=["#112233"])\n',
        encoding="utf-8",
    )
    (lib_dir / "models" / "shared" / "broken.py").write_text(
        "def broken(:\n",
        encoding="utf-8",
    )
    # Non-CAD script should be ignored
    (lib_dir / "models" / "shared" / "notes.py").write_text(
        "X = 42\n",
        encoding="utf-8",
    )
    # utils/ should be skipped at root
    (lib_dir / "utils" / "helper.py").write_text(
        "def create_ignored(): pass\n",
        encoding="utf-8",
    )

    with running_worker_server(
        libraries=[{"id": "code-cad", "name": "Code CAD", "path": str(lib_dir)}],
        auth_token="tok",
    ) as (_state, token, port):
        auth = {"Authorization": f"Bearer {token}"}
        status, _, raw = http_request(port, "GET", "/models", headers=auth)
        assert status == 200
        models = json.loads(raw)["models"]
        by_id = {m["id"]: m for m in models}

        assert set(by_id.keys()) == {
            "code-cad/box_part",
            "code-cad/shared/script_show",
            "code-cad/shared/broken",
        }
        assert by_id["code-cad/box_part"]["kind"] == "parametric"
        assert Path(by_id["code-cad/box_part"]["presets_path"]).name == "presets.json"

        assert by_id["code-cad/shared/script_show"]["kind"] == "script"
        assert Path(by_id["code-cad/shared/script_show"]["presets_path"]).name == "script_show.presets.json"

        assert by_id["code-cad/shared/broken"]["kind"] == "error"
        assert "SyntaxError" in by_id["code-cad/shared/broken"]["parse_error"]

        # Tier 1 script schema returns empty fields without executing the script
        status, _, raw = http_request(port, "GET", "/models/code-cad%2Fshared%2Fscript_show/schema", headers=auth)
        assert status == 200
        schema = json.loads(raw)
        assert schema["fields"] == []
        assert schema["defaults"] == {}
        assert schema["model"]["kind"] == "script"

        # Error model returns 422 on schema and render
        status, _, raw = http_request(port, "GET", "/models/code-cad%2Fshared%2Fbroken/schema", headers=auth)
        assert status == 422
        assert "SyntaxError" in json.loads(raw)["error"]

        status, _, raw = http_request(
            port,
            "POST",
            "/models/code-cad%2Fshared%2Fbroken/render",
            headers=auth,
            body={"params": {}},
        )
        assert status == 422


def test_tier1_script_show_capture_and_toplevel_fallback(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    lib_dir = tmp_path / "lib"
    (lib_dir / "models").mkdir(parents=True)

    (lib_dir / "models" / "via_show.py").write_text(
        "from build123d import Box\n"
        "from ocp_vscode import show\n"
        "part_a = Box(2, 2, 2)\n"
        'show(part_a, names=["CapturedBox"], colors=["#abcdef"])\n',
        encoding="utf-8",
    )
    (lib_dir / "models" / "via_var.py").write_text(
        "from build123d import Box\n"
        "result = Box(4, 5, 6)\n",
        encoding="utf-8",
    )

    # Provide lightweight fake build123d module so Tier 1 capture/fallback runs cleanly even without OCCT installed
    fake_b3d = types.ModuleType("build123d")
    fake_b3d.Box = lambda *a, **kw: _DummyShape("Box")  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "build123d", fake_b3d)
    monkeypatch.setattr(worker, "HAS_BUILD123D", True)
    monkeypatch.setattr(worker, "_CAD_WARMUP_DONE", worker.threading.Event())
    worker._CAD_WARMUP_DONE.set()
    monkeypatch.setattr(worker, "_CAD_WARMUP_STARTED", True)

    state = worker.WorkerState([{"id": "lib", "name": "Lib", "path": str(lib_dir)}])
    state.discover_models()

    _, res_show, _, _ = state._build_shape("lib/via_show", {})
    parts_show = worker._collect_named_parts(res_show, "via_show")
    assert len(parts_show) == 1
    assert parts_show[0][0] == "CapturedBox"
    assert parts_show[0][2] == "#abcdef"

    _, res_var, _, _ = state._build_shape("lib/via_var", {})
    parts_var = worker._collect_named_parts(res_var, "via_var")
    assert len(parts_var) == 1
    assert parts_var[0][0] == "Box"


def test_ast_discovery_cache_hits_and_invalidates(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    lib_dir = tmp_path / "lib"
    (lib_dir / "models").mkdir(parents=True)
    p1 = lib_dir / "models" / "part_one.py"
    p1.write_text("def create_part_one(): return None\n", encoding="utf-8")

    parse_calls = {"count": 0}
    real_parse = ast.parse

    def counting_parse(*args, **kwargs):
        parse_calls["count"] += 1
        return real_parse(*args, **kwargs)

    monkeypatch.setattr(worker.ast, "parse", counting_parse)

    state = worker.WorkerState([{"id": "lib", "name": "Lib", "path": str(lib_dir)}])
    # WorkerState.__init__ calls configure -> discover_models once
    assert parse_calls["count"] == 1

    # Second discover without changes hits (mtime_ns, size) cache
    state.discover_models()
    assert parse_calls["count"] == 1

    # Modify file content & bump mtime -> invalidates cache for changed file
    p1.write_text("def create_part_one():\n    return 1\n", encoding="utf-8")
    st = p1.stat()
    os.utime(p1, ns=(st.st_atime_ns, st.st_mtime_ns + 1_000_000))
    state.discover_models()
    assert parse_calls["count"] == 2

    # Delete file -> pruned from _ast_cache
    p1.unlink()
    state.discover_models()
    assert str(p1) not in state._ast_cache


def test_render_cache_byte_cap_and_deterministic_cache_tag(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    lib_dir = tmp_path / "lib"
    (lib_dir / "models").mkdir(parents=True)
    p1 = lib_dir / "models" / "cube.py"
    p1.write_text(
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class CubeParams:\n"
        "    size: float = 10.0\n"
        "def create_cube(params: CubeParams):\n"
        "    return params.size\n",
        encoding="utf-8",
    )

    monkeypatch.setattr(worker, "HAS_BUILD123D", True)
    monkeypatch.setattr(worker, "_CAD_WARMUP_DONE", worker.threading.Event())
    worker._CAD_WARMUP_DONE.set()
    monkeypatch.setattr(worker, "_CAD_WARMUP_STARTED", True)
    # Keep a small byte cap to verify LRU byte eviction deterministically
    monkeypatch.setattr(worker, "MAX_RENDER_CACHE_BYTES", 15000)

    seen_prefixes: list[str] = []

    def fake_tessellate(result, model_name, cache_prefix=""):
        seen_prefixes.append(cache_prefix)
        payload_buf = "A" * 5000
        shapes = {
            "version": 3,
            "name": model_name,
            "id": f"/{model_name}",
            "loc": [[0.0, 0.0, 0.0], [0.0, 0.0, 0.0, 1.0]],
            "bb": {"xmin": 0.0, "xmax": 1.0, "ymin": 0.0, "ymax": 1.0, "zmin": 0.0, "zmax": 1.0},
            "parts": [{"shape": {"vertices": {"dtype": "float32", "codec": "b64", "buffer": payload_buf}}}],
        }
        bbox = {"xmin": 0.0, "xmax": 1.0, "ymin": 0.0, "ymax": 1.0, "zmin": 0.0, "zmax": 1.0, "dx": 1.0, "dy": 1.0, "dz": 1.0}
        return shapes, bbox

    monkeypatch.setattr(worker, "tessellate_result", fake_tessellate)

    state = worker.WorkerState([{"id": "lib", "name": "Lib", "path": str(lib_dir)}])

    # Verify deterministic cache_prefix after clearing cache (not dependent on monotonic_ns)
    r1 = state.render("lib/cube", {"size": 10.0})
    assert r1["cached"] is False
    r1_cached = state.render("lib/cube", {"size": 10.0})
    assert r1_cached["cached"] is True

    state._render_cache.clear()
    state._render_cache_bytes = 0
    state.render("lib/cube", {"size": 10.0})
    assert len(seen_prefixes) == 2
    assert seen_prefixes[0] == seen_prefixes[1]
    assert seen_prefixes[0].startswith("lib/cube:")

    # Adding 2 more entries (~9.3 KiB each -> > 15 KiB cap) evicts older entries
    state.render("lib/cube", {"size": 20.0})
    state.render("lib/cube", {"size": 30.0})
    assert state._render_cache_bytes <= 15000
    assert len(state._render_cache) == 1
    assert ("lib/cube", json.dumps({"size": 30.0}, sort_keys=True)) in state._render_cache

    state.reload_project()
    assert state._render_cache_bytes == 0
    assert len(state._render_cache) == 0


def test_tier1_show_list_flattening_and_pos_exclusion(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    lib_dir = tmp_path / "lib"
    (lib_dir / "models").mkdir(parents=True)

    (lib_dir / "models" / "show_list.py").write_text(
        "from build123d import Box\n"
        "from ocp_vscode import show\n"
        "a = Box(1, 1, 1)\n"
        "b = Box(2, 2, 2)\n"
        "c = Box(3, 3, 3)\n"
        "show([a, b])\n"
        "show(c)\n",
        encoding="utf-8",
    )
    (lib_dir / "models" / "with_pos.py").write_text(
        "from build123d import Box, Pos\n"
        "my_part = Box(1, 2, 3)\n"
        "my_pos = Pos(10, 0, 0)\n",
        encoding="utf-8",
    )
    # Non-CAD script using plt.show() must NOT be discovered as a CAD model
    (lib_dir / "models" / "plot_helper.py").write_text(
        "import matplotlib.pyplot as plt\n"
        "plt.plot([1, 2], [3, 4])\n"
        "plt.show()\n",
        encoding="utf-8",
    )

    class _FakeShape:
        def __init__(self, label: str = "") -> None:
            self.wrapped = _DummyWrapped()
            self.label = label

    class _FakePos:
        def __init__(self, *args: float) -> None:
            self.wrapped = _DummyWrapped()
            self.coords = args

    fake_b3d = types.ModuleType("build123d")
    fake_b3d.Shape = _FakeShape  # type: ignore[attr-defined]
    fake_b3d.BuildPart = type("BuildPart", (), {})  # type: ignore[attr-defined]
    fake_b3d.BuildSketch = type("BuildSketch", (), {})  # type: ignore[attr-defined]
    fake_b3d.BuildLine = type("BuildLine", (), {})  # type: ignore[attr-defined]
    fake_b3d.Box = lambda *a, **kw: _FakeShape("Box")  # type: ignore[attr-defined]
    fake_b3d.Pos = _FakePos  # type: ignore[attr-defined]

    monkeypatch.setitem(sys.modules, "build123d", fake_b3d)
    monkeypatch.setattr(worker, "HAS_BUILD123D", True)
    monkeypatch.setattr(worker, "_CAD_WARMUP_DONE", worker.threading.Event())
    worker._CAD_WARMUP_DONE.set()
    monkeypatch.setattr(worker, "_CAD_WARMUP_STARTED", True)

    assert worker._is_script_shape(_FakePos(1, 2, 3)) is False
    assert worker._is_script_shape(_FakeShape("Box")) is True

    state = worker.WorkerState([{"id": "lib", "name": "Lib", "path": str(lib_dir)}])
    models, _ = state.discover_models()
    by_id = {m["id"]: m for m in models}
    assert "lib/plot_helper" not in by_id
    assert set(by_id.keys()) == {"lib/show_list", "lib/with_pos"}

    _, res_list, _, _ = state._build_shape("lib/show_list", {})
    parts = worker._collect_named_parts(res_list, "show_list")
    assert len(parts) == 3
    assert all(isinstance(shp, _FakeShape) for _, shp, _, _ in parts)

    # Nested list passed directly to _collect_named_parts also flattens cleanly
    nested_parts = worker._collect_named_parts([[_FakeShape("A"), _FakeShape("B")], _FakeShape("C")], "nested")
    assert [p[0] for p in nested_parts] == ["A", "B", "C"]
    assert all(isinstance(p[1], _FakeShape) for p in nested_parts)

    _, res_pos, _, _ = state._build_shape("lib/with_pos", {})
    assert isinstance(res_pos, _FakeShape)


def test_component_discovery_and_schema_linking(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    lib_dir = tmp_path / "lib"
    (lib_dir / "utils").mkdir(parents=True)
    (lib_dir / "components" / "pull_tab").mkdir(parents=True)
    (lib_dir / "components" / "hardware" / "knob").mkdir(parents=True)
    (lib_dir / "models" / "can_lid").mkdir(parents=True)
    (lib_dir / "models" / "spacer").mkdir(parents=True)

    params_src = (Path(__file__).resolve().parents[1] / "params.py").read_text(encoding="utf-8")
    (lib_dir / "utils" / "params.py").write_text(params_src, encoding="utf-8")
    (lib_dir / "components" / "__init__.py").write_text("", encoding="utf-8")
    (lib_dir / "components" / "helper.py").write_text("VALUE = 42\n", encoding="utf-8")

    (lib_dir / "components" / "pull_tab" / "__init__.py").write_text(
        "from .pull_tab import PullTabParams, create_pull_tab\n",
        encoding="utf-8",
    )
    (lib_dir / "components" / "pull_tab" / "pull_tab.py").write_text(
        "from dataclasses import dataclass\n"
        "from utils.params import param\n"
        "@dataclass\n"
        "class PullTabParams:\n"
        "    length: float = param(6.0, unit='mm')\n"
        "    width: float = param(16.0, unit='mm')\n"
        "def create_pull_tab(params: PullTabParams):\n"
        "    return params\n",
        encoding="utf-8",
    )
    (lib_dir / "components" / "hardware" / "knob" / "knob.py").write_text(
        "from dataclasses import dataclass\n"
        "from utils.params import param\n"
        "@dataclass\n"
        "class KnobParams:\n"
        "    diameter: float = param(20.0, unit='mm')\n"
        "def create_knob(params: KnobParams):\n"
        "    return params\n",
        encoding="utf-8",
    )
    (lib_dir / "models" / "spacer" / "spacer.py").write_text(
        "from dataclasses import dataclass\n"
        "from utils.params import param\n"
        "@dataclass\n"
        "class SpacerParams:\n"
        "    thickness: float = param(3.0, unit='mm')\n"
        "def create_spacer(params: SpacerParams):\n"
        "    return params\n",
        encoding="utf-8",
    )
    (lib_dir / "models" / "can_lid" / "can_lid.py").write_text(
        "from __future__ import annotations\n"
        "from dataclasses import dataclass\n"
        "from components.pull_tab import PullTabParams\n"
        "from models.spacer.spacer import SpacerParams\n"
        "from utils.params import group, param\n"
        "@dataclass\n"
        "class CoreParams:\n"
        "    diameter: float = param(54.0, unit='mm')\n"
        "@dataclass\n"
        "class CanLidParams:\n"
        "    core: CoreParams = group(CoreParams)\n"
        "    pull_tab: PullTabParams | None = group(PullTabParams)\n"
        "    spacer: SpacerParams | None = group(SpacerParams, enabled=False)\n"
        "def create_can_lid(params: CanLidParams):\n"
        "    return params\n",
        encoding="utf-8",
    )

    monkeypatch.setattr(worker, "_CAD_WARMUP_DONE", worker.threading.Event())
    worker._CAD_WARMUP_DONE.set()
    monkeypatch.setattr(worker, "_CAD_WARMUP_STARTED", True)

    state = worker.WorkerState([{"id": "lib", "name": "Lib", "path": str(lib_dir)}])
    try:
        models, _ = state.discover_models()
        by_id = {m["id"]: m for m in models}

        # Non-CAD helper in components/ is still ignored; parametric components are discovered
        assert "lib/components/helper" not in by_id
        assert set(by_id.keys()) == {
            "lib/can_lid",
            "lib/spacer",
            "lib/components/pull_tab",
            "lib/components/hardware/knob",
        }
        assert by_id["lib/can_lid"]["category"] == "model"
        assert by_id["lib/components/pull_tab"]["category"] == "component"
        assert by_id["lib/components/pull_tab"]["folders"] == []
        assert by_id["lib/components/pull_tab"]["presets_path"].endswith("components/pull_tab/presets.json")
        assert by_id["lib/components/hardware/knob"]["category"] == "component"
        assert by_id["lib/components/hardware/knob"]["folders"] == ["hardware"]

        # Models sort ahead of components
        assert [m["category"] for m in models] == ["model", "model", "component", "component"]

        # Parent model schema automatically links both component and imported-model groups
        schema = state.get_schema("lib/can_lid")
        fields_by_name = {f["name"]: f for f in schema["fields"]}

        assert "component_id" not in fields_by_name["core"]

        pt_field = fields_by_name["pull_tab"]
        assert pt_field["type"] == "group"
        assert pt_field["nullable"] is True
        assert pt_field["component_id"] == "lib/components/pull_tab"
        assert pt_field["component_name"] == "Pull Tab"
        assert pt_field["default"] == {"length": 6.0, "width": 16.0}
        assert pt_field["group_defaults"] == {"length": 6.0, "width": 16.0}
        assert schema["defaults"]["pull_tab"] == {"length": 6.0, "width": 16.0}

        sp_field = fields_by_name["spacer"]
        assert sp_field["type"] == "group"
        assert sp_field["nullable"] is True
        assert sp_field["component_id"] == "lib/spacer"
        assert sp_field["component_name"] == "Spacer"
        assert sp_field["default"] is None
        assert sp_field["group_defaults"] == {"thickness": 3.0}
        assert schema["defaults"]["spacer"] is None

        # Update-defaults supports group() toggles and overrides on linked component groups
        plan = state.update_defaults("lib/can_lid", {"pull_tab": None, "spacer": {"thickness": 5.0}}, dry_run=True)
        assert plan["unsupported"] == []
        changes_by_path = {c["path"]: c for c in plan["changes"]}
        assert changes_by_path["pull_tab"]["new_literal"] == "enabled=False"
        assert changes_by_path["spacer"]["new_literal"] == "enabled=True"
        assert changes_by_path["spacer.thickness"]["new_literal"] == "5.0"
    finally:
        state._evict_project_modules()


def test_model_name_discovery_and_rename(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    (lib_dir / "models" / "battery_caddy").mkdir(parents=True)
    py_path = lib_dir / "models" / "battery_caddy" / "battery_caddy.py"
    py_path.write_text(
        '"""Battery caddy."""\n'
        "from dataclasses import dataclass\n\n"
        "EXPORT_STL: bool = False\n"
        "@dataclass\n"
        "class BatteryCaddyParams:\n"
        "    slots: int = 4\n"
        "def create_battery_caddy(params: BatteryCaddyParams):\n"
        "    return None\n",
        encoding="utf-8",
    )

    state = worker.WorkerState([{"id": "lib", "name": "Lib", "path": str(lib_dir)}])
    try:
        models, _ = state.discover_models()
        assert models[0]["name"] == "Battery Caddy"

        # 1. Rename display name only -> writes MODEL_NAME before EXPORT_STL
        res1 = state.rename_model("lib/battery_caddy", name="RC Battery Caddy")
        assert res1["ok"] is True
        assert res1["model_id"] == "lib/battery_caddy"
        assert res1["name"] == "RC Battery Caddy"
        assert 'MODEL_NAME: str = "RC Battery Caddy"' in py_path.read_text(encoding="utf-8")

        # 2. Rename both file stem and display name -> moves directory + updates MODEL_NAME
        res2 = state.rename_model("lib/battery_caddy", name="2S RC Battery Caddy", file_stem="rc_battery_caddy")
        assert res2["ok"] is True
        assert res2["model_id"] == "lib/rc_battery_caddy"
        assert res2["name"] == "2S RC Battery Caddy"
        new_py = lib_dir / "models" / "rc_battery_caddy" / "rc_battery_caddy.py"
        assert new_py.exists()
        assert not py_path.exists()
        assert 'MODEL_NAME: str = "2S RC Battery Caddy"' in new_py.read_text(encoding="utf-8")
    finally:
        state._evict_project_modules()


def test_get_schema_prefers_module_docstring_over_function_docstring(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    model_dir = lib_dir / "models" / "box"
    model_dir.mkdir(parents=True)
    (model_dir / "box.py").write_text(
        '"""Storage box for small hardware."""\n\n'
        "from dataclasses import dataclass\n\n"
        "@dataclass\n"
        "class BoxParams:\n"
        "    width: float = 40.0\n\n"
        "def create_box(params: BoxParams):\n"
        '    """Decoupled generator function returning CAD geometry."""\n'
        "    return None\n",
        encoding="utf-8",
    )

    state = worker.WorkerState([{"id": "lib", "name": "Lib", "path": str(lib_dir)}])
    try:
        schema = state.get_schema("lib/box")
        assert schema["description"] == "Storage box for small hardware."
    finally:
        state._evict_project_modules()
