from typing import Any


def tessellate(
    shapes: list[Any],
    cache_tag: str = "",
    quality: float = 0.1,
    deviation: float = 0.1,
    angular_tolerance: float = 0.2,
    compute_edges: bool = True,
    compute_faces: bool = True,
    **_: Any,
) -> dict[str, Any]:
    wrapped = shapes[0] if shapes else None
    xmin, ymin, zmin, xmax, ymax, zmax = getattr(
        wrapped, "bbox", (-5.0, -5.0, -5.0, 5.0, 5.0, 5.0)
    )

    # Simple 2-triangle quad on top face + 12 box edges for lightweight IPC framing.
    vertices = [
        xmin, ymin, zmin,
        xmax, ymin, zmin,
        xmax, ymax, zmin,
        xmin, ymax, zmin,
        xmin, ymin, zmax,
        xmax, ymin, zmax,
        xmax, ymax, zmax,
        xmin, ymax, zmax,
    ]
    normals = [0.0, 0.0, 1.0] * 8
    triangles = [
        0, 1, 2, 0, 2, 3,
        4, 5, 6, 4, 6, 7,
    ]
    edges = [
        xmin, ymin, zmin, xmax, ymin, zmin,
        xmax, ymin, zmin, xmax, ymax, zmin,
        xmax, ymax, zmin, xmin, ymax, zmin,
        xmin, ymax, zmin, xmin, ymin, zmin,
        xmin, ymin, zmax, xmax, ymin, zmax,
        xmax, ymin, zmax, xmax, ymax, zmax,
        xmax, ymax, zmax, xmin, ymax, zmax,
        xmin, ymax, zmax, xmin, ymin, zmax,
        xmin, ymin, zmin, xmin, ymin, zmax,
        xmax, ymin, zmin, xmax, ymin, zmax,
        xmax, ymax, zmin, xmax, ymax, zmax,
        xmin, ymax, zmin, xmin, ymax, zmax,
    ]
    return {
        "vertices": vertices,
        "normals": normals,
        "triangles": triangles,
        "triangles_per_face": [2, 2],
        "edges": edges if compute_edges else [],
        "segments_per_edge": [1] * 12 if compute_edges else [],
        "obj_vertices": [],
        "face_types": [0, 0],
        "edge_types": [0] * 12 if compute_edges else [],
    }
