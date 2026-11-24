import time
from dataclasses import dataclass
import build123d as b3d


@dataclass
class InfiniteLoopParams:
    loop: bool = True


def create_infinite_loop(params: InfiniteLoopParams):
    while params.loop:
        time.sleep(0.05)
    return b3d.Box(10, 10, 10)
