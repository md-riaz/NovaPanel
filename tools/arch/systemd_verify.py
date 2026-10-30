"""Build and verify staged units without installing or starting services."""
from pathlib import Path
import os
import subprocess
import tempfile

root = Path(__file__).resolve().parents[2]
units = sorted((root / "packaging/systemd").glob("*.service"))
if not units:
    raise SystemExit("Missing packaged systemd units")
with tempfile.TemporaryDirectory(prefix="novapanel-systemd-", dir=os.environ.get("TMPDIR")) as tmp:
    stage = Path(tmp)
    staged = []
    commands = {"/usr/sbin/novapanel-agent": "panel-agent", "/usr/bin/novapanel-web": "panel-web"}
    for unit in units:
        text = unit.read_text()
        for installed, command in commands.items():
            if f"ExecStart={installed}\n" in text:
                binary = stage / command
                subprocess.run(["go", "build", "-o", str(binary), f"./cmd/{command}"], cwd=root, check=True)
                text = text.replace(f"ExecStart={installed}\n", f"ExecStart={binary}\n")
        target = stage / unit.name
        target.write_text(text)
        staged.append(str(target))
    subprocess.run(["systemd-analyze", "verify", "--recursive-errors=no", *staged], check=True)
