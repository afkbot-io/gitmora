#!/usr/bin/env python3
"""Built Workgraph processes + real Tasktopia source/HTTP MCP + isolated PostgreSQL schemas."""
from __future__ import annotations

import base64
import datetime
import importlib.util
import http.server
import json
import os
from pathlib import Path
import select
import shutil
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request
import urllib.parse
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
assert spec and spec.loader
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)
forge_spec = importlib.util.spec_from_file_location("forge_smoke", ROOT / "scripts/smoke-forge.py")
assert forge_spec and forge_spec.loader
forge = importlib.util.module_from_spec(forge_spec)
forge_spec.loader.exec_module(forge)


class WorkGitLabFixture(forge.GitLabFixture):
    """Read-model fixture with actual provider reconciliation across an immutable head/CI change."""
    changed = False

    def reply(self, value):
        raw = json.dumps(value).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        if self.path == "/__test/head":
            type(self).changed = True
            self.reply({"changed": True})
        else:
            self.send_error(404)

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        if self.headers.get("PRIVATE-TOKEN") != "synthetic-gitlab-token":
            self.send_error(401)
            return
        head = ("f" if self.changed else "b") * 40
        updated = datetime.datetime.now(datetime.timezone.utc).isoformat()
        if parsed.path == "/api/v4/projects/42/merge_requests/7":
            self.reply(dict(id=107, iid=7, project_id=42, source_project_id=42, title="Observed work MR", state="opened", sha=head,
                            updated_at=updated, diff_refs=dict(head_sha=head, base_sha="a" * 40, start_sha="e" * 40),
                            web_url=f"http://127.0.0.1:{self.server.server_port}/team/project/-/merge_requests/7"))
        elif parsed.path == "/api/v4/projects/42/merge_requests/7/commits":
            self.reply([dict(id="a" * 40), dict(id=head)])
        elif parsed.path == "/api/v4/projects/42/pipelines":
            if urllib.parse.parse_qs(parsed.query).get("source") == ["parent_pipeline"]:
                self.reply([])
            else:
                values = [dict(id=31, project_id=42, sha="b" * 40, status="success", ref="main", source="push", updated_at=updated)]
                if self.changed:
                    values.append(dict(id=32, project_id=42, sha=head, status="failed", ref="main", source="push", updated_at=updated))
                self.reply(values)
        else:
            super().do_GET()


