from __future__ import annotations

import hashlib
import json
from pathlib import Path
import pytest

from conftest import http_request, running_worker_server
import worker


def test_bom_file_discovery_and_line1_default_update_preserves_bom(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    model_dir = lib_dir / "models" / "bom_part"
    model_dir.mkdir(parents=True)
    py_path = model_dir / "bom_part.py"

    # Put a dataclass field on line 1 right after the 3-byte UTF-8 BOM to exercise bom_len offset math,
    # plus a multi-line param() field on subsequent lines.
    src = (
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class BomPartParams:\n"
        "    width: float = 10.0\n"
        "    height: float = 20.0\n"
        "def create_bom_part(params: BomPartParams):\n"
        "    return None\n"
    )
    # Also test a BOM script with a dataclass field literal directly on line 1 to exercise bom_len offset math
    line1_py = model_dir / "line1_bom.py"
    line1_src = (
        "class Line1Params: val: float = 5.0\n"
        "import dataclasses\n"
        "Line1Params = dataclasses.dataclass(Line1Params)\n"
        "def create_line1_bom(params: Line1Params): return None\n"
    )
    py_path.write_bytes(b"\xef\xbb\xbf" + src.encode("utf-8"))
    line1_py.write_bytes(b"\xef\xbb\xbf" + line1_src.encode("utf-8"))

    state = worker.WorkerState([{"id": "demo", "name": "Demo", "path": str(lib_dir)}])
    models, _ = state.discover_models()
    by_id = {m["id"]: m for m in models}
    assert "demo/bom_part" in by_id
    assert by_id["demo/bom_part"]["kind"] == "parametric"

    # Dry-run preview
    preview = state.update_defaults("demo/bom_part", {"width": 15.5, "height": 25.0}, dry_run=True)
    assert preview["ok"] is True
    assert preview["sha256"] == hashlib.sha256(py_path.read_bytes()).hexdigest()
    assert len(preview["changes"]) == 2
    assert preview["changes"][0]["old_literal"] == "10.0"
    assert preview["changes"][0]["new_literal"] == "15.5"

    # Apply with matching SHA-256
    applied = state.update_defaults(
        "demo/bom_part",
        {"width": 15.5, "height": 25.0},
        dry_run=False,
        expected_sha256=preview["sha256"],
    )
    assert applied["ok"] is True
    assert applied["updated"] == 2
    updated_bytes = py_path.read_bytes()
    assert updated_bytes.startswith(b"\xef\xbb\xbf")
    assert "width: float = 15.5" in updated_bytes.decode("utf-8-sig")
    assert "height: float = 25.0" in updated_bytes.decode("utf-8-sig")

    # Line-1 offset check with BOM
    line1_preview = state.update_defaults("demo/bom_part/line1_bom", {"val": 9.25}, dry_run=True)
    assert line1_preview["ok"] is True
    assert len(line1_preview["changes"]) == 1
    assert line1_preview["changes"][0]["line"] == 1
    assert line1_preview["changes"][0]["old_literal"] == "5.0"
    assert line1_preview["changes"][0]["new_literal"] == "9.25"
    line1_apply = state.update_defaults(
        "demo/bom_part/line1_bom",
        {"val": 9.25},
        dry_run=False,
        expected_sha256=line1_preview["sha256"],
    )
    assert line1_apply["ok"] is True
    assert line1_apply["updated"] == 1
    assert line1_py.read_bytes().startswith(b"\xef\xbb\xbfclass Line1Params: val: float = 9.25\n")


def test_update_defaults_requires_expected_sha256_on_apply(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    model_dir = lib_dir / "models" / "lock_part"
    model_dir.mkdir(parents=True)
    py_path = model_dir / "lock_part.py"
    py_path.write_text(
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class LockPartParams:\n"
        "    thickness: float = 3.0\n"
        "    computed: float = 1.0 + 2.0\n"
        "def create_lock_part(params: LockPartParams):\n"
        "    return None\n",
        encoding="utf-8",
    )
    original = py_path.read_text(encoding="utf-8")

    with running_worker_server(
        libraries=[{"id": "demo", "name": "Demo", "path": str(lib_dir)}],
        auth_token="tok",
    ) as (_state, token, port):
        auth = {"Authorization": f"Bearer {token}"}

        # 1. Missing expected_sha256 with dry_run=False -> HTTP 400
        status, _, raw = http_request(
            port,
            "POST",
            "/models/demo%2Flock_part/update-defaults",
            headers=auth,
            body={"dry_run": False, "params": {"thickness": 5.0}},
        )
        assert status == 400
        resp = json.loads(raw)
        assert resp["ok"] is False
        assert "expected_sha256" in resp["error"]
        assert py_path.read_text(encoding="utf-8") == original

        # 2. Stale expected_sha256 -> ok=False, file untouched
        status, _, raw = http_request(
            port,
            "POST",
            "/models/demo%2Flock_part/update-defaults",
            headers=auth,
            body={"dry_run": False, "expected_sha256": "deadbeef", "params": {"thickness": 5.0}},
        )
        assert json.loads(raw)["ok"] is False
        assert py_path.read_text(encoding="utf-8") == original

        # 3. Unsupported non-literal field preserves unsupported diagnostics
        status, _, raw = http_request(
            port,
            "POST",
            "/models/demo%2Flock_part/update-defaults",
            headers=auth,
            body={"dry_run": False, "params": {"computed": 9.0}},
        )
        bad_resp = json.loads(raw)
        assert bad_resp["ok"] is False
        assert len(bad_resp["unsupported"]) == 1
        assert bad_resp["unsupported"][0]["path"] == "computed"
        assert py_path.read_text(encoding="utf-8") == original


def test_replace_with_retry_recovers_from_transient_permission_errors(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    src = tmp_path / "src.tmp"
    dst = tmp_path / "dst.py"
    src.write_text("new", encoding="utf-8")
    dst.write_text("old", encoding="utf-8")

    real_replace = worker.os.replace
    attempts = {"count": 0}

    def flaky_replace(a: Path, b: Path) -> None:
        attempts["count"] += 1
        if attempts["count"] <= 2:
            raise PermissionError("WinError 5: Access is denied")
        real_replace(a, b)

    monkeypatch.setattr(worker.os, "replace", flaky_replace)
    monkeypatch.setattr(worker.time, "sleep", lambda _s: None)

    worker._replace_with_retry(src, dst, attempts=5, initial_delay=0.001)
    assert attempts["count"] == 3
    assert dst.read_text(encoding="utf-8") == "new"

    # Exhausting retries raises PermissionError
    src2 = tmp_path / "src2.tmp"
    src2.write_text("newer", encoding="utf-8")

    def always_fail(_a: Path, _b: Path) -> None:
        raise PermissionError("locked")

    monkeypatch.setattr(worker.os, "replace", always_fail)
    with pytest.raises(PermissionError):
        worker._replace_with_retry(src2, dst, attempts=3, initial_delay=0.001)


def test_update_defaults_preserves_tuple_literals(tmp_path: Path) -> None:
    assert worker._format_python_literal(()) == "()"
    assert worker._format_python_literal((5.0,)) == "(5.0,)"
    assert worker._format_python_literal((3.0, 4.0)) == "(3.0, 4.0)"
    assert worker._format_python_literal([3.0, 4.0]) == "[3.0, 4.0]"

    lib_dir = tmp_path / "lib"
    model_dir = lib_dir / "models" / "tuple_part"
    model_dir.mkdir(parents=True)
    py_path = model_dir / "tuple_part.py"
    py_path.write_text(
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class TuplePartParams:\n"
        "    coords: tuple[float, ...] = (\n"
        "        1.0,\n"
        "        2.0,\n"
        "    )\n"
        "    single: tuple[float, ...] = (1.0, 2.0)\n"
        "    empty_tup: tuple[float, ...] = (1.0, 2.0)\n"
        "def create_tuple_part(params: TuplePartParams):\n"
        "    return None\n",
        encoding="utf-8",
    )

    state = worker.WorkerState([{"id": "demo", "name": "Demo", "path": str(lib_dir)}])
    preview = state.update_defaults(
        "demo/tuple_part",
        {"coords": [3.0, 4.0], "single": [5.0], "empty_tup": []},
        dry_run=True,
    )
    assert preview["ok"] is True
    by_path = {c["path"]: c for c in preview["changes"]}
    assert by_path["coords"]["new_literal"] == "(3.0, 4.0)"
    assert by_path["single"]["new_literal"] == "(5.0,)"
    assert by_path["empty_tup"]["new_literal"] == "()"

    applied = state.update_defaults(
        "demo/tuple_part",
        {"coords": [3.0, 4.0], "single": [5.0], "empty_tup": []},
        dry_run=False,
        expected_sha256=preview["sha256"],
    )
    assert applied["ok"] is True
    assert applied["updated"] == 3
    text = py_path.read_text(encoding="utf-8")
    assert "coords: tuple[float, ...] = (3.0, 4.0)" in text
    assert "single: tuple[float, ...] = (5.0,)" in text
    assert "empty_tup: tuple[float, ...] = ()" in text


def test_update_defaults_partial_apply_with_external_component(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    comp_dir = lib_dir / "components"
    comp_dir.mkdir(parents=True)
    comp_path = comp_dir / "pull_tab.py"
    comp_src = (
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class PullTabParams:\n"
        "    enabled: bool = True\n"
        "    length: float = 12.0\n"
    )
    comp_path.write_text(comp_src, encoding="utf-8")

    model_dir = lib_dir / "models" / "can_lid"
    model_dir.mkdir(parents=True)
    py_path = model_dir / "can_lid.py"
    py_src = (
        "from dataclasses import dataclass, field\n"
        "from components.pull_tab import PullTabParams\n"
        "@dataclass\n"
        "class CanLidParams:\n"
        "    diameter: float = 65.0\n"
        "    pull_tab: PullTabParams = field(default_factory=PullTabParams)\n"
        "def create_can_lid(params: CanLidParams):\n"
        "    return None\n"
    )
    py_path.write_text(py_src, encoding="utf-8")

    state = worker.WorkerState([{"id": "demo", "name": "Demo", "path": str(lib_dir)}])

    # 1. Only external component field changed -> fails closed
    only_ext = state.update_defaults(
        "demo/can_lid",
        {"diameter": 65.0, "pull_tab": {"enabled": True, "length": 18.0}},
        dry_run=False,
        expected_sha256="ignored",
    )
    assert only_ext["ok"] is False
    assert only_ext["changes"] == []
    assert len(only_ext["unsupported"]) == 1
    u = only_ext["unsupported"][0]
    assert u["path"] == "pull_tab.length"
    assert u["reason_code"] == "external_component"
    assert u["owner_class"] == "PullTabParams"
    assert u["in_library"] is True
    assert u["source_rel_path"] == "components/pull_tab.py"
    assert u["source_file"] == str(comp_path.resolve())
    assert py_path.read_text(encoding="utf-8") == py_src
    assert comp_path.read_text(encoding="utf-8") == comp_src

    # 2. Mixed local + external component changes -> preview + partial apply succeeds
    preview = state.update_defaults(
        "demo/can_lid",
        {"diameter": 54.0, "pull_tab": {"enabled": True, "length": 18.0}},
        dry_run=True,
    )
    assert preview["ok"] is True
    assert len(preview["changes"]) == 1
    assert preview["changes"][0]["path"] == "diameter"
    assert len(preview["unsupported"]) == 1
    assert preview["unsupported"][0]["path"] == "pull_tab.length"

    applied = state.update_defaults(
        "demo/can_lid",
        {"diameter": 54.0, "pull_tab": {"enabled": True, "length": 18.0}},
        dry_run=False,
        expected_sha256=preview["sha256"],
    )
    assert applied["ok"] is True
    assert applied["updated"] == 1
    assert len(applied["unsupported"]) == 1
    assert applied["unsupported"][0]["path"] == "pull_tab.length"
    assert "diameter: float = 54.0" in py_path.read_text(encoding="utf-8")
    # External shared component file must remain untouched
    assert comp_path.read_text(encoding="utf-8") == comp_src


def _write_utils_params(lib_dir: Path) -> None:
    utils_dir = lib_dir / "utils"
    utils_dir.mkdir(parents=True, exist_ok=True)
    src = Path(__file__).resolve().parents[1] / "params.py"
    (utils_dir / "params.py").write_text(src.read_text(encoding="utf-8"), encoding="utf-8")


def test_update_defaults_default_preset_add_update_remove_and_bom(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    _write_utils_params(lib_dir)
    model_dir = lib_dir / "models" / "caddy"
    model_dir.mkdir(parents=True)
    py_path = model_dir / "caddy.py"

    # Model with try/except ImportError for utils.params, no @default_preset yet, and UTF-8 BOM
    src = (
        "from dataclasses import dataclass\n"
        "try:\n"
        "    from utils.params import group, param\n"
        "except ImportError:\n"
        "    import sys\n"
        "    from utils.params import group, param\n"
        "\n"
        "@dataclass\n"
        "class CaddyParams:\n"
        "    slots: int = param(4, desc='Number of slots')\n"
        "\n"
        "def create_caddy(params: CaddyParams):\n"
        "    return None\n"
    )
    py_path.write_bytes(b"\xef\xbb\xbf" + src.encode("utf-8"))

    state = worker.WorkerState([{"id": "demo", "name": "Demo", "path": str(lib_dir)}])

    # 1. Preview without default_preset returns empty raw metadata & can_edit=True
    preview0 = state.update_defaults("demo/caddy", {}, dry_run=True)
    assert preview0["ok"] is True
    assert preview0["default_preset"] == {"label": "", "description": "", "can_edit": True}
    assert "default_preset_change" not in preview0

    # 2. Validation error when description is non-empty and label is empty
    with pytest.raises(worker._HttpError) as exc_info:
        state.update_defaults(
            "demo/caddy",
            {},
            dry_run=True,
            default_preset={"label": "   ", "description": "Some desc"},
        )
    assert exc_info.value.status == 400

    # 3. Add @default_preset + update parameter default atomically (preserving BOM)
    preview_add = state.update_defaults(
        "demo/caddy",
        {"slots": 6},
        dry_run=True,
        default_preset={"label": "2S Shorty 4×1", "description": "Upright pit caddy"},
    )
    assert preview_add["ok"] is True
    assert preview_add["default_preset_change"]["action"] == "add"
    assert len(preview_add["changes"]) == 1

    applied_add = state.update_defaults(
        "demo/caddy",
        {"slots": 6},
        dry_run=False,
        expected_sha256=preview_add["sha256"],
        default_preset={"label": "2S Shorty 4×1", "description": "Upright pit caddy"},
    )
    assert applied_add["ok"] is True
    assert applied_add["updated"] == 1
    assert applied_add["default_preset_updated"] is True
    assert applied_add["default_preset"] == {
        "label": "2S Shorty 4×1",
        "description": "Upright pit caddy",
        "can_edit": True,
    }
    raw_after_add = py_path.read_bytes()
    assert raw_after_add.startswith(b"\xef\xbb\xbf")
    text_after_add = raw_after_add.decode("utf-8-sig")
    assert text_after_add.count("from utils.params import default_preset, group, param") == 2
    assert '@default_preset("2S Shorty 4×1", desc="Upright pit caddy")' in text_after_add

    # Schema reflects newly added @default_preset
    schema = state.get_schema("demo/caddy")
    assert schema["default_preset"] == {
        "label": "2S Shorty 4×1",
        "description": "Upright pit caddy",
    }

    # 4. Update existing @default_preset to multi-line (long label+desc) without changing params
    long_desc = "4-pack pinwheel on a 2x2 Gridfinity base with a support-free center finger pull loop"
    preview_upd = state.update_defaults(
        "demo/caddy",
        {},
        dry_run=True,
        default_preset={"label": "2S Shorty Pinwheel", "description": long_desc},
    )
    assert preview_upd["ok"] is True
    assert preview_upd["default_preset_change"]["action"] == "update"
    assert preview_upd["changes"] == []

    applied_upd = state.update_defaults(
        "demo/caddy",
        {},
        dry_run=False,
        expected_sha256=preview_upd["sha256"],
        default_preset={"label": "2S Shorty Pinwheel", "description": long_desc},
    )
    assert applied_upd["ok"] is True
    assert applied_upd["updated"] == 0
    assert applied_upd["default_preset_updated"] is True
    schema2 = state.get_schema("demo/caddy")
    assert schema2["default_preset"] == {
        "label": "2S Shorty Pinwheel",
        "description": long_desc,
    }

    # 5. Remove @default_preset by clearing both label and description
    preview_rem = state.update_defaults(
        "demo/caddy",
        {},
        dry_run=True,
        default_preset={"label": "", "description": ""},
    )
    assert preview_rem["default_preset_change"]["action"] == "remove"
    applied_rem = state.update_defaults(
        "demo/caddy",
        {},
        dry_run=False,
        expected_sha256=preview_rem["sha256"],
        default_preset={"label": "", "description": ""},
    )
    assert applied_rem["ok"] is True
    assert applied_rem["default_preset_updated"] is True
    assert "@default_preset" not in py_path.read_text(encoding="utf-8-sig")
    schema3 = state.get_schema("demo/caddy")
    assert "default_preset" not in schema3


def test_update_defaults_default_preset_without_utils_params_fails_closed(tmp_path: Path) -> None:
    lib_dir = tmp_path / "lib"
    model_dir = lib_dir / "models" / "bare"
    model_dir.mkdir(parents=True)
    py_path = model_dir / "bare.py"
    py_path.write_text(
        "from dataclasses import dataclass\n"
        "@dataclass\n"
        "class BareParams:\n"
        "    width: float = 10.0\n"
        "def create_bare(params: BareParams):\n"
        "    return None\n",
        encoding="utf-8",
    )

    state = worker.WorkerState([{"id": "demo", "name": "Demo", "path": str(lib_dir)}])
    preview = state.update_defaults("demo/bare", {"width": 12.0}, dry_run=True)
    assert preview["ok"] is True
    assert preview["default_preset"]["can_edit"] is False
    assert "utils/params.py" in preview["default_preset"]["unsupported_reason"]
    assert len(preview["changes"]) == 1

    # Adding utils/params.py enables adding @default_preset even on a bare @dataclass file
    _write_utils_params(lib_dir)
    preview2 = state.update_defaults(
        "demo/bare",
        {},
        dry_run=True,
        default_preset={"label": "Standard Bare", "description": ""},
    )
    assert preview2["ok"] is True
    assert preview2["default_preset"]["can_edit"] is True
    applied2 = state.update_defaults(
        "demo/bare",
        {},
        dry_run=False,
        expected_sha256=preview2["sha256"],
        default_preset={"label": "Standard Bare", "description": ""},
    )
    assert applied2["ok"] is True
    assert applied2["default_preset_updated"] is True
    assert state.get_schema("demo/bare")["default_preset"] == {"label": "Standard Bare"}
