from __future__ import annotations

import json
from pathlib import Path
import pytest

from conftest import REPO_ROOT, WORKER_DIR, http_request, running_worker_server
import worker


def test_params_py_three_way_parity() -> None:
    worker_params = (WORKER_DIR / "params.py").read_bytes()
    demo_params = (REPO_ROOT / "examples" / "demo_project" / "utils" / "params.py").read_bytes()
    assert worker_params == demo_params, "worker/params.py and examples/demo_project/utils/params.py differ"

    ref_params_path = Path("/home/luke/repos/code-cad/utils/params.py")
    if ref_params_path.exists():
        assert worker_params == ref_params_path.read_bytes(), "worker/params.py differs from /home/luke/repos/code-cad/utils/params.py"


def test_export_quality_presets_and_format_allowlist(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    assert worker._resolve_export_deflection("draft") == (0.02, 0.3)
    assert worker._resolve_export_deflection("standard") == (0.001, 0.1)
    assert worker._resolve_export_deflection("fine") == (0.0005, 0.05)
    assert worker._resolve_export_deflection("custom", 0.002, 0.15) == (0.002, 0.15)

    for bad_args in (
        ("ultra", None, None),
        ("custom", None, 0.1),
        ("custom", True, 0.1),
        ("custom", float("nan"), 0.1),
        ("custom", 0.0, 0.1),
        ("custom", 0.01, 5.0),
    ):
        with pytest.raises(worker._HttpError) as exc_info:
            worker._resolve_export_deflection(*bad_args)
        assert exc_info.value.status == 400

    lib_dir = tmp_path / "lib"
    (lib_dir / "models").mkdir(parents=True)
    (lib_dir / "models" / "part.py").write_text(
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class PartParams:\n"
        "    size: float = 10.0\n"
        "def create_part(params: PartParams):\n"
        "    return object()\n",
        encoding="utf-8",
    )

    recorded: list[tuple[str, float, float]] = []

    def fake_export(result, fmt, out_path, linear_deflection=0.001, angular_deflection=0.1):
        recorded.append((fmt, linear_deflection, angular_deflection))
        Path(out_path).write_bytes(b"solid test\nendsolid test\n")

    monkeypatch.setattr(worker, "HAS_BUILD123D", True)
    monkeypatch.setattr(worker, "_CAD_WARMUP_DONE", worker.threading.Event())
    worker._CAD_WARMUP_DONE.set()
    monkeypatch.setattr(worker, "_CAD_WARMUP_STARTED", True)
    monkeypatch.setattr(worker, "export_shape_to_file", fake_export)

    with running_worker_server(
        libraries=[{"id": "lib", "name": "Lib", "path": str(lib_dir)}],
        auth_token="tok",
    ) as (_state, token, port):
        auth = {"Authorization": f"Bearer {token}"}

        # Disallowed format -> 400
        status, _, _ = http_request(
            port,
            "POST",
            "/models/lib%2Fpart/export",
            headers=auth,
            body={"format": "obj", "quality": "standard"},
        )
        assert status == 400

        # Invalid quality -> 400
        status, _, _ = http_request(
            port,
            "POST",
            "/models/lib%2Fpart/export",
            headers=auth,
            body={"format": "stl", "quality": "invalid"},
        )
        assert status == 400

        # Fine STL export
        status, _, raw = http_request(
            port,
            "POST",
            "/models/lib%2Fpart/export",
            headers=auth,
            body={"format": "stl", "quality": "fine"},
        )
        assert status == 200
        assert raw.startswith(b"solid test")
        assert recorded[-1] == ("stl", 0.0005, 0.05)

        # Custom 3MF export
        status, _, _ = http_request(
            port,
            "POST",
            "/models/lib%2Fpart/export",
            headers=auth,
            body={
                "format": "3mf",
                "quality": "custom",
                "linear_deflection": 0.004,
                "angular_deflection": 0.2,
            },
        )
        assert status == 200
        assert recorded[-1] == ("3mf", 0.004, 0.2)


def test_real_build123d_tessellation_and_exports(tmp_path: Path) -> None:
    b3d = pytest.importorskip("build123d")
    worker._wait_for_cad_warmup()

    box = b3d.Box(10, 20, 30)
    raw_ocp, bbox_ocp = worker._tessellate_shape_ocp(box)
    assert len(raw_ocp["vertices"]) > 0
    assert len(raw_ocp["triangles"]) > 0
    # A box has 12 unique topological edges (TopExp.MapShapes_s deduplicates shared edges across faces)
    assert len(raw_ocp["segments_per_edge"]) == 12
    assert bbox_ocp["xmax"] - bbox_ocp["xmin"] == pytest.approx(10.0, abs=1e-3)

    cyl = b3d.Cylinder(radius=15.0, height=20.0)
    draft_stl = tmp_path / "draft.stl"
    fine_stl = tmp_path / "fine.stl"
    step_out = tmp_path / "cyl.step"
    threemf_out = tmp_path / "cyl.3mf"

    worker.export_shape_to_file(cyl, "stl", draft_stl, *worker._resolve_export_deflection("draft"))
    worker.export_shape_to_file(cyl, "stl", fine_stl, *worker._resolve_export_deflection("fine"))
    worker.export_shape_to_file(cyl, "step", step_out)
    worker.export_shape_to_file(cyl, "3mf", threemf_out, *worker._resolve_export_deflection("standard"))

    assert draft_stl.stat().st_size > 0
    assert fine_stl.stat().st_size > draft_stl.stat().st_size
    assert step_out.stat().st_size > 0
    assert threemf_out.stat().st_size > 0

    # Real demo_project spool_cap render + Tier 1 script render
    demo_dir = REPO_ROOT / "examples" / "demo_project"
    tier1_dir = tmp_path / "tier1"
    (tier1_dir / "models").mkdir(parents=True)
    (tier1_dir / "models" / "quick_box.py").write_text(
        "from build123d import Box\n"
        "part = Box(5, 6, 7)\n",
        encoding="utf-8",
    )

    state = worker.WorkerState(
        [
            {"id": "demo", "name": "Demo", "path": str(demo_dir)},
            {"id": "tier1", "name": "Tier1", "path": str(tier1_dir)},
        ]
    )
    rendered = state.render("demo/spool_cap", {})
    assert rendered["ok"] is True
    assert rendered["bbox"]["dx"] > 0

    rendered_t1 = state.render("tier1/quick_box", {})
    assert rendered_t1["ok"] is True
    assert rendered_t1["bbox"]["dx"] == pytest.approx(5.0, abs=1e-3)
