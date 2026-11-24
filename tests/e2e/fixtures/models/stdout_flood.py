import sys
from dataclasses import dataclass
import build123d as b3d


@dataclass
class StdoutFloodParams:
    size: float = 10.0


def create_stdout_flood(params: StdoutFloodParams):
    sys.stdout.write("X" * 262144 + "\n")
    sys.stdout.flush()
    sys.stderr.write("Y" * 262144 + "\n")
    sys.stderr.flush()
    return b3d.Box(params.size, params.size, params.size)
