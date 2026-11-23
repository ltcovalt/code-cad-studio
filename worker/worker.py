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
import hashlib
import importlib
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

# Warmed up on a background thread right after the HTTP server prints READY,
# so directory discovery (GET /models) returns immediately without blocking on
# OpenCASCADE / build123d DLL imports.
HAS_BUILD123D = False
BUILD123D_ERROR = ""
HAS_OCP_TESSELLATE = False
_CAD_WARMUP_DONE = threading.Event()
_CAD_WARMUP_STARTED = False
_CAD_WARMUP_LOCK = threading.Lock()


def _warmup_cad_imports() -> None:
    global HAS_BUILD123D, BUILD123D_ERROR, HAS_OCP_TESSELLATE
    try:
        import build123d as b3d  # noqa: F401

        HAS_BUILD123D = True
        BUILD123D_ERROR = ""
    except Exception as exc:  # pragma: no cover - environment dependent
        HAS_BUILD123D = False
        BUILD123D_ERROR = str(exc)

    try:
        import ocp_tessellate  # noqa: F401

        HAS_OCP_TESSELLATE = True
    except Exception:
        HAS_OCP_TESSELLATE = False

    _CAD_WARMUP_DONE.set()


def _start_cad_warmup() -> None:
    global _CAD_WARMUP_STARTED
    with _CAD_WARMUP_LOCK:
        if _CAD_WARMUP_STARTED:
            return
        _CAD_WARMUP_STARTED = True
        t = threading.Thread(target=_warmup_cad_imports, name="cad-warmup", daemon=True)
        t.start()


def _wait_for_cad_warmup() -> None:
    _start_cad_warmup()
    _CAD_WARMUP_DONE.wait()


ALWAYS_SKIP_DIRS = {
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
}

