#!/usr/bin/env python3
"""Actual browser + embedded API + signed local IdP + isolated PostgreSQL; synthetic source only."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def uid():
    return str(uuid.uuid4())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--serve", action="store_true", help="Keep synthetic fixtures alive for CLI visual inspection")
    parser.add_argument("--history", action="store_true", help="Exercise accepted-source navigation and session revisions")
    args = parser.parse_args()
    schema = "web_smoke_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {k: v for k, v in os.environ.items() if k in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = fixture = None
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    public = f"http://127.0.0.1:{port}"
    try:
        result = api.command(["bootstrap", "--project", "Platform", "--repository", "Platform", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert result.returncode == 0, "Synthetic setup failed"
        identity = json.loads(result.stdout)
        issued = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "directory:read,directory:manage,platform:manage,credentials:manage,capture:read,capture:write,integration:read"], environment)
        assert issued.returncode == 0
        admin = json.loads(issued.stdout)["token"]
        fixture = subprocess.Popen([str(ROOT / "bin/verification/oidc-fixture"), "--callback", public + "/v1/auth/oidc/callback"], cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        issuer = json.loads(fixture.stdout.readline())["issuer"]
        environment.update(WORKGRAPH_OIDC_ISSUER=issuer, WORKGRAPH_OIDC_CLIENT_ID="workgraph", WORKGRAPH_OIDC_CLIENT_SECRET="synthetic-oidc-client-secret", WORKGRAPH_PUBLIC_URL=public)
        process, _ = api.start_api(environment, f"127.0.0.1:{port}")

        def call(path, body=None, method=None):
            request = urllib.request.Request(public + path, data=json.dumps(body).encode() if body is not None else None, method=method, headers={"Authorization": "Bearer " + admin, "Content-Type": "application/json"})
            try:
                response = urllib.request.urlopen(request, timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                return response.status, json.load(response)

        user, session = uid(), uid()
        if args.history:
            session = "11111111-1111-4111-8111-111111111111"
        assert call("/v1/admin/users", dict(request_id=uid(), id=user, display_name="Инженер команды"))[0] == 201
        assert call(f'/v1/projects/{identity["project_id"]}/members/{user}', dict(request_id=uid(), expected_version=0, role="reader"), "PUT")[0] == 200
        binding = uid()
        status, receipt = call("/v1/admin/identities", dict(request_id=uid(), id=binding, user_id=user, subject="subject-reader"))
        assert status == 201
        other = uid()
        assert call("/v1/projects", dict(request_id=uid(), id=other, name="Закрытая лаборатория"))[0] == 201
        assert call(f"/v1/projects/{other}/repositories",dict(request_id=uid(),id=uid(),provider="",gitlab_url="https://gitlab.internal",gitlab_project_id=43,name="Empty repository"))[0]==201
        if args.history:
            assert call(f'/v1/projects/{other}/members/{user}', dict(request_id=uid(), expected_version=0, role="reader"), "PUT")[0] == 200
        # Put non-member metadata first to make default-selection coverage deterministic.
        metadata, custodian = "00000000-0000-4000-8000-" + uuid.uuid4().hex[:12], uid()
        assert call("/v1/admin/users", dict(request_id=uid(), id=custodian, display_name="Хранитель каталога"))[0] == 201
        assert call("/v1/projects", dict(request_id=uid(), id=metadata, name="Каталог без доступа"))[0] == 201
        assert call(f"/v1/projects/{metadata}/members/{custodian}", dict(request_id=uid(), expected_version=0, role="admin"), "PUT")[0] == 200
        own_membership = next(item for item in call(f"/v1/projects/{metadata}/members")[1]["items"] if item["user_id"] == identity["user_id"])
        assert call(f'/v1/projects/{metadata}/members/{identity["user_id"]}', dict(request_id=uid(), expected_version=own_membership["version"]), "DELETE")[0] == 200
        text = "refreshToken: проверяем обновление доступа 🧪. <img src=x onerror=window.untrustedExecuted=true>\n" * 40
        capture = dict(schema_version=1, repository_id=identity["repository_id"], session_id=session, revision=1,
                       started_at="2026-10-30T10:00:00Z", agent=dict(name="OpenCode", model="synthetic-local"),
                       messages=[dict(role="assistant", text="obsoleteWord refreshToken: старая версия")], files=["src/auth/refresh-token.ts"])
        assert call("/v1/captures", capture)[0] == 201
        capture.update(revision=2, messages=[dict(role="assistant", text=text)])
        if args.history:
            capture["messages"] += [dict(role="tool", tool_name="bash", text=f"QuotedToolMessage {i}") for i in range(25)]
        assert call("/v1/captures", capture)[0] == 201
        if args.history:
            digest = call(f'/v1/repositories/{identity["repository_id"]}/sessions/{session}')[1]["digest"]
            for index in range(1, 22):
                extra = {**capture, "revision": 1, "session_id": f"eeeeeeee-eeee-4eee-8eee-{index:012x}", "messages": [dict(role="assistant", text=f"Archive marker {index}")]}
                if index == 1:
                    extra["continuation"] = dict(root_session_id=session, previous_session_id=session, previous_revision=2, previous_digest=digest, segment=2, reason="window")
                else:
                    extra["agent"] = dict(name=f"Archive window {index}", model="synthetic-local")
                assert call("/v1/captures", extra)[0] == 201
        for _ in range(30):
            indexed = api.command(["worker", "--once"], environment)
            assert indexed.returncode == 0, "Synthetic index worker failed"
            if not json.loads(indexed.stdout)["worked"]:
                break
        assert call("/v1/context/search", dict(project_id=identity["project_id"], text="refreshToken"))[1]["items"]
        if args.serve:
            print(json.dumps({"url": public}), flush=True)
            signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
            signal.pause()
        else:
            payload = dict(url=public, issuer=issuer, admin=admin, project=identity["project_id"], other_project=other, metadata_project=metadata, user=user, binding=binding, binding_version=receipt["version"], repository=identity["repository_id"], session=session)
            if args.history:
                issued = api.command(["issue-token", "--user-id", user, "--scopes", "directory:read,capture:read"], environment)
                assert issued.returncode == 0
                payload["capture_only"] = json.loads(issued.stdout)["token"]
            script = "scripts/smoke-history.mjs" if args.history else "scripts/smoke-web.mjs"
            checked = subprocess.run(["node", script], input=json.dumps(payload), text=True, cwd=ROOT, timeout=600 if os.environ.get("GITMORA_UI_AUDIT") == "1" else 180)
            assert checked.returncode == 0, "Actual browser workflow failed"
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
