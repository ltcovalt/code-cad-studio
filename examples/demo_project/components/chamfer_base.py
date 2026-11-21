"""Reusable chamfered base flange component."""

from dataclasses import dataclass

from utils.params import param


@dataclass
class BaseFlangeParams:
    """Bottom mounting flange with chamfered perimeter."""

    flange_diameter: float = param(
        32.0,
        desc="Outer flange diameter",
        unit="mm", min=16.0, max=60.0, step=0.5,
    )
    flange_thickness: float = param(
        4.0,
        desc="Flange axial thickness",
        unit="mm", min=1.5, max=12.0, step=0.5,
    )
    chamfer_size: float = param(
        0.8,
        desc="Perimeter edge chamfer",
        unit="mm", min=0.0, max=3.0, step=0.1,
    )


def create_base_flange(params: BaseFlangeParams):
    import build123d as b3d

    with b3d.BuildPart() as bp:
        b3d.Cylinder(radius=params.flange_diameter / 2.0, height=params.flange_thickness)
        if params.chamfer_size > 0:
            b3d.chamfer(bp.edges(), length=min(params.chamfer_size, params.flange_thickness * 0.4))
    return bp.part
