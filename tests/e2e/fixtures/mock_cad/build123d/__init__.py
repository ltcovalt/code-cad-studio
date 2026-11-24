"""Test-only mock build123d package for control-plane E2E IPC tests when real CAD is absent."""

from __future__ import annotations

import enum
import zipfile
from pathlib import Path
from typing import Any

__version__ = "0.0.0-mock"


class Mode(enum.Enum):
    ADD = "add"
    SUBTRACT = "subtract"
    INTERSECT = "intersect"
    REPLACE = "replace"


class GeomType(enum.Enum):
    CIRCLE = "circle"
    LINE = "line"


class Axis:
    X = "X"
    Y = "Y"
    Z = "Z"


class Color:
    def __init__(self, r: float, g: float = 0.0, b: float = 0.0, a: float = 1.0) -> None:
        if isinstance(r, str):
            self._rgba = (0.33, 0.63, 0.89, 1.0)
        else:
            self._rgba = (float(r), float(g), float(b), float(a))

    def to_tuple(self) -> tuple[float, float, float, float]:
        return self._rgba


class _MockWrapped:
    def __init__(self, bbox: tuple[float, float, float, float, float, float]) -> None:
        self.bbox = bbox


class Shape:
    def __init__(
        self,
        bbox: tuple[float, float, float, float, float, float] = (-5.0, -5.0, -5.0, 5.0, 5.0, 5.0),
        label: str = "",
        color: Any = None,
    ) -> None:
        self.wrapped = _MockWrapped(bbox)
        self.label = label
        self.color = color
        self.children: list[Any] = []

    def edges(self) -> "_ShapeList":
        return _ShapeList([])


class _ShapeList(list):
    def filter_by(self, *_: Any) -> "_ShapeList":
        return _ShapeList([])

    def sort_by(self, *_: Any) -> "_ShapeList":
        return _ShapeList([])


class Solid(Shape):
    pass


class Part(Shape):
    pass


class Compound(Shape):
    def __init__(self, children: list[Any] | None = None, label: str = "") -> None:
        super().__init__(label=label)
        self.children = list(children or [])
        if self.children:
            xs = [c.wrapped.bbox[0] for c in self.children] + [c.wrapped.bbox[3] for c in self.children]
            ys = [c.wrapped.bbox[1] for c in self.children] + [c.wrapped.bbox[4] for c in self.children]
            zs = [c.wrapped.bbox[2] for c in self.children] + [c.wrapped.bbox[5] for c in self.children]
            self.wrapped = _MockWrapped((min(xs), min(ys), min(zs), max(xs), max(ys), max(zs)))


class Box(Part):
    def __init__(self, length: float = 10.0, width: float = 10.0, height: float = 10.0, label: str = "") -> None:
        lx, ly, lz = float(length) / 2.0, float(width) / 2.0, float(height) / 2.0
        super().__init__(bbox=(-lx, -ly, -lz, lx, ly, lz), label=label)


class Cylinder(Part):
    def __init__(self, radius: float = 5.0, height: float = 10.0, label: str = "") -> None:
        r = float(radius)
        hz = float(height) / 2.0
        super().__init__(bbox=(-r, -r, -hz, r, r, hz), label=label)


class Sphere(Part):
    def __init__(self, radius: float = 5.0, label: str = "") -> None:
        r = float(radius)
        super().__init__(bbox=(-r, -r, -r, r, r, r), label=label)


_BUILD_STACK: list["BuildPart"] = []


class BuildPart:
    def __init__(self) -> None:
        self.part: Part = Part()
        self._shapes: list[Shape] = []

    def __enter__(self) -> "BuildPart":
        _BUILD_STACK.append(self)
        return self

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        if _BUILD_STACK and _BUILD_STACK[-1] is self:
            _BUILD_STACK.pop()
        if self._shapes:
            self.part = Part(bbox=self._shapes[0].wrapped.bbox)

    def edges(self) -> _ShapeList:
        return _ShapeList([])


class Locations:
    def __init__(self, *_: Any) -> None:
        pass

    def __enter__(self) -> "Locations":
        return self

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        pass


def add(shape: Any) -> None:
    if _BUILD_STACK and isinstance(shape, Shape):
        _BUILD_STACK[-1]._shapes.append(shape)


def chamfer(*_: Any, **__: Any) -> None:
    pass


_SHOWN: list[Any] = []


def show(*objs: Any, **_: Any) -> None:
    _SHOWN.extend(objs)


def export_step(shape: Any, file_path: str) -> None:
    Path(file_path).write_text(
        "ISO-10303-21;\nHEADER;\nFILE_DESCRIPTION(('Mock STEP'),'2;1');\nENDSEC;\nDATA;\nENDSEC;\nEND-ISO-10303-21;\n",
        encoding="utf-8",
    )


def export_stl(shape: Any, file_path: str, **_: Any) -> None:
    Path(file_path).write_text(
        "solid mock_part\n"
        "  facet normal 0 0 1\n"
        "    outer loop\n"
        "      vertex 0 0 0\n"
        "      vertex 10 0 0\n"
        "      vertex 0 10 0\n"
        "    endloop\n"
        "  endfacet\n"
        "endsolid mock_part\n",
        encoding="utf-8",
    )


class Mesher:
    def __init__(self) -> None:
        self._shapes: list[Any] = []

    def add_shape(self, shape: Any, **_: Any) -> None:
        self._shapes.append(shape)

    def write(self, file_path: str) -> None:
        with zipfile.ZipFile(file_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
            zf.writestr(
                "[Content_Types].xml",
                '<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>',
            )
            zf.writestr(
                "3D/3dmodel.model",
                '<?xml version="1.0" encoding="UTF-8"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/></model>',
            )
