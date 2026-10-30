"""Text backstop; the Go AST checks are authoritative."""
from pathlib import Path
import re
import sys

bad = []
for base in (Path("internal"), Path("cmd")):
    for path in base.rglob("*.go"):
        if "testdata" in path.parts or path.name.endswith("_test.go"):
            continue
        if path.is_relative_to("internal/exec"):
            continue
        if re.search(r'"(?:os/exec|syscall)"', path.read_text()):
            bad.append(str(path))
if bad:
    sys.exit("process import outside internal/exec: " + ", ".join(bad))
