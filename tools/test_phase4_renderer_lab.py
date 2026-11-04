import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import phase4_renderer_lab as lab


class RendererLabTest(unittest.TestCase):
    def test_optimized_python_rejected_before_effects(self):
        with tempfile.TemporaryDirectory() as tmp:
            env = dict(os.environ, TMPDIR=tmp)
            script = "import sys; from unittest.mock import patch; sys.path.insert(0, sys.argv[1]); import phase4_renderer_lab as lab; " \
                     "exec(\"with patch.object(lab.subprocess, 'run', side_effect=RuntimeError('external command invoked')):\\n lab.main()\")"
            result = subprocess.run([sys.executable, "-O", "-c", script, str(Path(lab.__file__).parent)], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b"optimization", result.stderr.lower())
            self.assertEqual(list(Path(tmp).iterdir()), [])

    def run_lab(self, grant, teardown_failure=None):
        calls = []
        names = []
        inspect_counts = {}
        existing = set()
        tokens = {}
        ids = {}
        host = {"ReadonlyRootfs": True, "Privileged": False, "NetworkMode": "none", "PortBindings": {}, "Binds": [], "Devices": [], "CapAdd": [], "CapDrop": ["ALL"], "SecurityOpt": ["no-new-privileges:true"], "Memory": 805306368, "MemorySwap": 805306368, "NanoCpus": 1000000000, "PidsLimit": 96, "Tmpfs": {"/tmp": "", "/var/lib/mysql": ""}}

        def fake_run(args, input=None, **kwargs):
            calls.append(args)
            out, err, code = b"", b"", 0
            if args[:3] == ("git", "rev-parse", "HEAD"):
                out = b"abc\n"
            elif args[:2] == ("go", "run"):
                out = json.dumps({"Pool": "pool", "Grants": {"CreateDatabase": "create", "Grant": "grant", "DropDatabase": "drop"}}).encode()
            elif args[:2] == ("docker", "create"):
                names.append(args[3])
                tokens[args[3]] = args[args.index('--label') + 1].split('=', 1)[1]
                ids[args[3]] = 'id' + str(len(names))
                out = (ids[args[3]] + '\n').encode()
                existing.add(args[3])
                if teardown_failure == "foreign":
                    tokens[args[3]] = "foreign"
                if teardown_failure in ("create-timeout", "delayed-create"):
                    if teardown_failure == "delayed-create":
                        existing.discard(args[3])
                    raise subprocess.TimeoutExpired(args, 180)
                if teardown_failure == "foreign":
                    code, err = 1, b"name already in use"
            elif args[:2] == ("docker", "inspect"):
                target = args[2]
                name = next((n for n, cid in ids.items() if cid == target), target)
                inspect_counts[name] = inspect_counts.get(name, 0) + 1
                if teardown_failure == "inspect" and name == names[0] and inspect_counts[name] == 3:
                    code = 1
                elif teardown_failure == "verify-daemon" and name == names[0] and inspect_counts[name] == 4:
                    code, err = 1, b"Error: No such object: " + target.encode()
                elif name not in existing:
                    code, err = 1, b"Error: No such object: " + target.encode()
                else:
                    uid = "33:33" if name == names[0] else "999:999"
                    out = json.dumps([{"Id": ids[name], "Config": {"User": uid, "Labels": {"novapanel.lab": tokens[name]}}, "HostConfig": host, "Mounts": []}]).encode()
            elif args[:2] == ("docker", "ps"):
                target = next(a[3:] for a in args if a.startswith("id="))
                name = next((n for n, cid in ids.items() if cid == target), target)
                if teardown_failure == "verify-daemon":
                    code, err = 1, b"Error: No such object: " + target.encode()
                elif name in existing:
                    out = (target + "\n").encode()
            elif args[:3] == ("docker", "rm", "-f"):
                name = next((n for n, cid in ids.items() if cid == args[3]), args[3])
                if teardown_failure == "remove" and name == names[0]:
                    code = 1
                else:
                    existing.discard(name)
            elif args[:2] == ("docker", "exec") and any('stat -c' in arg for arg in args):
                out = b"33:33 660 socket\n" if teardown_failure != "bad-socket" else b"33:33 666 socket\n"
            elif input and b"SECRET_PASSWORD" in input and teardown_failure == "write-secret":
                code, err = 1, input
            elif input and b"CREATE USER" in input and teardown_failure == "sql-secret":
                code, err = 1, input
            elif input and input.startswith(b"SHOW GRANTS"):
                out = ("GRANT USAGE ON *.* TO 'u'@'127.0.0.1' IDENTIFIED BY PASSWORD 'SECRET_HASH'\n" + grant + "\n").encode()
            elif input and input.startswith(b"SELECT VERSION"):
                out = b"10.11.19\tmode\n"
            elif input and input.startswith(b"CREATE TABLE"):
                out = b"42\n"
            elif input and input.startswith(b"SELECT *"):
                code, err = 1, b"1142 denied"
            return subprocess.CompletedProcess(args, code, out, err)

        stderr = io.StringIO()
        with tempfile.TemporaryDirectory() as tmp, patch.dict(os.environ, {"TMPDIR": tmp}), patch.object(lab.subprocess, "run", side_effect=fake_run), patch.object(lab.secrets, "token_urlsafe", return_value="SECRET_PASSWORD"), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(stderr):
            error = None
            try:
                lab.main()
            except Exception as exc:
                error = exc
            evidence = next(Path(tmp).iterdir())
            files = b"\n".join(p.read_bytes() for p in evidence.iterdir() if p.is_file()) + stderr.getvalue().encode()
        return calls, names, error, files

    def test_requires_exact_escaped_database_grant_without_leaking_hash(self):
        wrong = "GRANT SELECT ON `d_01arz3ndektsv4rrffq69g5fav`.* TO 'u_01arz3ndektsv4rrffq69g5faw'@'127.0.0.1'"
        calls, names, error, files = self.run_lab(wrong)
        self.assertIsInstance(error, AssertionError)
        self.assertNotIn(b"SECRET_HASH", files)

    def test_accepts_escaped_database_grant_without_leaking_hash(self):
        grant = "GRANT SELECT ON `d\\_01arz3ndektsv4rrffq69g5fav`.* TO 'u_01arz3ndektsv4rrffq69g5faw'@'127.0.0.1'"
        calls, names, error, files = self.run_lab(grant)
        self.assertIsNone(error)
        self.assertNotIn(b"SECRET_HASH", files)

    def test_uncertain_create_is_cleaned_but_foreign_name_is_not_removed(self):
        grant = "GRANT SELECT ON `d\\_01arz3ndektsv4rrffq69g5fav`.* TO 'u_01arz3ndektsv4rrffq69g5faw'@'127.0.0.1'"
        for failure in ("create-timeout", "foreign"):
            with self.subTest(failure=failure):
                calls, names, error, _ = self.run_lab(grant, failure)
                self.assertIsNotNone(error)
                removals = [c[3] for c in calls if c[:3] == ("docker", "rm", "-f")]
                self.assertEqual(removals, ['id1'] if failure == "create-timeout" else [])

    def test_create_timeout_with_delayed_container_reports_unresolved_ownership(self):
        _, names, error, output = self.run_lab("", "delayed-create")
        self.assertIsNotNone(error)
        self.assertIn(b"unresolved", output.lower())
        self.assertIn(names[0].encode(), output)

    def test_sql_error_does_not_record_password(self):
        _, _, error, output = self.run_lab("", "sql-secret")
        self.assertIsNotNone(error)
        self.assertNotIn(b"SECRET_PASSWORD", output)
        self.assertNotIn("SECRET_PASSWORD", str(error))

    def test_socket_mode_mismatch_fails_acceptance(self):
        _, _, error, _ = self.run_lab("GRANT SELECT ON `d\\_01arz3ndektsv4rrffq69g5fav`.* TO 'u_01arz3ndektsv4rrffq69g5faw'@'127.0.0.1'", "bad-socket")
        self.assertIsNotNone(error)

    def test_app_config_error_does_not_record_password(self):
        _, _, error, output = self.run_lab("", "write-secret")
        self.assertIsNotNone(error)
        self.assertNotIn(b"SECRET_PASSWORD", output)
        self.assertNotIn("SECRET_PASSWORD", str(error))

    def test_cleanup_error_identifies_target_with_primary_error(self):
        _, _, error, output = self.run_lab("wrong grant", "remove")
        self.assertIsInstance(error, AssertionError)
        self.assertIn(b"id1", output)
        self.assertIn(b"remove", output.lower())

    def test_daemon_error_does_not_prove_removal(self):
        grant = "GRANT SELECT ON `d\\_01arz3ndektsv4rrffq69g5fav`.* TO 'u_01arz3ndektsv4rrffq69g5faw'@'127.0.0.1'"
        _, _, error, _ = self.run_lab(grant, "verify-daemon")
        self.assertIsNotNone(error)

    def test_cleanup_attempts_all_containers_despite_failures(self):
        grant = "GRANT SELECT ON `d\\_01arz3ndektsv4rrffq69g5fav`.* TO 'u_01arz3ndektsv4rrffq69g5faw'@'127.0.0.1'"
        for failure in ("inspect", "remove"):
            with self.subTest(failure=failure):
                calls, names, error, files = self.run_lab(grant, failure)
                self.assertEqual(len(names), 2)
                self.assertIsNotNone(error)
                self.assertEqual([c[3] for c in calls if c[:3] == ("docker", "rm", "-f")], ['id1', 'id2'])
                self.assertNotIn(b"SECRET_HASH", files)


if __name__ == "__main__":
    unittest.main()
