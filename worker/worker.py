#!/usr/bin/env python3
"""
Persistent Python worker for Code CAD Studio.

Keeps build123d / OCP imported in memory and serves a local HTTP JSON API for:
  - GET  /health
  - POST /configure
  - POST /reload
  - GET  /models
  - GET  /models/<id>/schema
  - POST /models/<id>/render
  - POST /models/<id>/export-name
  - POST /models/<id>/export
"""

from __future__ import annotations

import argparse
import array
import ast
import base64
from collections import OrderedDict
import dataclasses
import enum
import importlib.util
import inspect
import json
import math
import os
import re
import sys
import tempfile
import threading
import time
import traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, Literal, Union, get_args, get_origin, get_type_hints
from urllib.parse import unquote, urlparse

# Optional imports checked lazily/at startup so worker can still start and report
# a helpful error if the selected Python environment lacks build123d.
HAS_BUILD123D = False
BUILD123D_ERROR = ""
try:
    import build123d as b3d  # noqa: F401

    HAS_BUILD123D = True
except Exception as exc:  # pragma: no cover - environment dependent
    BUILD123D_ERROR = str(exc)

HAS_OCP_TESSELLATE = False
try:
    import ocp_tessellate  # noqa: F401

    HAS_OCP_TESSELLATE = True
except Exception:
    HAS_OCP_TESSELLATE = False


SKIP_DIRS = {
    ".git",
    ".venv",
    "venv",
    "__pycache__",
    "node_modules",
    ".mypy_cache",
    ".pytest_cache",
    ".ruff_cache",
    "build",
    "dist",
    "components",
    "tests",
    "test",
}

DEFAULT_COLORS = [
    "#55a0e3",
    "#e08b4f",
    "#5ec287",
    "#c972d4",
    "#e0c355",
    "#56c4d4",
    "#e06875",
    "#8e99e8",
]


def humanize_name(name: str) -> str:
    return name.replace("_", " ").strip().title()


def _extract_dataclass_comments(source: str, class_name: str) -> dict[str, str]:
    """Extract inline (# ...) or preceding line comments for dataclass fields."""
    comments: dict[str, str] = {}
    lines = source.splitlines()
    try:
        tree = ast.parse(source)
    except SyntaxError:
        return comments

    for node in ast.walk(tree):
        if isinstance(node, ast.ClassDef) and node.name == class_name:
            for stmt in node.body:
                if isinstance(stmt, ast.AnnAssign) and isinstance(stmt.target, ast.Name):
                    field_name = stmt.target.id
                    lineno = stmt.lineno - 1
                    if 0 <= lineno < len(lines):
                        line = lines[lineno]
                        if "#" in line:
                            comment = line.split("#", 1)[1].strip()
                            if comment:
                                comments[field_name] = comment
                                continue
                        # Check immediately preceding comment line
                        if lineno > 0:
                            prev = lines[lineno - 1].strip()
                            if prev.startswith("#"):
                                comments[field_name] = prev.lstrip("#").strip()
    return comments


def _discover_script_metadata(py_file: Path, project_root: Path) -> dict[str, Any] | None:
    """Statically inspect a .py file via AST to see if it looks like a CAD model."""
    try:
        source = py_file.read_text(encoding="utf-8")
        tree = ast.parse(source, filename=str(py_file))
    except Exception:
        return None

    params_classes: list[str] = []
    create_funcs: list[str] = []
    fallback_funcs: list[str] = []

    for node in tree.body:
        if isinstance(node, ast.ClassDef):
            if node.name.endswith("Params"):
                params_classes.append(node.name)
        elif isinstance(node, ast.FunctionDef):
            if node.name.startswith("create_"):
                create_funcs.append(node.name)
            elif node.name in ("build", "generate", "create"):
                fallback_funcs.append(node.name)

    if not create_funcs and not fallback_funcs:
        return None

    rel = py_file.relative_to(project_root)
    # Model ID: for models/<name>/<name>.py use <name>, else relative path without .py
    parts = rel.parts
    if len(parts) == 3 and parts[0] == "models" and parts[2] == f"{parts[1]}.py":
        model_id = parts[1]
    elif len(parts) == 2 and parts[0] == "models":
        model_id = py_file.stem
    else:
        model_id = rel.with_suffix("").as_posix().replace("/", "__")

    docstring = ast.get_docstring(tree) or ""
    first_line = docstring.splitlines()[0].strip() if docstring else ""

    expected_fn = f"create_{py_file.stem}"
    if expected_fn in create_funcs:
        entrypoint = expected_fn
    elif create_funcs:
        entrypoint = create_funcs[-1]
    else:
        entrypoint = fallback_funcs[0]
    # Prefer the class matching <ModelName>Params, else the last *Params class (since sub-dataclasses precede the root)
    expected_cls = "".join(part.capitalize() for part in py_file.stem.split("_")) + "Params"
    if expected_cls in params_classes:
        params_class = expected_cls
    elif params_classes:
        params_class = params_classes[-1]
    else:
        params_class = ""

    return {
        "id": model_id,
        "name": humanize_name(py_file.stem),
        "path": str(py_file),
        "rel_path": rel.as_posix(),
        "description": first_line,
        "params_class": params_class,
        "entrypoint": entrypoint,
        "dir": str(py_file.parent),
    }


def _unwrap_optional(tp: Any) -> tuple[Any, bool]:
    origin = get_origin(tp)
    if origin is Union:
        args = get_args(tp)
        non_none = [a for a in args if a is not type(None)]
        has_none = len(non_none) < len(args)
        if has_none and len(non_none) == 1:
            return non_none[0], True
    # Python 3.10+ X | None
    if str(origin) == "types.UnionType":
        args = get_args(tp)
        non_none = [a for a in args if a is not type(None)]
        has_none = len(non_none) < len(args)
        if has_none and len(non_none) == 1:
            return non_none[0], True
    return tp, False


def _serialize_default(val: Any) -> Any:
    if isinstance(val, enum.Enum):
        return val.value
    if dataclasses.is_dataclass(val):
        return {f.name: _serialize_default(getattr(val, f.name)) for f in dataclasses.fields(val)}
    if isinstance(val, (list, tuple)):
        return [_serialize_default(v) for v in val]
    if isinstance(val, dict):
        return {str(k): _serialize_default(v) for k, v in val.items()}
    return val


