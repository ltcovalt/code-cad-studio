from dataclasses import dataclass
import build123d as b3d


@dataclass
class RuntimeErrorParams:
    divisor: float = 0.0


def create_runtime_error(params: RuntimeErrorParams):
    val = 10.0 / params.divisor
    return b3d.Box(val, 10.0, 10.0)
