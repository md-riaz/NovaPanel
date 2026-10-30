#!/usr/bin/env python3
"""Stage only the Phase 3 maintenance page, never install on the host."""
from pathlib import Path
import io
import sys
import tarfile

root = Path(__file__).resolve().parents[1] / "packaging/rootfs"
name = "var/www/novapanel/maintenance.html"
data = (root / name).read_bytes()
with tarfile.open(sys.argv[1], "w", format=tarfile.USTAR_FORMAT) as archive:
    for directory in ("var", "var/www", "var/www/novapanel"):
        entry = tarfile.TarInfo(directory + "/")
        entry.type = tarfile.DIRTYPE
        entry.mode = 0o755
        entry.uname = entry.gname = "root"
        archive.addfile(entry)
    entry = tarfile.TarInfo(name)
    entry.mode = 0o644
    entry.uname = entry.gname = "root"
    entry.size = len(data)
    archive.addfile(entry, io.BytesIO(data))
