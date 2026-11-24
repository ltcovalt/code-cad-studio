from dataclasses import dataclass
import build123d as b3d


@dataclass
class CurvedExportParams:
    radius: float = 20.0
    height: float = 30.0


def export_filename(params: CurvedExportParams) -> str:
    return f"curved_export_{int(round(params.radius))}mm"


def create_curved_export(params: CurvedExportParams):
    with b3d.BuildPart() as bp:
        b3d.Cylinder(radius=params.radius * 0.75, height=params.height)
        b3d.Sphere(radius=params.radius)
    return bp.part
