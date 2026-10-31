#!/usr/bin/env python3
"""Real browser settings writes against built API/PostgreSQL, signed IdP and loopback GitLab."""
import argparse
import base64
import http.server
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import threading
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


def module(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / file)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


api = module("api_settings", "scripts/smoke-api.py")
forge = module("forge_settings", "scripts/smoke-forge.py")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--serve", action="store_true")
    parser.add_argument("--design", action="store_true")
    args = parser.parse_args()
    schema = "settings_smoke_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), forge.GitLabFixture)
    threading.Thread(target=provider.serve_forever, daemon=True).start()
    environment = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL", "GITMORA_UI_AUDIT", "GITMORA_BROWSER", "GITMORA_MOBILE_DEVICE", "GITMORA_UI_AUDIT_LABEL"}}
    gitlab = f"http://127.0.0.1:{provider.server_port}"
    environment.update(WORKGRAPH_DATABASE_URL=api.BASE_DSN + "&search_path=" + schema,
                       WORKGRAPH_ENCRYPTION_KEYS=json.dumps({"test": base64.b64encode(bytes([3]) * 32).decode()}),
                       WORKGRAPH_ENCRYPTION_ACTIVE_KEY="test", WORKGRAPH_GITLAB_ALLOWLIST=gitlab)
    process = fixture = None
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        public = f"http://127.0.0.1:{listener.getsockname()[1]}"
    try:
        setup = api.command(["bootstrap", "--project", "Исходный проект", "--repository", "Исходный репозиторий", "--gitlab-url", gitlab, "--gitlab-project-id", "42"], environment)
        assert setup.returncode == 0
        identity = json.loads(setup.stdout)
        issued = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "directory:read,directory:manage,platform:manage,integration:read,integration:manage,capture:read,capture:write"], environment)
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

        write("/v1/admin/identities", dict(request_id=str(uuid.uuid4()), id=str(uuid.uuid4()), user_id=identity["user_id"], subject="subject-reader"))
        reader = str(uuid.uuid4())
        write("/v1/admin/users", dict(request_id=str(uuid.uuid4()), id=reader, display_name="Читатель настроек"))
        write(f'/v1/projects/{identity["project_id"]}/members/{reader}', dict(request_id=str(uuid.uuid4()), expected_version=0, role="reader"), "PUT")
        issued = api.command(["issue-token", "--user-id", reader, "--scopes", "directory:read,integration:read,capture:read"], environment)
        assert issued.returncode == 0
        if args.serve:
            print(json.dumps({"url": public}), flush=True)
            signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
            signal.pause()
        else:
            payload = dict(url=public, issuer=issuer, oidc=True, admin=admin, admin_user=identity["user_id"], reader=reader, reader_token=json.loads(issued.stdout)["token"], project=identity["project_id"], repository=identity["repository_id"], gitlab=gitlab)
            write_key = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "directory:read,integration:manage"], environment)
            assert write_key.returncode == 0
            payload["write_only_token"] = json.loads(write_key.stdout)["token"]
            result = subprocess.run(["node", "scripts/smoke-design.mjs" if args.design else "scripts/smoke-settings.mjs"], input=json.dumps(payload), text=True, env=environment, cwd=ROOT, timeout=600 if args.design or os.environ.get("GITMORA_UI_AUDIT") == "1" else 180)
            assert result.returncode == 0, "Browser setup workflow failed"
    except KeyboardInterrupt:
        pass
    finally:
        provider.shutdown()
        provider.server_close()
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
