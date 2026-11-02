"""Exercise CI command sequencing with owned stand-ins, without touching an OS keyring."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class VerificationRunner(unittest.TestCase):
    def run_fixture(self, *, omit_report=False):
        with tempfile.TemporaryDirectory(prefix="gitmora-ci-runner-test-") as temporary:
            root = Path(temporary)
            scripts = root / ".github/scripts"
            scripts.mkdir(parents=True)
            shutil.copyfile(ROOT / ".github/scripts/verify-linux.sh", scripts / "verify-linux.sh")
            commands = root / "commands"
            commands.mkdir()
            programs = {
                "uname": "printf 'Linux\\n'\n",
                "dbus-run-session": 'shift; exec "$@"\n',
                "dbus-send": "printf 'boolean true\\n'\n",
                "secret-tool": "exit 0\n",
                "gnome-keyring-daemon": "cat >/dev/null\nexec sleep 60\n",
                "make": (
                    'printf "%s\\n" "$1" >> "$GITMORA_TEST_CALL_LOG"\n'
                    # A child must not consume the next command in the CI program.
                    'cat >/dev/null\n'
                    'if [[ "$1" == smoke-load && "$GITMORA_TEST_OMIT_REPORT" != 1 ]]; then\n'
                    "  mkdir -p build/load\n"
                    "  printf '{\"result\":\"passed\"}\\n' > build/load/report.json\n"
                    "fi\n"
                ),
            }
            for name, program in programs.items():
                path = commands / name
                path.write_text("#!/usr/bin/env bash\nset -euo pipefail\n" + program)
                path.chmod(0o700)
            calls = root / "calls"
            environment = {
                **os.environ,
                "PATH": str(commands) + os.pathsep + os.environ["PATH"],
                "GITMORA_TEST_CALL_LOG": str(calls),
                "GITMORA_TEST_OMIT_REPORT": "1" if omit_report else "0",
            }
            completed = subprocess.run(
                ["bash", str(scripts / "verify-linux.sh")],
                env=environment, capture_output=True, timeout=15,
            )
            return completed.returncode, calls.read_text().splitlines(), (root / "build/load/report.json").exists()

    def test_stdin_consuming_release_gate_does_not_skip_load(self):
        code, calls, report = self.run_fixture()
        self.assertEqual(code, 0)
        self.assertEqual(calls, ["verify-release", "smoke-load"])
        self.assertTrue(report)

    def test_missing_load_report_fails_the_runner(self):
        code, calls, report = self.run_fixture(omit_report=True)
        self.assertEqual(calls, ["verify-release", "smoke-load"])
        self.assertNotEqual(code, 0)
        self.assertFalse(report)
