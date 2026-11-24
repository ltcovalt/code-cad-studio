class gp_Pnt:
    def __init__(self, x: float = 0.0, y: float = 0.0, z: float = 0.0) -> None:
        self._x = float(x)
        self._y = float(y)
        self._z = float(z)

    def X(self) -> float:
        return self._x

    def Y(self) -> float:
        return self._y

    def Z(self) -> float:
        return self._z
