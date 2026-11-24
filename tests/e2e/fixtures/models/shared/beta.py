from dataclasses import dataclass
import build123d as b3d


@dataclass
class BetaParams:
    radius: float = 8.0


def create_beta(params: BetaParams):
    return b3d.Cylinder(params.radius, 12.0)
