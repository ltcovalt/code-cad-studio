from dataclasses import dataclass
import build123d as b3d


@dataclass
class AlphaParams:
    width: float = 10.0


def create_alpha(params: AlphaParams):
    return b3d.Box(params.width, 10.0, 10.0)
