#!/usr/bin/env python3
"""Test renderer bytes in nonroot, network-none PHP/MariaDB containers.

This is not root unix_socket authentication or multi-UID deployment acceptance.
"""
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
PHP = "php@sha256:d41f4e57e97732698eddc7e17dcc75027ebf4f549aaf3724894a9f9ff062f8ba"
DB = "mariadb@sha256:d769e37960821dfdcf096954f6e7ac89f747e116c1bba98e48edd265dfc7deae"
DATABASE = "d_01arz3ndektsv4rrffq69g5fav"
USER = "u_01arz3ndektsv4rrffq69g5faw"


def main():
    evidence = Path(tempfile.mkdtemp(prefix="phase4-renderers-", dir=os.environ["TMPDIR"]))
    owned = []

    def run(*args, data=None, check=True):
        result = subprocess.run(args, input=data, capture_output=True, cwd=ROOT, timeout=180)
        # Stdin may contain ephemeral credentials. Never record it.
        with (evidence / "commands.log").open("ab") as log:
            log.write(("$ " + " ".join(args) + "\n").encode())
            log.write(result.stdout + result.stderr)
            log.write(f"\nexit={result.returncode}\n".encode())
        if check and result.returncode:
            raise RuntimeError(f"command failed; evidence={evidence}: {args}")
        return result

    def inspect(name, stage):
        raw = run("docker", "inspect", name).stdout
        (evidence / f"{name}-{stage}.json").write_bytes(raw)
        item = json.loads(raw)[0]
        host = item["HostConfig"]
        assert item["Config"]["User"] in ("33:33", "999:999")
        assert host["ReadonlyRootfs"] and not host["Privileged"]
        assert host["NetworkMode"] == "none" and not host["PortBindings"]
        assert not host["Binds"] and not host["Devices"] and not host["CapAdd"]
        assert host["CapDrop"] == ["ALL"]
        assert "no-new-privileges:true" in host["SecurityOpt"]
        assert host["Memory"] == 805306368 and host["MemorySwap"] == 805306368
        assert host["NanoCpus"] == 1000000000 and host["PidsLimit"] == 96
        assert all(m["Type"] == "tmpfs" for m in item["Mounts"])
        assert set(host["Tmpfs"]) == {"/tmp", "/var/lib/mysql"}

    def create(image, uid):
        name = "novapanel-p4-" + uuid.uuid4().hex[:12]
        run("docker", "create", "--name", name, "--label", "novapanel.lab=" + name,
            "--user", uid, "--read-only", "--network", "none", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges:true", "--memory", "768m", "--memory-swap", "768m",
            "--cpus", "1", "--pids-limit", "96", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m,mode=1777",
            "--tmpfs", "/var/lib/mysql:rw,nosuid,nodev,noexec,size=384m,mode=0700,uid=" + uid.split(":")[0] + ",gid=" + uid.split(":")[1],
            "--entrypoint", "/bin/sh", image, "-c", "exec sleep 300")
        owned.append(name)
        inspect(name, "before")
        run("docker", "start", name)
        return name

    def write(name, path, content):
        run("docker", "exec", "-i", name, "sh", "-c", 'umask 077; cat > "$1"', "sh", path, data=content.encode())

    def sql(name, query, app=False, check=True):
        opts = ["--defaults-extra-file=/tmp/app.cnf"] if app else ["--no-defaults", "--socket=/tmp/db.sock", "--user=root"]
        return run("docker", "exec", "-i", name, "mariadb", *opts, "--batch", "--skip-column-names", data=query.encode(), check=check)

    try:
        sha = run("git", "rev-parse", "HEAD").stdout.decode().strip()
        assert not run("git", "status", "--porcelain").stdout, "commit all lab inputs before running"
        (evidence / "source-sha.txt").write_text(sha + "\n")
        fixtures = json.loads(run("go", "run", "./tools/phase4fixtures").stdout)
        (evidence / "fixtures.json").write_text(json.dumps(fixtures, indent=2) + "\n")
        for key, image in (("php", PHP), ("mariadb", DB)):
            (evidence / (key + "-image.json")).write_bytes(run("docker", "image", "inspect", image).stdout)
        php = create(PHP, "33:33")
        run("docker", "exec", php, "php-fpm", "-v")
        raw = "[global]\nerror_log = /tmp/fpm.log\npid = /tmp/fpm.pid\n" + fixtures["Pool"]
        write(php, "/tmp/rendered.conf", raw)
        # Syntax gate consumes exact renderer bytes, including production paths/owners.
        run("docker", "exec", php, "php-fpm", "-t", "-y", "/tmp/rendered.conf")
        adapted = raw.replace("/run/php/novapanel/lab-site.sock", "/tmp/lab-site.sock").replace("/var/log/novapanel/lab-site/php-error.log", "/tmp/php-error.log")
        (evidence / "fpm-scratch.conf").write_text(adapted)
        write(php, "/tmp/scratch.conf", adapted)
        run("docker", "exec", "-d", php, "php-fpm", "-F", "-y", "/tmp/scratch.conf")
        run("docker", "exec", php, "sh", "-c", 'i=0; until test -S /tmp/lab-site.sock; do i=$((i+1)); test "$i" -lt 50 || exit 1; sleep .1; done; stat -c "%u:%g %a %F" /tmp/lab-site.sock')
        db = create(DB, "999:999")
        run("docker", "exec", db, "id")
        run("docker", "exec", db, "mariadb-install-db", "--no-defaults", "--datadir=/var/lib/mysql", "--auth-root-authentication-method=normal", "--skip-test-db")
        # Bootstrap has SQL root with no password in this disposable isolated server only.
        run("docker", "exec", "-d", db, "mariadbd", "--no-defaults", "--datadir=/var/lib/mysql", "--socket=/tmp/db.sock", "--pid-file=/tmp/db.pid", "--log-error=/tmp/db.log", "--bind-address=127.0.0.1", "--skip-name-resolve", "--innodb-buffer-pool-size=64M", "--innodb-log-file-size=16M")
        for _ in range(100):
            if sql(db, "SELECT 1;", check=False).returncode == 0:
                break
            time.sleep(.1)
        else:
            run("docker", "exec", db, "cat", "/tmp/db.log", check=False)
            raise RuntimeError("MariaDB readiness failed")
        mode = sql(db, "SELECT VERSION(), @@sql_mode;").stdout
        (evidence / "mariadb-version-mode.txt").write_bytes(mode)
        assert mode.startswith(b"10.11."), mode
        password = secrets.token_urlsafe(32)
        write(db, "/tmp/app.cnf", f"[client]\nprotocol=tcp\nhost=127.0.0.1\nuser={USER}\npassword={password}\n")
        lookalike = DATABASE.replace("_", "x", 1)
        sql(db, fixtures["Grants"]["CreateDatabase"] + f"\nCREATE USER '{USER}'@'127.0.0.1' IDENTIFIED BY '{password}';\n" + fixtures["Grants"]["Grant"] + f"\nCREATE DATABASE `{lookalike}`; CREATE DATABASE unrelated; CREATE TABLE `{lookalike}`.probe (n INT); CREATE TABLE unrelated.probe (n INT);")
        grants = sql(db, f"SHOW GRANTS FOR '{USER}'@'127.0.0.1';").stdout.decode()
        # SHOW GRANTS includes a password hash; retain only grant statements without IDENTIFIED.
        safe = "\n".join(line for line in grants.splitlines() if " IDENTIFIED " not in line)
        (evidence / "show-grants.txt").write_text(safe + "\n")
        assert "GRANT OPTION" not in grants and "FILE" not in grants and "SUPER" not in grants
        allowed = sql(db, f"CREATE TABLE `{DATABASE}`.probe (n INT); INSERT INTO `{DATABASE}`.probe VALUES (42); SELECT n FROM `{DATABASE}`.probe;", app=True)
        assert allowed.stdout.strip() == b"42"
        for target in (lookalike, "unrelated", "mysql"):
            refused = sql(db, f"SELECT * FROM `{target}`." + ("user" if target == "mysql" else "probe") + ";", app=True, check=False)
            assert refused.returncode != 0 and b"1142" in refused.stderr, refused.stderr
        sql(db, fixtures["Grants"]["DropDatabase"])
        for name in owned:
            inspect(name, "after")
        (evidence / "result.txt").write_text("PASS exact FPM syntax, self-UID scratch socket, MariaDB rendered CREATE/GRANT/DROP, permitted CRUD, denied lookalike/unrelated/mysql reads. VM boundary remains unpassed.\n")
        print("PASS", evidence)
    finally:
        for name in owned:
            inspect(name, "teardown")
            run("docker", "rm", "-f", name)
            assert run("docker", "inspect", name, check=False).returncode != 0
        print("Evidence:", evidence)


if __name__ == "__main__":
    main()
