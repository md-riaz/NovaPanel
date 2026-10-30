"""Verify repository-packaged units without installing or starting services."""
from pathlib import Path
import subprocess

units = sorted(str(path) for path in Path(".").rglob("*")
               if path.suffix in {".service", ".socket", ".timer", ".target"}
               and ".git" not in path.parts)
if units:
    subprocess.run(["systemd-analyze", "verify", *units], check=True)
else:
    print("No packaged systemd units yet (Phase 1); verification is a no-op.")
