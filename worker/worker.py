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
import hmac
import importlib
from importlib import metadata as importlib_metadata
import importlib.util
import inspect
import json
import math
import os
import re
import secrets
import sys
import tempfile
import threading
import time
import traceback
import types
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, ForwardRef, Literal, Union, get_args, get_origin, get_type_hints
from urllib.parse import unquote, urlparse

try:
    import annotationlib as _annotationlib
except ImportError:  # Python < 3.14
    _annotationlib = None  # type: ignore[assignment]

# Warmed up on a background thread right after the HTTP server prints READY,
# so directory discovery (GET /models) returns immediately without blocking on
# OpenCASCADE / build123d DLL imports.
HAS_BUILD123D = False
BUILD123D_ERROR = ""
HAS_OCP_TESSELLATE = False
_CAD_WARMUP_DONE = threading.Event()
_CAD_WARMUP_STARTED = False
_CAD_WARMUP_LOCK = threading.Lock()

MAX_REQUEST_BYTES = 2 * 1024 * 1024
ALLOWED_LOOPBACK_HOSTS = {"127.0.0.1", "localhost", "[::1]", "::1"}
MAX_RENDER_CACHE = 24
MAX_RENDER_CACHE_BYTES = 128 * 1024 * 1024
ALLOWED_EXPORT_FORMATS = {"stl", "step", "stp", "3mf"}
EXPORT_QUALITY_PRESETS: dict[str, tuple[float, float]] = {
    "draft": (0.02, 0.3),
    "standard": (0.001, 0.1),
    "fine": (0.0005, 0.05),
}


class _HttpError(Exception):
    def __init__(self, status: int, message: str, extra: dict[str, Any] | None = None) -> None:
        super().__init__(message)
        self.status = status
        self.message = message
        self.extra = extra or {}


def _package_version(dist_name: str) -> str:
    try:
        return importlib_metadata.version(dist_name)
    except Exception:
        return ""


def _validate_model_id(model_id: str) -> None:
    if not isinstance(model_id, str) or not model_id:
        raise _HttpError(400, "Invalid model id")
    if any(ord(ch) < 32 for ch in model_id):
        raise _HttpError(400, "Invalid model id")
    if "\\" in model_id or ":" in model_id:
        raise _HttpError(400, "Invalid model id")
    if model_id.startswith("/") or model_id.endswith("/") or "//" in model_id:
        raise _HttpError(400, "Invalid model id")
    parts = model_id.split("/")
    if len(parts) < 2:
        raise _HttpError(400, "Invalid model id")
    if any(seg in ("", ".", "..") for seg in parts):
        raise _HttpError(400, "Invalid model id")


def _replace_with_retry(
    src: Path,
    dst: Path,
    attempts: int = 8,
    initial_delay: float = 0.05,
) -> None:
    """Atomically replace dst with src, retrying transient Windows AV/editor locks."""
    delay = initial_delay
    for attempt in range(attempts):
        try:
            os.replace(src, dst)
            return
        except PermissionError:
            if attempt >= attempts - 1:
                raise
            time.sleep(delay)
            delay *= 1.5
        except OSError as exc:
            if getattr(exc, "winerror", None) in (5, 32) and attempt < attempts - 1:
                time.sleep(delay)
                delay *= 1.5
                continue
            raise


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
    spaced = re.sub(r"(?<=[a-z0-9])(?=[A-Z])|(?<=[A-Z])(?=[A-Z][a-z])", " ", name)
    return re.sub(r"[_-]+", " ", spaced).strip().title()


def humanize_class_name(name: str) -> str:
    spaced = re.sub(r"(?<=[a-z0-9])(?=[A-Z])|(?<=[A-Z])(?=[A-Z][a-z])", " ", name).strip()
    if spaced.endswith(" Params") and len(spaced) > len(" Params"):
        spaced = spaced[: -len(" Params")].strip()
    return spaced or "Item"


def _tree_uses_build123d(tree: ast.AST) -> bool:
    """Return True if an AST imports build123d/ocp_vscode/ocp_viewer or calls show()/show_object()."""
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                name = alias.name
                if name == "build123d" or name.startswith("build123d.") or name in ("ocp_vscode", "ocp_viewer"):
                    return True
        elif isinstance(node, ast.ImportFrom):
            mod = node.module or ""
            if mod == "build123d" or mod.startswith("build123d.") or mod in ("ocp_vscode", "ocp_viewer"):
                return True
        elif isinstance(node, ast.Call):
            if isinstance(node.func, ast.Name) and node.func.id in ("show", "show_object"):
                return True
    return False


def _inspect_script_ast(py_file: Path) -> dict[str, Any] | None:
    """Parse a .py file and classify it into parametric, script, error, or None (non-CAD)."""
    try:
        source = py_file.read_text(encoding="utf-8-sig")
    except OSError:
        return None
    except UnicodeDecodeError as exc:
        return {
            "kind": "error",
            "parse_error": f"Encoding error: {exc}",
            "description": "",
            "params_class": "",
            "entrypoint": "",
        }

    try:
        tree = ast.parse(source, filename=str(py_file))
    except SyntaxError as exc:
        return {
            "kind": "error",
            "parse_error": f"SyntaxError (line {exc.lineno or '?'}): {exc.msg}",
            "description": "",
            "params_class": "",
            "entrypoint": "",
        }

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

    # Skip actual Python test suites named test_*.py (imports pytest/unittest or defines def test_*/class Test*),
    # while still discovering maker CAD models like test_bracket.py or test_parametric_model.py.
    if py_file.name.startswith("test_"):
        is_unit_test = False
        for node in tree.body:
            if isinstance(node, ast.FunctionDef) and node.name.startswith("test_"):
                is_unit_test = True
                break
            if isinstance(node, ast.ClassDef) and node.name.startswith("Test") and not node.name.endswith("Params"):
                is_unit_test = True
                break
            if isinstance(node, ast.Import) and any(
                a.name in ("pytest", "unittest") or a.name.startswith(("pytest.", "unittest."))
                for a in node.names
            ):
                is_unit_test = True
                break
            if isinstance(node, ast.ImportFrom) and node.module and (
                node.module in ("pytest", "unittest") or node.module.startswith(("pytest.", "unittest."))
            ):
                is_unit_test = True
                break
        if is_unit_test:
            return None

    docstring = ast.get_docstring(tree) or ""
    first_line = docstring.splitlines()[0].strip() if docstring else ""
    stem = py_file.stem

    if create_funcs or fallback_funcs:
        expected_fn = f"create_{stem}"
        if expected_fn in create_funcs:
            entrypoint = expected_fn
        elif create_funcs:
            entrypoint = create_funcs[-1]
        else:
            entrypoint = fallback_funcs[0]

        expected_cls = "".join(part.capitalize() for part in stem.split("_")) + "Params"
        if expected_cls in params_classes:
            params_class = expected_cls
        elif params_classes:
            params_class = params_classes[-1]
        else:
            params_class = ""

        return {
            "kind": "parametric",
            "parse_error": "",
            "description": first_line,
            "params_class": params_class,
            "entrypoint": entrypoint,
        }

    if _tree_uses_build123d(tree):
        return {
            "kind": "script",
            "parse_error": "",
            "description": first_line,
            "params_class": "",
            "entrypoint": "",
        }

    return None