def _infer_numeric_bounds(name: str, default: float | int, is_int: bool) -> dict[str, Any]:
    """Provide sensible UI step values for numeric inputs."""
    step: float | int = 1 if is_int else 0.1
    if not is_int:
        if isinstance(default, (int, float)):
            if abs(default) >= 50:
                step = 0.5
            elif abs(default) >= 10:
                step = 0.5
            elif abs(default) < 1 and default != 0:
                step = 0.05
    return {"step": step}


def _introspect_dataclass(
    cls: type,
    source_path: Path | None = None,
    prefix: str = "",
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Introspect a @dataclass into UI schema fields and canonical default dict."""
    try:
        hints = get_type_hints(cls)
    except Exception:
        hints = {}

    comments: dict[str, str] = {}
    if source_path and source_path.exists():
        try:
            comments = _extract_dataclass_comments(source_path.read_text(encoding="utf-8"), cls.__name__)
        except Exception:
            pass
    else:
        try:
            src_file = inspect.getsourcefile(cls)
            if src_file:
                comments = _extract_dataclass_comments(Path(src_file).read_text(encoding="utf-8"), cls.__name__)
        except Exception:
            pass

    instance = cls()
    fields_schema: list[dict[str, Any]] = []
    defaults: dict[str, Any] = {}

    for f in dataclasses.fields(cls):
        if f.name.startswith("_"):
            continue

        raw_type = hints.get(f.name, f.type)
        unwrapped, is_nullable = _unwrap_optional(raw_type)
        default_val = getattr(instance, f.name)
        serialized_default = _serialize_default(default_val)
        defaults[f.name] = serialized_default

        field_path = f"{prefix}.{f.name}" if prefix else f.name
        meta = dict(f.metadata) if f.metadata else {}
        desc = meta.get("description") or comments.get(f.name, "")
        label = meta.get("label") or humanize_name(f.name)

        # Nested dataclass -> explicit collapsible section (toggleable if it has `enabled: bool`)
        if dataclasses.is_dataclass(unwrapped) or dataclasses.is_dataclass(default_val):
            nested_cls = unwrapped if dataclasses.is_dataclass(unwrapped) else type(default_val)
            nested_fields, nested_defaults = _introspect_dataclass(nested_cls, None, prefix=field_path)
            defaults[f.name] = nested_defaults
            if not desc:
                nested_doc = inspect.getdoc(nested_cls) or ""
                desc = nested_doc.splitlines()[0].strip() if nested_doc else ""
            group_info: dict[str, Any] = {
                "name": f.name,
                "path": field_path,
                "label": label,
                "type": "group",
                "description": desc,
                "children": nested_fields,
            }
            if "when" in meta and isinstance(meta["when"], dict):
                group_info["when"] = meta["when"]
            fields_schema.append(group_info)
            continue

        origin = get_origin(unwrapped)
        field_info: dict[str, Any] = {
            "name": f.name,
            "path": field_path,
            "label": label,
            "default": serialized_default,
            "description": desc,
        }
        if is_nullable:
            field_info["nullable"] = True
        if "when" in meta and isinstance(meta["when"], dict):
            field_info["when"] = meta["when"]

        if origin is Literal:
            choices = list(get_args(unwrapped))
            field_info["type"] = "enum"
            field_info["choices"] = [str(c) for c in choices]
            field_info["default"] = None if (is_nullable and serialized_default is None) else str(serialized_default)
        elif inspect.isclass(unwrapped) and issubclass(unwrapped, enum.Enum):
            field_info["type"] = "enum"
            field_info["choices"] = [str(m.value) for m in unwrapped]
            field_info["default"] = None if (is_nullable and serialized_default is None) else str(serialized_default)
        elif unwrapped is bool or isinstance(default_val, bool):
            field_info["type"] = "bool"
        elif unwrapped is int or (isinstance(default_val, int) and not isinstance(default_val, bool)):
            field_info["type"] = "int"
            num_default = int(default_val) if default_val is not None else 0
            bounds = _infer_numeric_bounds(f.name, num_default, is_int=True)
            field_info["step"] = meta.get("step", bounds["step"])
            if "min" in meta:
                field_info["min"] = meta["min"]
            if "max" in meta:
                field_info["max"] = meta["max"]
        elif unwrapped is float or isinstance(default_val, float):
            field_info["type"] = "float"
            num_default = float(default_val) if default_val is not None else 0.0
            bounds = _infer_numeric_bounds(f.name, num_default, is_int=False)
            field_info["step"] = meta.get("step", bounds["step"])
            if "min" in meta:
                field_info["min"] = meta["min"]
            if "max" in meta:
                field_info["max"] = meta["max"]
        else:
            field_info["type"] = "str"
            field_info["default"] = None if (is_nullable and serialized_default is None) else ("" if serialized_default is None else str(serialized_default))

        fields_schema.append(field_info)

    return fields_schema, defaults


def _introspect_function_signature(func: Any) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Fallback introspection for scripts with build(a: float = 10.0, ...) functions."""
    sig = inspect.signature(func)
    try:
        hints = get_type_hints(func)
    except Exception:
        hints = {}

    fields_schema: list[dict[str, Any]] = []
    defaults: dict[str, Any] = {}

    for param_name, param in sig.parameters.items():
        if param.kind in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD):
            continue
        default_val = None if param.default is inspect.Parameter.empty else param.default
        raw_type = hints.get(param_name, param.annotation)
        if raw_type is not inspect.Parameter.empty:
            unwrapped, is_nullable = _unwrap_optional(raw_type)
        else:
            unwrapped, is_nullable = type(default_val), default_val is None

        serialized_default = _serialize_default(default_val)
        defaults[param_name] = serialized_default

        field_info: dict[str, Any] = {
            "name": param_name,
            "path": param_name,
            "label": humanize_name(param_name),
            "default": serialized_default,
            "description": "",
        }
        if is_nullable:
            field_info["nullable"] = True

        origin = get_origin(unwrapped)
        if origin is Literal:
            field_info["type"] = "enum"
            field_info["choices"] = [str(c) for c in get_args(unwrapped)]
        elif inspect.isclass(unwrapped) and issubclass(unwrapped, enum.Enum):
            field_info["type"] = "enum"
            field_info["choices"] = [str(m.value) for m in unwrapped]
        elif unwrapped is bool or isinstance(default_val, bool):
            field_info["type"] = "bool"
            field_info["default"] = None if (is_nullable and default_val is None) else (bool(default_val) if default_val is not None else False)
        elif unwrapped is int or (isinstance(default_val, int) and not isinstance(default_val, bool)):
            field_info["type"] = "int"
            field_info["step"] = 1
            field_info["default"] = None if (is_nullable and default_val is None) else (int(default_val) if default_val is not None else 0)
        elif unwrapped is float or isinstance(default_val, float):
            field_info["type"] = "float"
            field_info["step"] = 0.5
            field_info["default"] = None if (is_nullable and default_val is None) else (float(default_val) if default_val is not None else 0.0)
        else:
            field_info["type"] = "str"
            field_info["default"] = None if (is_nullable and default_val is None) else ("" if default_val is None else str(default_val))

        fields_schema.append(field_info)

    return fields_schema, defaults