def main() -> None:
    source = Path(os.environ["TASKTOPIA_SOURCE"]).resolve()
    node = Path(os.environ.get("TASKTOPIA_NODE", shutil.which("node") or ""))
    assert source.is_dir() and (source / "src/server/mcp.ts").is_file(), "Tasktopia source is required"
    version = subprocess.check_output([str(node), "-p", "process.versions.node"], text=True).strip()
    assert int(version.split(".")[0]) >= 24, "Tasktopia requires Node 24+"
    sandbox = ROOT / "build/tasktopia-contract"
    sandbox.mkdir(parents=True, exist_ok=True)
    # Keep vendored npm Go examples outside the platform's ./... package traversal.
    (sandbox / "go.mod").write_text("module workgraph.local/verification/tasktopia-fixture\n\ngo 1.27.3\n")
    shutil.copytree(source / "src", sandbox / "src", dirs_exist_ok=True)
    shutil.copytree(source / "migrations", sandbox / "migrations", dirs_exist_ok=True)
    manifests = sandbox / "assets/pixel-city-pack"
    manifests.mkdir(parents=True, exist_ok=True)
    for name in ("manifest.json", "micro-ambient-manifest.json"):
        shutil.copy2(source / "assets/pixel-city-pack" / name, manifests / name)
    for name in ("package.json", "package-lock.json"):
        shutil.copy2(source / name, sandbox / name)
    shutil.copy2(ROOT / "scripts/fixtures/tasktopia-server.ts", sandbox / "fixture.ts")
    # No .env, user uploads, real credentials, or existing database are copied.
    assert not (sandbox / ".env").exists(), "Disposable harness must not contain .env"
    clean = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL", "GITMORA_UI_AUDIT", "GITMORA_UI_AUDIT_LABEL"}}
    clean["PATH"] = str(node.parent) + os.pathsep + clean.get("PATH", "")
    subprocess.run(["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=sandbox, env=clean, check=True, stdout=subprocess.DEVNULL, timeout=600 if os.environ.get("GITMORA_UI_AUDIT") == "1" else 180)
    with socket.socket() as port:
        port.bind(("127.0.0.1", 0))
        number = port.getsockname()[1]
    origin = f"http://127.0.0.1:{number}"
    task_schema = "tasktopia_contract_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{task_schema}"')
    task_env = {**clean, "NODE_ENV": "test", "APP_ORIGIN": origin, "TEST_DATABASE_URL": api.BASE_DSN, "TASKTOPIA_TEST_SCHEMA": task_schema, "DATABASE_URL": api.BASE_DSN, "REDIS_URL": ""}
    task_process = subprocess.Popen([str(node), "--import", "tsx", "fixture.ts"], cwd=sandbox, env=task_env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    process = worker = oidc_fixture = None
    work_browser = os.environ.get("WORKGRAPH_WORK_BROWSER") == "1"
    gitlab = http.server.ThreadingHTTPServer(("127.0.0.1", 0), WorkGitLabFixture if work_browser else forge.GitLabFixture)
    threading.Thread(target=gitlab.serve_forever, daemon=True).start()
    gitlab_origin = f"http://127.0.0.1:{gitlab.server_port}"
    schema = "tasktopia_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    try:
        assert task_process.stdout is not None
        deadline = time.monotonic() + 100
        task_identity = None
        while time.monotonic() < deadline:
            if task_process.poll() is not None:
                raise AssertionError("Isolated Tasktopia fixture failed: " + (task_process.stderr.read() if task_process.stderr else ""))
            if select.select([task_process.stdout], [], [], 0.2)[0]:
                line = task_process.stdout.readline()
                if line.startswith("{"):
                    task_identity = json.loads(line)
                    break
        assert task_identity, "Tasktopia did not become ready"
        environment = {**clean, "WORKGRAPH_DATABASE_URL": api.BASE_DSN + "&search_path=" + schema,
                       "WORKGRAPH_ENCRYPTION_KEYS": json.dumps({"test": base64.b64encode(bytes([2]) * 32).decode()}),
                       "WORKGRAPH_ENCRYPTION_ACTIVE_KEY": "test", "WORKGRAPH_TASKTOPIA_ALLOWLIST": origin + "/mcp", "WORKGRAPH_GITLAB_ALLOWLIST": gitlab_origin}
        assert api.command(["migrate"], environment).returncode == 0
        setup = api.command(["bootstrap", "--project", "Task integration", "--repository", "task-test", "--gitlab-url", gitlab_origin, "--gitlab-project-id", "42"], environment)
        assert setup.returncode == 0
        identity = json.loads(setup.stdout)
        issuer = None
        listen = "127.0.0.1:0"
        if work_browser or os.environ.get("WORKGRAPH_TASKTOPIA_BROWSER") == "1":
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                listen = "127.0.0.1:" + str(listener.getsockname()[1])
            public = "http://" + listen
            oidc_fixture = subprocess.Popen([str(ROOT / "bin/verification/oidc-fixture"), "--callback", public + "/v1/auth/oidc/callback"], cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            issuer = json.loads(oidc_fixture.stdout.readline())["issuer"]
            environment.update(WORKGRAPH_OIDC_ISSUER=issuer, WORKGRAPH_OIDC_CLIENT_ID="workgraph", WORKGRAPH_OIDC_CLIENT_SECRET="synthetic-oidc-client-secret", WORKGRAPH_PUBLIC_URL=public)
        process, address = api.start_api(environment, listen)

        def request(method: str, path: str, body: object = None) -> tuple[int, object]:
            raw = json.dumps(body).encode() if body is not None else None
            req = urllib.request.Request("http://" + address + path, data=raw, method=method,
                                         headers={"Content-Type": "application/json", "Authorization": "Bearer " + identity["token"]})
            try:
                response = urllib.request.urlopen(req, timeout=20)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                data = response.read()
                return response.status, json.loads(data) if data else None

        def batch() -> None:
            result = api.command(["worker", "--once"], environment)
            assert result.returncode == 0 and json.loads(result.stdout)["worked"], "Task worker failed"

        if os.environ.get("WORKGRAPH_TASKTOPIA_BROWSER") == "1":
            # Reuse the actual isolated source/provider processes for browser setup, without the later API-only flow.
            issued = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "directory:read,directory:manage,platform:manage,integration:read,integration:manage,capture:read,capture:write"], environment)
            assert issued.returncode == 0
            identity["token"] = json.loads(issued.stdout)["token"]
            reader = str(uuid.uuid4())
            assert request("POST", "/v1/admin/users", dict(request_id=str(uuid.uuid4()), id=reader, display_name="Читатель настроек"))[0] == 201
            assert request("PUT", f'/v1/projects/{identity["project_id"]}/members/{reader}', dict(request_id=str(uuid.uuid4()), expected_version=0, role="reader"))[0] == 200
            reader_key = api.command(["issue-token", "--user-id", reader, "--scopes", "directory:read,integration:read,capture:read"], environment)
            assert reader_key.returncode == 0
            payload = dict(url="http://" + address, issuer=issuer, oidc=False, admin=identity["token"], admin_user=identity["user_id"], reader=reader, reader_token=json.loads(reader_key.stdout)["token"], project=identity["project_id"], repository=identity["repository_id"], gitlab=gitlab_origin,
                           tasktopia=dict(endpoint=origin + "/mcp", country_id=task_identity["country_id"], task_id=task_identity["task_id"], token=task_identity["token"]))
            checked = subprocess.run([str(node), "scripts/smoke-tasktopia-settings.mjs"], input=json.dumps(payload), text=True, cwd=ROOT, env=environment, timeout=600 if os.environ.get("GITMORA_UI_AUDIT") == "1" else 180)
            assert checked.returncode == 0, "Actual Tasktopia browser setup failed"
            return

        route = f"/v1/projects/{identity['project_id']}/tasktopia"
        config = {"endpoint": origin + "/mcp", "country_id": task_identity["country_id"], "api_token": task_identity["token"]}
        status, connection = request("PUT", route, config)
        assert status == 200, f"Actual MCP connection failed: {status} {connection}"
        assert task_identity["token"] not in json.dumps(connection)
        forge_route = f"/v1/repositories/{identity['repository_id']}/gitlab"
        forge_config = {"api_token": "synthetic-gitlab-token", "webhook_signing_token": "whsec_" + base64.b64encode(bytes([3]) * 32).decode()}
        assert request("PUT", forge_route, forge_config)[0] == 200
        task_route = route + "/tasks/" + task_identity["task_id"]
        snapshot = {"schema_version": 1, "repository_id": identity["repository_id"], "session_id": str(uuid.uuid4()), "revision": 1,
                    "started_at": "2026-10-30T10:00:00Z", "agent": {"name": "contract-agent", "model": "fixture"},
                    "messages": [{"role": "user", "text": "Keep this transcript private"}], "files": [], "commits": ["a" * 40],
                    "task": {"country_id": task_identity["country_id"], "task_id": task_identity["task_id"]}}
        assert request("POST", "/v1/captures", snapshot)[0] == 201
        publication_input = {"repository_id": identity["repository_id"], "session_id": snapshot["session_id"], "revision": 1}
        assert request("POST", route + "/publications", publication_input)[0] == 409
        context_query = {"project_id": identity["project_id"], "country_id": task_identity["country_id"], "task_id": task_identity["task_id"], "kinds": ["task", "document"]}
        for _ in range(60):
            batch()
            status, view = request("GET", task_route)
            _, task_context = request("POST", "/v1/context/search", context_query)
            _, captured = request("GET", f"/v1/repositories/{identity['repository_id']}/sessions/{snapshot['session_id']}")
            mr_status, mr = request("GET", forge_route + "/merge-requests/7")
            if status == 200 and task_context["items"] and "a" * 40 in captured.get("confirmed_commits", []) and mr_status == 200 and mr["commits_complete"]:
                break
        else:
            raise AssertionError("Automatic task discovery / provider-confirmed MR membership did not converge")
        assert view["task"]["id"] == task_identity["task_id"], "Capture binding did not discover actual task.get"
        assert all(item["source"]["digest"] == view["digest"] and item["source"]["task_id"] == task_identity["task_id"] for item in task_context["items"])
        pack_query = {"project_id": identity["project_id"], "country_id": task_identity["country_id"], "task_id": task_identity["task_id"]}
        status, context_pack = request("POST", "/v1/context/pack", pack_query)
        assert status == 200 and context_pack["task"]["source"]["digest"] == view["digest"]
        assert context_pack["task"]["checklist_count"] == 1 and context_pack["task"]["defect_count"] == 1 and context_pack["task"]["open_defect_count"] == 1
        assert context_pack["task"]["comment_count"] == 1 and context_pack["task"]["dependency_count"] == 1
        assert "context-comment-secret" not in json.dumps(context_pack) and len(context_pack["merge_requests"]["items"]) == 1
        assert context_pack["captures"]["items"] and context_pack["evidence"]
        assert request("GET", task_route + "/links")[1]["items"] == [], "Default policy published links"
        if work_browser:
            issued = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "directory:read,directory:manage,platform:manage,integration:read,integration:manage,capture:read,capture:write"], environment)
            assert issued.returncode == 0
            identity["token"] = json.loads(issued.stdout)["token"]
            worker = subprocess.Popen([str(ROOT / "bin/workgraphd"), "worker"], cwd=ROOT, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            payload = dict(url="http://" + address, issuer=issuer, admin=identity["token"], project=identity["project_id"], repository=identity["repository_id"],
                           task=task_identity["task_id"], session=snapshot["session_id"], gitlab=gitlab_origin, tasktopia=origin)
            checked = subprocess.run([str(node), "scripts/smoke-work.mjs"], input=json.dumps(payload), text=True, cwd=ROOT, env=environment, timeout=600 if os.environ.get("GITMORA_UI_AUDIT") == "1" else 180)
            assert checked.returncode == 0, "Actual Tasktopia work navigation failed"
            return
        status, publication = request("POST", route + "/publications", publication_input)
        assert status == 202 and "Keep this transcript private" not in publication["document"]["content"]
        assert request("POST", route + "/publications", publication_input)[1]["id"] == publication["id"]
        # Durable pending operation survives the API process being restarted before delivery.
        process.terminate()
        process.wait(timeout=10)
        process.stderr.close()
        batch()
        process, address = api.start_api(environment)
        status, finished = request("GET", route + "/publications/" + publication["id"])
        assert status == 200 and finished["state"] == "complete", "Actual document_upsert did not complete"
        with urllib.request.urlopen(origin + "/__test/state") as response:
            state = json.load(response)
        documents = [doc for doc in state["task"]["documents"] if doc["fileName"] == publication["document"]["fileName"]]
        assert len(documents) == 1 and documents[0]["content"] == publication["document"]["content"] and state["writes"] == 1
        assert state["task"]["status"] == "PLANNING" and state["task"]["progress"] == 0, "Publication changed workflow"
        # Replay uncertain completion after rotating the same service identity: readback must acknowledge it.
        api.sql(f'UPDATE "{schema}".work_jobs SET state=\'pending\',completed_at=NULL,available_at=now() WHERE id=\'{publication["id"]}\'')
        assert request("PUT", route, config)[0] == 200
        for _ in range(4):
            batch()
            if request("GET", route + "/publications/" + publication["id"])[1]["state"] == "complete":
                break
        else:
            raise AssertionError("Uncertain publication was not reconciled")
        with urllib.request.urlopen(origin + "/__test/state") as response:
            assert json.load(response)["writes"] == 1, "Readback repeated a remote mutation"
        config["auto_link_merge_requests"] = True
        assert request("PUT", route, config)[0] == 200
        # Discovery creates a stable link intent; the older refresh is processed first.
        batch()
        queued_links = request("GET", task_route + "/links")[1]["items"]
        assert len(queued_links) == 1 and queued_links[0]["state"] == "pending", "Verified MR did not create a durable link intent"
        link_id = queued_links[0]["id"]
        process.terminate()
        process.wait(timeout=10)
        process.stderr.close()
        batch()
        process, address = api.start_api(environment)
        delivered_links = request("GET", task_route + "/links")[1]["items"]
        assert len(delivered_links) == 1 and delivered_links[0]["state"] == "complete"
        assert delivered_links[0]["evidence"]["commit_sha"] == "a" * 40
        with urllib.request.urlopen(origin + "/__test/state") as response:
            state = json.load(response)
        assert state["linkWrites"] == 1 and len(state["task"]["mergeRequests"]) == 1
        assert state["task"]["mergeRequests"][0]["url"] == gitlab_origin + "/team/project/-/merge_requests/7"
        assert state["task"]["status"] == "PLANNING" and state["task"]["progress"] == 0
        # Replay an uncertain local ACK using the same immutable operation; readback avoids a second link_add.
        api.sql(f'UPDATE "{schema}".work_jobs SET state=\'pending\',completed_at=NULL,available_at=now() WHERE id=\'{link_id}\'')
        batch()
        assert request("GET", task_route + "/links")[1]["items"][0]["state"] == "complete"
        with urllib.request.urlopen(origin + "/__test/state") as response:
            assert json.load(response)["linkWrites"] == 1
        with urllib.request.urlopen(urllib.request.Request(origin + "/__test/revoke", data=b"{}", method="POST", headers={"Content-Type": "application/json"})) as response:
            assert json.load(response)["revoked"]
        assert request("POST", task_route + "/sync")[0] == 202
        batch()
        assert request("GET", task_route)[0] == 404, "Revoked service token exposed cached task"
        assert request("GET", route)[1]["blocked"], "Revoked source was not blocked"
        assert request("POST", "/v1/context/search", context_query)[1]["items"] == [], "Revoked Tasktopia source leaked through retrieval"
        hidden_pack = request("POST", "/v1/context/pack", pack_query)[1]
        assert "task" not in hidden_pack and hidden_pack["task_state"] == "unavailable"
        assert request("DELETE", route)[0] == 204
        assert request("GET", task_route)[0] == 404
        print("Actual Tasktopia MCP + GitLab HTTP fixture + built Workgraph/PostgreSQL smoke passed: automatic task discovery, independent commit/MR verification, opt-in MR links, immutable publication, restart/readback without duplicate writes, revoked-source hiding, unchanged task workflow.")
    finally:
        gitlab.shutdown()
        gitlab.server_close()
        for child in (worker, process, task_process, oidc_fixture):
            if child is not None:
                if child.poll() is None:
                    child.terminate()
                try:
                    child.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=5)
                if child.stdout:
                    child.stdout.close()
                if child.stderr:
                    child.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')
        api.sql(f'DROP SCHEMA "{task_schema}" CASCADE')


if __name__ == "__main__":
    main()