def _build_model_info(
    py_file: Path,
    library_id: str,
    library_name: str,
    library_root: Path,
    ast_info: dict[str, Any],
) -> dict[str, Any]:
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
    model_id = f"{library_id}/{local_id}"
    presets_path = (
        py_file.parent / "presets.json"
        if py_file.parent.name == stem
        else py_file.parent / f"{stem}.presets.json"
    )

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
        "presets_path": str(presets_path),
        "description": ast_info["description"],
        "params_class": ast_info["params_class"],
        "entrypoint": ast_info["entrypoint"],
        "kind": ast_info["kind"],
        "parse_error": ast_info["parse_error"],
        "dir": str(py_file.parent),
    }


def _discover_script_metadata(
    py_file: Path,
    library_id: str,
    library_name: str,
    library_root: Path,
) -> dict[str, Any] | None:
    """Statically inspect a .py file via AST to see if it looks like a CAD model."""
    ast_info = _inspect_script_ast(py_file)
    if ast_info is None:
        return None
    return _build_model_info(py_file, library_id, library_name, library_root, ast_info)


def _is_union_type(tp: Any) -> bool:
    origin = get_origin(tp)
    union_type = getattr(types, "UnionType", None)
    return origin is Union or (union_type is not None and origin is union_type)


def _unwrap_optional(tp: Any) -> tuple[Any, bool]:
    if _is_union_type(tp):
        args = get_args(tp)
        non_none = [a for a in args if a is not type(None)]
        has_none = len(non_none) < len(args)
        if has_none:
            if len(non_none) == 1:
                return non_none[0], True
            if len(non_none) > 1:
                return Union[tuple(non_none)], True
    return tp, False


def _evaluate_annotation(
    ann: Any,
    globalns: dict[str, Any],
    localns: dict[str, Any] | None = None,
) -> Any:
    if isinstance(ann, str):
        return eval(ann, globalns, localns if localns is not None else globalns)
    if hasattr(ann, "evaluate"):
        return ann.evaluate(globals=globalns, locals=localns)
    if isinstance(ann, ForwardRef):
        return ann._evaluate(globalns, localns, recursive_guard=frozenset())
    return ann


def _get_raw_annotations(obj: Any) -> dict[str, Any]:
    if _annotationlib is not None:
        try:
            return dict(inspect.get_annotations(obj, format=_annotationlib.Format.FORWARDREF))
        except Exception:
            pass
    try:
        return dict(inspect.get_annotations(obj))
    except Exception:
        return {}


def _safe_get_type_hints(obj: Any) -> dict[str, Any]:
    """Resolve type hints per-field so one unresolvable forward-ref never drops sibling hints."""
    try:
        return get_type_hints(obj)
    except Exception:
        pass

    hints: dict[str, Any] = {}
    if inspect.isclass(obj):
        for base in reversed(obj.__mro__):
            if base is object:
                continue
            mod = sys.modules.get(getattr(base, "__module__", ""))
            base_globals = vars(mod) if mod is not None else {}
            raw_anns = _get_raw_annotations(base)
            for name, ann in raw_anns.items():
                try:
                    hints[name] = _evaluate_annotation(ann, base_globals)
                except Exception:
                    try:
                        hints[name] = _evaluate_annotation(ann, base_globals, dict(vars(base)))
                    except Exception:
                        continue
        return hints

    func_globals = getattr(obj, "__globals__", None)
    if func_globals is None:
        mod = sys.modules.get(getattr(obj, "__module__", ""))
        func_globals = vars(mod) if mod is not None else {}
    raw_anns = _get_raw_annotations(obj)
    for name, ann in raw_anns.items():
        try:
            hints[name] = _evaluate_annotation(ann, func_globals)
        except Exception:
            continue
    return hints


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
    if _is_union_type(elem_unwrapped):
        members = get_args(elem_unwrapped)
        if members and all(m in (int, float) for m in members):
            is_float_union = float in members
            item_info["type"] = "float" if is_float_union else "int"
            default_num: int | float = (
                (float(meta["min"]) if is_float_union else int(meta["min"]))
                if "min" in meta
                else (0.0 if is_float_union else 0)
            )
            item_info["default"] = None if elem_nullable else default_num
            item_info["step"] = meta.get("step", 0.5 if is_float_union else 1)
            if "min" in meta:
                item_info["min"] = meta["min"]
            if "max" in meta:
                item_info["max"] = meta["max"]
            return item_info
        if str in members:
            item_info["type"] = "str"
            item_info["default"] = None if elem_nullable else ""
            return item_info

    return None


