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
        source = path.read_text()
        if path.as_posix() == "internal/wordpress/tar_extract.go":
            # The Go AST checker permits only syscall.Stat_t in this file.
            source = source.replace('"syscall"', '', 1)
        if re.search(r'"(?:os/exec|syscall)"', source):
            bad.append(str(path))
if bad:
    sys.exit("process import outside internal/exec: " + ", ".join(bad))