def _coerce_value(val: Any, target_type: Any, default_val: Any) -> Any:
    unwrapped, is_nullable = _unwrap_optional(target_type)
    if val is None:
        return None if is_nullable else default_val

    if dataclasses.is_dataclass(unwrapped) or dataclasses.is_dataclass(default_val):
        cls = unwrapped if dataclasses.is_dataclass(unwrapped) else type(default_val)
        if isinstance(val, dict):
            return _instantiate_dataclass(cls, val)
        return default_val

    if inspect.isclass(unwrapped) and issubclass(unwrapped, enum.Enum):
        for member in unwrapped:
            if member.value == val or str(member.value) == str(val) or member.name == str(val):
                return member
        return unwrapped(val)

    origin = get_origin(unwrapped)
    if origin is Literal:
        choices = get_args(unwrapped)
        for c in choices:
            if c == val or str(c) == str(val):
                return c
        return default_val

    if origin in (list, tuple):
        if isinstance(val, str):
            stripped = val.strip()
            if stripped == "":
                return None if (is_nullable or default_val is None) else default_val
            try:
                parsed = json.loads(stripped)
                if isinstance(parsed, list):
                    val = parsed
                else:
                    val = [parsed]
            except Exception:
                val = [part.strip() for part in stripped.strip("[]()").split(",") if part.strip()]
        if isinstance(val, (list, tuple)):
            elem_args = get_args(unwrapped)
            if origin is tuple and elem_args and not (len(elem_args) == 2 and elem_args[1] is Ellipsis):
                coerced_items = [
                    _coerce_value(item, elem_args[i] if i < len(elem_args) else Any, None)
                    for i, item in enumerate(val)
                ]
                return tuple(coerced_items)
            elem_type = elem_args[0] if elem_args else Any
            coerced_items = [
                _coerce_value(item, elem_type, None) if elem_type is not Any else item
                for item in val
            ]
            return tuple(coerced_items) if origin is tuple else coerced_items
        return None if is_nullable else default_val

    if unwrapped is bool or isinstance(default_val, bool):
        if isinstance(val, bool):
            return val
        if isinstance(val, str):
            return val.lower() in ("true", "1", "on", "yes")
        return bool(val)

    if unwrapped is int or (isinstance(default_val, int) and not isinstance(default_val, bool)):
        if val == "":
            return None if (is_nullable or default_val is None) else default_val
        return int(round(float(val)))

    if unwrapped is float or isinstance(default_val, float):
        if val == "":
            return None if (is_nullable or default_val is None) else default_val
        return float(val)

    return str(val)


def _instantiate_dataclass(cls: type, overrides: dict[str, Any]) -> Any:
    """Instantiate a dataclass safely, filtering unknown keys and coercing types."""
    try:
        hints = get_type_hints(cls)
    except Exception:
        hints = {}
    default_instance = cls()
    valid_fields = {f.name: f for f in dataclasses.fields(cls)}
    kwargs: dict[str, Any] = {}

    for name, f in valid_fields.items():
        if name in overrides:
            target_type = hints.get(name, f.type)
            default_val = getattr(default_instance, name)
            kwargs[name] = _coerce_value(overrides[name], target_type, default_val)

    return cls(**kwargs)


def _sanitize_filename_stem(raw: str, fallback: str) -> str:
    """Sanitize a candidate filename stem to safe lowercase snake_case without extension."""
    stem = Path(str(raw).strip()).name
    for ext in (".step", ".stp", ".stl", ".3mf"):
        if stem.lower().endswith(ext):
            stem = stem[: -len(ext)]
            break
    stem = stem.strip().lower()
    stem = re.sub(r"[^a-z0-9._-]+", "_", stem)
    stem = re.sub(r"_+", "_", stem).strip("._-")
    return stem or fallback


