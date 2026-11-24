from dataclasses import dataclass, field
import build123d as b3d


@dataclass
class TypingBomParams:
    opt_float: float | None = None
    opt_bool: bool | None = None
    opt_str: str | None = None
    opt_int: int | None = None
    tags: list = field(default_factory=list)
    width: float = 10.0


def export_filename(params: TypingBomParams) -> str:
    flag = "none" if params.opt_bool is None else str(params.opt_bool).lower()
    return f"bom_flag_{flag}_w{int(round(params.width))}"


def create_typing_bom(params: TypingBomParams):
    w = float(params.width)
    part = b3d.Box(w, w, 5.0)
    flag_tag = "none" if params.opt_bool is None else str(params.opt_bool).lower()
    part.label = f"flag_{flag_tag}_tags_{len(params.tags)}"
    return part