ROOT_ONLY_SKIP_DIRS = {
    "components",
    "utils",
    "exports",
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
    return re.sub(r"[_-]+", " ", name).strip().title()


def humanize_class_name(name: str) -> str:
    spaced = re.sub(r"(?<=[a-z0-9])(?=[A-Z])|(?<=[A-Z])(?=[A-Z][a-z])", " ", name).strip()
    if spaced.endswith(" Params") and len(spaced) > len(" Params"):
        spaced = spaced[: -len(" Params")].strip()
    return spaced or "Item"


def _discover_script_metadata(
    py_file: Path,
    library_id: str,
    library_name: str,
    library_root: Path,
) -> dict[str, Any] | None:
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

    rel = py_file.relative_to(library_root)
    parts = list(rel.parts)
    if parts and parts[0] == "models":
        parts = parts[1:]

    stem = py_file.stem
    parent_dirs = parts[:-1]
    # Deduplicate canonical <name>/<name>.py directory wrapper at any depth
    if parent_dirs and parent_dirs[-1] == stem:
        category_folders = parent_dirs[:-1]
    else:
        category_folders = parent_dirs

    local_segments = [*category_folders, stem]
    local_id = "/".join(local_segments)
    model_id = f"{library_id}::{'__'.join(local_segments)}"

    docstring = ast.get_docstring(tree) or ""
    first_line = docstring.splitlines()[0].strip() if docstring else ""

    expected_fn = f"create_{stem}"
    if expected_fn in create_funcs:
        entrypoint = expected_fn
    elif create_funcs:
        entrypoint = create_funcs[-1]
    else:
        entrypoint = fallback_funcs[0]
    # Prefer the class matching <ModelName>Params, else the last *Params class (since sub-dataclasses precede the root)
    expected_cls = "".join(part.capitalize() for part in stem.split("_")) + "Params"
    if expected_cls in params_classes:
        params_class = expected_cls
    elif params_classes:
        params_class = params_classes[-1]
    else:
        params_class = ""

    return {
        "id": model_id,
        "local_id": local_id,
        "library_id": library_id,
        "library_name": library_name,
        "folders": category_folders,
        "folder_labels": [humanize_name(seg) for seg in category_folders],
        "name": humanize_name(stem),
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


def _introspect_list_item(
    elem_type: Any,
    sample_val: Any,
    meta: dict[str, Any],
) -> dict[str, Any] | None:
    """Build a ParamField schema describing a single element of a list parameter."""
    elem_unwrapped, elem_nullable = _unwrap_optional(elem_type)
    if (elem_unwrapped is Any or elem_unwrapped is None) and sample_val is not None:
        elem_unwrapped = type(sample_val)

    explicit_item_label = str(meta.get("item_label") or "").strip()

    if dataclasses.is_dataclass(elem_unwrapped):
        item_fields, item_defaults = _introspect_dataclass(elem_unwrapped, prefix="")
        item_doc = inspect.getdoc(elem_unwrapped) or ""
        item_desc = item_doc.splitlines()[0].strip() if item_doc else ""
        item_label = explicit_item_label or humanize_class_name(elem_unwrapped.__name__)
        item_schema: dict[str, Any] = {
            "name": "item",
            "path": "",
            "label": item_label,
            "type": "group",
            "default": item_defaults,
            "description": item_desc,
            "children": item_fields,
        }
        if elem_nullable:
            item_schema["nullable"] = True
        return item_schema

    item_label = explicit_item_label or "Item"
    unit = str(meta.get("unit") or "").strip()
    origin = get_origin(elem_unwrapped)

    item_info: dict[str, Any] = {
        "name": "item",
        "path": "",
        "label": item_label,
        "description": "",
    }
    if unit:
        item_info["unit"] = unit
    if elem_nullable:
        item_info["nullable"] = True

    if origin is Literal:
        choices = [str(c) for c in get_args(elem_unwrapped)]
        item_info["type"] = "enum"
        item_info["choices"] = choices
        item_info["default"] = None if elem_nullable else (choices[0] if choices else "")
        return item_info
    if inspect.isclass(elem_unwrapped) and issubclass(elem_unwrapped, enum.Enum):
        choices = [str(m.value) for m in elem_unwrapped]
        item_info["type"] = "enum"
        item_info["choices"] = choices
        item_info["default"] = None if elem_nullable else (choices[0] if choices else "")
        return item_info
    if elem_unwrapped is bool:
        item_info["type"] = "bool"
        item_info["default"] = None if elem_nullable else False
        return item_info
    if elem_unwrapped is int:
        item_info["type"] = "int"
        default_int = int(meta["min"]) if "min" in meta else 0
        item_info["default"] = None if elem_nullable else default_int
        item_info["step"] = meta.get("step", 1)
        if "min" in meta:
            item_info["min"] = meta["min"]
        if "max" in meta:
            item_info["max"] = meta["max"]
        return item_info
    if elem_unwrapped is float:
        item_info["type"] = "float"
        default_float = float(meta["min"]) if "min" in meta else 0.0
        item_info["default"] = None if elem_nullable else default_float
        item_info["step"] = meta.get("step", 0.5)
        if "min" in meta:
            item_info["min"] = meta["min"]
        if "max" in meta:
            item_info["max"] = meta["max"]
        return item_info
    if elem_unwrapped is str:
        item_info["type"] = "str"
        item_info["default"] = None if elem_nullable else ""
        return item_info

    return None


def _introspect_dataclass(
    cls: type,
    prefix: str = "",
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Introspect a @dataclass into UI schema fields and canonical default dict."""
    try:
        hints = get_type_hints(cls)
    except Exception:
        hints = {}

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
        desc = str(meta.get("description") or "").strip()
        label = str(meta.get("label") or "").strip() or humanize_name(f.name)
        unit = str(meta.get("unit") or "").strip()

        # Nested dataclass -> explicit collapsible section (toggleable if it has `enabled: bool`)
        if dataclasses.is_dataclass(unwrapped) or dataclasses.is_dataclass(default_val):
            nested_cls = unwrapped if dataclasses.is_dataclass(unwrapped) else type(default_val)
            nested_fields, nested_defaults = _introspect_dataclass(nested_cls, prefix=field_path)
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
        if unit:
            field_info["unit"] = unit
        if is_nullable:
            field_info["nullable"] = True
        if "when" in meta and isinstance(meta["when"], dict):
            field_info["when"] = meta["when"]

        if origin is list or unwrapped is list:
            elem_args = get_args(unwrapped)
            elem_type = elem_args[0] if elem_args else Any
            sample_val = default_val[0] if isinstance(default_val, list) and len(default_val) > 0 else None
            item_schema = _introspect_list_item(elem_type, sample_val, meta)
            if item_schema is not None:
                field_info["type"] = "list"
                field_info["item"] = item_schema
                field_info["default"] = None if (is_nullable and serialized_default is None) else (
                    serialized_default if isinstance(serialized_default, list) else []
                )
                fields_schema.append(field_info)
                continue

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
        return default_val if default_val is not None else cls()

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


def _values_equal(a: Any, b: Any) -> bool:
    if isinstance(a, bool) or isinstance(b, bool):
        return type(a) is type(b) and a == b
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return float(a) == float(b)
    if isinstance(a, (list, tuple)) and isinstance(b, (list, tuple)):
        return len(a) == len(b) and all(_values_equal(x, y) for x, y in zip(a, b))
    return a == b


def _is_safe_literal_expr(node: ast.AST) -> bool:
    """Allow only simple literals (constants, signed numbers, flat literal lists/tuples)."""
    if isinstance(node, ast.Constant):
        return node.value is None or isinstance(node.value, (bool, int, float, str))
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.UAdd, ast.USub)):
        return (
            isinstance(node.operand, ast.Constant)
            and isinstance(node.operand.value, (int, float))
            and not isinstance(node.operand.value, bool)
        )
    if isinstance(node, (ast.List, ast.Tuple)):
        return all(_is_safe_literal_expr(el) for el in node.elts)
    return False


def _format_python_literal(val: Any) -> str:
    if val is None:
        return "None"
    if isinstance(val, bool):
        return "True" if val else "False"
    if isinstance(val, int):
        return str(val)
    if isinstance(val, float):
        if not math.isfinite(val):
            raise ValueError(f"non-finite float cannot be written as literal: {val}")
        rounded = round(val, 10)
        if rounded == 0.0:
            rounded = 0.0
        return repr(float(rounded))
    if isinstance(val, str):
        return json.dumps(val, ensure_ascii=False)
    if isinstance(val, (list, tuple)):
        return "[" + ", ".join(_format_python_literal(v) for v in val) + "]"
    raise ValueError(f"unsupported literal type: {type(val).__name__}")


def _locate_default_expr(stmt: ast.AnnAssign) -> tuple[ast.AST | None, str | None]:
    val = stmt.value
    if val is None:
        return None, "field has no inline default value"
    if isinstance(val, ast.Call):
        callee = ""
        if isinstance(val.func, ast.Name):
            callee = val.func.id
        elif isinstance(val.func, ast.Attribute):
            callee = val.func.attr
        if callee == "param":
            for kw in val.keywords:
                if kw.arg == "default":
                    return kw.value, None
            if len(val.args) >= 1:
                return val.args[0], None
            return None, "param() call has no default argument"
        if callee == "param_list":
            return None, "param_list() uses default_factory and cannot be updated automatically"
        if callee == "field":
            for kw in val.keywords:
                if kw.arg == "default":
                    return kw.value, None
            return None, "field() uses default_factory or has no default= argument"
        return None, f"uses non-standard call ({callee or 'call'})"
    return val, None


def _collect_default_changes(
    cls: type,
    overrides: dict[str, Any],
    prefix: str = "",
) -> list[dict[str, Any]]:
    """Return leaf parameter changes comparing overrides against canonical dataclass defaults."""
    try:
        hints = get_type_hints(cls)
    except Exception:
        hints = {}

    default_instance = cls()
    changes: list[dict[str, Any]] = []

    for f in dataclasses.fields(cls):
        if f.name not in overrides:
            continue
        field_path = f"{prefix}.{f.name}" if prefix else f.name
        target_type = hints.get(f.name, f.type)
        unwrapped, _ = _unwrap_optional(target_type)
        default_val = getattr(default_instance, f.name)
        raw_override = overrides[f.name]

        if dataclasses.is_dataclass(unwrapped) or dataclasses.is_dataclass(default_val):
            nested_cls = unwrapped if dataclasses.is_dataclass(unwrapped) else type(default_val)
            if isinstance(raw_override, dict):
                changes.extend(_collect_default_changes(nested_cls, raw_override, prefix=field_path))
            continue

        coerced = _coerce_value(raw_override, target_type, default_val)
        if not _values_equal(coerced, default_val):
            changes.append(
                {
                    "path": field_path,
                    "owner_cls": cls,
                    "field_name": f.name,
                    "old_value": _serialize_default(default_val),
                    "new_value": _serialize_default(coerced),
                    "typed_value": coerced,
                }
            )

    return changes


def _plan_dataclass_default_updates(
    py_path: Path,
    params_cls: type,
    overrides: dict[str, Any],
) -> tuple[bytes, str, list[dict[str, Any]], list[dict[str, Any]], list[tuple[int, int, bytes]]]:
    """Plan surgical UTF-8 byte-span replacements for changed dataclass default literals."""
    raw_bytes = py_path.read_bytes()
    file_sha256 = hashlib.sha256(raw_bytes).hexdigest()
    source = raw_bytes.decode("utf-8")
    tree = ast.parse(source, filename=str(py_path))

    line_starts = [0]
    for line_b in raw_bytes.splitlines(keepends=True):
        line_starts.append(line_starts[-1] + len(line_b))

    class_defs: dict[str, list[ast.ClassDef]] = {}
    for node in tree.body:
        if isinstance(node, ast.ClassDef):
            class_defs.setdefault(node.name, []).append(node)

    resolved_py = py_path.resolve()
    raw_changes = _collect_default_changes(params_cls, overrides)
    changes_out: list[dict[str, Any]] = []
    unsupported_out: list[dict[str, Any]] = []
    spans: list[tuple[int, int, bytes]] = []

    for ch in raw_changes:
        path = ch["path"]
        owner_cls = ch["owner_cls"]
        field_name = ch["field_name"]
        typed_val = ch["typed_value"]

        if isinstance(typed_val, enum.Enum):
            unsupported_out.append({"path": path, "reason": "Enum defaults cannot be updated automatically"})
            continue

        try:
            cls_file = inspect.getsourcefile(owner_cls)
        except Exception:
            cls_file = None
        if not cls_file or Path(cls_file).resolve() != resolved_py:
            unsupported_out.append({"path": path, "reason": "dataclass is defined outside this model file"})
            continue

        matches = class_defs.get(owner_cls.__name__, [])
        if len(matches) != 1:
            unsupported_out.append({"path": path, "reason": f"class {owner_cls.__name__} not uniquely located"})
            continue

        ann_matches = [
            stmt
            for stmt in matches[0].body
            if isinstance(stmt, ast.AnnAssign)
            and isinstance(stmt.target, ast.Name)
            and stmt.target.id == field_name
        ]
        if len(ann_matches) != 1:
            unsupported_out.append({"path": path, "reason": f"field {field_name} not uniquely located"})
            continue

        expr, err_reason = _locate_default_expr(ann_matches[0])
        if expr is None or err_reason is not None:
            unsupported_out.append({"path": path, "reason": err_reason or "default expression not found"})
            continue

        if not _is_safe_literal_expr(expr):
            unsupported_out.append({"path": path, "reason": "uses a non-literal expression"})
            continue

        if (
            expr.lineno is None
            or expr.col_offset is None
            or expr.end_lineno is None
            or expr.end_col_offset is None
        ):
            unsupported_out.append({"path": path, "reason": "source span unavailable"})
            continue

        try:
            replacement_str = _format_python_literal(typed_val)
        except Exception as exc:
            unsupported_out.append({"path": path, "reason": str(exc)})
            continue

        start_b = line_starts[expr.lineno - 1] + expr.col_offset
        end_b = line_starts[expr.end_lineno - 1] + expr.end_col_offset
        if start_b < 0 or end_b <= start_b or end_b > len(raw_bytes):
            unsupported_out.append({"path": path, "reason": "invalid source byte span"})
            continue

        old_src = raw_bytes[start_b:end_b].decode("utf-8")
        spans.append((start_b, end_b, replacement_str.encode("utf-8")))
        changes_out.append(
            {
                "path": path,
                "line": expr.lineno,
                "old_value": ch["old_value"],
                "new_value": ch["new_value"],
                "old_literal": old_src,
                "new_literal": replacement_str,
            }
        )

    return raw_bytes, file_sha256, changes_out, unsupported_out, spans


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


def _compute_bbox_bounds(wrapped: Any) -> tuple[float, float, float, float, float, float]:
    """Compute (xmin, ymin, zmin, xmax, ymax, zmax) compatible across OCCT 7.7-7.9+."""
    from OCP.Bnd import Bnd_Box
    from OCP.BRepBndLib import BRepBndLib

    bnd = Bnd_Box()
    BRepBndLib.AddOptimal_s(wrapped, bnd, True, False)
    if bnd.IsVoid():
        return 0.0, 0.0, 0.0, 0.0, 0.0, 0.0
    pmin = bnd.CornerMin()
    pmax = bnd.CornerMax()
    return pmin.X(), pmin.Y(), pmin.Z(), pmax.X(), pmax.Y(), pmax.Z()


def _tessellate_shape_ocp(shape: Any, linear_deflection: float = 0.1, angular_deflection: float = 0.2) -> tuple[dict[str, Any], dict[str, float]]:
    """Pure OCP tessellator producing three-cad-viewer v5 Shape JSON + bbox."""
    from OCP.BRep import BRep_Tool
    from OCP.BRepAdaptor import BRepAdaptor_Curve
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
    xmin, ymin, zmin, xmax, ymax, zmax = _compute_bbox_bounds(wrapped)
    diag = math.sqrt((xmax - xmin) ** 2 + (ymax - ymin) ** 2 + (zmax - zmin) ** 2)
    defl = max(0.02, min(linear_deflection, diag * 0.002 if diag > 0 else 0.1))

    BRepMesh_IncrementalMesh(wrapped, defl, False, angular_deflection, True)

    vertices: list[float] = []
    normals: list[float] = []
    triangles: list[int] = []
    triangles_per_face: list[int] = []
    face_types: list[int] = []

    to_face = getattr(TopoDS, "Face_s", None) or TopoDS.Face
    to_edge = getattr(TopoDS, "Edge_s", None) or TopoDS.Edge

    vertex_offset = 0
    face_exp = TopExp_Explorer(wrapped, TopAbs_FACE)
    while face_exp.More():
        face = to_face(face_exp.Current())
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
        edge = to_edge(edge_exp.Current())
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
            from ocp_tessellate.tessellator import tessellate as ocp_tess

            wrapped = getattr(shape, "wrapped", shape)
            xmin, ymin, zmin, xmax, ymax, zmax = _compute_bbox_bounds(wrapped)
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


def _library_import_roots(root: Path) -> list[Path]:
    """Return sys.path roots for a library (including parent if user selected models/ directly)."""
    roots = [root]
    if root.name == "models" and (
        (root.parent / "components").is_dir() or (root.parent / "utils").is_dir()
    ):
        roots.append(root.parent)
    return roots


def _path_is_within(candidate: Path, roots: list[Path]) -> bool:
    for root in roots:
        try:
            candidate.relative_to(root)
            return True
        except ValueError:
            continue
    return False


def _module_belongs_to_roots(mod: Any, roots: list[Path]) -> bool:
    """Detect whether a loaded module or namespace package originates under any of `roots`."""
    if not roots:
        return False
    mod_file = getattr(mod, "__file__", None)
    if mod_file:
        try:
            if _path_is_within(Path(mod_file).resolve(), roots):
                return True
        except Exception:
            pass

    # Handle PEP 420 namespace packages (e.g. utils/ or components/ without __init__.py)
    mod_path = getattr(mod, "__path__", None)
    if mod_path is not None:
        try:
            for entry in list(mod_path):
                if _path_is_within(Path(str(entry)).resolve(), roots):
                    return True
        except Exception:
            pass

    spec = getattr(mod, "__spec__", None)
    locs = getattr(spec, "submodule_search_locations", None) if spec is not None else None
    if locs is not None:
        try:
            for entry in list(locs):
                if _path_is_within(Path(str(entry)).resolve(), roots):
                    return True
        except Exception:
            pass

    return False


class WorkerState:
    def __init__(self, libraries: list[dict[str, Any]] | None = None) -> None:
        self.lock = threading.RLock()
        self._seq_lock = threading.Lock()
        self._render_seq = 0
        self.libraries: list[dict[str, Any]] = []
        self.libraries_by_id: dict[str, dict[str, Any]] = {}
        self.library_errors: dict[str, str] = {}
        self.models: dict[str, dict[str, Any]] = {}
        self.loaded_modules: dict[str, Any] = {}
        self.last_build: dict[str, Any] = {}
        self._render_cache: OrderedDict[tuple[str, str], dict[str, Any]] = OrderedDict()
        self._active_library_id: str | None = None
        self._library_local_modules: dict[str, dict[str, Any]] = {}
        if libraries:
            self.configure(libraries)

    def _next_render_seq(self) -> int:
        with self._seq_lock:
            self._render_seq += 1
            return self._render_seq

    def _latest_render_seq(self) -> int:
        with self._seq_lock:
            return self._render_seq

    def _all_import_roots(self) -> list[Path]:
        roots: list[Path] = []
        seen: set[Path] = set()
        for lib in self.libraries:
            for r in lib["import_roots"]:
                if r not in seen:
                    seen.add(r)
                    roots.append(r)
        return roots

    def _remove_roots_from_sys_path(self, roots: list[Path]) -> None:
        root_strs = {str(r) for r in roots}
        sys.path[:] = [p for p in sys.path if p not in root_strs]

    def _activate_library(self, library_id: str) -> None:
        """Isolate sys.path and library-local modules (utils/, components/) for `library_id`."""
        lib = self.libraries_by_id.get(library_id)
        if lib is None:
            return

        if self._active_library_id == library_id:
            # Ensure incoming import roots stay at the front of sys.path
            for r in reversed(lib["import_roots"]):
                r_str = str(r)
                if r_str in sys.path:
                    sys.path.remove(r_str)
                sys.path.insert(0, r_str)
            return

        # 1. Snapshot and pop outgoing library-local modules
        if self._active_library_id is not None:
            out_lib = self.libraries_by_id.get(self._active_library_id)
            if out_lib is not None:
                out_roots = out_lib["import_roots"]
                snapshot = self._library_local_modules.setdefault(self._active_library_id, {})
                for mod_name, mod in list(sys.modules.items()):
                    if mod_name.startswith("code_cad_model_"):
                        continue
                    if _module_belongs_to_roots(mod, out_roots):
                        snapshot[mod_name] = mod
                        sys.modules.pop(mod_name, None)
                self._remove_roots_from_sys_path(out_roots)

        # 2. Clear any stray local modules belonging to other libraries
        other_roots: list[Path] = []
        for other_id, other_lib in self.libraries_by_id.items():
            if other_id != library_id:
                other_roots.extend(other_lib["import_roots"])
        if other_roots:
            for mod_name, mod in list(sys.modules.items()):
                if mod_name.startswith("code_cad_model_"):
                    continue
                if _module_belongs_to_roots(mod, other_roots):
                    sys.modules.pop(mod_name, None)
            self._remove_roots_from_sys_path(other_roots)

        # 3. Install incoming library roots at the front of sys.path & restore its local modules
        for r in reversed(lib["import_roots"]):
            r_str = str(r)
            if r_str in sys.path:
                sys.path.remove(r_str)
            sys.path.insert(0, r_str)

        cached_local = self._library_local_modules.get(library_id)
        if cached_local:
            for mod_name, mod in cached_local.items():
                sys.modules[mod_name] = mod

        importlib.invalidate_caches()
        self._active_library_id = library_id

    def _evict_project_modules(self) -> None:
        """Evict loaded model and project helper modules from sys.modules for warm reload."""
        roots = self._all_import_roots()

        to_delete: list[str] = []
        for mod_name, mod in list(sys.modules.items()):
            if mod_name.startswith("code_cad_model_"):
                to_delete.append(mod_name)
                continue
            if _module_belongs_to_roots(mod, roots):
                to_delete.append(mod_name)

        for mod_name in to_delete:
            sys.modules.pop(mod_name, None)

        self._remove_roots_from_sys_path(roots)
        self._library_local_modules.clear()
        self._active_library_id = None
        self.loaded_modules.clear()
        self.last_build.clear()
        self._render_cache.clear()
        importlib.invalidate_caches()

    def reload_project(self) -> tuple[list[dict[str, Any]], dict[str, str]]:
        """Warm-reload project scripts without restarting Python or re-importing build123d."""
        with self.lock:
            self._evict_project_modules()
            return self.discover_models()

    def configure(self, libraries: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], dict[str, str]]:
        with self.lock:
            self._evict_project_modules()

            next_libs: list[dict[str, Any]] = []
            next_by_id: dict[str, dict[str, Any]] = {}
            for raw in libraries or []:
                if not isinstance(raw, dict):
                    continue
                raw_path = str(raw.get("path") or "").strip()
                if not raw_path:
                    continue
                root = Path(raw_path).expanduser().resolve()
                lib_id = str(raw.get("id") or "").strip() or re.sub(r"[^a-z0-9]+", "-", root.name.lower()).strip("-") or "library"
                lib_name = str(raw.get("name") or "").strip() or root.name or lib_id
                if lib_id in next_by_id:
                    continue
                entry = {
                    "id": lib_id,
                    "name": lib_name,
                    "path": str(root),
                    "root": root,
                    "import_roots": _library_import_roots(root),
                }
                next_libs.append(entry)
                next_by_id[lib_id] = entry

            self.libraries = next_libs
            self.libraries_by_id = next_by_id
            return self.discover_models()

    def discover_models(self) -> tuple[list[dict[str, Any]], dict[str, str]]:
        with self.lock:
            self.models.clear()
            self.library_errors.clear()
            if not self.libraries:
                return [], {}

            found: list[dict[str, Any]] = []
            for lib in self.libraries:
                lib_id: str = lib["id"]
                lib_name: str = lib["name"]
                root: Path = lib["root"]
                if not root.exists() or not root.is_dir():
                    self.library_errors[lib_id] = f"Folder not found: {root}"
                    continue

                # Refresh import_roots in case components/ or utils/ was newly created
                lib["import_roots"] = _library_import_roots(root)

                lib_models: list[dict[str, Any]] = []
                for dirpath, dirnames, filenames in os.walk(root):
                    is_root = Path(dirpath) == root
                    skip = (ALWAYS_SKIP_DIRS | ROOT_ONLY_SKIP_DIRS) if is_root else ALWAYS_SKIP_DIRS
                    dirnames[:] = sorted(
                        d for d in dirnames if d not in skip and not d.startswith(".")
                    )
                    for fname in sorted(filenames):
                        if not fname.endswith(".py"):
                            continue
                        if fname.startswith("_") or fname.startswith("test_") or fname == "setup.py":
                            continue
                        py_file = Path(dirpath) / fname
                        meta = _discover_script_metadata(py_file, lib_id, lib_name, root)
                        if meta is None:
                            continue
                        # Guard against rare intra-library ID collision (e.g. a__b vs a/b)
                        base_id = meta["id"]
                        unique_id = base_id
                        suffix = 2
                        while unique_id in self.models:
                            unique_id = f"{base_id}_{suffix}"
                            suffix += 1
                        meta["id"] = unique_id
                        self.models[unique_id] = meta
                        lib_models.append(meta)

                lib_models.sort(key=lambda m: ([f.lower() for f in m["folders"]], m["name"].lower()))
                found.extend(lib_models)

            return found, dict(self.library_errors)

    def _load_model_module(self, model_id: str) -> tuple[dict[str, Any], Any]:
        if model_id not in self.models:
            self.discover_models()
        if model_id not in self.models:
            raise KeyError(f"Unknown model: {model_id}")

        meta = self.models[model_id]
        self._activate_library(meta["library_id"])
        if model_id in self.loaded_modules:
            return meta, self.loaded_modules[model_id]

        py_path = Path(meta["path"])
        _wait_for_cad_warmup()
        mod_hash = hashlib.sha1(model_id.encode("utf-8")).hexdigest()[:8]
        mod_name = f"code_cad_model_{re.sub(r'[^a-zA-Z0-9_]', '_', model_id)}_{mod_hash}"
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
                fields, defaults = _introspect_dataclass(params_cls)
            else:
                fields, defaults = _introspect_function_signature(func)

            default_preset: dict[str, str] | None = None
            if params_cls is not None:
                raw_dp = getattr(params_cls, "__default_preset__", None)
                if isinstance(raw_dp, dict):
                    dp_label = str(raw_dp.get("label") or "").strip()
                    dp_desc = str(raw_dp.get("description") or "").strip()
                    if dp_label or dp_desc:
                        default_preset = {}
                        if dp_label:
                            default_preset["label"] = dp_label
                        if dp_desc:
                            default_preset["description"] = dp_desc

            doc = inspect.getdoc(func) or inspect.getdoc(module) or meta.get("description", "")
            resp: dict[str, Any] = {
                "ok": True,
                "model": meta,
                "description": doc.splitlines()[0] if doc else "",
                "fields": fields,
                "defaults": defaults,
            }
            if default_preset is not None:
                resp["default_preset"] = default_preset
            return resp

    def update_defaults(
        self,
        model_id: str,
        params_dict: dict[str, Any],
        dry_run: bool = True,
        expected_sha256: str = "",
    ) -> dict[str, Any]:
        with self.lock:
            meta, module = self._load_model_module(model_id)
            params_cls, _func = self._resolve_entrypoints(meta, module)
            if params_cls is None:
                return {
                    "ok": False,
                    "error": "Model does not define a Params @dataclass; defaults cannot be updated automatically.",
                }

            py_path = Path(meta["path"])
            raw_bytes, file_sha256, changes, unsupported, spans = _plan_dataclass_default_updates(
                py_path, params_cls, params_dict if isinstance(params_dict, dict) else {}
            )

            base_resp: dict[str, Any] = {
                "ok": True,
                "file": str(py_path),
                "rel_path": meta.get("rel_path", py_path.name),
                "sha256": file_sha256,
                "changes": changes,
                "unsupported": unsupported,
            }
            if dry_run:
                return base_resp

            if expected_sha256 and expected_sha256 != file_sha256:
                return {
                    **base_resp,
                    "ok": False,
                    "error": "File changed on disk since preview; please preview again before applying.",
                }
            if unsupported:
                return {
                    **base_resp,
                    "ok": False,
                    "error": "One or more changed fields cannot be updated safely; no changes were written.",
                }
            if not spans:
                return {**base_resp, "updated": 0}

            # Verify non-overlapping spans and splice in descending byte order
            sorted_spans = sorted(spans, key=lambda s: s[0], reverse=True)
            for i in range(len(sorted_spans) - 1):
                if sorted_spans[i][0] < sorted_spans[i + 1][1]:
                    return {
                        **base_resp,
                        "ok": False,
                        "error": "Overlapping source spans detected; aborted without modifying file.",
                    }

            patched = bytearray(raw_bytes)
            for start_b, end_b, repl_b in sorted_spans:
                patched[start_b:end_b] = repl_b
            new_bytes = bytes(patched)

            # Fail-closed syntax validation in memory before touching disk
            try:
                new_source = new_bytes.decode("utf-8")
                ast.parse(new_source, filename=str(py_path))
            except Exception as exc:
                return {
                    **base_resp,
                    "ok": False,
                    "error": f"Generated Python failed syntax validation ({exc}); aborted without modifying file.",
                }

            # Atomic replace preserving original file mode
            st_mode = py_path.stat().st_mode
            tmp_path: Path | None = None
            try:
                fd, tmp_name = tempfile.mkstemp(dir=str(py_path.parent), prefix=f".{py_path.name}.", suffix=".tmp")
                tmp_path = Path(tmp_name)
                try:
                    os.write(fd, new_bytes)
                finally:
                    os.close(fd)
                os.chmod(tmp_path, st_mode)
                os.replace(tmp_path, py_path)
            except Exception as exc:
                if tmp_path is not None:
                    try:
                        tmp_path.unlink(missing_ok=True)
                    except OSError:
                        pass
                return {
                    **base_resp,
                    "ok": False,
                    "error": f"Failed to write updated file: {exc}",
                }

            # Remove any stale .pyc bytecode so same-second / same-length edits reload cleanly
            try:
                Path(importlib.util.cache_from_source(str(py_path))).unlink(missing_ok=True)
            except Exception:
                pass

            # Clear module/render caches so subsequent schema/renders immediately reflect new defaults
            self._evict_project_modules()
            self.discover_models()
            new_sha256 = hashlib.sha256(new_bytes).hexdigest()
            return {
                **base_resp,
                "sha256": new_sha256,
                "updated": len(changes),
            }

    def _resolve_export_filename(
        self,
        meta: dict[str, Any],
        module: Any,
        params_cls: type | None,
        params_dict: dict[str, Any],
        params_obj: Any = None,
    ) -> str:
        fallback = _sanitize_filename_stem(Path(meta["path"]).stem, "model")
        hook = getattr(module, "export_filename", None)
        if not callable(hook):
            return fallback
        try:
            if params_obj is None and params_cls is not None:
                params_obj = _instantiate_dataclass(params_cls, params_dict)
            if params_obj is not None:
                raw = hook(params_obj)
            else:
                raw = hook(params_dict)
            if isinstance(raw, str) and raw.strip():
                return _sanitize_filename_stem(raw, fallback)
        except Exception:
            pass
        return fallback

    def get_export_filename(self, model_id: str, params_dict: dict[str, Any]) -> dict[str, Any]:
        with self.lock:
            meta, module = self._load_model_module(model_id)
            params_cls, _ = self._resolve_entrypoints(meta, module)
            stem = self._resolve_export_filename(meta, module, params_cls, params_dict)
            return {
                "ok": True,
                "export_filename": stem,
                "library_id": meta["library_id"],
            }

    def _build_shape(self, model_id: str, params_dict: dict[str, Any]) -> tuple[dict[str, Any], Any, float, str]:
        _wait_for_cad_warmup()
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
        export_stem = self._resolve_export_filename(meta, module, params_cls, params_dict, params_obj)
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

            try:
                meta, result, build_ms, export_stem = self._build_shape(model_id, params_dict)
            except Exception:
                if my_seq != self._latest_render_seq():
                    return {"ok": False, "superseded": True, "model_id": model_id}
                raise
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
                export_stem = self.last_build.get("export_filename") or "model"
            else:
                _, result, _, export_stem = self._build_shape(model_id, params_dict)

            if output_path:
                out = Path(output_path).expanduser().resolve()
                out.parent.mkdir(parents=True, exist_ok=True)
                export_shape_to_file(result, fmt, out)
                return {"ok": True, "path": str(out), "size": out.stat().st_size}, None

            safe_stem = _sanitize_filename_stem(export_stem, "model")
            with tempfile.TemporaryDirectory() as tmpdir:
                out = Path(tmpdir) / f"{safe_stem}.{fmt.lower().lstrip('.')}"
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
            try:
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                # Client aborted an in-flight superseded request; ignore silently.
                pass

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
                    _wait_for_cad_warmup()
                    with state.lock:
                        libs = [
                            {"id": lib["id"], "name": lib["name"], "path": lib["path"]}
                            for lib in state.libraries
                        ]
                    self._send_json(
                        {
                            "ok": True,
                            "python": sys.executable,
                            "build123d": HAS_BUILD123D,
                            "build123d_error": BUILD123D_ERROR,
                            "ocp_tessellate": HAS_OCP_TESSELLATE,
                            "libraries": libs,
                        }
                    )
                    return

                if path == "/models":
                    models, lib_errors = state.discover_models()
                    self._send_json({"ok": True, "models": models, "library_errors": lib_errors})
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
                    libraries = body.get("libraries", [])
                    models, lib_errors = state.configure(libraries)
                    self._send_json({"ok": True, "models": models, "library_errors": lib_errors})
                    return

                if path == "/reload":
                    models, lib_errors = state.reload_project()
                    self._send_json({"ok": True, "models": models, "library_errors": lib_errors})
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

                m_update_defaults = re.match(r"^/models/([^/]+)/update-defaults$", path)
                if m_update_defaults:
                    model_id = unquote(m_update_defaults.group(1))
                    params = body.get("params", {})
                    dry_run = bool(body.get("dry_run", True))
                    expected_sha256 = str(body.get("expected_sha256", "") or "")
                    self._send_json(
                        state.update_defaults(
                            model_id,
                            params,
                            dry_run=dry_run,
                            expected_sha256=expected_sha256,
                        )
                    )
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
    sys.dont_write_bytecode = True
    parser = argparse.ArgumentParser(description="Code CAD Studio Python worker")
    parser.add_argument("--libraries-json", default="", help="JSON array of configured libraries")
    parser.add_argument("--port", type=int, default=0, help="Port to bind (0 for ephemeral)")
    args = parser.parse_args()

    libraries: list[dict[str, Any]] = []
    if args.libraries_json:
        try:
            parsed = json.loads(args.libraries_json)
            if isinstance(parsed, list):
                libraries = parsed
        except Exception:
            libraries = []

    state = WorkerState(libraries)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), make_handler(state))
    actual_port = server.server_address[1]

    # Signal readiness to the Go supervisor immediately, then warm up CAD imports
    # in the background so GET /models returns without waiting on build123d/OCP.
    print(f"READY {actual_port}", flush=True)
    _start_cad_warmup()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
