from dataclasses import dataclass, field
import build123d as b3d


@dataclass
class StdlibBoxParams:
    """Plain stdlib dataclass model without utils.params."""

    width: float = field(
        default=12.0,
        metadata={"description": "Box width", "unit": "mm", "min": 2.0, "max": 100.0},
    )
    depth: float = field(
        default=18.0,
        metadata={"description": "Box depth", "unit": "mm", "min": 2.0, "max": 100.0},
    )
    height: float = field(
        default=6.0,
        metadata={"description": "Box height", "unit": "mm", "min": 1.0, "max": 50.0},
    )


def export_filename(params: StdlibBoxParams) -> str:
    return f"stdlib_box_{int(round(params.width))}x{int(round(params.depth))}mm"


def create_stdlib_dataclass(params: StdlibBoxParams):
    part = b3d.Box(params.width, params.depth, params.height)
    part.label = "StdlibBox"
    return part