def _introspect_dataclass(
    cls: type,
    prefix: str = "",
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Introspect a @dataclass into UI schema fields and canonical default dict."""
    hints = _safe_get_type_hints(cls)

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
        elif _is_union_type(unwrapped) and all(m in (int, float) for m in get_args(unwrapped)):
            members = get_args(unwrapped)
            is_float_union = float in members
            field_info["type"] = "float" if is_float_union else "int"
            num_default = (
                (float(default_val) if is_float_union else int(default_val))
                if default_val is not None
                else (0.0 if is_float_union else 0)
            )
            bounds = _infer_numeric_bounds(f.name, num_default, is_int=not is_float_union)
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
    hints = _safe_get_type_hints(func)

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
        elif _is_union_type(unwrapped) and all(m in (int, float) for m in get_args(unwrapped)):
            is_float_union = float in get_args(unwrapped)
            field_info["type"] = "float" if is_float_union else "int"
            field_info["step"] = 0.5 if is_float_union else 1
            field_info["default"] = (
                None
                if (is_nullable and default_val is None)
                else (
                    (float(default_val) if is_float_union else int(default_val))
                    if default_val is not None
                    else (0.0 if is_float_union else 0)
                )
            )
        else:
            field_info["type"] = "str"
            field_info["default"] = None if (is_nullable and default_val is None) else ("" if default_val is None else str(default_val))

        fields_schema.append(field_info)

    return fields_schema, defaults


def _coerce_union_value(
    val: Any,
    union_args: tuple[Any, ...],
    is_nullable: bool,
    default_val: Any,
) -> Any:
    has_bool = bool in union_args
    has_int = int in union_args
    has_float = float in union_args
    has_str = str in union_args

    if isinstance(val, bool):
        if has_bool:
            return val
    elif isinstance(val, int):
        if has_int:
            return val
        if has_float:
            return float(val)
    elif isinstance(val, float):
        if has_float:
            return val
        if has_int:
            return int(round(val))
    elif isinstance(val, str):
        stripped = val.strip()
        if stripped == "":
            if not has_str:
                return None if (is_nullable or default_val is None) else default_val
            return ""
        if has_str:
            return val
    elif isinstance(val, dict):
        for member in union_args:
            if dataclasses.is_dataclass(member):
                return _instantiate_dataclass(member, val)
    elif isinstance(val, (list, tuple)):
        for member in union_args:
            m_origin = get_origin(member)
            if m_origin in (list, tuple) or member in (list, tuple):
                return _coerce_value(val, member, default_val)

    for member in union_args:
        if inspect.isclass(member) and issubclass(member, enum.Enum):
            for enum_member in member:
                if enum_member.value == val or str(enum_member.value) == str(val) or enum_member.name == str(val):
                    return enum_member
        elif get_origin(member) is Literal:
            for choice in get_args(member):
                if choice == val or str(choice) == str(val):
                    return choice

    if isinstance(val, str) and not has_str:
        stripped = val.strip()
        if has_bool and stripped.lower() in ("true", "false", "1", "0", "on", "off", "yes", "no"):
            return stripped.lower() in ("true", "1", "on", "yes")
        if has_int:
            try:
                return int(stripped)
            except ValueError:
                if not has_float:
                    try:
                        return int(round(float(stripped)))
                    except ValueError:
                        pass
        if has_float:
            try:
                return float(stripped)
            except ValueError:
                pass
        for member in union_args:
            m_origin = get_origin(member)
            if m_origin in (list, tuple) or member in (list, tuple):
                return _coerce_value(val, member, default_val)

    if has_str:
        return str(val)
    return None if is_nullable else default_val


def _coerce_value(val: Any, target_type: Any, default_val: Any) -> Any:
    unwrapped, is_nullable = _unwrap_optional(target_type)
    if val is None:
        return None if is_nullable else default_val

    if _is_union_type(unwrapped):
        return _coerce_union_value(val, get_args(unwrapped), is_nullable, default_val)

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

    seq_type = origin if origin in (list, tuple) else (unwrapped if unwrapped in (list, tuple) else None)
    if seq_type is not None:
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
            elem_args = get_args(unwrapped) if origin in (list, tuple) else ()
            if seq_type is tuple and elem_args and not (len(elem_args) == 2 and elem_args[1] is Ellipsis):
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
            return tuple(coerced_items) if seq_type is tuple else coerced_items
        return None if is_nullable else default_val

    if unwrapped is bool or isinstance(default_val, bool):
        if isinstance(val, bool):
            return val
        if isinstance(val, str):
            stripped = val.strip()
            if stripped == "":
                return None if (is_nullable or default_val is None) else default_val
            return stripped.lower() in ("true", "1", "on", "yes")
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
    hints = _safe_get_type_hints(cls)
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
    if isinstance(val, tuple):
        if len(val) == 1:
            return f"({_format_python_literal(val[0])},)"
        return "(" + ", ".join(_format_python_literal(v) for v in val) + ")"
    if isinstance(val, list):
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
    hints = _safe_get_type_hints(cls)

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
    bom_len = 3 if raw_bytes.startswith(b"\xef\xbb\xbf") else 0
    body_bytes = raw_bytes[bom_len:]
    source = body_bytes.decode("utf-8")
    tree = ast.parse(source, filename=str(py_path))

    line_starts = [0]
    for line_b in body_bytes.splitlines(keepends=True):
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

        start_b = bom_len + line_starts[expr.lineno - 1] + expr.col_offset
        end_b = bom_len + line_starts[expr.end_lineno - 1] + expr.end_col_offset
        if start_b < bom_len or end_b <= start_b or end_b > len(raw_bytes):
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


def _finite_float(val: Any) -> float:
    f = float(val)
    return f if math.isfinite(f) else 0.0


@dataclasses.dataclass
class _CapturedShape:
    shape: Any
    label: str = ""
    color: Any = None


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
    """Unwrap _CapturedShape / BuildPart / BuildSketch / BuildLine or Shape wrappers."""
    if isinstance(obj, _CapturedShape):
        obj = obj.shape
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
        explicit_color = None
        if isinstance(item, _CapturedShape):
            if item.label:
                name = item.label
            explicit_color = item.color
            item = item.shape
        if isinstance(item, (list, tuple)):
            for sub_idx, sub_item in enumerate(item):
                if isinstance(sub_item, _CapturedShape):
                    sub_lbl = sub_item.label or getattr(sub_item.shape, "label", "") or f"{name}_{sub_idx + 1}"
                    if explicit_color is not None and sub_item.color is None:
                        sub_item = _CapturedShape(sub_item.shape, label=sub_lbl, color=explicit_color)
                else:
                    sub_lbl = getattr(sub_item, "label", "") or f"{name}_{sub_idx + 1}"
                    if explicit_color is not None:
                        sub_item = _CapturedShape(sub_item, label=sub_lbl, color=explicit_color)
                add_item(sub_lbl, sub_item, idx + sub_idx)
            return
        item = _unwrap_shape(item)
        if item is None:
            return
        if isinstance(item, (list, tuple)):
            add_item(name, item, idx)
            return
        fallback_color = DEFAULT_COLORS[len(parts) % len(DEFAULT_COLORS)]
        # Compound with labeled children
        children = getattr(item, "children", None)
        if children and len(children) > 0:
            for c_idx, child in enumerate(children):
                child_label = getattr(child, "label", "") or f"{name}_{c_idx + 1}"
                add_item(child_label, child, idx + c_idx)
            return
        color_attr = explicit_color if explicit_color is not None else getattr(item, "color", None)
        hex_col, alpha = _color_to_hex(color_attr, fallback_color)
        parts.append((name, item, hex_col, alpha))

    if isinstance(result, dict):
        for i, (k, v) in enumerate(result.items()):
            add_item(str(k), v, i)
    elif isinstance(result, (list, tuple)):
        for i, v in enumerate(result):
            if isinstance(v, _CapturedShape):
                lbl = v.label or getattr(v.shape, "label", "") or f"Part_{i + 1}"
            else:
                lbl = getattr(v, "label", "") or f"Part_{i + 1}"
            add_item(lbl, v, i)
    else:
        if isinstance(result, _CapturedShape):
            lbl = result.label or getattr(result.shape, "label", "") or root_name
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
    return (
        _finite_float(pmin.X()),
        _finite_float(pmin.Y()),
        _finite_float(pmin.Z()),
        _finite_float(pmax.X()),
        _finite_float(pmax.Y()),
        _finite_float(pmax.Z()),
    )


def _tessellate_shape_ocp(shape: Any, linear_deflection: float = 0.1, angular_deflection: float = 0.2) -> tuple[dict[str, Any], dict[str, float]]:
    """Pure OCP tessellator producing three-cad-viewer v5 Shape JSON + bbox."""
    from OCP.BRep import BRep_Tool
    from OCP.BRepAdaptor import BRepAdaptor_Curve
    from OCP.BRepGProp import BRepGProp_Face
    from OCP.BRepMesh import BRepMesh_IncrementalMesh
    from OCP.GCPnts import GCPnts_QuasiUniformDeflection
    from OCP.gp import gp_Pnt, gp_Vec
    from OCP.TopAbs import TopAbs_EDGE, TopAbs_FACE, TopAbs_REVERSED
    from OCP.TopExp import TopExp, TopExp_Explorer
    from OCP.TopLoc import TopLoc_Location
    from OCP.TopoDS import TopoDS
    from OCP.TopTools import TopTools_IndexedMapOfShape

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

    # Discretize unique edges via TopExp.MapShapes_s to avoid duplicate shared edges
    edges: list[float] = []
    segments_per_edge: list[int] = []
    edge_types: list[int] = []

    edge_map = TopTools_IndexedMapOfShape()
    map_shapes = getattr(TopExp, "MapShapes_s", None) or TopExp.MapShapes
    map_shapes(wrapped, TopAbs_EDGE, edge_map)
    for idx in range(1, edge_map.Extent() + 1):
        edge = to_edge(edge_map.FindKey(idx))
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


def tessellate_result(
    result: Any,
    model_name: str,
    cache_prefix: str = "",
) -> tuple[dict[str, Any], dict[str, float]]:
    """Tessellate a model's returned shape(s) into three-cad-viewer v5 Shapes JSON."""
    named_parts = _collect_named_parts(result, model_name)
    if not named_parts:
        raise ValueError("Model returned no geometry.")

    root_id = f"/{model_name}"
    viewer_parts: list[dict[str, Any]] = []
    g_xmin = g_ymin = g_zmin = float("inf")
    g_xmax = g_ymax = g_zmax = float("-inf")

    seen_names: dict[str, int] = {}
    for name, shp, hex_color, alpha in named_parts:
        clean_name = re.sub(r"[^A-Za-z0-9_\- ]+", "_", name).strip() or "Part"
        count = seen_names.get(clean_name, 0)
        seen_names[clean_name] = count + 1
        if count > 0:
            clean_name = f"{clean_name}_{count + 1}"

        cache_tag = f"{cache_prefix}:{clean_name}" if cache_prefix else clean_name
        shape_data, bb = _tessellate_shape_fast(shp, cache_tag)
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
        "xmin": _finite_float(g_xmin),
        "xmax": _finite_float(g_xmax),
        "ymin": _finite_float(g_ymin),
        "ymax": _finite_float(g_ymax),
        "zmin": _finite_float(g_zmin),
        "zmax": _finite_float(g_zmax),
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
        "dx": round(overall_bb["xmax"] - overall_bb["xmin"], 3),
        "dy": round(overall_bb["ymax"] - overall_bb["ymin"], 3),
        "dz": round(overall_bb["zmax"] - overall_bb["zmin"], 3),
    }
    return shapes_root, bbox_summary


