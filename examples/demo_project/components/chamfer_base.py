"""Reusable chamfered base flange component."""

from dataclasses import dataclass, field


@dataclass
class BaseFlangeParams:
    flange_diameter: float = field(default=32.0, metadata={"min": 16.0, "max": 60.0, "step": 0.5})  # Outer flange diameter (mm)
    flange_thickness: float = field(default=4.0, metadata={"min": 1.5, "max": 12.0, "step": 0.5})  # Flange thickness (mm)
    chamfer_size: float = field(default=0.8, metadata={"min": 0.0, "max": 3.0, "step": 0.1})  # Edge chamfer (mm)


def create_base_flange(params: BaseFlangeParams):
    import build123d as b3d

    with b3d.BuildPart() as bp:
        b3d.Cylinder(radius=params.flange_diameter / 2.0, height=params.flange_thickness)
        if params.chamfer_size > 0:
            b3d.chamfer(bp.edges(), length=min(params.chamfer_size, params.flange_thickness * 0.4))
    return bp.part
