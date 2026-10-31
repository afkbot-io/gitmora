"""Terminal-level operator account creation against the isolated actual provider/database."""
import json
import os
import pty
import select
import subprocess
import time
from pathlib import Path


def create_user(root: Path, directory: Path, environment: dict, username: str, password: str):
    master, slave = pty.openpty()
    process = subprocess.Popen([str(root / 'bin/gitmorad'), 'users', 'create', '--directory', str(directory),
                                '--username', username, '--name', 'CLI Engineer'], cwd=root, env=environment,
                               stdin=slave, stderr=slave, stdout=subprocess.PIPE)
    os.close(slave)
    transcript = b''
    sent = 0
    deadline = time.monotonic() + 40
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    data = os.read(master, 8192)
                except OSError:
                    break
                if not data:
                    break
                transcript += data
                if sent == 0 and b'Password (at least' in transcript:
                    os.write(master, password.encode() + b'\n'); sent = 1
                if sent == 1 and b'Repeat password:' in transcript:
                    os.write(master, password.encode() + b'\n'); sent = 2
            if process.poll() is not None:
                break
        stdout, _ = process.communicate(timeout=5)
        if process.returncode != 0:
            diagnostic = transcript.decode(errors='replace').replace(password, '[redacted]') + '\n' + stdout.decode(errors='replace').replace(password, '[redacted]')
            target = root / 'build/registration-operator-debug.txt'
            target.write_text(diagnostic); target.chmod(0o600)
        assert process.returncode == 0, 'Operator command failed with isolated provider; synthetic redacted diagnostic retained' 
        assert sent == 2 and password.encode() not in transcript and password.encode() not in stdout, 'Password echo/leak'
        value = json.loads(stdout)
        assert set(value) == {'status', 'user_id', 'subject', 'username'} and value['status'] == 'created'
        return value
    finally:
        if process.poll() is None:
            process.kill(); process.wait(timeout=5)
        os.close(master)
