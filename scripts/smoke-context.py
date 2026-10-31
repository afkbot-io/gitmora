#!/usr/bin/env python3
"""Built API/index worker/PostgreSQL + HTTP MCP search, exact expansion and live authorization."""
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
from native_context_fixture import verify as verify_native_context
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
assert spec and spec.loader
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def main() -> None:
    schema = "context_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = None
    try:
        assert api.command(["migrate"], environment).returncode == 0
        setup = api.command(["bootstrap", "--project", "Context test", "--repository", "context-test", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert setup.returncode == 0
        identity = json.loads(setup.stdout)
        process, address = api.start_api(environment)

        def request(path: str, body: object, token: str | None = None, extra: dict | None = None) -> tuple[int, object]:
            headers = {"Content-Type": "application/json", "Authorization": "Bearer " + (token or identity["token"]), **(extra or {})}
            req = urllib.request.Request("http://" + address + path, data=json.dumps(body).encode(), headers=headers)
            try:
                response = urllib.request.urlopen(req, timeout=10)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                return response.status, json.loads(raw) if raw else None

        text = "Авторизация refreshToken: проверка доступа. 🧪\n" * 100
        country, task = str(uuid.uuid4()), str(uuid.uuid4())
        snapshot = {"schema_version": 1, "repository_id": identity["repository_id"], "session_id": str(uuid.uuid4()), "revision": 1,
                    "started_at": "2026-10-30T10:00:00Z", "agent": {"name": "context-agent", "model": "no-model-needed"},
                    "messages": [{"role": "assistant", "text": text}], "files": ["src/auth/refresh_token.go"], "commits": ["a" * 40],
                    "task": {"country_id": country, "task_id": task}}
        status, accepted = request("/v1/captures", snapshot)
        assert status == 201
        query = {"project_id": identity["project_id"], "text": "авторизации"}
        status, pending = request("/v1/context/search", query)
        assert status == 200 and pending["items"] == [] and pending["pending_sources_in_project"] == 1
        # Shut down the API with a committed outbox, then run a separate provider-free index worker.
        process.terminate()
        process.wait(timeout=10)
        process.stderr.close()
        indexed = api.command(["worker", "--once"], environment)
        assert indexed.returncode == 0 and json.loads(indexed.stdout)["worked"], "Provider-free index worker failed"
        process, address = api.start_api(environment)
        status, found = request("/v1/context/search", query)
        assert status == 200 and len(found["items"]) >= 2 and found["pending_sources_in_project"] == 0
        hit = found["items"][0]
        assert hit["source"]["digest"] == accepted["digest"] and hit["source"]["revision"] == 1 and hit["current"]
        assert len(hit["content"]) <= 500
        status, expanded = request("/v1/context/expand", {"evidence_id": hit["id"], "limit": 96})
        assert status == 200 and expanded["content"] == text[expanded["start"]:expanded["end"]] and len(expanded["content"]) == 96
        assert expanded["next_offset"] == 96 and expanded["content_digest"] == hit["content_digest"]
        file_query = {"project_id": identity["project_id"], "text": "refreshToken", "kinds": ["code_reference"]}
        assert request("/v1/context/search", file_query)[1]["items"] == [], "Unconfirmed code reference exposed"
        # Actual provider confirmation is independently exercised by smoke-forge.
        api.sql(f'UPDATE "{schema}".commits SET confirmed_at=now()')
        status, references = request("/v1/context/search", file_query)
        assert status == 200 and len(references["items"]) == 1 and references["items"][0]["confirmed_count"] == 1

        headers = {"Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2026-07-28"}
        sequence = 0

        def rpc(method: str, params: dict, token: str | None = None) -> tuple[int, object]:
            nonlocal sequence
            sequence += 1
            meta={"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"context-process-smoke","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}
            selected={**headers,"Mcp-Method":method}
            if method=="tools/call":
                selected["Mcp-Name"]=params["name"]
            return request("/mcp", {"jsonrpc": "2.0", "id": sequence, "method": method, "params": {**params,"_meta":meta}}, token, selected)

        status, initialized = rpc("server/discover", {})
        assert status == 200 and initialized.get("result", {}).get("_meta", {}).get("io.modelcontextprotocol/serverInfo", {}).get("name") == "Gitmora", f"MCP initialization failed: {status} {initialized}"
        status, tools = rpc("tools/list", {})
        assert status == 200 and {tool["name"] for tool in tools["result"]["tools"]} == {"context.search", "context.expand", "context.task","context.file","context.pack","context.browse"}
        discovery_query = dict(project_id=identity["project_id"], kind="session", limit=1)
        status, discovered = rpc("tools/call", {"name": "context.browse", "arguments": discovery_query})
        assert status == 200 and not discovered["result"].get("isError")
        source = discovered["result"]["structuredContent"]["result"]["items"][0]
        assert source["query"]["session_id"] == snapshot["session_id"]
        status, packed = rpc("tools/call", {"name": "context.pack", "arguments": source["query"]})
        assert status == 200 and packed["result"]["structuredContent"]["result"]["captures"]["items"][0]["source"]["session_id"] == snapshot["session_id"]
        status, searched = rpc("tools/call", {"name": "context.search", "arguments": query})
        assert status == 200 and not searched["result"].get("isError")
        assert searched["result"]["structuredContent"]["result"]["items"][0]["id"] == hit["id"]
        status, task_context = rpc("tools/call", {"name": "context.task", "arguments": {"project_id": identity["project_id"], "country_id": country, "task_id": task}})
        assert status == 200 and task_context["result"]["structuredContent"]["result"]["items"], "Bound capture missing from task context"
        status, detail = rpc("tools/call", {"name": "context.expand", "arguments": {"evidence_id": hit["id"], "offset": 10, "limit": 40}})
        assert status == 200 and len(detail["result"]["structuredContent"]["result"]["content"]) == 40
        issued = api.command(["issue-token", "--user-id", identity["user_id"], "--label", "Task-only context", "--scopes", "integration:read", "--ttl", "1h"], environment)
        assert issued.returncode == 0
        scoped = json.loads(issued.stdout)
        assert request("/v1/context/browse", discovery_query, scoped["token"])[0] == 403
        status, denied_browse = rpc("tools/call", {"name": "context.browse", "arguments": discovery_query}, scoped["token"])
        assert status == 200 and denied_browse["result"].get("isError") and snapshot["session_id"] not in json.dumps(denied_browse)
        assert request("/v1/context/search", query, scoped["token"])[1]["items"] == []
        assert request("/v1/context/expand", {"evidence_id": hit["id"]}, scoped["token"])[0] == 404
        status, narrow = rpc("tools/call", {"name": "context.search", "arguments": query}, scoped["token"])
        assert status == 200 and narrow["result"]["structuredContent"]["result"]["items"] == []
        snapshot["revision"] = 2
        snapshot["messages"] = [{"role": "assistant", "text": "Current source has changed."}]
        assert request("/v1/captures", snapshot)[0] == 201
        assert api.command(["worker", "--once"], environment).returncode == 0
        assert request("/v1/context/search", query)[1]["items"] == []
        status, historical = request("/v1/context/expand", {"evidence_id": hit["id"]})
        assert status == 200 and not historical["current"] and historical["source"]["revision"] == 1
        api.sql(f'UPDATE "{schema}".api_tokens SET revoked_at=now() WHERE id=\'{scoped["token_id"]}\'')
        assert rpc("tools/list", {}, scoped["token"])[0] == 401, "MCP reused revoked credentials"
        assert request("/mcp", {"jsonrpc": "2.0", "id": 99, "method": "tools/list", "params": {}}, extra={**headers, "Origin": "https://unapproved.example"})[0] == 403
        native_key = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes", "capture:read,credentials:manage", "--ttl", "1h"], environment)
        assert native_key.returncode == 0
        native_identity = json.loads(native_key.stdout)
        # The immutable historical source remains readable by explicit expansion; search the latest current revision.
        native_query = dict(project_id=identity["project_id"], text="Current source")
        native_hit = request("/v1/context/search", native_query)[1]["items"][0]
        verify_native_context(ROOT, "http://" + address, native_identity["token"], native_query, native_hit["id"],
            lambda: api.sql(f'UPDATE "{schema}".api_tokens SET revoked_at=now() WHERE id=\'{native_identity["token_id"]}\''))
        print("Native CLI API/stdio MCP processes passed: explicit environment authority, six read tools, search, exact expansion, live revocation and no credential stdout.")
        print("Built API/index worker/PostgreSQL + MCP passed: source discovery/returned-selector pack, bilingual search, exact Unicode citations, pending coverage, restart, confirmed references, task context, independent source capabilities, historical expansion, token revocation.")
    finally:
        if process is not None:
            if process.poll() is None:
                process.terminate()
            process.wait(timeout=10)
            if process.stderr:
                process.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
