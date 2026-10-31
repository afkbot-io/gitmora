#!/usr/bin/env python3
"""Signed browser consent + isolated PostgreSQL, optionally actual CLI/OS store/Git hooks."""
import importlib.util
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import secrets
import socket
import subprocess
import threading
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_cli_auth", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def main():
    schema = "cli_auth_smoke_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    allowed = {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL", "GITMORA_UI_AUDIT", "GITMORA_UI_AUDIT_LABEL", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "DISPLAY"}
    environment = {key: value for key, value in os.environ.items() if key in allowed}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = fixture = control = None
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        public = f"http://127.0.0.1:{listener.getsockname()[1]}"
    try:
        boot = api.command(["bootstrap", "--project", "CLI verification", "--repository", "Native delivery", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert boot.returncode == 0, "Synthetic bootstrap failed"
        identity = json.loads(boot.stdout)
        issued = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "directory:read,platform:manage,credentials:manage,capture:read,capture:write"], environment)
        assert issued.returncode == 0, "Synthetic operator issuance failed"
        admin = json.loads(issued.stdout)["token"]
        fixture = subprocess.Popen([str(ROOT / "bin/verification/oidc-fixture"), "--callback", public + "/v1/auth/oidc/callback"], cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        issuer = json.loads(fixture.stdout.readline())["issuer"]
        environment.update(WORKGRAPH_OIDC_ISSUER=issuer, WORKGRAPH_OIDC_CLIENT_ID="workgraph", WORKGRAPH_OIDC_CLIENT_SECRET="synthetic-oidc-client-secret", WORKGRAPH_PUBLIC_URL=public)
        process, _ = api.start_api(environment, public.removeprefix("http://"))
        binding = str(uuid.uuid4())
        request = urllib.request.Request(public + "/v1/admin/identities", data=json.dumps(dict(request_id=str(uuid.uuid4()), id=binding, user_id=identity["user_id"], subject="subject-reader")).encode(), headers={"Authorization": "Bearer " + admin, "Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=15) as response:
            assert response.status == 201
        verification_nonce = secrets.token_urlsafe(32)

        class Control(BaseHTTPRequestHandler):
            """Private test coordinator only; no control endpoint is added to Workgraph."""
            def log_message(self, *_):
                pass

            def do_POST(self):
                nonlocal process
                if self.path != "/restart" or not secrets.compare_digest(self.headers.get("X-Verification-Nonce", ""), verification_nonce) or int(self.headers.get("Content-Length", "0")) > 256:
                    self.send_response(403)
                    self.end_headers()
                    return
                self.rfile.read(int(self.headers.get("Content-Length", "0")))
                process.terminate()
                process.wait(timeout=10)
                if process.stderr:
                    process.stderr.close()
                process, _ = api.start_api(environment, public.removeprefix("http://"))
                self.send_response(204)
                self.end_headers()

        control = ThreadingHTTPServer(("127.0.0.1", 0), Control)
        threading.Thread(target=control.serve_forever, daemon=True).start()
        payload = dict(url=public, issuer=issuer, admin=admin, user=identity["user_id"], repository=identity["repository_id"], binding=binding, native=os.environ.get("WORKGRAPH_NATIVE_AUTH") == "1", control_url=f"http://127.0.0.1:{control.server_port}", control_nonce=verification_nonce)
        child_environment = {key: value for key, value in environment.items() if key in allowed}
        checked = subprocess.run(["node", "scripts/smoke-cli-auth.mjs"], input=json.dumps(payload), text=True, cwd=ROOT, env=child_environment, timeout=600 if os.environ.get("GITMORA_UI_AUDIT") == "1" else 240)
        assert checked.returncode == 0, "Actual CLI consent workflow failed"
    finally:
        if control is not None:
            control.shutdown()
            control.server_close()
        for child in [process, fixture]:
            if child is not None:
                if child.poll() is None:
                    child.terminate()
                    try:
                        child.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        child.wait(timeout=5)
                for pipe in [child.stdout, child.stderr]:
                    if pipe:
                        pipe.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
