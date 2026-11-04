#!/usr/bin/env python3
"""Real isolated Codex + installed hooks + loopback Responses fixture + Git + API/PostgreSQL.

This is a protocol/lifecycle test, not a model evaluation. No personal Codex config,
history, account or paid model is used. The test invocation vets its four generated
hook commands before using Codex's automation-only hook-trust option.
"""
from __future__ import annotations

import http.server
import importlib.util
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import tempfile
import threading
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("codex_api_fixture", ROOT / "scripts/smoke-api.py")
assert spec and spec.loader
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


class ResponsesFixture(http.server.BaseHTTPRequestHandler):
    calls = 0
    tool_calls = 0
    tool_names: list[str] = []

    def log_message(self, *_args):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        type(self).calls += 1
        tools = body.get("tools", [])
        type(self).tool_names = [row.get("name", "") for row in tools]
        serialized = json.dumps(body.get("input", []))
        done = 'function_call_output' in serialized
        output = []
        if not done:
            names = type(self).tool_names
            command = "printf 'package fixture\\n' > codex.go && git add codex.go && git commit -m 'Codex fixture commit' && git push"
            if "exec_command" in names:
                name, arguments = "exec_command", {"cmd": command, "yield_time_ms": 30000, "max_output_tokens": 1000}
            elif "shell_command" in names:
                name, arguments = "shell_command", {"command": command}
            elif "shell" in names:
                name, arguments = "shell", {"command": ["/bin/sh", "-lc", command]}
            else:
                name, arguments = "unavailable_fixture_shell", {}
            output = [{"type": "function_call", "id": "fc_gitmorafixture", "call_id": "call_gitmorafixture", "name": name, "arguments": json.dumps(arguments)}]
            type(self).tool_calls += 1
        else:
            output = [{"type": "message", "id": "msg_gitmorafixture", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "Exact Codex final answer sentinel", "annotations": []}]}]
        response = {"id": "resp_" + uuid.uuid4().hex, "object": "response", "created_at": 1793318400, "status": "in_progress", "model": "gitmora-fixture", "output": []}
        events = [{"type": "response.created", "response": response}]
        for index, item in enumerate(output):
            events.append({"type": "response.output_item.added", "output_index": index, "item": item})
            if item["type"] == "message":
                events.append({"type": "response.output_text.delta", "output_index": index, "content_index": 0, "item_id": item["id"], "delta": item["content"][0]["text"]})
            events.append({"type": "response.output_item.done", "output_index": index, "item": item})
        events.append({"type": "response.completed", "response": {**response, "status": "completed", "output": output, "usage": {"input_tokens": 20, "output_tokens": 10, "total_tokens": 30}}})
        data = "".join("event: " + event["type"] + "\ndata: " + json.dumps(event) + "\n\n" for event in events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def main():
    executable = os.environ.get("GITMORA_TEST_CODEX") or shutil.which("codex")
    if not executable:
        bundled = Path("/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex")
        if bundled.is_file(): executable = str(bundled)
    assert executable, "set GITMORA_TEST_CODEX to the installed Codex binary"
    client = Path(os.environ.get("GITMORA_TEST_CLIENT", str(ROOT / "bin/gitmora"))).resolve()
    assert client.is_file(), "selected collector does not exist"
    schema = "codex_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), ResponsesFixture)
    threading.Thread(target=provider.serve_forever, daemon=True).start()
    process = None
    try:
        environment = {key: value for key, value in os.environ.items() if key in {"PATH", "TMPDIR", "USER", "LANG", "SHELL"}}
        environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
        assert api.command(["migrate"], environment).returncode == 0, "migration failed"
        setup = api.command(["bootstrap", "--project", "Codex fixture", "--repository", "Codex fixture", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert setup.returncode == 0, "setup failed"
        identity = json.loads(setup.stdout)
        process, address = api.start_api(environment)
        base = "http://" + address
        with tempfile.TemporaryDirectory(prefix="gitmora-codex-", dir="/tmp") as temporary:
            temp = Path(temporary).resolve()
            repository = temp / "repo"
            repository.mkdir()
            codex_home = temp / "codex"
            codex_home.mkdir()
            environment.update({"HOME": str(temp / "home"), "CODEX_HOME": str(codex_home), "WORKGRAPH_TOKEN": identity["token"], "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Codex fixture", "GIT_AUTHOR_EMAIL": "codex@example.invalid", "GIT_COMMITTER_NAME": "Codex fixture", "GIT_COMMITTER_EMAIL": "codex@example.invalid"})
            Path(environment["HOME"]).mkdir()

            def run(*args, input_value=None, timeout=30):
                # Native plugin refreshes can outlive the foreground CLI. Own the
                # whole process group so no child writes into a removed fixture.
                with subprocess.Popen(args, cwd=repository, env=environment, stdout=subprocess.PIPE,
                                      stderr=subprocess.PIPE, text=True, start_new_session=True) as child:
                    try:
                        stdout, stderr = child.communicate(input_value, timeout=timeout)
                        return subprocess.CompletedProcess(args, child.returncode, stdout, stderr)
                    finally:
                        try:
                            os.killpg(child.pid, signal.SIGTERM)
                            deadline = time.monotonic() + 2
                            while time.monotonic() < deadline:
                                try:
                                    os.killpg(child.pid, 0)
                                except ProcessLookupError:
                                    break
                                time.sleep(0.05)
                            else:
                                os.killpg(child.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass

            def git(*args):
                result = run("git", *args)
                assert result.returncode == 0, "temporary Git operation failed"
                return result.stdout.strip()

            def collector(*args):
                result = run(str(client), *args)
                assert result.returncode == 0, "collector failed: " + result.stderr[-500:]
                return json.loads(result.stdout) if result.stdout.strip() else None

            git("init", "-b", "main")
            (repository / "base.txt").write_text("base")
            git("add", ".")
            git("commit", "-m", "base")
            git("init", "--bare", str(temp / "remote.git"))
            git("remote", "add", "origin", str(temp / "remote.git"))
            git("push", "-u", "origin", "main")
            collector("init", "--server", base, "--repository-id", identity["repository_id"])
            installed = collector("agent", "enable", "codex", "--config-dir", str(codex_home), "--codex", executable)
            assert installed["trust_required"] is True, "product must retain native trust review"
            hooks = json.loads((codex_home / "hooks.json").read_text())
            assert set(hooks["hooks"]) == {"SessionStart", "PreToolUse", "PostToolUse", "Stop"}
            assert all(Path(shlex.split(group["hooks"][0]["command"])[0]).resolve() == client and shlex.split(group["hooks"][0]["command"])[1:] == ["internal-codex-hook", "--codex", executable] for rows in hooks["hooks"].values() for group in rows), "fixture hook source not vetted"
            (codex_home / "config.toml").write_text('model = "gitmora-fixture"\nmodel_provider = "gitmora_fixture"\napproval_policy = "never"\nsandbox_mode = "danger-full-access"\nweb_search = "disabled"\n[model_providers.gitmora_fixture]\nname = "Deterministic loopback fixture"\nbase_url = "http://127.0.0.1:' + str(provider.server_port) + '/v1"\nwire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = false\n')
            native = run(executable, "--dangerously-bypass-hook-trust", "exec", "--json", "--skip-git-repo-check", "Exact Codex user sentinel. DB_PASSWORD=codex-do-not-persist", timeout=70)
            # All diagnostics are synthetic, but raw bearer tokens still stay out of logs.
            safe_diagnostic = native.stderr.replace(identity["token"], "[REDACTED]")
            (ROOT / "build/codex-native-fixture-server.log").write_text(safe_diagnostic)
            assert native.returncode == 0, "isolated native CLI failed; inspect build/codex-native-fixture-server.log"
            assert ResponsesFixture.calls == 2 and ResponsesFixture.tool_calls == 1, "metadata capture triggered unexpected model/tool calls"
            assert (repository / "codex.go").exists(), "native tool not executed; tools=" + json.dumps(ResponsesFixture.tool_names)
            commit = git("rev-parse", "HEAD")
            assert git("rev-parse", "origin/main") == commit, "native push failed"
            # Stop capture completes with the native CLI turn; late publication must be durable.
            collector("sync")
            session = api.sql(f'SELECT session_id FROM "{schema}".capture_revisions ORDER BY created_at DESC LIMIT 1')
            if not session:
                diagnostic = {"native_events": native.stdout.replace(identity["token"], "[REDACTED]").replace("codex-do-not-persist", "[REDACTED]"), "status": collector("status"), "frontiers": collector("frontier", "list")}
                (ROOT / "build/codex-native-capture-diagnostic.json").write_text(json.dumps(diagnostic, indent=2))
            assert session, "automatic native hooks did not deliver; inspect build/codex-native-capture-diagnostic.json"
            request = urllib.request.Request(f'{base}/v1/repositories/{identity["repository_id"]}/sessions/{session}', headers={"Authorization": "Bearer " + identity["token"]})
            with urllib.request.urlopen(request, timeout=5) as response: stored = json.load(response)
            snapshot = stored["snapshot"]
            assert snapshot["agent"]["name"] == "codex" and commit in snapshot["commits"], "native source/commit lost"
            assert snapshot["messages"][-1]["text"] == "Exact Codex final answer sentinel", "native final answer lost"
            assert "codex-do-not-persist" not in json.dumps(stored), "secret persisted"
            collector("agent", "disable", "codex", "--config-dir", str(codex_home))
            assert not json.loads((codex_home / "hooks.json").read_text())["hooks"], "owned native hooks retained"
            print(json.dumps({"result": "passed", "agent": "actual installed Codex", "model": "deterministic loopback Responses fixture", "verified": ["owned hook install", "actual tool and commit/push", "read-only native app-server history", "late final reply", "real API/PostgreSQL", "redaction", "owned hook removal"], "trust": "only vetted temporary test hooks; product requires /hooks review"}))

    finally:
        provider.shutdown(); provider.server_close()
        if process is not None:
            process.terminate(); process.wait(timeout=12)
            if process.stderr: process.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