def _to_json_safe(obj: Any) -> Any:
    """Recursively convert numpy arrays / tuples into plain JSON-serializable lists."""
    if hasattr(obj, "tolist"):
        return obj.tolist()
    if isinstance(obj, dict):
        return {str(k): _to_json_safe(v) for k, v in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [_to_json_safe(v) for v in obj]
    if isinstance(obj, float) and (math.isnan(obj) or math.isinf(obj)):
        return 0.0
    return obj


def _color_to_hex(color: Any, fallback: str) -> tuple[str, float]:
    """Convert build123d Color / RGBA tuple / hex string to (#rrggbb, alpha)."""
    if color is None:
        return fallback, 1.0
    if isinstance(color, str):
        return color, 1.0
    # build123d.Color has .to_tuple() -> (r, g, b, a) in 0..1
    if hasattr(color, "to_tuple"):
        tup = color.to_tuple()
        r, g, b = [max(0, min(255, int(round(c * 255)))) for c in tup[:3]]
        a = float(tup[3]) if len(tup) > 3 else 1.0
        return f"#{r:02x}{g:02x}{b:02x}", a
    if isinstance(color, (list, tuple)) and len(color) >= 3:
        vals = list(color)
        scale = 255.0 if max(vals[:3]) <= 1.0 else 1.0
        r, g, b = [max(0, min(255, int(round(c * scale)))) for c in vals[:3]]
        a = float(vals[3]) if len(vals) > 3 else 1.0
        return f"#{r:02x}{g:02x}{b:02x}", a
    return fallback, 1.0


def _unwrap_shape(obj: Any) -> Any:
    """Unwrap BuildPart/BuildSketch/BuildLine or Shape wrappers."""
    if hasattr(obj, "part") and getattr(obj, "part") is not None:
        return obj.part
    if hasattr(obj, "sketch") and getattr(obj, "sketch") is not None:
        return obj.sketch
    if hasattr(obj, "line") and getattr(obj, "line") is not None:
        return obj.line
    return obj


def _collect_named_parts(result: Any, root_name: str) -> list[tuple[str, Any, str, float]]:
    """Flatten Part / Compound / dict / list into (name, shape, hex_color, alpha)."""
    parts: list[tuple[str, Any, str, float]] = []

    def add_item(name: str, item: Any, idx: int) -> None:
        item = _unwrap_shape(item)
        if item is None:
            return
        fallback_color = DEFAULT_COLORS[idx % len(DEFAULT_COLORS)]
        # Compound with labeled children
        children = getattr(item, "children", None)
        if children and len(children) > 0:
            for c_idx, child in enumerate(children):
                child_label = getattr(child, "label", "") or f"{name}_{c_idx + 1}"
                add_item(child_label, child, idx + c_idx)
            return
        color_attr = getattr(item, "color", None)
        hex_col, alpha = _color_to_hex(color_attr, fallback_color)
        parts.append((name, item, hex_col, alpha))

    if isinstance(result, dict):
        for i, (k, v) in enumerate(result.items()):
            add_item(str(k), v, i)
    elif isinstance(result, (list, tuple)):
        for i, v in enumerate(result):
            lbl = getattr(v, "label", "") or f"Part_{i + 1}"
            add_item(lbl, v, i)
    else:
        lbl = getattr(result, "label", "") or root_name
        add_item(lbl, result, 0)

    return parts


def _tessellate_shape_ocp(shape: Any, linear_deflection: float = 0.1, angular_deflection: float = 0.2) -> tuple[dict[str, Any], dict[str, float]]:
    """Pure OCP tessellator producing three-cad-viewer v5 Shape JSON + bbox."""
    from OCP.Bnd import Bnd_Box
    from OCP.BRep import BRep_Tool
    from OCP.BRepAdaptor import BRepAdaptor_Curve
    from OCP.BRepBndLib import BRepBndLib
    from OCP.BRepGProp import BRepGProp_Face
    from OCP.BRepMesh import BRepMesh_IncrementalMesh
    from OCP.GCPnts import GCPnts_QuasiUniformDeflection
    from OCP.gp import gp_Pnt, gp_Vec
    from OCP.TopAbs import TopAbs_EDGE, TopAbs_FACE, TopAbs_REVERSED
    from OCP.TopExp import TopExp_Explorer
    from OCP.TopLoc import TopLoc_Location
    from OCP.TopoDS import TopoDS

    wrapped = getattr(shape, "wrapped", shape)

    # Bounding box
    bnd = Bnd_Box()
    BRepBndLib.AddOptimal_s(wrapped, bnd, True, False)
    xmin, ymin, zmin, xmax, ymax, zmax = bnd.Get()
    diag = math.sqrt((xmax - xmin) ** 2 + (ymax - ymin) ** 2 + (zmax - zmin) ** 2)
    defl = max(0.02, min(linear_deflection, diag * 0.002 if diag > 0 else 0.1))

    BRepMesh_IncrementalMesh(wrapped, defl, False, angular_deflection, True)

    vertices: list[float] = []
    normals: list[float] = []
    triangles: list[int] = []
    triangles_per_face: list[int] = []
    face_types: list[int] = []

    vertex_offset = 0
    face_exp = TopExp_Explorer(wrapped, TopAbs_FACE)
    while face_exp.More():
        face = TopoDS.Face_s(face_exp.Current())
        loc = TopLoc_Location()
        tri = BRep_Tool.Triangulation_s(face, loc)
        if tri is not None:
            trsf = loc.Transformation()
            nb_nodes = tri.NbNodes()
            nb_tris = tri.NbTriangles()
            is_rev = face.Orientation() == TopAbs_REVERSED

            face_verts: list[tuple[float, float, float]] = []
            face_norms: list[tuple[float, float, float]] = []
            has_tri_normals = tri.HasNormals()
            has_uv = (not has_tri_normals) and tri.HasUVNodes()
            gprop = BRepGProp_Face(face) if has_uv else None
            gp_p = gp_Pnt()
            gp_v = gp_Vec()

            for i in range(1, nb_nodes + 1):
                p = tri.Node(i).Transformed(trsf)
                face_verts.append((p.X(), p.Y(), p.Z()))
                if has_tri_normals:
                    n = tri.Normal(i).Transformed(trsf)
                    nx, ny, nz = n.X(), n.Y(), n.Z()
                    if is_rev:
                        nx, ny, nz = -nx, -ny, -nz
                    length = math.sqrt(nx * nx + ny * ny + nz * nz)
                    if length > 1e-12:
                        face_norms.append((nx / length, ny / length, nz / length))
                    else:
                        face_norms.append((0.0, 0.0, 0.0))
                elif gprop is not None:
                    try:
                        uv = tri.UVNode(i)
                        gprop.Normal(uv.X(), uv.Y(), gp_p, gp_v)
                        nx, ny, nz = gp_v.X(), gp_v.Y(), gp_v.Z()
                        length = math.sqrt(nx * nx + ny * ny + nz * nz)
                        if length > 1e-12:
                            face_norms.append((nx / length, ny / length, nz / length))
                        else:
                            face_norms.append((0.0, 0.0, 0.0))
                    except Exception:
                        face_norms.append((0.0, 0.0, 0.0))
                else:
                    face_norms.append((0.0, 0.0, 0.0))

            face_tris: list[tuple[int, int, int]] = []
            for i in range(1, nb_tris + 1):
                t = tri.Triangle(i)
                n1, n2, n3 = t.Get()
                if is_rev:
                    face_tris.append((n1 - 1, n3 - 1, n2 - 1))
                else:
                    face_tris.append((n1 - 1, n2 - 1, n3 - 1))

            # Fallback: compute vertex normals from triangle cross products if any are zero
            if not has_tri_normals and not has_uv:
                acc = [[0.0, 0.0, 0.0] for _ in range(nb_nodes)]
                for a, b, c in face_tris:
                    ax, ay, az = face_verts[a]
                    bx, by, bz = face_verts[b]
                    cx, cy, cz = face_verts[c]
                    ux, uy, uz = bx - ax, by - ay, bz - az
                    vx, vy, vz = cx - ax, cy - ay, cz - az
                    nx = uy * vz - uz * vy
                    ny = uz * vx - ux * vz
                    nz = ux * vy - uy * vx
                    for idx in (a, b, c):
                        acc[idx][0] += nx
                        acc[idx][1] += ny
                        acc[idx][2] += nz
                face_norms = []
                for nx, ny, nz in acc:
                    length = math.sqrt(nx * nx + ny * ny + nz * nz)
                    if length > 1e-12:
                        face_norms.append((nx / length, ny / length, nz / length))
                    else:
                        face_norms.append((0.0, 0.0, 1.0))

            for vx, vy, vz in face_verts:
                vertices.extend([vx, vy, vz])
            for nx, ny, nz in face_norms:
                if nx == 0.0 and ny == 0.0 and nz == 0.0:
                    normals.extend([0.0, 0.0, 1.0])
                else:
                    normals.extend([nx, ny, nz])
            for a, b, c in face_tris:
                triangles.extend([vertex_offset + a, vertex_offset + b, vertex_offset + c])

            triangles_per_face.append(nb_tris)
            face_types.append(0)
            vertex_offset += nb_nodes

        face_exp.Next()

    # Discretize edges for crisp CAD wireframe
    edges: list[float] = []
    segments_per_edge: list[int] = []
    edge_types: list[int] = []

    edge_exp = TopExp_Explorer(wrapped, TopAbs_EDGE)
    while edge_exp.More():
        edge = TopoDS.Edge_s(edge_exp.Current())
        if not BRep_Tool.Degenerated_s(edge):
            try:
                adaptor = BRepAdaptor_Curve(edge)
                discr = GCPnts_QuasiUniformDeflection(adaptor, defl)
                if discr.IsDone() and discr.NbPoints() >= 2:
                    n_pts = discr.NbPoints()
                    seg_count = 0
                    prev = adaptor.Value(discr.Parameter(1))
                    for i in range(2, n_pts + 1):
                        curr = adaptor.Value(discr.Parameter(i))
                        edges.extend([prev.X(), prev.Y(), prev.Z(), curr.X(), curr.Y(), curr.Z()])
                        prev = curr
                        seg_count += 1
                    if seg_count > 0:
                        segments_per_edge.append(seg_count)
                        edge_types.append(0)
            except Exception:
                pass
        edge_exp.Next()

    shape_payload = {
        "vertices": vertices,
        "normals": normals,
        "triangles": triangles,
        "triangles_per_face": triangles_per_face,
        "edges": edges,
        "segments_per_edge": segments_per_edge,
        "obj_vertices": [],
        "face_types": face_types,
        "edge_types": edge_types,
    }
    bbox = {
        "xmin": xmin,
        "xmax": xmax,
        "ymin": ymin,
        "ymax": ymax,
        "zmin": zmin,
        "zmax": zmax,
    }
    return shape_payload, bbox


def _encode_b64_array(values: Any, dtype: str) -> dict[str, str]:
    """Encode a numpy array or Python sequence as a three-cad-viewer inline b64 buffer."""
    if hasattr(values, "tobytes"):
        np_dtype = "<f4" if dtype == "float32" else "<u4"
        raw = values.astype(np_dtype, copy=False).tobytes()
    else:
        typecode = "f" if dtype == "float32" else "I"
        arr = array.array(typecode, values)
        if sys.byteorder != "little":
            arr.byteswap()
        raw = arr.tobytes()
    return {
        "dtype": dtype,
        "codec": "b64",
        "buffer": base64.b64encode(raw).decode("ascii"),
    }


def _encode_shape_buffers(raw_shape: dict[str, Any]) -> dict[str, Any]:
    """Convert raw numeric arrays in a three-cad-viewer shape payload to compact b64 buffers."""
    float_keys = ("vertices", "normals", "edges", "obj_vertices")
    uint_keys = ("triangles", "triangles_per_face", "segments_per_edge", "face_types", "edge_types")
    encoded: dict[str, Any] = {}
    for k in float_keys:
        encoded[k] = _encode_b64_array(raw_shape.get(k, ()), "float32")
    for k in uint_keys:
        encoded[k] = _encode_b64_array(raw_shape.get(k, ()), "uint32")
    return encoded


def _tessellate_shape_fast(
    shape: Any,
    cache_tag: str,
    linear_deflection: float = 0.1,
    angular_deflection: float = 0.2,
) -> tuple[dict[str, Any], dict[str, float]]:
    """Use ocp_tessellate when available for fast NumPy tessellation, else fallback to pure OCP."""
    if HAS_OCP_TESSELLATE:
        try:
            from OCP.Bnd import Bnd_Box
            from OCP.BRepBndLib import BRepBndLib
            from ocp_tessellate.tessellator import tessellate as ocp_tess

            wrapped = getattr(shape, "wrapped", shape)
            bnd = Bnd_Box()
            BRepBndLib.AddOptimal_s(wrapped, bnd, True, False)
            xmin, ymin, zmin, xmax, ymax, zmax = bnd.Get()
            diag = math.sqrt((xmax - xmin) ** 2 + (ymax - ymin) ** 2 + (zmax - zmin) ** 2)
            defl = max(0.02, min(linear_deflection, diag * 0.002 if diag > 0 else 0.1))

            mesh = ocp_tess(
                [wrapped],
                cache_tag,
                quality=defl,
                deviation=defl,
                angular_tolerance=angular_deflection,
                compute_edges=True,
                compute_faces=True,
            )
            bbox = {
                "xmin": xmin,
                "xmax": xmax,
                "ymin": ymin,
                "ymax": ymax,
                "zmin": zmin,
                "zmax": zmax,
            }
            return _encode_shape_buffers(mesh), bbox
        except Exception:
            pass

    shape_data, bbox = _tessellate_shape_ocp(
        shape,
        linear_deflection=linear_deflection,
        angular_deflection=angular_deflection,
    )
    return _encode_shape_buffers(shape_data), bbox


def tessellate_result(result: Any, model_name: str) -> tuple[dict[str, Any], dict[str, float]]:
    """Tessellate a model's returned shape(s) into three-cad-viewer v5 Shapes JSON."""
    named_parts = _collect_named_parts(result, model_name)
    if not named_parts:
        raise ValueError("Model returned no geometry.")

    root_id = f"/{model_name}"
    viewer_parts: list[dict[str, Any]] = []
    g_xmin = g_ymin = g_zmin = float("inf")
    g_xmax = g_ymax = g_zmax = float("-inf")

    seen_names: dict[str, int] = {}
    ts_ns = time.monotonic_ns()
    for name, shp, hex_color, alpha in named_parts:
        clean_name = re.sub(r"[^A-Za-z0-9_\- ]+", "_", name).strip() or "Part"
        count = seen_names.get(clean_name, 0)
        seen_names[clean_name] = count + 1
        if count > 0:
            clean_name = f"{clean_name}_{count + 1}"

        shape_data, bb = _tessellate_shape_fast(shp, f"{clean_name}_{id(shp)}_{ts_ns}")
        g_xmin = min(g_xmin, bb["xmin"])
        g_xmax = max(g_xmax, bb["xmax"])
        g_ymin = min(g_ymin, bb["ymin"])
        g_ymax = max(g_ymax, bb["ymax"])
        g_zmin = min(g_zmin, bb["zmin"])
        g_zmax = max(g_zmax, bb["zmax"])

        viewer_parts.append(
            {
                "version": 3,
                "id": f"{root_id}/{clean_name}",
                "name": clean_name,
                "type": "shapes",
                "subtype": "solid",
                "state": [1, 1],
                "color": hex_color,
                "alpha": alpha,
                "loc": [[0.0, 0.0, 0.0], [0.0, 0.0, 0.0, 1.0]],
                "shape": shape_data,
            }
        )

    overall_bb = {
        "xmin": g_xmin,
        "xmax": g_xmax,
        "ymin": g_ymin,
        "ymax": g_ymax,
        "zmin": g_zmin,
        "zmax": g_zmax,
    }
    shapes_root = {
        "version": 3,
        "name": model_name,
        "id": root_id,
        "loc": [[0.0, 0.0, 0.0], [0.0, 0.0, 0.0, 1.0]],
        "bb": overall_bb,
        "parts": viewer_parts,
    }
    bbox_summary = {
        **overall_bb,
        "dx": round(g_xmax - g_xmin, 3),
        "dy": round(g_ymax - g_ymin, 3),
        "dz": round(g_zmax - g_zmin, 3),
    }
    return shapes_root, bbox_summary


def export_shape_to_file(result: Any, fmt: str, out_path: Path) -> None:
    """Export build123d shape or dict/list of shapes to .step, .stl, or .3mf."""
    import build123d as b3d

    named_parts = _collect_named_parts(result, "model")
    if not named_parts:
        raise ValueError("No geometry to export.")

    if len(named_parts) == 1:
        export_obj = named_parts[0][1]
    else:
        children = []
        for name, shp, _, _ in named_parts:
            try:
                shp.label = name
            except Exception:
                pass
            children.append(shp)
        export_obj = b3d.Compound(children=children)

    fmt_lower = fmt.lower().lstrip(".")
    if fmt_lower in ("step", "stp"):
        b3d.export_step(export_obj, str(out_path))
    elif fmt_lower == "stl":
        b3d.export_stl(export_obj, str(out_path))
    elif fmt_lower == "3mf":
        mesher = b3d.Mesher()
        mesher.add_shape(export_obj)
        mesher.write(str(out_path))
    else:
        raise ValueError(f"Unsupported export format: {fmt}")


MAX_RENDER_CACHE = 24


class WorkerState:
    def __init__(self, project_root: str = "") -> None:
        self.lock = threading.RLock()
        self._seq_lock = threading.Lock()
        self._render_seq = 0
        self.project_root: Path | None = None
        self.models: dict[str, dict[str, Any]] = {}
        self.loaded_modules: dict[str, Any] = {}
        self.last_build: dict[str, Any] = {}
        self._render_cache: OrderedDict[tuple[str, str], dict[str, Any]] = OrderedDict()
        if project_root:
            self.configure(project_root)

    def _next_render_seq(self) -> int:
        with self._seq_lock:
            self._render_seq += 1
            return self._render_seq

    def _latest_render_seq(self) -> int:
        with self._seq_lock:
            return self._render_seq

    def _evict_project_modules(self) -> None:
        """Evict loaded model and project helper modules from sys.modules for warm reload."""
        roots: list[Path] = []
        if self.project_root is not None:
            roots.append(self.project_root)
            if self.project_root.name == "models" and (self.project_root.parent / "components").is_dir():
                roots.append(self.project_root.parent / "components")

        to_delete: list[str] = []
        for mod_name, mod in list(sys.modules.items()):
            if mod_name.startswith("code_cad_model_"):
                to_delete.append(mod_name)
                continue
            mod_file = getattr(mod, "__file__", None)
            if not mod_file or not roots:
                continue
            try:
                resolved = Path(mod_file).resolve()
            except Exception:
                continue
            for root in roots:
                try:
                    resolved.relative_to(root)
                    to_delete.append(mod_name)
                    break
                except ValueError:
                    continue

        for mod_name in to_delete:
            sys.modules.pop(mod_name, None)

        self.loaded_modules.clear()
        self.last_build.clear()
        self._render_cache.clear()

    def reload_project(self) -> list[dict[str, Any]]:
        """Warm-reload project scripts without restarting Python or re-importing build123d."""
        with self.lock:
            self._evict_project_modules()
            return self.discover_models()

    def configure(self, project_root: str) -> list[dict[str, Any]]:
        with self.lock:
            root = Path(project_root).expanduser().resolve()
            if not root.exists() or not root.is_dir():
                raise FileNotFoundError(f"Project root does not exist: {root}")

            self._evict_project_modules()

            # Remove previous project root from sys.path
            if self.project_root is not None:
                old_str = str(self.project_root)
                if old_str in sys.path:
                    sys.path.remove(old_str)

            self.project_root = root
            root_str = str(root)
            if root_str not in sys.path:
                sys.path.insert(0, root_str)
            # If user selected the models/ subdirectory directly, also add its parent
            # to sys.path when a sibling components/ directory exists.
            if root.name == "models" and (root.parent / "components").is_dir():
                parent_str = str(root.parent)
                if parent_str not in sys.path:
                    sys.path.insert(0, parent_str)

            return self.discover_models()

    def discover_models(self) -> list[dict[str, Any]]:
        with self.lock:
            self.models.clear()
            if self.project_root is None:
                return []

            found: list[dict[str, Any]] = []
            for dirpath, dirnames, filenames in os.walk(self.project_root):
                dirnames[:] = sorted(
                    d for d in dirnames if d not in SKIP_DIRS and not d.startswith(".")
                )
                for fname in sorted(filenames):
                    if not fname.endswith(".py"):
                        continue
                    if fname.startswith("_") or fname.startswith("test_") or fname == "setup.py":
                        continue
                    py_file = Path(dirpath) / fname
                    meta = _discover_script_metadata(py_file, self.project_root)
                    if meta is not None:
                        self.models[meta["id"]] = meta
                        found.append(meta)

            found.sort(key=lambda m: m["name"].lower())
            return found

    def _load_model_module(self, model_id: str) -> tuple[dict[str, Any], Any]:
        if model_id not in self.models:
            self.discover_models()
        if model_id not in self.models:
            raise KeyError(f"Unknown model: {model_id}")

        meta = self.models[model_id]
        if model_id in self.loaded_modules:
            return meta, self.loaded_modules[model_id]

        py_path = Path(meta["path"])
        mod_name = f"code_cad_model_{re.sub(r'[^a-zA-Z0-9_]', '_', model_id)}"
        spec = importlib.util.spec_from_file_location(mod_name, str(py_path))
        if spec is None or spec.loader is None:
            raise ImportError(f"Could not load module spec for {py_path}")

        module = importlib.util.module_from_spec(spec)
        sys.modules[mod_name] = module
        spec.loader.exec_module(module)
        self.loaded_modules[model_id] = module
        return meta, module

    def _resolve_entrypoints(self, meta: dict[str, Any], module: Any) -> tuple[type | None, Any]:
        func = getattr(module, meta["entrypoint"], None)
        if func is None:
            raise AttributeError(f"Entrypoint {meta['entrypoint']} not found in {meta['path']}")

        params_cls = None
        # 1. Explicit type annotation on the first parameter of create_<model>(params: <Model>Params)
        try:
            sig = inspect.signature(func)
            hints = get_type_hints(func)
            first_param = next(iter(sig.parameters.values()), None)
            if first_param is not None:
                ann = hints.get(first_param.name, first_param.annotation)
                if inspect.isclass(ann) and dataclasses.is_dataclass(ann):
                    params_cls = ann
        except Exception:
            pass

        # 2. Discovered params_class name from AST
        if params_cls is None and meta.get("params_class") and hasattr(module, meta["params_class"]):
            candidate = getattr(module, meta["params_class"])
            if dataclasses.is_dataclass(candidate) and isinstance(candidate, type):
                params_cls = candidate

        # 3. Fallback: scan module for a *Params dataclass
        if params_cls is None:
            for attr_name in dir(module):
                if attr_name.endswith("Params"):
                    candidate = getattr(module, attr_name)
                    if dataclasses.is_dataclass(candidate) and isinstance(candidate, type):
                        params_cls = candidate
                        break

        return params_cls, func

    def get_schema(self, model_id: str) -> dict[str, Any]:
        with self.lock:
            meta, module = self._load_model_module(model_id)
            params_cls, func = self._resolve_entrypoints(meta, module)

            if params_cls is not None:
                fields, defaults = _introspect_dataclass(params_cls, Path(meta["path"]))
            else:
                fields, defaults = _introspect_function_signature(func)

            doc = inspect.getdoc(func) or inspect.getdoc(module) or meta.get("description", "")
            return {
                "ok": True,
                "model": meta,
                "description": doc.splitlines()[0] if doc else "",
                "fields": fields,
                "defaults": defaults,
            }

    def _resolve_export_filename(
        self,
        model_id: str,
        module: Any,
        params_cls: type | None,
        params_dict: dict[str, Any],
        params_obj: Any = None,
    ) -> str:
        hook = getattr(module, "export_filename", None)
        if not callable(hook):
            return model_id
        try:
            if params_obj is None and params_cls is not None:
                params_obj = _instantiate_dataclass(params_cls, params_dict)
            if params_obj is not None:
                raw = hook(params_obj)
            else:
                raw = hook(params_dict)
            if isinstance(raw, str) and raw.strip():
                return _sanitize_filename_stem(raw, model_id)
        except Exception:
            pass
        return model_id

    def get_export_filename(self, model_id: str, params_dict: dict[str, Any]) -> dict[str, Any]:
        with self.lock:
            meta, module = self._load_model_module(model_id)
            params_cls, _ = self._resolve_entrypoints(meta, module)
            stem = self._resolve_export_filename(model_id, module, params_cls, params_dict)
            return {"ok": True, "export_filename": stem}

    def _build_shape(self, model_id: str, params_dict: dict[str, Any]) -> tuple[dict[str, Any], Any, float, str]:
        if not HAS_BUILD123D:
            raise RuntimeError(f"build123d is not available in {sys.executable}: {BUILD123D_ERROR}")

        meta, module = self._load_model_module(model_id)
        params_cls, func = self._resolve_entrypoints(meta, module)

        t0 = time.perf_counter()
        params_obj = None
        if params_cls is not None:
            params_obj = _instantiate_dataclass(params_cls, params_dict)
            result = func(params_obj)
        else:
            sig = inspect.signature(func)
            try:
                hints = get_type_hints(func)
            except Exception:
                hints = {}
            kwargs = {}
            for name, p in sig.parameters.items():
                if name in params_dict:
                    default_val = None if p.default is inspect.Parameter.empty else p.default
                    kwargs[name] = _coerce_value(params_dict[name], hints.get(name, p.annotation), default_val)
            result = func(**kwargs)

        build_ms = (time.perf_counter() - t0) * 1000.0
        export_stem = self._resolve_export_filename(model_id, module, params_cls, params_dict, params_obj)
        self.last_build = {
            "model_id": model_id,
            "params_json": json.dumps(params_dict, sort_keys=True),
            "result": result,
            "meta": meta,
            "export_filename": export_stem,
        }
        return meta, result, build_ms, export_stem

    def render(self, model_id: str, params_dict: dict[str, Any]) -> dict[str, Any]:
        my_seq = self._next_render_seq()
        params_key = json.dumps(params_dict, sort_keys=True)
        cache_key = (model_id, params_key)

        with self.lock:
            if my_seq != self._latest_render_seq():
                return {"ok": False, "superseded": True, "model_id": model_id}

            cached = self._render_cache.get(cache_key)
            if cached is not None:
                self._render_cache.move_to_end(cache_key)
                return {**cached, "cached": True, "build_time_ms": 0.0, "tessellate_time_ms": 0.0}

            meta, result, build_ms, export_stem = self._build_shape(model_id, params_dict)
            if my_seq != self._latest_render_seq():
                return {"ok": False, "superseded": True, "model_id": model_id}

            t1 = time.perf_counter()
            shapes, bbox = tessellate_result(result, meta["name"])
            tess_ms = (time.perf_counter() - t1) * 1000.0
            payload = {
                "ok": True,
                "model_id": model_id,
                "build_time_ms": round(build_ms, 1),
                "tessellate_time_ms": round(tess_ms, 1),
                "bbox": bbox,
                "shapes": _to_json_safe(shapes),
                "export_filename": export_stem,
            }
            self._render_cache[cache_key] = payload
            self._render_cache.move_to_end(cache_key)
            while len(self._render_cache) > MAX_RENDER_CACHE:
                self._render_cache.popitem(last=False)
            return payload

    def export(self, model_id: str, params_dict: dict[str, Any], fmt: str, output_path: str = "") -> tuple[dict[str, Any] | None, bytes | None]:
        with self.lock:
            params_key = json.dumps(params_dict, sort_keys=True)
            if (
                self.last_build.get("model_id") == model_id
                and self.last_build.get("params_json") == params_key
                and self.last_build.get("result") is not None
            ):
                result = self.last_build["result"]
            else:
                _, result, _, _ = self._build_shape(model_id, params_dict)

            if output_path:
                out = Path(output_path).expanduser().resolve()
                out.parent.mkdir(parents=True, exist_ok=True)
                export_shape_to_file(result, fmt, out)
                return {"ok": True, "path": str(out), "size": out.stat().st_size}, None

            with tempfile.TemporaryDirectory() as tmpdir:
                out = Path(tmpdir) / f"{model_id}.{fmt.lower().lstrip('.')}"
                export_shape_to_file(result, fmt, out)
                return None, out.read_bytes()


def make_handler(state: WorkerState) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, format: str, *args: Any) -> None:
            # Keep stdout clean; send logs to stderr.
            sys.stderr.write("[worker] " + (format % args) + "\n")
            sys.stderr.flush()

        def _send_json(self, payload: dict[str, Any], status: int = 200) -> None:
            body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _read_json(self) -> dict[str, Any]:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0:
                return {}
            raw = self.rfile.read(length)
            return json.loads(raw.decode("utf-8"))

        def do_GET(self) -> None:
            parsed = urlparse(self.path)
            path = parsed.path
            try:
                if path == "/health":
                    self._send_json(
                        {
                            "ok": True,
                            "python": sys.executable,
                            "build123d": HAS_BUILD123D,
                            "build123d_error": BUILD123D_ERROR,
                            "ocp_tessellate": HAS_OCP_TESSELLATE,
                            "project_root": str(state.project_root) if state.project_root else "",
                        }
                    )
                    return

                if path == "/models":
                    models = state.discover_models()
                    self._send_json({"ok": True, "models": models})
                    return

                m = re.match(r"^/models/([^/]+)/schema$", path)
                if m:
                    model_id = unquote(m.group(1))
                    self._send_json(state.get_schema(model_id))
                    return

                self._send_json({"ok": False, "error": f"Not found: {path}"}, status=404)
            except Exception as exc:
                self._send_json(
                    {
                        "ok": False,
                        "error": str(exc),
                        "traceback": traceback.format_exc(),
                    },
                    status=500,
                )

        def do_POST(self) -> None:
            parsed = urlparse(self.path)
            path = parsed.path
            try:
                body = self._read_json()

                if path == "/configure":
                    project_root = body.get("project_root", "")
                    models = state.configure(project_root)
                    self._send_json({"ok": True, "project_root": str(state.project_root), "models": models})
                    return

                if path == "/reload":
                    models = state.reload_project()
                    self._send_json({"ok": True, "models": models})
                    return

                m_render = re.match(r"^/models/([^/]+)/render$", path)
                if m_render:
                    model_id = unquote(m_render.group(1))
                    params = body.get("params", {})
                    self._send_json(state.render(model_id, params))
                    return

                m_export_name = re.match(r"^/models/([^/]+)/export-name$", path)
                if m_export_name:
                    model_id = unquote(m_export_name.group(1))
                    params = body.get("params", {})
                    self._send_json(state.get_export_filename(model_id, params))
                    return

                m_export = re.match(r"^/models/([^/]+)/export$", path)
                if m_export:
                    model_id = unquote(m_export.group(1))
                    params = body.get("params", {})
                    fmt = body.get("format", "step")
                    output_path = body.get("output_path", "")
                    meta_resp, raw_bytes = state.export(model_id, params, fmt, output_path)
                    if meta_resp is not None:
                        self._send_json(meta_resp)
                    else:
                        assert raw_bytes is not None
                        self.send_response(200)
                        self.send_header("Content-Type", "application/octet-stream")
                        self.send_header("Content-Length", str(len(raw_bytes)))
                        self.end_headers()
                        self.wfile.write(raw_bytes)
                    return

                self._send_json({"ok": False, "error": f"Not found: {path}"}, status=404)
            except Exception as exc:
                self._send_json(
                    {
                        "ok": False,
                        "error": str(exc),
                        "traceback": traceback.format_exc(),
                    },
                    status=500,
                )

    return Handler


def main() -> None:
    parser = argparse.ArgumentParser(description="Code CAD Studio Python worker")
    parser.add_argument("--project-root", default="", help="Path to build123d project root")
    parser.add_argument("--port", type=int, default=0, help="Port to bind (0 for ephemeral)")
    args = parser.parse_args()

    state = WorkerState(args.project_root)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), make_handler(state))
    actual_port = server.server_address[1]

    # Signal readiness to the Go supervisor
    print(f"READY {actual_port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
