"""Parametric press-fit spool rod cap with optional grip flutes."""

from dataclasses import dataclass
from pathlib import Path
from typing import Literal

try:
    from utils.params import group, param
except ImportError:
    import sys

    for _p in Path(__file__).resolve().parents:
        if (_p / "utils").is_dir():
            sys.path.insert(0, str(_p))
            break
    from utils.params import group, param

from components.chamfer_base import BaseFlangeParams, create_base_flange


@dataclass
class CoreParams:
    """Shaft fit and sleeve dimensions."""

    shaft_diameter: float = param(
        10.0,
        desc="Rod diameter",
        unit="mm", min=4.0, max=30.0, step=0.2,
    )
    wall_thickness: float = param(
        2.4,
        desc="Sleeve wall thickness",
        unit="mm", min=1.2, max=6.0, step=0.2,
    )
    sleeve_height: float = param(
        18.0,
        desc="Sleeve length",
        unit="mm", min=8.0, max=45.0, step=1.0,
    )
    cap_style: Literal["flat", "dome", "chamfer"] = param(
        "chamfer",
        desc="Top cap profile",
    )


@dataclass
class FlutesParams:
    """Radial perimeter grip flutes."""

    enabled: bool = param(
        True,
        desc="Add radial grip flutes",
    )
    count: int = param(
        12,
        desc="Number of grip flutes",
        min=4, max=32, step=1,
    )
    depth: float = param(
        0.8,
        desc="Flute cut depth",
        unit="mm", min=0.2, max=2.0, step=0.1,
    )


@dataclass
class SpoolCapParams:
    core: CoreParams = group(CoreParams)
    flutes: FlutesParams = group(FlutesParams)
    base: BaseFlangeParams = group(BaseFlangeParams)


def create_spool_cap(params: SpoolCapParams):
    import math
    import build123d as b3d

    outer_r = params.core.shaft_diameter / 2.0 + params.core.wall_thickness
    inner_r = params.core.shaft_diameter / 2.0

    base = create_base_flange(params.base)

    with b3d.BuildPart() as bp:
        b3d.add(base)
        with b3d.Locations((0, 0, params.base.flange_thickness / 2.0 + params.core.sleeve_height / 2.0)):
            b3d.Cylinder(radius=outer_r, height=params.core.sleeve_height)
        if params.core.cap_style == "dome":
            with b3d.Locations((0, 0, params.base.flange_thickness / 2.0 + params.core.sleeve_height)):
                b3d.Sphere(radius=outer_r)
        elif params.core.cap_style == "chamfer":
            top_edges = bp.edges().filter_by(b3d.GeomType.CIRCLE).sort_by(b3d.Axis.Z)[-1:]
            b3d.chamfer(top_edges, length=min(1.2, params.core.wall_thickness * 0.45))

        # Inner bore
        with b3d.Locations((0, 0, params.base.flange_thickness / 2.0 + params.core.sleeve_height / 2.0 - 1.0)):
            b3d.Cylinder(radius=inner_r, height=params.core.sleeve_height, mode=b3d.Mode.SUBTRACT)

        if params.flutes.enabled and params.flutes.count > 0:
            for i in range(params.flutes.count):
                angle = 2.0 * math.pi * i / params.flutes.count
                fx = (outer_r + params.flutes.depth * 0.5) * math.cos(angle)
                fy = (outer_r + params.flutes.depth * 0.5) * math.sin(angle)
                with b3d.Locations((fx, fy, params.base.flange_thickness / 2.0 + params.core.sleeve_height / 2.0)):
                    b3d.Cylinder(
                        radius=params.flutes.depth,
                        height=params.core.sleeve_height * 0.85,
                        mode=b3d.Mode.SUBTRACT,
                    )

    part = bp.part
    part.label = "Spool Cap"
    return part
