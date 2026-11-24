from .gp import gp_Pnt


class Bnd_Box:
    def __init__(self) -> None:
        self._bounds = None

    def _set_bounds(self, bounds) -> None:
        self._bounds = tuple(float(v) for v in bounds)

    def IsVoid(self) -> bool:
        return self._bounds is None

    def CornerMin(self) -> gp_Pnt:
        if self._bounds is None:
            return gp_Pnt(0.0, 0.0, 0.0)
        return gp_Pnt(self._bounds[0], self._bounds[1], self._bounds[2])

    def CornerMax(self) -> gp_Pnt:
        if self._bounds is None:
            return gp_Pnt(0.0, 0.0, 0.0)
        return gp_Pnt(self._bounds[3], self._bounds[4], self._bounds[5])
