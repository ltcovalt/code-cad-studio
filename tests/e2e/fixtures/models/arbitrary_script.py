from build123d import Box
from ocp_vscode import show

part = Box(10, 20, 5)
part.label = "ArbitraryBox"
show(part)
