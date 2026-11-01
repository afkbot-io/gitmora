#!/usr/bin/env python3
"""Build a committed private archive and exercise its account theme as the nonroot provider."""
from __future__ import annotations

import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import uuid

ROOT = Path(__file__).resolve().parent.parent


def run(args, **kwargs):
    result = subprocess.run(args, cwd=ROOT, capture_output=True, timeout=900, **kwargs)
    if result.returncode:
        log = ROOT / "build/identity-permissions-debug.log"
        log.write_bytes((result.stdout + result.stderr)[-16384:])
        log.chmod(0o600)
        raise RuntimeError("Private-source identity gate failed; private diagnostics retained")
    return result.stdout


def main():
    image = "gitmora-identity-permissions:" + uuid.uuid4().hex
    built = False
    try:
        with tempfile.TemporaryDirectory(prefix="gitmora-private-theme-") as name:
            source = Path(name)
            raw = run(["git", "archive", "--format=tar", "HEAD"])
            previous = os.umask(0o077)
            try:
                with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
                    archive.extractall(source, filter="data")
            finally:
                os.umask(previous)
            login = source / "deploy/identity/themes/gitmora/login"
            assert login.stat().st_mode & 0o777 == 0o700, "Fixture must reproduce private source directories"
            run(["docker", "build", "--file", str(source / "deploy/identity/Dockerfile"), "--tag", image, str(source)])
            built = True
        run(["docker", "run", "--rm", "--network", "none", "--user", "1000:0", "--read-only",
             "--cap-drop", "ALL", "--entrypoint", "/bin/sh", image, "-ec",
             "test -r /opt/keycloak/themes/gitmora/login/register.ftl; "
             "test -r /opt/keycloak/themes/gitmora/login/template.ftl; "
             "test -r /opt/keycloak/themes/gitmora/login/resources/img/favicon.svg"])
        environment = dict(os.environ)
        environment.update(GITMORA_KEYCLOAK_IMAGE=image, GITMORA_KEYCLOAK_REGISTRATION="1")
        run(["python3", "scripts/smoke-keycloak.py"], env=environment)
        print("Private umask-077 source image: UID 1000 reads templates/assets; actual registration, login and account isolation passed.")
    finally:
        if built:
            subprocess.run(["docker", "image", "rm", image], cwd=ROOT, capture_output=True, timeout=60)


if __name__ == "__main__":
    main()
