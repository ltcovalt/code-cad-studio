"""Parametric press-fit spool rod cap with optional grip flutes."""

from dataclasses import dataclass, field
from typing import Literal

from components.chamfer_base import BaseFlangeParams, create_base_flange


@dataclass
class CoreParams:
    shaft_diameter: float = field(default=10.0, metadata={"min": 4.0, "max": 30.0, "step": 0.2})  # Rod diameter (mm)
    wall_thickness: float = field(default=2.4, metadata={"min": 1.2, "max": 6.0, "step": 0.2})  # Sleeve wall thickness (mm)
    sleeve_height: float = field(default=18.0, metadata={"min": 8.0, "max": 45.0, "step": 1.0})  # Sleeve length (mm)
    cap_style: Literal["flat", "dome", "chamfer"] = "chamfer"  # Top cap profile


@dataclass
class FlutesParams:
    enabled: bool = True  # Add radial grip flutes
    count: int = field(default=12, metadata={"min": 4, "max": 32, "step": 1})  # Number of grip flutes
    depth: float = field(default=0.8, metadata={"min": 0.2, "max": 2.0, "step": 0.1})  # Flute cut depth (mm)


@dataclass
class SpoolCapParams:
    core: CoreParams = field(default_factory=CoreParams)
    flutes: FlutesParams = field(default_factory=FlutesParams)
    base: BaseFlangeParams = field(default_factory=BaseFlangeParams)


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
