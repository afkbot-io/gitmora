#!/usr/bin/env python3
"""Run real OpenCode against a deterministic loopback model fixture and real API/PostgreSQL.

This checks the native plugin lifecycle, not model intelligence or a live GitLab provider.
Every repository, provider configuration and credential used here is temporary and synthetic.
"""
from __future__ import annotations

import http.server
import importlib.util
import json
import os
import socket
from pathlib import Path
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
assert spec and spec.loader
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


class ModelFixture(http.server.BaseHTTPRequestHandler):
    """Return one explicit Bash call, then a final answer after the actual tool result."""
    tool_calls = 0

    def log_message(self, *_args: object) -> None:
        pass

    def do_POST(self) -> None:
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        messages = body.get("messages", [])
        private = "Private no commits" in json.dumps(messages)
        write_only = "Write without commit" in json.dumps(messages)
        tools = bool(body.get("tools")) and not private
        has_result = any(message.get("role") == "tool" for message in messages)
        continuation = "Native compaction summary" in json.dumps(messages)
        delta: dict = {"role": "assistant", "content": "Private answer" if private else "Native final answer after git push"}
        if not tools and not private:
            delta["content"] = "Native compaction summary"
        if tools and continuation:
            delta["content"] = "Native continued after compaction"
        finish = "stop"
        if tools and not has_result and not continuation:
            type(self).tool_calls += 1
            command = "printf 'package native\\n' > native.go && git add native.go && git commit -m 'native first' && printf 'package native\\n' > second.go && git add second.go && git commit -m 'native second' && git push"
            if write_only:
                command = "printf 'package manual\\n' > manual.go"
            delta = {"role": "assistant", "tool_calls": [{"index": 0, "id": "call_native", "type": "function", "function": {"name": "bash", "arguments": json.dumps({"command": command, "description": "Write two files, commit twice and push the temporary repository"})}}]}
            finish = "tool_calls"
        chunk = {"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "created": 1793300000, "model": "fixture", "choices": [{"index": 0, "delta": delta, "finish_reason": None}]}
        last = {**chunk, "choices": [{"index": 0, "delta": {}, "finish_reason": finish}], "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
        data = ("data: " + json.dumps(chunk) + "\n\ndata: " + json.dumps(last) + "\n\ndata: [DONE]\n\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def main() -> None:
    schema = "native_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = None
    provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), ModelFixture)
    thread = threading.Thread(target=provider.serve_forever, daemon=True)
    thread.start()
    try:
        assert api.command(["migrate"], environment).returncode == 0, "migration failed"
        setup = api.command(["bootstrap", "--project", "Native test", "--repository", "Native test", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert setup.returncode == 0, "native test setup failed"
        identity = json.loads(setup.stdout)
        process, address = api.start_api(environment)
        base = "http://" + address
        with tempfile.TemporaryDirectory(prefix="workgraph-native-") as temporary:
            temp = Path(temporary)
            repository = temp / "repository"
            repository.mkdir()
            config_dir = temp / "config/opencode"
            environment.update({
                "WORKGRAPH_TOKEN": identity["token"], "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
                "GIT_AUTHOR_NAME": "Native test", "GIT_AUTHOR_EMAIL": "native@example.invalid", "GIT_COMMITTER_NAME": "Native test", "GIT_COMMITTER_EMAIL": "native@example.invalid",
                "XDG_CONFIG_HOME": str(temp / "config"), "XDG_DATA_HOME": str(temp / "data"), "XDG_STATE_HOME": str(temp / "state"), "XDG_CACHE_HOME": str(temp / "cache"),
                "OPENCODE_DISABLE_AUTOUPDATE": "true", "OPENCODE_DISABLE_MODELS_FETCH": "true", "OPENCODE_DISABLE_DEFAULT_PLUGINS": "true", "OPENCODE_DISABLE_EXTERNAL_SKILLS": "true", "OPENCODE_DISABLE_LSP_DOWNLOAD": "true", "OPENCODE_DISABLE_CLAUDE_CODE": "true", "OPENCODE_DISABLE_EMBEDDED_WEB_UI": "true",
            })

            def run(*args: str, timeout: int = 20, input_value: str | None = None) -> subprocess.CompletedProcess[str]:
                return subprocess.run(args, cwd=repository, env=environment, capture_output=True, input=input_value, text=True, timeout=timeout)

            def git(*args: str) -> str:
                result = run("git", *args)
                assert result.returncode == 0, "temporary Git operation failed"
                return result.stdout.strip()

            def collector(*args: str) -> str:
                result = run(str(ROOT / "bin/workgraph"), *args)
                assert result.returncode == 0, "native collector command failed"
                return result.stdout

            git("init", "-b", "main")
            (repository / "base.txt").write_text("base")
            git("add", ".")
            git("commit", "-m", "base")
            git("init", "--bare", str(temp / "remote.git"))
            git("remote", "add", "origin", str(temp / "remote.git"))
            git("push", "-u", "origin", "main")
            collector("init", "--server", base, "--repository-id", identity["repository_id"])
            country, task = str(uuid.uuid4()), str(uuid.uuid4())
            collector("bind", "--country-id", country, "--task-id", task)
            collector("agent", "enable", "opencode", "--config-dir", str(config_dir))
            config = {"autoupdate": False, "share": "disabled", "enabled_providers": ["workgraph-local"], "model": "workgraph-local/fixture", "small_model": "workgraph-local/fixture", "permission": {"*": "deny", "bash": "allow"}, "lsp": False, "formatter": False, "compaction": {"auto": False, "prune": False}, "provider": {"workgraph-local": {"npm": "@ai-sdk/openai-compatible", "name": "Local fixture", "options": {"baseURL": f"http://127.0.0.1:{provider.server_port}/v1", "apiKey": "fixture-local"}, "models": {"fixture": {"name": "Fixture", "tool_call": True, "limit": {"context": 32000, "output": 2048}}}}}}
            (config_dir / "opencode.json").write_text(json.dumps(config))
            binary = str(ROOT / "node_modules/.bin/opencode")
            native = run(binary, "run", "--format", "json", "--model", "workgraph-local/fixture", "Implement native test. DB_PASSWORD=native-never-persist", timeout=55)
            assert native.returncode == 0, "native agent failed: " + native.stderr[-1200:]
            assert ModelFixture.tool_calls == 1, "native fixture tool was not called exactly once"
            assert (repository / "native.go").exists(), "native Bash tool was not executed"
            commits = git("rev-list", "--max-count=2", "HEAD").splitlines()
            assert git("rev-parse", "origin/main") == commits[0], "native push did not reach temporary remote"
            session_id = api.sql(f'SELECT session_id FROM "{schema}".capture_revisions ORDER BY revision DESC LIMIT 1')
            assert session_id, "native session did not reach API; status=" + collector("status") + "; native=" + native.stderr[-1500:]
            request = urllib.request.Request(f'{base}/v1/repositories/{identity["repository_id"]}/sessions/{session_id}', headers={"Authorization": "Bearer " + identity["token"]})
            with urllib.request.urlopen(request, timeout=5) as response:
                stored = json.load(response)
            snapshot = stored["snapshot"]
            assert set(commits).issubset(snapshot["commits"]), "native intermediate commit was lost"
            assert snapshot["messages"][-1]["text"] == "Native final answer after git push", "final native answer after push was lost"
            assert snapshot["task"] == {"country_id": country, "task_id": task}, "Tasktopia binding lost"
            assert "native-never-persist" not in json.dumps(stored), "known secret was persisted"
            assert any(message["role"] == "tool" for message in snapshot["messages"]), "native tool evidence missing"
            assert stored["confirmed_commits"] == [], "client push was misreported as provider confirmation"
            count = api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions')
            private = run(binary, "run", "--format", "json", "--model", "workgraph-local/fixture", "Private no commits", timeout=55)
            assert private.returncode == 0, "private native run failed"
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == count, "unpublished native session was uploaded"
            status = json.loads(collector("status"))
            assert status["queue"]["recordings"] == 2 and status["queue"]["eligible"] == 0, "private capture or delivery state is wrong"
            edited = run(binary, "run", "--format", "json", "--model", "workgraph-local/fixture", "Write without commit", timeout=55)
            assert edited.returncode == 0 and (repository / "manual.go").exists(), "native write-only tool failed"
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == count, "uncommitted native edits published without request"
            git("add", "manual.go")
            git("commit", "-m", "human commits native edit")
            manual_commit = git("rev-parse", "HEAD")
            git("push")
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == "2", "human commit did not publish native edit evidence"
            raw = api.sql(f"SELECT convert_from(canonical,'UTF8') FROM \"{schema}\".capture_revisions WHERE convert_from(canonical,'UTF8')::jsonb->'commits' ? '{manual_commit}' ORDER BY revision DESC LIMIT 1")
            manual_snapshot = json.loads(raw)
            assert "manual.go" in manual_snapshot["files"] and manual_commit in manual_snapshot["commits"], "native path observation was lost"
            # Resume the same real native session through its actual summarize API. The new
            # worktree binding must not retarget the already published original conversation.
            collector("bind", "--country-id", country, "--task-id", str(uuid.uuid4()))
            with socket.socket() as port:
                port.bind(("127.0.0.1", 0))
                native_port = port.getsockname()[1]
            native_origin = f"http://127.0.0.1:{native_port}"
            native_server = subprocess.Popen([binary, "serve", "--hostname", "127.0.0.1", "--port", str(native_port)], cwd=repository, env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            try:
                for _ in range(150):
                    if native_server.poll() is not None:
                        raise AssertionError("Isolated native server stopped")
                    try:
                        with urllib.request.urlopen(native_origin + "/global/health", timeout=1) as response:
                            if response.status == 200:
                                break
                    except (OSError, urllib.error.URLError):
                        time.sleep(0.1)
                else:
                    raise AssertionError("Isolated native server was not ready")
                query = urllib.parse.urlencode({"directory": str(repository)})
                with urllib.request.urlopen(native_origin + "/session?" + query, timeout=5) as response:
                    sessions = json.load(response)
                native_id = min(sessions, key=lambda row: row["time"]["created"])["id"]
                req = urllib.request.Request(native_origin + f"/session/{native_id}/summarize?" + query, data=json.dumps({"providerID": "workgraph-local", "modelID": "fixture"}).encode(), method="POST", headers={"Content-Type": "application/json"})
                with urllib.request.urlopen(req, timeout=45) as response:
                    assert response.status == 200, "Actual native compaction failed"
                for _ in range(100):
                    collector("sync")
                    if api.sql(f'SELECT count(*) FROM "{schema}".capture_continuations') != "0":
                        break
                    time.sleep(0.1)
                else:
                    raise AssertionError("Compacted native history did not produce a continuation")
                child_id = api.sql(f'SELECT session_id FROM "{schema}".capture_continuations ORDER BY segment DESC LIMIT 1')
                req = urllib.request.Request(f'{base}/v1/repositories/{identity["repository_id"]}/sessions/{child_id}', headers={"Authorization": "Bearer " + identity["token"]})
                with urllib.request.urlopen(req, timeout=5) as response:
                    child_view = json.load(response)
                child = child_view["snapshot"]
                assert child["continuation"]["root_session_id"] == session_id and child["continuation"]["reason"] == "compaction"
                assert child_view["parent_reference_verified"], "Exact original snapshot was not independently matched"
                assert child["task"] == snapshot["task"], "Compaction retargeted the original task"
                assert any(message.get("kind") == "agent_summary" and message["text"] == "Native compaction summary" for message in child["messages"])
                assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == "3", "Private session was published or continuation duplicated"
                with urllib.request.urlopen(native_origin + f"/session/{native_id}/message?" + query, timeout=5) as response:
                    native_rows = json.load(response)
                original_tool = next((row["info"]["id"], part["id"]) for row in native_rows for part in row["parts"] if part["type"] == "tool")
                req = urllib.request.Request(native_origin + f"/session/{native_id}/message/{original_tool[0]}/part/{original_tool[1]}?" + query, method="DELETE")
                with urllib.request.urlopen(req, timeout=5) as response:
                    assert response.status == 200, "Isolated native history edit failed"
                for _ in range(100):
                    collector("sync")
                    if api.sql(f"SELECT count(*) FROM \"{schema}\".capture_continuations WHERE reason='history_replacement'") != "0":
                        break
                    time.sleep(0.1)
                else:
                    raise AssertionError("Native part removal did not preserve history through a replacement branch")
                assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == "4"
            finally:
                native_server.terminate()
                try:
                    native_server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    native_server.kill()
                    native_server.wait(timeout=5)
                if native_server.stderr is not None:
                    native_server.stderr.close()
            # Exercise the shipped SDK normalizer and built CLI with >2 MiB of exposed
            # synthetic history. The ordinary human commit/push must also seal its dirty tail.
            module = (ROOT / "build/agents/internal/agent/opencode/assets/workgraph/normalize.js").as_uri()
            generate = f'import {{normalizePages}} from {json.dumps(module)}; const root={json.dumps(str(repository))}; const rows=Array.from({{length:80}},(_,i)=>({{info:{{id:"large-message-"+i,role:"user",model:{{providerID:"workgraph-local",modelID:"fixture"}}}},parts:[{{id:"large-part-"+i,type:"text",text:"Window-"+i+" "+"x".repeat(32768)}}]}})); rows.splice(1,0,{{info:{{id:"large-edit-message",role:"assistant",time:{{completed:1}}}},parts:[{{id:"large-edit",type:"tool",tool:"write",state:{{status:"completed",input:{{filePath:root+"/large.go"}},metadata:{{filepath:root+"/large.go"}},output:"written"}}}}]}}); process.stdout.write(JSON.stringify(normalizePages({{id:"ses_large_contract",time:{{created:1793300000000}}}},rows,root,root)));'
            generated = run("node", "--input-type=module", "-e", generate)
            assert generated.returncode == 0, "Native history window generation failed"
            windows = json.loads(generated.stdout)
            assert len(windows) > 1 and sum(len(message["text"].encode()) for window in windows for message in window["messages"]) > 2 * 1024 * 1024
            window_ids = []
            for window in windows:
                recorded = run(str(ROOT / "bin/workgraph"), "capture", "--pin-source", input_value=json.dumps(window))
                assert recorded.returncode == 0, "Built collector rejected a bounded native window"
                window_ids.append(json.loads(recorded.stdout)["session_id"])
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == "4", "Long private history was uploaded before push"
            (repository / "large.go").write_text("package large\n")
            git("add", "large.go")
            git("commit", "-m", "human commits long native session")
            git("push")
            collector("sync")
            exposed = []
            for identifier in window_ids:
                req = urllib.request.Request(f'{base}/v1/repositories/{identity["repository_id"]}/sessions/{identifier}', headers={"Authorization": "Bearer " + identity["token"]})
                with urllib.request.urlopen(req, timeout=5) as response:
                    window_view = json.load(response)
                exposed.extend(window_view["snapshot"]["messages"])
                if identifier != window_ids[0]:
                    assert window_view["parent_reference_verified"], "Long-history parent reference lost"
            assert len(exposed) == 81 and len({message["text"].split()[0] for message in exposed if message["role"] == "user"}) == 80, "Long history was truncated or duplicated"
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == str(4 + len(windows)), "Another private source inherited long-stream consent"
            collector("agent", "disable", "opencode", "--config-dir", str(config_dir))
            assert (config_dir / "opencode.json").exists(), "provider config removed"
            print(json.dumps({"result": "passed", "agent": "OpenCode 1.19.5", "model": "deterministic loopback fixture", "verified": ["native plugin loading", "actual Bash tool", "two commits and push in one tool", "real API and PostgreSQL", "late final reply", "task binding", "redaction", "private uncommitted session", "human commits native edits", "actual compaction API", "actual history removal event", "typed continuation and exact parent verification", "summary provenance", "continued original task and consent", "SDK-fixture history over 2 MiB without truncation", "human push seals dirty tail", "safe plugin disable"]}))
    finally:
        provider.shutdown()
        provider.server_close()
        if process is not None:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=12)
            if process.stderr is not None:
                process.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
