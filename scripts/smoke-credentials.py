#!/usr/bin/env python3
"""Built browser/API/PostgreSQL access lifecycle with a real signed, controllable local OIDC provider."""
import argparse
import importlib.util
import json
import os
import re
from pathlib import Path
import signal
import socket
import subprocess
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_credentials", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--serve", action="store_true")
    args = parser.parse_args()
    schema = "credentials_smoke_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL", "GITMORA_UI_AUDIT", "GITMORA_UI_AUDIT_LABEL"}}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = fixture = None
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        public = f"http://127.0.0.1:{listener.getsockname()[1]}"
    try:
        boot = api.command(["bootstrap", "--project", "Контроль доступа", "--repository", "Fixture", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert boot.returncode == 0
        identity = json.loads(boot.stdout)
        issued = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "directory:read,directory:manage,platform:manage,credentials:manage,capture:read,capture:write"], environment)
        assert issued.returncode == 0
        admin = json.loads(issued.stdout)["token"]
        fixture = subprocess.Popen([str(ROOT / "bin/verification/oidc-fixture"), "--callback", public + "/v1/auth/oidc/callback"], cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        issuer = json.loads(fixture.stdout.readline())["issuer"]
        environment.update(WORKGRAPH_OIDC_ISSUER=issuer, WORKGRAPH_OIDC_CLIENT_ID="workgraph", WORKGRAPH_OIDC_CLIENT_SECRET="synthetic-oidc-client-secret", WORKGRAPH_PUBLIC_URL=public)
        process, _ = api.start_api(environment, public.removeprefix("http://"))

        def write(path, body, method="POST"):
            request = urllib.request.Request(public + path, data=json.dumps(body).encode(), method=method, headers={"Authorization": "Bearer " + admin, "Content-Type": "application/json"})
            with urllib.request.urlopen(request, timeout=15) as response:
                return json.load(response)

        reader = str(uuid.uuid4())
        write("/v1/admin/users", dict(request_id=str(uuid.uuid4()), id=reader, display_name="Инженер доступа"))
        write(f'/v1/projects/{identity["project_id"]}/members/{reader}', dict(request_id=str(uuid.uuid4()), expected_version=0, role="reader"), "PUT")
        if args.serve:
            # Visual mode starts with an explicit local subject binding; automated mode tests its creation.
            write("/v1/admin/identities", dict(request_id=str(uuid.uuid4()), id=str(uuid.uuid4()), user_id=identity["user_id"], subject="subject-reader"))
            print(json.dumps({"url": public}), flush=True)
            signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
            signal.pause()
        else:
            payload = dict(url=public, issuer=issuer, admin=admin, user=identity["user_id"], reader=reader, project=identity["project_id"])
            checked = subprocess.run(["node", "scripts/smoke-credentials.mjs"], input=json.dumps(payload), text=True, cwd=ROOT, env=environment, timeout=600 if os.environ.get("GITMORA_UI_AUDIT") == "1" else 180, capture_output=True)
            if checked.returncode:
                diagnostic = re.sub(r"\b(?:wg_|wcd_|wcp_)[A-Za-z0-9_-]{43}\b", "[redacted]", checked.stdout + checked.stderr)
                target = ROOT / "build/credentials-browser-debug.log"
                target.write_text(diagnostic); target.chmod(0o600)
            assert checked.returncode == 0, "Actual access browser workflow failed; redacted synthetic diagnostics retained"
            print(checked.stdout.strip())
    except KeyboardInterrupt:
        pass
    finally:
        for child in [process, fixture]:
            if child is not None:
                if child.poll() is None:
                    child.terminate()
                    try:
                        child.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        child.wait(timeout=5)
                if child.stderr:
                    child.stderr.close()
                if child.stdout:
                    child.stdout.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
