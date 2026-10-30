#!/usr/bin/env python3
"""Compile an opt-in test and execute only in an isolated nonroot nginx container."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid

ROOT = Path(__file__).resolve().parents[1]
IMAGE = "nginx@sha256:1d13701a5f9f3fb01aaa88cef2344d65b6b5bf6b7d9fa4cf0dca557a8d7702ba"
EVIDENCE = Path(tempfile.mkdtemp(prefix="phase3-realnginx-", dir=os.environ["TMPDIR"]))
NAME = "novapanel-realnginx-" + uuid.uuid4().hex[:12]


def run(*args, check=True, **kwargs):
    result = subprocess.run(args, cwd=ROOT, capture_output=True, **kwargs)
    with (EVIDENCE / "commands.log").open("ab") as log:
        log.write(("$ " + " ".join(map(str, args)) + "\n").encode())
        log.write(result.stdout + result.stderr)
        log.write(f"\nexit={result.returncode}\n".encode())
    if check and result.returncode:
        raise RuntimeError(f"command failed: {args}; evidence: {EVIDENCE}")
    return result


def inspect(stage):
    result = run("docker", "inspect", NAME)
    (EVIDENCE / (stage + ".json")).write_bytes(result.stdout)
    data = json.loads(result.stdout)[0]
    host = data["HostConfig"]
    assert not data["Mounts"] and not host["Privileged"]
    assert host["NetworkMode"] == "none" and not host["PortBindings"]
    assert not host["CapAdd"] and host["CapDrop"] == ["ALL"]
    assert "no-new-privileges:true" in host["SecurityOpt"]
    assert data["Config"]["User"] == "65534:65534"
    assert host["Memory"] == 536870912 and host["PidsLimit"] == 64
    assert host["NanoCpus"] == 1000000000
    assert data["Config"]["Labels"]["novapanel.lab"] == NAME


created = False
try:
    sha = run("git", "rev-parse", "HEAD").stdout.decode().strip()
    run("git", "diff", "--exit-code", "HEAD")
    (EVIDENCE / "source-sha.txt").write_text(sha + "\n")
    archive = run("git", "archive", "--format=tar", sha).stdout
    (EVIDENCE / "source.tar").write_bytes(archive)
    env = dict(os.environ, CGO_ENABLED="0")
    run("go", "test", "-c", "-o", str(EVIDENCE / "acceptance.test"), "./tests/integration", env=env)
    run("docker", "create", "--name", NAME, "--label", "novapanel.lab=" + NAME,
        "--user", "65534:65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
        "--memory", "512m", "--memory-swap", "512m", "--cpus", "1", "--pids-limit", "64",
        "--network", "none", "--entrypoint", "/bin/sh", IMAGE, "-c", "sleep 300")
    created = True
    inspect("before")
    run("docker", "start", NAME)
    run("docker", "cp", str(EVIDENCE / "source.tar"), NAME + ":/tmp/source.tar")
    run("docker", "cp", str(EVIDENCE / "acceptance.test"), NAME + ":/tmp/acceptance.test")
    digest = hashlib.sha256((EVIDENCE / "acceptance.test").read_bytes()).hexdigest()
    actual = run("docker", "exec", NAME, "sha256sum", "/tmp/acceptance.test").stdout.decode().split()[0]
    assert actual == digest
    (EVIDENCE / "binary-sha256.txt").write_text(digest + "\n")
    result = run("docker", "exec", "-e", "NOVAPANEL_REAL_NGINX=1", NAME,
                 "/tmp/acceptance.test", "-test.run", "^TestRealNginx", "-test.v", "-test.count=3", check=False)
    (EVIDENCE / "acceptance.log").write_bytes(result.stdout + result.stderr)
    inspect("after")
    assert result.returncode == 0, f"acceptance failed: {EVIDENCE}"
    print(f"PASS: real nginx acceptance repeated three times; evidence={EVIDENCE}; container={NAME}")
finally:
    if created:
        inspect("teardown")
        run("docker", "rm", "-f", NAME)
        absent = run("docker", "inspect", NAME, check=False)
        assert absent.returncode != 0
        (EVIDENCE / "teardown.txt").write_text("Owned container removed; exact-name inspection returned absent.\n")
    print(f"Evidence: {EVIDENCE}")
