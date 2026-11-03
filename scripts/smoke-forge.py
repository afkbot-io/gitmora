#!/usr/bin/env python3
"""Exercise built GitLab connector API/worker processes with PostgreSQL and a loopback API fixture."""
from __future__ import annotations

import base64
import argparse
import hashlib
import hmac
import http.server
import importlib.util
import json
import os
from pathlib import Path
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


class GitLabFixture(http.server.BaseHTTPRequestHandler):
    """Serve only exact provider project/object identities; never log authorization headers."""
    commits = {"a" * 40}
    code_allowed = True
    code_body = b'package auth\nvar token="provider-code-secret-never-persist"\nfunc ProviderVerifiedRefreshToken() {}\n'
    files = {"src/auth.go": code_body, "assets/image.bin": bytes([0, 255, 1])}
    large_raw_calls = 0
    installation_prefix = ""

    def strip_installation_prefix(self) -> bool:
        if self.installation_prefix:
            if not self.path.startswith(self.installation_prefix + "/"):
                self.send_error(404)
                return False
            self.path = self.path[len(self.installation_prefix):]
        return True

    def do_HEAD(self) -> None:
        if not self.strip_installation_prefix():
            return
        path = urllib.parse.unquote(urllib.parse.urlparse(self.path).path.removeprefix("/api/v4/projects/42/repository/files/"))
        query = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        if self.headers.get("PRIVATE-TOKEN") != "synthetic-gitlab-token" or not self.code_allowed:
            self.send_response(401)
            self.end_headers()
            return
        if query.get("ref") != ["a" * 40] or path not in {*self.files, "large.bin"}:
            self.send_response(404)
            self.end_headers()
            return
        raw = self.files.get(path, b"unused")
        blob = hashlib.sha1(f"blob {len(raw)}\0".encode() + raw).hexdigest()
        self.send_response(200)
        for name, value in {"X-Gitlab-Blob-Id": blob, "X-Gitlab-Commit-Id": "a" * 40, "X-Gitlab-Ref": "a" * 40, "X-Gitlab-File-Path": path, "X-Gitlab-Size": str(len(raw) if path != "large.bin" else 512*1024+1)}.items():
            self.send_header(name, value)
        self.end_headers()

    def log_message(self, *_args: object) -> None:
        pass

    def do_GET(self) -> None:
        if not self.strip_installation_prefix():
            return
        if self.headers.get("PRIVATE-TOKEN") != "synthetic-gitlab-token":
            self.send_error(401)
            return
        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        lookup_paths = {"/api/v4/projects/example%2Fproject": (42, "example/project"),
                        "/api/v4/projects/example%2Fsecond": (43, "example/second")}
        if parsed.path in lookup_paths:
            repository_id, repository_path = lookup_paths[parsed.path]
            raw = json.dumps(dict(id=repository_id, web_url="http://" + self.headers["Host"] + self.installation_prefix + "/" + repository_path, path_with_namespace=repository_path)).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(raw)
            return
        if parsed.path.startswith("/api/v4/projects/42/repository/files/"):
            if not self.code_allowed:
                self.send_error(401)
                return
            path = urllib.parse.unquote(parsed.path.removeprefix("/api/v4/projects/42/repository/files/").removesuffix("/raw"))
            if path == "large.bin":
                type(self).large_raw_calls += 1
            if query.get("ref") != ["a" * 40] or path not in self.files or query.get("lfs") != ["false"]:
                self.send_error(404)
                return
            raw = self.files[path]
            self.send_response(200)
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return
        more = None
        updated = "2026-10-29T10:00:00Z"
        if parsed.path in {"/api/v4/projects/42", "/api/v4/projects/43"}:
            result = {"id": int(parsed.path.rsplit("/", 1)[1])}
        elif parsed.path == "/api/v4/projects/42/merge_requests":
            result = [{"iid": 7, "project_id": 42, "updated_at": updated}] if query.get("page") == ["1"] else []
            more = "2" if result else ""
        elif parsed.path == "/api/v4/projects/42/merge_requests/7":
            result = {"id": 107, "iid": 7, "project_id": 42, "source_project_id": 42, "title": "Trace merge", "state": "merged", "sha": "b" * 40, "squash_commit_sha": "c" * 40, "merge_commit_sha": "d" * 40, "updated_at": updated, "diff_refs": {"head_sha": "b" * 40, "base_sha": "a" * 40, "start_sha": "e" * 40}}
            result["web_url"] = f"http://127.0.0.1:{self.server.server_port}{self.installation_prefix}/team/project/-/merge_requests/7"
        elif parsed.path == "/api/v4/projects/42/merge_requests/7/commits":
            result = [{"id": "a" * 40}, {"id": "b" * 40}] if query.get("page") == ["1"] else [{"id": "e" * 40}]
            more = "2" if query.get("page") == ["1"] else ""
        elif parsed.path == "/api/v4/projects/42/pipelines":
            child = query.get("source") == ["parent_pipeline"]
            result = [{"id": 12 if child else 11, "project_id": 42, "sha": ("b" if child else "a") * 40, "status": "failed" if child else "success", "ref": "main", "source": "parent_pipeline" if child else "push", "updated_at": updated}]
        elif parsed.path == "/api/v4/projects/42/deployments":
            result = [{"id": 21, "sha": "c" * 40, "status": "success", "ref": "main", "updated_at": updated, "environment": {"id": 1, "name": "production"}}]
        elif parsed.path.startswith("/api/v4/projects/42/repository/commits/") and parsed.path.endswith("/diff"):
            sha = parsed.path.split("/")[-2]
            if sha not in self.commits:
                self.send_error(404)
                return
            if sha == "a" * 40:
                if query.get("page") == ["1"]:
                    result = [{"new_path": "src/auth.go", "b_mode": "100644"}, {"new_path": "link", "b_mode": "120000"}]
                    more = "2"
                else:
                    result = [{"new_path": "assets/image.bin", "b_mode": "100644"}, {"new_path": "large.bin", "b_mode": "100644"}, {"new_path": "removed.go", "b_mode": "000000", "deleted_file": True}]
                    more = ""
            else:
                result = []
                more = ""
        else:
            sha = self.path.removeprefix("/api/v4/projects/42/repository/commits/").split("?", 1)[0]
            if sha not in self.commits:
                self.send_error(404)
                return
            result = {"id": sha}
        raw = json.dumps(result).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        if more is not None:
            self.send_header("X-Next-Page", more)
        self.end_headers()
        self.wfile.write(raw)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--subpath", action="store_true", help="Serve the fixture below /gitlab")
    args = parser.parse_args()
    GitLabFixture.installation_prefix = "/gitlab" if args.subpath else ""
    schema = "forge_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), GitLabFixture)
    threading.Thread(target=provider.serve_forever, daemon=True).start()
    environment = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}}
    environment.update({"WORKGRAPH_DATABASE_URL": api.BASE_DSN + "&search_path=" + schema,
                        "WORKGRAPH_ENCRYPTION_KEYS": json.dumps({"test": base64.b64encode(bytes([1]) * 32).decode()}),
                        "WORKGRAPH_ENCRYPTION_ACTIVE_KEY": "test",
                        "WORKGRAPH_GITLAB_ALLOWLIST": f"http://127.0.0.1:{provider.server_port}{GitLabFixture.installation_prefix}"})
    process = None
    worker = None
    try:
        assert api.command(["migrate"], environment).returncode == 0, "migration failed"
        setup = api.command(["bootstrap", "--project", "Forge test", "--repository", "Forge test", "--gitlab-url", environment["WORKGRAPH_GITLAB_ALLOWLIST"], "--gitlab-project-id", "42"], environment)
        assert setup.returncode == 0, "bootstrap failed"
        identity = json.loads(setup.stdout)
        process, address = api.start_api(environment)

        def request(method: str, path: str, body: object = None, headers: dict | None = None) -> tuple[int, object]:
            raw = json.dumps(body).encode() if body is not None else None
            selected = {"Content-Type": "application/json", "Authorization": "Bearer " + identity["token"]} if headers is None else {"Content-Type": "application/json", **headers}
            req = urllib.request.Request("http://" + address + path, data=raw, headers=selected, method=method)
            try:
                response = urllib.request.urlopen(req, timeout=10)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                contents = response.read()
                return response.status, json.loads(contents) if contents else None

        def batch() -> None:
            result = api.command(["worker", "--once"], environment)
            assert result.returncode == 0 and json.loads(result.stdout)["worked"], "worker batch did not run"

        repo = identity["repository_id"]
        route = f"/v1/repositories/{repo}/gitlab"
        key = bytes([3]) * 32
        secret = {"api_token": "synthetic-gitlab-token", "webhook_signing_token": "whsec_" + base64.b64encode(key).decode()}
        status, connected = request("PUT", route, {"api_token": "synthetic-gitlab-token", "webhook_mode": "disabled"})
        assert status == 200 and connected["webhook_mode"] == "disabled" and connected["webhook_path"] == "", "API-only provider setup failed"
        assert "synthetic-gitlab-token" not in json.dumps(connected), "provider credential in response"
        assert request("POST", f"/v1/integrations/gitlab/{repo}/webhook", {"object_kind": "push", "project_id": 42}, {"X-Gitlab-Event": "Push Hook", "Webhook-Id": "disabled-hook"})[0] == 401, "API-only connection accepted webhook"
        issued = api.command(["issue-token", "--user-id", identity["user_id"], "--label", "Scoped operator", "--scopes", "integration:read,integration:manage", "--ttl", "1h"], environment)
        assert issued.returncode == 0, "offline scoped credential issuance failed"
        operator = json.loads(issued.stdout)
        operator_headers = {"Authorization": "Bearer " + operator["token"]}
        assert request("GET", route, headers=operator_headers)[0] == 200, "issued integration credential cannot read health"
        session = str(uuid.uuid4())
        snapshot = {"schema_version": 1, "repository_id": repo, "session_id": session, "revision": 1,
                    "agent": {"name": "forge-smoke", "model": "synthetic-local"}, "started_at": "2026-10-30T10:00:00Z",
                    "messages": [{"role": "user", "text": "Verify exact provider commit"}], "files": ["a.go"], "commits": ["a" * 40]}
        assert request("POST", "/v1/captures", snapshot)[0] == 201
        assert request("POST", "/v1/captures", snapshot, operator_headers)[0] == 403, "integration credential received capture authority"
        session_route = f"/v1/repositories/{repo}/sessions/{session}"
        assert request("GET", session_route)[1]["confirmed_commits"] == []
        process.terminate()
        assert process.wait(timeout=12) == 0
        process.stderr.close()
        batch()  # API-only credentials survive API exit; a separate worker does the verification.
        process, address = api.start_api(environment)
        assert request("GET", route)[1]["webhook_mode"] == "disabled", "API-only policy lost after API/worker restart"
        assert request("GET", session_route)[1]["confirmed_commits"] == ["a" * 40], "API-only reconciliation failed"
        status, connected = request("PUT", route, secret)
        assert status == 200 and connected["webhook_mode"] == "signature" and connected["webhook_path"], "explicit webhook enable failed"
        snapshot["revision"] = 2
        snapshot["commits"].append("b" * 40)
        assert request("POST", "/v1/captures", snapshot)[0] == 201
        body = {"object_kind": "push", "project_id": 42, "after": "b" * 40, "variables": [{"key": "SECRET", "value": "raw-variable-must-not-be-stored"}]}
        stamp = str(int(time.time()))
        delivery_id = str(uuid.uuid4())
        raw = json.dumps(body).encode()
        signature = "v1," + base64.b64encode(hmac.new(key, delivery_id.encode() + b"." + stamp.encode() + b"." + raw, hashlib.sha256).digest()).decode()
        headers = {"Webhook-Id": delivery_id, "Webhook-Timestamp": stamp, "Webhook-Signature": signature, "X-Gitlab-Event": "Push Hook"}
        hook = connected["webhook_path"]
        assert request("POST", hook, body, {"X-Gitlab-Event": "Push Hook"})[0] == 401
        assert request("POST", hook, body, headers)[1]["created"] is True
        assert request("POST", hook, body, headers)[1]["created"] is False
        assert request("GET", session_route)[1]["confirmed_commits"] == ["a" * 40], "webhook claimed provider confirmation"
        process.terminate()
        assert process.wait(timeout=12) == 0
        process.stderr.close()
        GitLabFixture.commits.add("b" * 40)
        batch()  # Independent worker process recovers work after the API has exited.
        process, address = api.start_api(environment)
        assert request("GET", session_route)[1]["confirmed_commits"] == ["a" * 40, "b" * 40], "durable worker evidence missing after restart"
        GitLabFixture.commits.update({"c" * 40, "d" * 40, "e" * 40})
        for _ in range(30):
            result = api.command(["worker", "--once"], environment)
            assert result.returncode == 0, "metadata worker failed"
            if not json.loads(result.stdout)["worked"]:
                break
        else:
            raise AssertionError("metadata queue did not drain")
        status, mr = request("GET", route + "/merge-requests/7?limit=2")
        assert status == 200 and mr["commits_complete"] and len(mr["source_commits"]) == 2 and mr["next_sha"], "MR commit page missing"
        status, expanded = request("GET", route + "/merge-requests/7?after_sha=" + mr["next_sha"])
        assert status == 200 and len(expanded["source_commits"]) == 1, "MR expansion cursor lost source commits"
        assert set(mr["result_shas"]) == {"c" * 40, "d" * 40}, "squash/merge result evidence missing"
        status, pipelines = request("GET", route + "/pipelines?limit=1")
        assert status == 200 and pipelines["items"][0]["sha"] == "a" * 40 and pipelines["items"][0]["sha"] != mr["model"]["head"], "historical pipeline SHA was lost"
        assert request("GET", route + "/pipelines?after_id=" + str(pipelines["next_id"]))[1]["items"][0]["id"] == 12, "child pipeline expansion missing"
        assert request("GET", route + "/deployments")[1]["items"][0]["sha"] == "c" * 40, "deployed code SHA missing"
        assert api.sql(f'SELECT count(*) FROM "{schema}".forge_commit_lineage') == "6", "complete source-to-result lineage missing"
        code_route = f"/v1/repositories/{repo}/code"
        status, code = request("GET", code_route + "/commits/" + "a" * 40)
        assert status == 200 and code["discovery_ended"] and code["stored"] == 1 and code["skipped"] == 4 and code["pending"] == 0, "Code ingestion coverage missing"
        assert code["provider_diff_limits_apply"] and GitLabFixture.large_raw_calls == 0
        status, quote = request("POST", code_route + "/file", {"commit_sha": "a" * 40, "file_path": "src/auth.go", "limit": 500}, operator_headers)
        assert status == 200 and quote["redacted"] and "ProviderVerifiedRefreshToken" in quote["content"] and "provider-code-secret-never-persist" not in json.dumps(quote)
        status, code_search = request("POST", "/v1/context/search", {"project_id": identity["project_id"], "text": "ProviderVerifiedRefreshToken", "kinds": ["code"]}, operator_headers)
        assert status == 200 and len(code_search["items"]) == 1 and code_search["items"][0]["source"]["commit_sha"] == "a" * 40 and code_search["items"][0]["line_start"] > 0
        rpc_headers = {**operator_headers, "Accept": "application/json, text/event-stream", "Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "context.file"}
        args = {"repository_id": repo, "commit_sha": "a" * 40, "file_path": "src/auth.go", "limit": 500}
        meta = {"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientInfo": {"name": "code-process-smoke", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": {}}
        status, mcp_file = request("POST", "/mcp", {"jsonrpc": "2.0", "id": 600, "method": "tools/call", "params": {"name": "context.file", "arguments": args, "_meta": meta}}, rpc_headers)
        assert status == 200 and not mcp_file["result"].get("isError") and mcp_file["result"]["structuredContent"]["result"]["digest"] == quote["digest"], "Pinned MCP file quote failed"
        persisted = api.sql(f'SELECT model::text FROM "{schema}".forge_code_snapshots')
        assert "provider-code-secret-never-persist" not in persisted, "Unredacted code persisted"
        GitLabFixture.code_allowed = False
        api.sql(f'UPDATE "{schema}".forge_connections SET code_next_at=now(),code_probe_at=now()')
        denied = api.command(["worker", "--once"], environment)
        assert denied.returncode != 0, "Denied code probe was not reported"
        assert request("POST", code_route + "/file", {"commit_sha": "a" * 40, "file_path": "src/auth.go"}, operator_headers)[0] == 404
        assert request("POST", "/v1/context/search", {"project_id": identity["project_id"], "text": "ProviderVerifiedRefreshToken", "kinds": ["code"]}, operator_headers)[1]["items"] == []
        assert not request("GET", route)[1]["blocked"], "Code-only failure blocked MR synchronization"
        encrypted = api.sql(f"SELECT encode(secret_envelope,'hex') FROM \"{schema}\".forge_connections")
        assert b"synthetic-gitlab-token" not in bytes.fromhex(encrypted), "clear provider token in database"
        inbox = api.sql(f'SELECT row_to_json(i)::text FROM "{schema}".forge_webhook_inbox i')
        assert "raw-variable-must-not-be-stored" not in inbox, "raw CI variable persisted"
        worker = subprocess.Popen([str(ROOT / "bin/workgraphd"), "worker"], cwd=ROOT, env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        time.sleep(0.3)
        assert worker.poll() is None, "continuous worker did not start"
        worker.terminate()
        assert worker.wait(timeout=12) == 0, "worker shutdown was not clean"
        assert request("DELETE", route)[0] == 204
        assert api.sql(f'SELECT secret_envelope IS NULL FROM "{schema}".forge_connections') == "t", "disconnect retained recoverable credentials"
        assert request("POST", hook, body, headers)[0] == 401
        print(json.dumps({"result": "passed", "scope": "built API/worker + PostgreSQL + GitLab HTTP fixture", "verified": ["encrypted connection", "signed webhook", "idempotent inbox", "independent commit confirmation", "missed webhook", "worker/API restart", "paginated MR discovery and membership", "bounded metadata expansion", "regular and child CI", "deployed SHA", "complete squash and merge lineage", "verified raw code blobs", "bounded code coverage and redaction", "MCP pinned file quotes", "independent code permission probe", "minimal provider data retention", "credential destruction", "graceful worker shutdown"]}))
    finally:
        provider.shutdown()
        provider.server_close()
        for child in (worker, process):
            if child is not None:
                if child.poll() is None:
                    child.kill()
                    child.wait(timeout=5)
                if child.stderr is not None and not child.stderr.closed:
                    child.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
