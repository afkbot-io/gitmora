#!/usr/bin/env python3
"""Real OpenCode -> authenticated Workgraph HTTP MCP -> PostgreSQL source -> exact expansion.

The model is a labelled local tool-calling fixture. No actual inference or provider readiness is claimed.
"""
from __future__ import annotations
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def objects(value, depth=0):
    """Decode protocol text envelopes only in this bounded synthetic model fixture."""
    if depth > 10:
        return
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from objects(child, depth + 1)
    elif isinstance(value, list):
        for child in value:
            yield from objects(child, depth + 1)
    elif isinstance(value, str) and value.lstrip().startswith(("{", "[")):
        try:
            yield from objects(json.loads(value), depth + 1)
        except ValueError:
            pass


class Model(BaseHTTPRequestHandler):
    query = {}
    pack_seen = False
    browse_seen = False
    expansion_seen = False
    calls = 0
    failure = ""

    def log_message(self, *_):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        messages = body.get("messages", [])
        tools = [tool["function"]["name"] for tool in body.get("tools", [])]
        tool_results = [message.get("content") for message in messages if message.get("role") == "tool"]
        expanded_results = [message.get("content") for message in messages if message.get("role") == "tool" and message.get("tool_call_id") == "context_expand"]
        pack = next((obj for obj in objects(tool_results) if obj.get("schema_version") == 1 and "captures" in obj), None)
        browsed = next((obj for obj in objects(tool_results) if obj.get("schema_version") == 1 and obj.get("query", {}).get("kind") == "session" and "items" in obj), None)
        expansion = next((obj for obj in objects(expanded_results) if isinstance(obj.get("content"), str) and obj["content"].startswith("Original context marker") and "content_digest" in obj), None)
        delta = {"role": "assistant", "content": "Context source verified through Workgraph MCP"}
        finish = "stop"
        if not tools:
            delta = {"role": "assistant", "content": "Local metadata fixture"}
        else:
            try:
                if expansion:
                    type(self).expansion_seen = True
                elif pack:
                    type(self).pack_seen = True
                    assert pack["captures"]["items"][0]["chain_verified"]
                    assert pack["evidence"] and pack["evidence"][0]["source"]["session_id"] == self.query["session_id"]
                    name = next(name for name in tools if "context_expand" in name or "context.expand" in name)
                    args = {"evidence_id": pack["evidence"][0]["id"], "offset": 0, "limit": 80}
                    delta = {"role": "assistant", "tool_calls": [{"index": 0, "id": "context_expand", "type": "function", "function": {"name": name, "arguments": json.dumps(args)}}]}
                    finish = "tool_calls"
                elif browsed:
                    type(self).browse_seen = True
                    assert browsed["items"] and browsed["items"][0]["query"]["session_id"] == self.query["session_id"]
                    name = next(name for name in tools if "context_pack" in name or "context.pack" in name)
                    delta = {"role": "assistant", "tool_calls": [{"index": 0, "id": "context_pack", "type": "function", "function": {"name": name, "arguments": json.dumps(browsed["items"][0]["query"])}}]}
                    finish = "tool_calls"
                else:
                    name = next(name for name in tools if "context_browse" in name or "context.browse" in name)
                    args = dict(project_id=self.query["project_id"], repository_id=self.query["repository_id"], kind="session", limit=1)
                    delta = {"role": "assistant", "tool_calls": [{"index": 0, "id": "context_browse", "type": "function", "function": {"name": name, "arguments": json.dumps(args)}}]}
                    finish = "tool_calls"
            except (AssertionError, StopIteration, KeyError) as error:
                type(self).failure = "native MCP tools/context unavailable: " + type(error).__name__ + "; tools=" + repr(tools)
                delta = {"role": "assistant", "content": "Fixture context validation failed"}
        if tools:
            type(self).calls += 1
        if self.calls > 5:
            type(self).failure = "native MCP workflow did not converge"
            finish = "stop"
            delta = {"role": "assistant", "content": "Fixture stopped"}
        chunk = {"id": "context_fixture", "object": "chat.completion.chunk", "created": 1793300000, "model": "fixture", "choices": [{"index": 0, "delta": delta, "finish_reason": None}]}
        last = {**chunk, "choices": [{"index": 0, "delta": {}, "finish_reason": finish}], "usage": {"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}}
        data = ("data: " + json.dumps(chunk) + "\n\ndata: " + json.dumps(last) + "\n\ndata: [DONE]\n\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def main():
    schema = "agent_context_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = os.environ.copy()
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = None
    model = ThreadingHTTPServer(("127.0.0.1", 0), Model)
    threading.Thread(target=model.serve_forever, daemon=True).start()
    try:
        assert api.command(["migrate"], environment).returncode == 0
        setup = api.command(["bootstrap", "--project", "Native context", "--repository", "Source",
                             "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert setup.returncode == 0
        identity = json.loads(setup.stdout)
        process, address = api.start_api(environment)
        base = "http://" + address
        session = str(uuid.uuid4())
        snapshot = {"schema_version": 1, "repository_id": identity["repository_id"], "session_id": session,
                    "revision": 1, "agent": {"name": "historical-agent", "model": "fixture"},
                    "started_at": "2026-10-30T00:00:00Z", "messages": [{"role": "assistant", "text": "Original context marker. Cache TTL was changed to 120 seconds. Report only; tests not run."}]}
        req = urllib.request.Request(base + "/v1/captures", data=json.dumps(snapshot).encode(), headers={"Content-Type": "application/json", "Authorization": "Bearer " + identity["token"]})
        with urllib.request.urlopen(req) as response:
            assert response.status == 201
        assert api.command(["worker", "--once"], environment).returncode == 0
        Model.query = {"project_id": identity["project_id"], "repository_id": identity["repository_id"], "session_id": session}
        with tempfile.TemporaryDirectory(prefix="workgraph-mcp-agent-") as temporary:
            temp = Path(temporary)
            config_dir = temp / "config/opencode"
            config_dir.mkdir(parents=True)
            environment.update({"WORKGRAPH_TOKEN": identity["token"], "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
                                "XDG_CONFIG_HOME": str(temp / "config"), "XDG_DATA_HOME": str(temp / "data"), "XDG_STATE_HOME": str(temp / "state"), "XDG_CACHE_HOME": str(temp / "cache"),
                                **{key: "true" for key in ("OPENCODE_DISABLE_AUTOUPDATE", "OPENCODE_DISABLE_MODELS_FETCH", "OPENCODE_DISABLE_DEFAULT_PLUGINS", "OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_LSP_DOWNLOAD", "OPENCODE_DISABLE_CLAUDE_CODE", "OPENCODE_DISABLE_EMBEDDED_WEB_UI")}})
            config = {"autoupdate": False, "share": "disabled", "enabled_providers": ["workgraph-local"], "model": "workgraph-local/fixture", "permission": {"*": "deny", "workgraph_*": "allow"}, "lsp": False, "formatter": False,
                      "provider": {"workgraph-local": {"npm": "@ai-sdk/openai-compatible", "name": "Local fixture", "options": {"baseURL": f"http://127.0.0.1:{model.server_port}/v1", "apiKey": "fixture"}, "models": {"fixture": {"name": "Fixture", "tool_call": True, "limit": {"context": 32000, "output": 2048}}}}},
                      "mcp": {"workgraph": {"type": "remote", "url": base + "/mcp", "oauth": False, "headers": {"Authorization": "Bearer {env:WORKGRAPH_TOKEN}"}}}}
            (config_dir / "opencode.json").write_text(json.dumps(config))
            subprocess.run(["git", "init", "-q"], cwd=temp, env=environment, check=True, capture_output=True)
            discovery = subprocess.run([str(ROOT / "node_modules/.bin/opencode"), "mcp", "list"], cwd=temp, env=environment, capture_output=True, text=True, timeout=35)
            (ROOT / "build/agent-context-discovery.log").write_text((discovery.stdout + discovery.stderr).replace(identity["token"], "[REDACTED]"))
            native = subprocess.run([str(ROOT / "node_modules/.bin/opencode"), "run", "--format", "json", "Discover the Workgraph session, read its context pack, expand its exact evidence, and report it."], cwd=temp, env=environment, capture_output=True, text=True, timeout=75)
            (ROOT / "build/agent-context-native.log").write_text((native.stdout + native.stderr).replace(identity["token"], "[REDACTED]"))
            assert native.returncode == 0, "Native context run failed"
            assert not Model.failure and Model.browse_seen and Model.pack_seen and Model.expansion_seen and Model.calls == 4, Model.failure or f"Actual native MCP workflow incomplete: calls={Model.calls}, browse={Model.browse_seen}, pack={Model.pack_seen}, expansion={Model.expansion_seen}"
            assert "Context source verified through Workgraph MCP" in native.stdout
        print("Actual OpenCode 1.19.5 -> authenticated Workgraph HTTP MCP context.browse -> context.pack -> exact context.expand -> final reply passed; model is a local fixture.")
    finally:
        if process:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=10)
            process.stderr.close()
        model.shutdown()
        model.server_close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
