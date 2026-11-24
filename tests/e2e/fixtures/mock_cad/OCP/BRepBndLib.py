class BRepBndLib:
    @staticmethod
    def AddOptimal_s(wrapped, bnd, use_triangulation: bool = True, use_shape_tolerance: bool = False) -> None:
        bounds = getattr(wrapped, "bbox", (-5.0, -5.0, -5.0, 5.0, 5.0, 5.0))
        bnd._set_bounds(bounds)