def _resolve_export_deflection(
    quality: str = "standard",
    linear_deflection: Any = None,
    angular_deflection: Any = None,
) -> tuple[float, float]:
    """Validate and resolve export tessellation deflections from preset or custom values."""
    q = str(quality or "standard").strip().lower()
    if q in EXPORT_QUALITY_PRESETS:
        return EXPORT_QUALITY_PRESETS[q]
    if q == "custom":
        if isinstance(linear_deflection, bool) or isinstance(angular_deflection, bool):
            raise _HttpError(400, "custom export deflection values must be numeric")
        try:
            lin = float(linear_deflection)
            ang = float(angular_deflection)
        except (TypeError, ValueError):
            raise _HttpError(400, "custom export quality requires numeric linear_deflection and angular_deflection")
        if not (math.isfinite(lin) and math.isfinite(ang)):
            raise _HttpError(400, "custom export deflection values must be finite")
        if not (1e-5 <= lin <= 10.0) or not (1e-3 <= ang <= 3.15):
            raise _HttpError(400, "custom export deflection values out of allowed range")
        return lin, ang
    raise _HttpError(400, f"Unsupported export quality: {quality}")


def export_shape_to_file(
    result: Any,
    fmt: str,
    out_path: Path,
    linear_deflection: float = 0.001,
    angular_deflection: float = 0.1,
) -> None:
    """Export build123d shape or dict/list of shapes to .step, .stl, or .3mf."""
    import build123d as b3d

    fmt_lower = str(fmt or "").strip().lower().lstrip(".")
    if fmt_lower not in ALLOWED_EXPORT_FORMATS:
        raise _HttpError(400, f"Unsupported export format: {fmt}")

    named_parts = _collect_named_parts(result, "model")
    if not named_parts:
        raise ValueError("No geometry to export.")

    direct_shape = None
    if not isinstance(result, (dict, list, tuple)):
        candidate = result.shape if isinstance(result, _CapturedShape) else result
        candidate = _unwrap_shape(candidate)
        if candidate is not None and hasattr(candidate, "wrapped"):
            direct_shape = candidate

    if direct_shape is not None:
        export_obj = direct_shape
    elif len(named_parts) == 1:
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

    if fmt_lower in ("step", "stp"):
        b3d.export_step(export_obj, str(out_path))
    elif fmt_lower == "stl":
        b3d.export_stl(
            export_obj,
            str(out_path),
            tolerance=linear_deflection,
            angular_tolerance=angular_deflection,
        )
    elif fmt_lower == "3mf":
        mesher = b3d.Mesher()
        mesher.add_shape(
            export_obj,
            linear_deflection=linear_deflection,
            angular_deflection=angular_deflection,
        )
        mesher.write(str(out_path))


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


