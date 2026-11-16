#!/usr/bin/env python3
"""Confined production renderer transport, not privileged VM acceptance."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
PHP = "php@sha256:d41f4e57e97732698eddc7e17dcc75027ebf4f549aaf3724894a9f9ff062f8ba"

NGINX_SHA256 = "4e7b52f8b31f26d77ffb5c427c41e36223e81b047242ecfa8dc1d7f939734885"
# Debian bookworm nginx_1.22.1-9+deb12u9_amd64.deb, extract only, no maintainer scripts.


def verify_response(path, status, body, expected):
    assert b"<?php" not in body, f"PHP source disclosure at {path}"
    if expected is None:
        assert status == 404, f"{path}: expected 404, got {status}"
    else:
        assert status == 200 and body == expected, f"{path}: expected execution, got {status} {body!r}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("nginx_deb", type=Path, help="exact pinned Debian package, already acquired")
    parser.add_argument("--require-production", action="store_true", help="fail on missing production PHP routing")
    args = parser.parse_args()
    package = args.nginx_deb.read_bytes()
    assert hashlib.sha256(package).hexdigest() == NGINX_SHA256, "nginx package pin mismatch"
    evidence = Path(tempfile.mkdtemp(prefix="phase4-php-nginx-", dir=os.environ["TMPDIR"]))
    name = "novapanel-p4-http-" + uuid.uuid4().hex[:12]
    created = False

    def run(*cmd, data=None, check=True):
        p = subprocess.run(cmd, input=data, capture_output=True, cwd=ROOT, timeout=180)
        with (evidence / "commands.log").open("ab") as log:
            log.write(("$ " + " ".join(cmd) + "\n").encode() + p.stdout + p.stderr + f"\nexit={p.returncode}\n".encode())
        if check and p.returncode:
            raise RuntimeError(f"command failed: {cmd}; evidence={evidence}")
        return p

    def ex(*cmd, **kwargs):
        return run("docker", "exec", "-i", name, *cmd, **kwargs)

    def write(path, data):
        ex("sh", "-c", 'umask 077; cat > "$1"', "sh", path, data=data.encode() if isinstance(data, str) else data)

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

    def probe(path):
        p = ex("curl", "--path-as-is", "--silent", "--show-error", "--max-time", "3", "-H", "Host: lab.invalid", "-o", "/tmp/body", "-w", "%{http_code}", "http://127.0.0.1:8080" + path)
        return int(p.stdout), ex("cat", "/tmp/body").stdout

    try:
        sha = run("git", "rev-parse", "HEAD").stdout.decode().strip()
        assert not run("git", "status", "--porcelain").stdout, "commit lab inputs before execution"
        (evidence / "source-sha.txt").write_text(sha + "\n")
        (evidence / "package-sha256.txt").write_text(NGINX_SHA256 + "\n")
        fixtures = json.loads(run("go", "run", "./tools/phase4phpfixtures").stdout)
        (evidence / "fixtures.json").write_text(json.dumps(fixtures, indent=2) + "\n")
        run("docker", "create", "--name", name, "--label", "novapanel.lab=" + name,
            "--user", "33:33", "--read-only", "--network", "none", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges:true", "--memory", "512m", "--memory-swap", "512m",
            "--cpus", "1", "--pids-limit", "64",
            "--tmpfs", "/tmp:rw,nosuid,nodev,exec,size=96m,mode=1777",
            "--tmpfs", "/run/php/novapanel:rw,nosuid,nodev,noexec,size=8m,mode=0755,uid=33,gid=33",
            "--tmpfs", "/var/log/novapanel/lab-site:rw,nosuid,nodev,noexec,size=8m,mode=0700,uid=33,gid=33",
            "--entrypoint", "/bin/sh", PHP, "-c", "exec sleep 300")
        created = True
        inspect("before")
        run("docker", "start", name)
        write("/tmp/nginx.deb", package)
        ex("dpkg-deb", "-x", "/tmp/nginx.deb", "/tmp/nginx")
        ex("/tmp/nginx/usr/sbin/nginx", "-v")
        ex("php-fpm", "-v")
        ex("sh", "-c", "mkdir -p /tmp/lab/public/uploads /tmp/lab/acme")
        write("/tmp/lab/public/index.php", "<?php echo 'executed:' . ini_get('cgi.fix_pathinfo') . ':' . $_SERVER['SCRIPT_FILENAME'] . ':' . ($_SERVER['PATH_INFO'] ?? '');")
        write("/tmp/lab/public/uploads/hostile.jpg", "<?php echo 'HOSTILE_EXECUTED';")
        # Exact pool bytes; only globals are test settings. Socket/log paths unchanged.
        write("/tmp/fpm.conf", "[global]\nerror_log=/tmp/fpm.log\npid=/tmp/fpm.pid\n" + fixtures["Pool"])
        ex("php-fpm", "-t", "-y", "/tmp/fpm.conf")
        run("docker", "exec", "-d", name, "php-fpm", "-F", "-y", "/tmp/fpm.conf")
        for _ in range(50):
            if ex("test", "-S", "/run/php/novapanel/lab-site.sock", check=False).returncode == 0:
                break
            time.sleep(.1)
        else:
            raise RuntimeError("FPM socket readiness failed")
        socket = ex("stat", "-c", "%u:%g %a", "/run/php/novapanel/lab-site.sock").stdout.strip()
        assert socket == b"33:33 660", socket
        (evidence / "socket.txt").write_bytes(socket + b"\n")
        # Only port changes for the current production baseline; no PHP directives added.
        baseline = fixtures["Nginx"].replace("listen 80;", "listen 127.0.0.1:8080;").replace("    listen [::]:80;\n", "")
        globals_ = "pid /tmp/nginx.pid; error_log /tmp/nginx.log; events {} http { access_log off; client_body_temp_path /tmp/client; fastcgi_temp_path /tmp/fastcgi; proxy_temp_path /tmp/proxy; uwsgi_temp_path /tmp/uwsgi; scgi_temp_path /tmp/scgi;\n"

        def configure(server, first=False):
            text = globals_ + server + "\n}\n"
            write("/tmp/nginx.conf", text)
            ex("/tmp/nginx/usr/sbin/nginx", "-t", "-c", "/tmp/nginx.conf", "-p", "/tmp/")
            ex("/tmp/nginx/usr/sbin/nginx", "-c", "/tmp/nginx.conf", "-p", "/tmp/", *([] if first else ["-s", "reload"]))
            time.sleep(.3)
            return text

        configure(baseline, first=True)
        (evidence / "production-nginx.conf").write_text(globals_ + baseline + "\n}\n")
        write("/tmp/lab/public/uploads/hostile.php", "<?php echo 'HOSTILE_EXECUTED';")
        write("/tmp/lab/public/.hidden.php", "<?php echo 'HOSTILE_EXECUTED';")
        results = []
        for path, expected in (("/index.php", b"executed:0:/tmp/lab/public/index.php:"), ("/missing.php", None), ("/uploads/hostile.jpg/missing.php", None), ("/index.php/extra", None), ("/uploads/hostile.php", None), ("/INDEX.PHP", None), ("/uploads/hostile.jpg/extra.php", None), ("/", b"executed:0:/tmp/lab/public/index.php:")):
            status, body = probe(path)
            verify_response(path, status, body, expected)
            assert b"HOSTILE_EXECUTED" not in body
            results.append({"path": path, "status": status, "body_sha256": hashlib.sha256(body).hexdigest()})
        inspect("after")
        result = {"production_transport": "PASS", "production_php_routing": "PASS", "probes": results, "vm_identity_custody": "UNPASSED"}
        (evidence / "result.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps(result), "Evidence:", evidence)
        if args.require_production:
            assert "fastcgi_pass" in fixtures["Nginx"]
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
