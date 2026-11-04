#!/usr/bin/env python3
"""Run the production in-process PHP/nginx coordinator in an owned confined container."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid
from phase4_php_runtime_lab import PHP, NGINX_SHA256, ROOT


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("nginx_deb", type=Path)
    args = parser.parse_args()
    package = args.nginx_deb.read_bytes()
    assert hashlib.sha256(package).hexdigest() == NGINX_SHA256
    evidence = Path(tempfile.mkdtemp(prefix="phase4-coordinated-", dir=os.environ["TMPDIR"]))
    name = "novapanel-p4-coordinated-" + uuid.uuid4().hex[:12]
    created = False

    def run(*cmd, data=None, check=True, env=None):
        p = subprocess.run(cmd, input=data, capture_output=True, cwd=ROOT, timeout=240, env=env)
        with (evidence / "commands.log").open("ab") as log:
            log.write(("$ " + " ".join(map(str, cmd)) + "\n").encode() + p.stdout + p.stderr + f"\nexit={p.returncode}\n".encode())
        if check and p.returncode:
            raise RuntimeError(f"command failed: {cmd}; evidence={evidence}")
        return p

    def ex(*cmd, **kwargs):
        return run("docker", "exec", "-i", name, *cmd, **kwargs)

    def inspect(stage):
        raw = run("docker", "inspect", name).stdout
        (evidence / f"inspect-{stage}.json").write_bytes(raw)
        item = json.loads(raw)[0]
        host = item["HostConfig"]
        assert item["Config"]["User"] == "33:33"
        assert host["ReadonlyRootfs"] and not host["Privileged"]
        assert host["NetworkMode"] == "none" and not host["PortBindings"]
        assert not host["Binds"] and not host["Devices"] and not host["CapAdd"]
        assert host["CapDrop"] == ["ALL"] and "no-new-privileges:true" in host["SecurityOpt"]
        assert host["Memory"] == host["MemorySwap"] == 536870912
        assert host["NanoCpus"] == 1000000000 and host["PidsLimit"] == 64
        assert set(host["Tmpfs"]) == {"/tmp", "/run/php/novapanel", "/var/log/novapanel/lab-site"}
        assert all(m["Type"] == "tmpfs" for m in item["Mounts"])
        assert "size=96m" in host["Tmpfs"]["/tmp"]
        for path in ("/run/php/novapanel", "/var/log/novapanel/lab-site"):
            assert "size=8m" in host["Tmpfs"][path] and "uid=33" in host["Tmpfs"][path]

    def write(path, data):
        ex("sh", "-c", 'umask 077; cat > "$1"', "sh", path, data=data)

    try:
        assert not run("git", "status", "--porcelain").stdout, "commit inputs before running"
        (evidence / "source-sha.txt").write_bytes(run("git", "rev-parse", "HEAD").stdout)
        run("go", "build", "-o", str(evidence / "coordinator"), "./tools/phase4phpcoordinated", env={**os.environ, "CGO_ENABLED": "0"})
        run("docker", "create", "--name", name, "--label", "novapanel.lab=" + name,
            "--user", "33:33", "--read-only", "--network", "none", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges:true", "--memory", "512m", "--memory-swap", "512m",
            "--cpus", "1", "--pids-limit", "64", "--tmpfs", "/tmp:rw,nosuid,nodev,exec,size=96m,mode=1777",
            "--tmpfs", "/run/php/novapanel:rw,nosuid,nodev,noexec,size=8m,mode=0755,uid=33,gid=33",
            "--tmpfs", "/var/log/novapanel/lab-site:rw,nosuid,nodev,noexec,size=8m,mode=0700,uid=33,gid=33",
            "--entrypoint", "/bin/sh", PHP, "-c", "exec sleep 300")
        created = True
        inspect("before")
        run("docker", "start", name)
        write("/tmp/nginx.deb", package)
        ex("dpkg-deb", "-x", "/tmp/nginx.deb", "/tmp/nginx")
        write("/tmp/coordinator", (evidence / "coordinator").read_bytes())
        ex("chmod", "700", "/tmp/coordinator")
        ex("php-fpm", "-v")
        ex("/tmp/nginx/usr/sbin/nginx", "-v")
        result = json.loads(ex("/tmp/coordinator").stdout)
        assert set(result) == {"fpm.validate", "nginx.validate", "fpm.reload", "fpm.probe", "nginx.reload", "nginx.probe", "commit"}
        assert all(item["status"] == "PASS" for item in result.values())
        (evidence / "result.json").write_text(json.dumps(result, indent=2) + "\n")
        inspect("after")
        print(json.dumps(result))
    finally:
        if created:
            try:
                inspect("teardown")
            finally:
                run("docker", "rm", "-f", name)
                assert run("docker", "inspect", name, check=False).returncode != 0
        print("Evidence:", evidence)


if __name__ == "__main__":
    main()