def _estimate_render_payload_bytes(payload: dict[str, Any]) -> int:
    """Estimate serialized payload size in O(#parts) without full JSON traversal."""
    total = 4096
    shapes = payload.get("shapes")
    if not isinstance(shapes, dict):
        return total
    parts = shapes.get("parts")
    if not isinstance(parts, list):
        return total
    for part in parts:
        total += 256
        shape = part.get("shape") if isinstance(part, dict) else None
        if isinstance(shape, dict):
            for buf in shape.values():
                if isinstance(buf, dict):
                    raw_buf = buf.get("buffer")
                    if isinstance(raw_buf, str):
                        total += len(raw_buf)
    return total


def _is_script_shape(val: Any) -> bool:
    """Return True if val looks like a build123d Shape or builder instance."""
    if val is None or isinstance(val, type) or inspect.ismodule(val) or callable(val):
        return False
    if HAS_BUILD123D:
        try:
            import build123d as b3d

            b3d_types = tuple(
                t for t in (
                    getattr(b3d, "Shape", None),
                    getattr(b3d, "BuildPart", None),
                    getattr(b3d, "BuildSketch", None),
                    getattr(b3d, "BuildLine", None),
                )
                if isinstance(t, type)
            )
            if b3d_types:
                return isinstance(val, b3d_types)
        except Exception:
            pass
    if hasattr(val, "wrapped"):
        return True
    for attr in ("part", "sketch", "line"):
        if hasattr(val, attr) and getattr(val, attr) is not None:
            return True
    return False


