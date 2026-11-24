from dataclasses import dataclass
import build123d as b3d


@dataclass
class WidgetParams:
    size: float = 10.0


def export_filename(params: WidgetParams) -> str:
    return f"widget_{int(round(params.size))}mm"


def create_widget(params: WidgetParams):
    return b3d.Box(params.size, params.size, params.size)