def _extract_script_geometry(module: Any, captured: list[Any]) -> Any:
    """Extract geometry from captured show()/show_object() calls or top-level module bindings."""
    if captured:
        return captured[0] if len(captured) == 1 else list(captured)

    priority_names = ("result", "part", "model", "compound", "assembly", "solid", "sketch", "shape", "obj")
    mod_dict = getattr(module, "__dict__", {})
    for name in priority_names:
        if name in mod_dict and _is_script_shape(mod_dict[name]):
            return mod_dict[name]

    for name, val in reversed(list(mod_dict.items())):
        if name.startswith("_"):
            continue
        if _is_script_shape(val):
            return val

    raise ValueError(
        "Script produced no build123d geometry (call show() or assign a Part/Compound/BuildPart to a top-level variable)."
    )


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
        self._render_cache: OrderedDict[tuple[str, str], tuple[int, dict[str, Any]]] = OrderedDict()
        self._render_cache_bytes: int = 0
        self._ast_cache: dict[str, tuple[int, int, dict[str, Any] | None]] = {}
        self._script_captures: dict[str, list[Any]] = {}
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
        self._script_captures.clear()
        self.last_build.clear()
        self._render_cache.clear()
        self._render_cache_bytes = 0
        ocp_tess_mod = sys.modules.get("ocp_tessellate.tessellator")
        if ocp_tess_mod is not None:
            ocp_cache = getattr(ocp_tess_mod, "cache", None)
            if ocp_cache is not None and hasattr(ocp_cache, "clear"):
                try:
                    ocp_cache.clear()
                except Exception:
                    pass
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
                explicit_id = str(raw.get("id") or "").strip()
                if explicit_id:
                    if not re.fullmatch(r"[A-Za-z0-9._-]+", explicit_id) or explicit_id in (".", ".."):
                        raise _HttpError(400, f"Invalid library id: {explicit_id}")
                    lib_id = explicit_id
                else:
                    lib_id = re.sub(r"[^a-z0-9]+", "-", root.name.lower()).strip("-") or "library"
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
                self._ast_cache.clear()
                return [], {}

            found: list[dict[str, Any]] = []
            seen_files: set[str] = set()
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
                        if fname.startswith("_") or fname == "setup.py":
                            continue
                        py_file = Path(dirpath) / fname
                        cache_key = str(py_file)
                        seen_files.add(cache_key)
                        try:
                            st = py_file.stat()
                        except OSError:
                            continue

                        cached = self._ast_cache.get(cache_key)
                        if cached is not None and cached[0] == st.st_mtime_ns and cached[1] == st.st_size:
                            ast_info = cached[2]
                        else:
                            ast_info = _inspect_script_ast(py_file)
                            self._ast_cache[cache_key] = (st.st_mtime_ns, st.st_size, ast_info)

                        if ast_info is None:
                            continue
                        meta = _build_model_info(py_file, lib_id, lib_name, root, ast_info)

                        # Guard against rare intra-library ID collision (e.g. foo/foo.py vs foo.py)
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

            for stale_key in [k for k in self._ast_cache if k not in seen_files]:
                del self._ast_cache[stale_key]

            return found, dict(self.library_errors)

    def _get_model_meta(self, model_id: str) -> dict[str, Any]:
        _validate_model_id(model_id)
        if model_id not in self.models:
            self.discover_models()
        if model_id not in self.models:
            raise _HttpError(404, f"Unknown model: {model_id}")
        return self.models[model_id]

    def _exec_script_with_capture(
        self,
        spec: Any,
        module: Any,
        mod_name: str,
    ) -> list[Any]:
        """Execute a Tier 1 arbitrary script while capturing show()/show_object() calls."""
        captured: list[Any] = []

        def _capture_show(*objs: Any, names: Any = None, colors: Any = None, **kwargs: Any) -> None:
            name_list = list(names) if isinstance(names, (list, tuple)) else None
            color_list = list(colors) if isinstance(colors, (list, tuple)) else None
            items: list[Any] = []

            def _flatten_into(val: Any) -> None:
                if val is None:
                    return
                if isinstance(val, (list, tuple)):
                    for sub in val:
                        _flatten_into(sub)
                else:
                    items.append(val)

            for obj in objs:
                _flatten_into(obj)
            for idx, obj in enumerate(items):
                if obj is None:
                    continue
                lbl = str(name_list[idx]) if name_list is not None and idx < len(name_list) and name_list[idx] is not None else ""
                col = color_list[idx] if color_list is not None and idx < len(color_list) else None
                if lbl or col is not None:
                    captured.append(_CapturedShape(obj, label=lbl, color=col))
                else:
                    captured.append(obj)

        def _capture_show_object(obj: Any, name: str | None = None, options: Any = None, **kwargs: Any) -> None:
            if obj is None:
                return
            col = options.get("color") if isinstance(options, dict) else kwargs.get("color")
            lbl = str(name) if name else ""
            if lbl or col is not None:
                captured.append(_CapturedShape(obj, label=lbl, color=col))
            else:
                captured.append(obj)

        def _noop(*args: Any, **kwargs: Any) -> None:
            return None

        mock_viewer = types.ModuleType("ocp_vscode")
        mock_viewer.show = _capture_show  # type: ignore[attr-defined]
        mock_viewer.show_object = _capture_show_object  # type: ignore[attr-defined]
        mock_viewer.set_defaults = _noop  # type: ignore[attr-defined]
        mock_viewer.set_colormap = _noop  # type: ignore[attr-defined]
        mock_viewer.reset_show = _noop  # type: ignore[attr-defined]
        mock_viewer.show_all = _noop  # type: ignore[attr-defined]
        mock_viewer.show_clear = _noop  # type: ignore[attr-defined]

        orig_vscode = sys.modules.get("ocp_vscode")
        orig_viewer = sys.modules.get("ocp_viewer")
        sys.modules["ocp_vscode"] = mock_viewer
        sys.modules["ocp_viewer"] = mock_viewer
        try:
            spec.loader.exec_module(module)
        except Exception:
            sys.modules.pop(mod_name, None)
            raise
        finally:
            if orig_vscode is None:
                sys.modules.pop("ocp_vscode", None)
            else:
                sys.modules["ocp_vscode"] = orig_vscode
            if orig_viewer is None:
                sys.modules.pop("ocp_viewer", None)
            else:
                sys.modules["ocp_viewer"] = orig_viewer
        return captured

    def _load_model_module(self, model_id: str) -> tuple[dict[str, Any], Any]:
        meta = self._get_model_meta(model_id)
        if meta.get("kind") == "error":
            raise _HttpError(422, meta.get("parse_error") or "Model script has syntax error")

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
        if meta.get("kind") == "script":
            captured = self._exec_script_with_capture(spec, module, mod_name)
            self._script_captures[model_id] = captured
        else:
            try:
                spec.loader.exec_module(module)
            except Exception:
                sys.modules.pop(mod_name, None)
                raise
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
            hints = _safe_get_type_hints(func)
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
            meta = self._get_model_meta(model_id)
            if meta.get("kind") == "error":
                raise _HttpError(422, meta.get("parse_error") or "Model script has syntax error")
            if meta.get("kind") == "script":
                return {
                    "ok": True,
                    "model": meta,
                    "description": meta.get("description", ""),
                    "fields": [],
                    "defaults": {},
                }

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
            meta = self._get_model_meta(model_id)
            if meta.get("kind") == "error":
                raise _HttpError(422, meta.get("parse_error") or "Model script has syntax error")
            if meta.get("kind") == "script":
                return {
                    "ok": False,
                    "error": "Model does not define a Params @dataclass; defaults cannot be updated automatically.",
                }

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

            if unsupported:
                return {
                    **base_resp,
                    "ok": False,
                    "error": "One or more changed fields cannot be updated safely; no changes were written.",
                }

            expected_clean = expected_sha256.strip()
            if not expected_clean:
                raise _HttpError(
                    400,
                    "expected_sha256 is required when dry_run=false",
                    extra={k: v for k, v in base_resp.items() if k != "ok"},
                )
            if expected_clean != file_sha256:
                return {
                    **base_resp,
                    "ok": False,
                    "error": "File changed on disk since preview; please preview again before applying.",
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
                new_source = new_bytes.decode("utf-8-sig")
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
                _replace_with_retry(tmp_path, py_path)
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

            # Clear module/render/AST caches so subsequent schema/renders immediately reflect new defaults
            self._ast_cache.pop(str(py_path), None)
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
            meta = self._get_model_meta(model_id)
            if meta.get("kind") == "error":
                raise _HttpError(422, meta.get("parse_error") or "Model script has syntax error")
            if meta.get("kind") == "script":
                fallback = _sanitize_filename_stem(Path(meta["path"]).stem, "model")
                return {
                    "ok": True,
                    "export_filename": fallback,
                    "library_id": meta["library_id"],
                }

            meta, module = self._load_model_module(model_id)
            params_cls, _ = self._resolve_entrypoints(meta, module)
            stem = self._resolve_export_filename(meta, module, params_cls, params_dict)
            return {
                "ok": True,
                "export_filename": stem,
                "library_id": meta["library_id"],
            }

    def _build_shape(self, model_id: str, params_dict: dict[str, Any]) -> tuple[dict[str, Any], Any, float, str]:
        meta = self._get_model_meta(model_id)
        if meta.get("kind") == "error":
            raise _HttpError(422, meta.get("parse_error") or "Model script has syntax error")

        _wait_for_cad_warmup()
        if not HAS_BUILD123D:
            raise RuntimeError(f"build123d is not available in {sys.executable}: {BUILD123D_ERROR}")

        meta, module = self._load_model_module(model_id)

        if meta.get("kind") == "script":
            t0 = time.perf_counter()
            result = _extract_script_geometry(module, self._script_captures.get(model_id, []))
            build_ms = (time.perf_counter() - t0) * 1000.0
            export_stem = self._resolve_export_filename(meta, module, None, params_dict)
            self.last_build = {
                "model_id": model_id,
                "params_json": json.dumps(params_dict, sort_keys=True),
                "result": result,
                "meta": meta,
                "export_filename": export_stem,
            }
            return meta, result, build_ms, export_stem

        params_cls, func = self._resolve_entrypoints(meta, module)

        t0 = time.perf_counter()
        params_obj = None
        if params_cls is not None:
            params_obj = _instantiate_dataclass(params_cls, params_dict)
            result = func(params_obj)
        else:
            sig = inspect.signature(func)
            hints = _safe_get_type_hints(func)
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
        _validate_model_id(model_id)
        my_seq = self._next_render_seq()
        params_key = json.dumps(params_dict, sort_keys=True)
        cache_key = (model_id, params_key)

        with self.lock:
            if my_seq != self._latest_render_seq():
                return {"ok": False, "superseded": True, "model_id": model_id}

            meta = self._get_model_meta(model_id)
            if meta.get("kind") == "error":
                raise _HttpError(422, meta.get("parse_error") or "Model script has syntax error")

            cached_entry = self._render_cache.get(cache_key)
            if cached_entry is not None:
                _, cached = cached_entry
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

            try:
                file_mtime_ns = Path(meta["path"]).stat().st_mtime_ns
            except OSError:
                file_mtime_ns = 0
            params_hash = hashlib.sha1(params_key.encode("utf-8")).hexdigest()[:12]
            cache_prefix = f"{model_id}:{file_mtime_ns}:{params_hash}"

            t1 = time.perf_counter()
            shapes, bbox = tessellate_result(result, meta["name"], cache_prefix=cache_prefix)
            tess_ms = (time.perf_counter() - t1) * 1000.0
            payload = {
                "ok": True,
                "cached": False,
                "model_id": model_id,
                "build_time_ms": round(build_ms, 1),
                "tessellate_time_ms": round(tess_ms, 1),
                "bbox": bbox,
                "shapes": shapes,
                "export_filename": export_stem,
            }
            entry_bytes = _estimate_render_payload_bytes(payload)
            if entry_bytes <= MAX_RENDER_CACHE_BYTES:
                if cache_key in self._render_cache:
                    old_bytes, _ = self._render_cache.pop(cache_key)
                    self._render_cache_bytes = max(0, self._render_cache_bytes - old_bytes)
                self._render_cache[cache_key] = (entry_bytes, payload)
                self._render_cache_bytes += entry_bytes
                self._render_cache.move_to_end(cache_key)
                while (
                    len(self._render_cache) > MAX_RENDER_CACHE
                    or self._render_cache_bytes > MAX_RENDER_CACHE_BYTES
                ):
                    _, (evicted_bytes, _) = self._render_cache.popitem(last=False)
                    self._render_cache_bytes = max(0, self._render_cache_bytes - evicted_bytes)
            return payload

    def export(
        self,
        model_id: str,
        params_dict: dict[str, Any],
        fmt: str,
        output_path: str = "",
        quality: str = "standard",
        linear_deflection: Any = None,
        angular_deflection: Any = None,
    ) -> tuple[dict[str, Any] | None, bytes | None]:
        fmt_lower = str(fmt or "").strip().lower().lstrip(".")
        if fmt_lower not in ALLOWED_EXPORT_FORMATS:
            raise _HttpError(400, f"Unsupported export format: {fmt}")
        lin_defl, ang_defl = _resolve_export_deflection(quality, linear_deflection, angular_deflection)

        with self.lock:
            meta = self._get_model_meta(model_id)
            if meta.get("kind") == "error":
                raise _HttpError(422, meta.get("parse_error") or "Model script has syntax error")

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
                export_shape_to_file(
                    result,
                    fmt_lower,
                    out,
                    linear_deflection=lin_defl,
                    angular_deflection=ang_defl,
                )
                return {"ok": True, "path": str(out), "size": out.stat().st_size}, None

            safe_stem = _sanitize_filename_stem(export_stem, "model")
            with tempfile.TemporaryDirectory() as tmpdir:
                out = Path(tmpdir) / f"{safe_stem}.{fmt_lower}"
                export_shape_to_file(
                    result,
                    fmt_lower,
                    out,
                    linear_deflection=lin_defl,
                    angular_deflection=ang_defl,
                )
                return None, out.read_bytes()


def _extract_host_name(host_header: str) -> str:
    raw = (host_header or "").strip().lower()
    if not raw:
        return ""
    if raw.startswith("["):
        end_idx = raw.find("]")
        if end_idx <= 1:
            return ""
        rest = raw[end_idx + 1 :]
        if rest and not (rest.startswith(":") and rest[1:].isdigit()):
            return ""
        return raw[: end_idx + 1]
    if ":" in raw:
        host_part, port_part = raw.rsplit(":", 1)
        if not port_part.isdigit():
            return ""
        return host_part
    return raw


def make_handler(state: WorkerState, auth_token: str | None = None) -> type[BaseHTTPRequestHandler]:
    expected_token = (
        auth_token.strip()
        if isinstance(auth_token, str) and auth_token.strip()
        else os.environ.get("CODE_CAD_WORKER_TOKEN", "").strip() or secrets.token_hex(32)
    )

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, format: str, *args: Any) -> None:
            # Keep stdout clean; send logs to stderr.
            sys.stderr.write("[worker] " + (format % args) + "\n")
            sys.stderr.flush()

        def _send_json(
            self,
            payload: dict[str, Any],
            status: int = 200,
            headers: dict[str, str] | None = None,
        ) -> None:
            body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
            try:
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                if headers:
                    for hk, hv in headers.items():
                        self.send_header(hk, hv)
                self.end_headers()
                self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                # Client aborted an in-flight superseded request; ignore silently.
                pass

        def _authorize_and_check_host(self) -> bool:
            hostname = _extract_host_name(self.headers.get("Host", ""))
            if hostname not in ALLOWED_LOOPBACK_HOSTS:
                self._send_json({"ok": False, "error": "forbidden host"}, status=403)
                return False

            auth_hdr = (self.headers.get("Authorization") or "").strip()
            parts = auth_hdr.split(None, 1)
            if len(parts) != 2 or parts[0].lower() != "bearer":
                self._send_json(
                    {"ok": False, "error": "unauthorized"},
                    status=401,
                    headers={"WWW-Authenticate": "Bearer"},
                )
                return False

            supplied = parts[1].strip()
            if not supplied or not hmac.compare_digest(
                supplied.encode("utf-8"),
                expected_token.encode("utf-8"),
            ):
                self._send_json(
                    {"ok": False, "error": "unauthorized"},
                    status=401,
                    headers={"WWW-Authenticate": "Bearer"},
                )
                return False
            return True

        def _read_json(self) -> dict[str, Any]:
            transfer_encodings = self.headers.get_all("Transfer-Encoding") or []
            if any(te.strip() for te in transfer_encodings):
                raise _HttpError(411, "transfer encoding not supported")
            content_lengths = self.headers.get_all("Content-Length") or []
            if len(content_lengths) > 1:
                raise _HttpError(400, "duplicate Content-Length header")
            raw_len = content_lengths[0] if content_lengths else None
            if raw_len is None or raw_len.strip() == "":
                return {}
            try:
                length = int(raw_len.strip())
            except ValueError:
                raise _HttpError(400, "invalid Content-Length")
            if length < 0:
                raise _HttpError(400, "invalid Content-Length")
            if length > MAX_REQUEST_BYTES:
                raise _HttpError(413, "request body exceeds 2 MiB limit")
            if length == 0:
                return {}
            raw = self.rfile.read(length)
            if len(raw) != length:
                raise _HttpError(400, "incomplete request body")
            try:
                text = raw.decode("utf-8")
                parsed = json.loads(text)
            except UnicodeDecodeError:
                raise _HttpError(400, "invalid UTF-8 request body")
            except json.JSONDecodeError:
                raise _HttpError(400, "invalid JSON request body")
            if not isinstance(parsed, dict):
                raise _HttpError(400, "JSON object expected")
            return parsed

        def do_GET(self) -> None:
            if not self._authorize_and_check_host():
                return
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
                            "python_version": sys.version.split()[0],
                            "build123d": HAS_BUILD123D,
                            "build123d_version": _package_version("build123d") if HAS_BUILD123D else "",
                            "build123d_error": BUILD123D_ERROR,
                            "ocp_tessellate": HAS_OCP_TESSELLATE,
                            "ocp_tessellate_version": _package_version("ocp_tessellate") if HAS_OCP_TESSELLATE else "",
                            "libraries": libs,
                        }
                    )
                    return

                if path == "/models":
                    models, lib_errors = state.discover_models()
                    self._send_json({"ok": True, "models": models, "library_errors": lib_errors})
                    return

                m = re.match(r"^/models/(.+)/schema$", path)
                if m:
                    model_id = unquote(m.group(1))
                    self._send_json(state.get_schema(model_id))
                    return

                self._send_json({"ok": False, "error": f"Not found: {path}"}, status=404)
            except _HttpError as exc:
                self._send_json({"ok": False, "error": exc.message, **exc.extra}, status=exc.status)
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
            if not self._authorize_and_check_host():
                return
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

                m_render = re.match(r"^/models/(.+)/render$", path)
                if m_render:
                    model_id = unquote(m_render.group(1))
                    params = body.get("params", {})
                    if not isinstance(params, dict):
                        raise _HttpError(400, "params must be a JSON object")
                    self._send_json(state.render(model_id, params))
                    return

                m_export_name = re.match(r"^/models/(.+)/export-name$", path)
                if m_export_name:
                    model_id = unquote(m_export_name.group(1))
                    params = body.get("params", {})
                    if not isinstance(params, dict):
                        raise _HttpError(400, "params must be a JSON object")
                    self._send_json(state.get_export_filename(model_id, params))
                    return

                m_update_defaults = re.match(r"^/models/(.+)/update-defaults$", path)
                if m_update_defaults:
                    model_id = unquote(m_update_defaults.group(1))
                    params = body.get("params", {})
                    if not isinstance(params, dict):
                        raise _HttpError(400, "params must be a JSON object")
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

                m_export = re.match(r"^/models/(.+)/export$", path)
                if m_export:
                    model_id = unquote(m_export.group(1))
                    params = body.get("params", {})
                    if not isinstance(params, dict):
                        raise _HttpError(400, "params must be a JSON object")
                    fmt = body.get("format", "step")
                    output_path = body.get("output_path", "")
                    quality = body.get("quality", "standard")
                    linear_deflection = body.get("linear_deflection")
                    angular_deflection = body.get("angular_deflection")
                    meta_resp, raw_bytes = state.export(
                        model_id,
                        params,
                        fmt,
                        output_path,
                        quality=quality,
                        linear_deflection=linear_deflection,
                        angular_deflection=angular_deflection,
                    )
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
            except _HttpError as exc:
                self._send_json({"ok": False, "error": exc.message, **exc.extra}, status=exc.status)
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

    auth_token = os.environ.get("CODE_CAD_WORKER_TOKEN", "").strip() or secrets.token_hex(32)
    state = WorkerState(libraries)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), make_handler(state, auth_token=auth_token))
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
