#!/usr/bin/env python3
"""Actual pinned native SDK pages -> shipped coordinator/collector -> private durable windows -> API.

All source messages/repositories/accounts are synthetic. The optional native title model is a
labelled loopback protocol fixture, and no production source/model/provider is contacted.
"""
import http.server
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


api = module("api_native_pages", "scripts/smoke-api.py")
native = module("native_page_fixture", "scripts/smoke-opencode.py")
frontiers = module("native_frontier_fixture", "scripts/fixtures/native-frontier.py")


def main():
    schema = "native_pages_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {k: v for k, v in os.environ.items() if k in {"PATH", "HOME", "USER", "LANG", "SHELL", "TMPDIR"}}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = server = None
    model = http.server.ThreadingHTTPServer(("127.0.0.1", 0), native.ModelFixture)
    threading.Thread(target=model.serve_forever, daemon=True).start()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        assert api.command(["migrate"], environment).returncode == 0
        seeded = api.command(["bootstrap", "--project", "Native pagination", "--repository", "Synthetic native source", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert seeded.returncode == 0
        identity = json.loads(seeded.stdout)
        process, address = api.start_api(environment)
        with tempfile.TemporaryDirectory(prefix="gitmora-native-pages-") as directory:
            state = Path(directory)
            repository = state / "repository"
            repository.mkdir()
            config_dir = state / "config/opencode"
            config_dir.mkdir(parents=True)
            environment.update({"WORKGRAPH_TOKEN": identity["token"], "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
                "GIT_AUTHOR_NAME": "Native pages", "GIT_AUTHOR_EMAIL": "native@example.invalid", "GIT_COMMITTER_NAME": "Native pages", "GIT_COMMITTER_EMAIL": "native@example.invalid",
                **{"XDG_" + k + "_HOME": str(state / k.lower()) for k in ("CONFIG", "DATA", "STATE", "CACHE")},
                **{k: "true" for k in ("OPENCODE_DISABLE_AUTOUPDATE", "OPENCODE_DISABLE_MODELS_FETCH", "OPENCODE_DISABLE_DEFAULT_PLUGINS", "OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_LSP_DOWNLOAD", "OPENCODE_DISABLE_CLAUDE_CODE", "OPENCODE_DISABLE_EMBEDDED_WEB_UI")}})
            config = {"autoupdate": False, "share": "disabled", "enabled_providers": ["workgraph-local"], "model": "workgraph-local/fixture", "small_model": "workgraph-local/fixture", "permission": {"*": "deny"}, "lsp": False, "formatter": False, "compaction": {"auto": False, "prune": False},
                "provider": {"workgraph-local": {"npm": "@ai-sdk/openai-compatible", "name": "Local fixture", "options": {"baseURL": f"http://127.0.0.1:{model.server_port}/v1", "apiKey": "fixture"}, "models": {"fixture": {"name": "Fixture", "tool_call": True, "limit": {"context": 32000, "output": 2048}}}}}}
            (config_dir / "opencode.json").write_text(json.dumps(config))

            def command(args, input=None, cwd=repository, timeout=20):
                completed = subprocess.run(args, cwd=cwd, env=environment, input=input, capture_output=True, text=True, timeout=timeout)
                assert completed.returncode == 0, "Synthetic native process failed"
                return completed.stdout

            command(["git", "init", "-q", "-b", "main"])
            command(["git", "remote", "add", "origin", str(state / "unused-remote.git")])
            command([str(ROOT / "bin/workgraph"), "init", "--server", "http://" + address, "--repository-id", identity["repository_id"]])
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                port = listener.getsockname()[1]
            origin = f"http://127.0.0.1:{port}"
            server = subprocess.Popen([str(ROOT / "node_modules/.bin/opencode"), "serve", "--hostname", "127.0.0.1", "--port", str(port)], cwd=repository, env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            for _ in range(150):
                assert server.poll() is None, "Isolated native server exited"
                try:
                    with opener.open(origin + "/global/health", timeout=1) as response:
                        if response.status == 200:
                            break
                except OSError:
                    time.sleep(.1)
            else:
                raise AssertionError("Isolated native server did not become ready")
            query = urllib.parse.urlencode({"directory": str(repository)})

            def request(base, path, body=None, key=None):
                headers = {"Content-Type": "application/json"}
                if key:
                    headers["Authorization"] = "Bearer " + key
                raw = json.dumps(body).encode() if body is not None else None
                req = urllib.request.Request(base + path, data=raw, headers=headers)
                with opener.open(req, timeout=15) as response:
                    return json.load(response)

            session = request(origin, "/session?" + query, {"title": "Synthetic paged source"})
            count = 103
            for index in range(count):
                request(origin, f'/session/{session["id"]}/message?' + query, {"noReply": True, "parts": [{"type": "text", "text": f"NativePage-{index} " + "x" * 32768}]})
            compiled = ROOT / "build/agents/internal/agent/opencode/assets/workgraph"
            script = """
import {createOpencodeClient} from '@opencode-ai/sdk';
const {Coordinator}=await import(process.env.PAGE_COORDINATOR);
const {LocalCollector}=await import(process.env.PAGE_COLLECTOR);
const sdk=createOpencodeClient({baseUrl:process.env.PAGE_ORIGIN});
const trace=[]; const observations=[];let reports=0;
const source={session:{get:q=>sdk.session.get(q),messages:async q=>{trace.push({limit:q.query.limit,before:q.query.before});return sdk.session.messages(q);}}};
const local=new LocalCollector(process.env.PAGE_CLI);
const coordinator=new Coordinator(source,{probe:d=>local.probe(d),begin:(d,s,c)=>local.begin(d,s,c),end:(d,s,c)=>local.end(d,s,c),frontiers:d=>local.frontiers(d),apply:(d,id)=>local.apply(d,id),record:async(d,o,ids,deliver)=>{observations.push(o);return local.record(d,o,ids,deliver);}},process.env.PAGE_DIRECTORY,()=>{reports++});
await coordinator.before(process.env.PAGE_SESSION,'source-page-observation');
if(reports||trace.length!==2||trace[0].limit!==100||trace[0].before!==undefined||!trace[1].before)throw new Error('Native SDK pagination not exercised');
process.stdout.write(JSON.stringify({windows:observations.length,messages:observations.flatMap(o=>o.messages).map(m=>m.text.split(' ')[0]),sdk_pages:trace.length}));
"""
            environment.update(PAGE_COORDINATOR=(compiled / "coordinator.js").as_uri(), PAGE_COLLECTOR=(compiled / "collector.js").as_uri(), PAGE_ORIGIN=origin, PAGE_CLI=str(ROOT / "bin/workgraph"), PAGE_DIRECTORY=str(repository), PAGE_SESSION=session["id"])
            observed = json.loads(command(["node", "--input-type=module", "-e", script], cwd=ROOT, timeout=60))
            assert observed["windows"] > 1 and observed["messages"] == [f"NativePage-{i}" for i in range(count)], "Actual source history lost/reordered native rows"
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == "0", "Paged private history was uploaded without publication consent"
            crash = frontiers.exercise(ROOT, environment, repository, command, request, origin, query)
            result = json.loads(command([str(ROOT / "bin/workgraph"), "sync", "--include-uncommitted"]))
            assert result["failed"] == 0
            ids = api.sql(f'SELECT string_agg(session_id::text,\',\' ORDER BY session_id) FROM "{schema}".capture_sessions').split(",")
            assert len(ids) == observed["windows"] + 1
            windows = []
            for identifier in ids:
                saved = request("http://" + address, f'/v1/repositories/{identity["repository_id"]}/sessions/{identifier}', key=identity["token"])
                if saved["snapshot"]["messages"][0]["text"] == "Durable tool boundary source":
                    continue
                segment = saved["snapshot"].get("continuation", {}).get("segment", 0)
                if segment:
                    assert saved["parent_reference_verified"], "Paged capture parent did not verify"
                windows.append((segment, saved["snapshot"]["messages"]))
            assert [m["text"] for _, messages in sorted(windows) for m in messages] == [f"NativePage-{index} " + "x" * 32768 for index in range(count)], "Native source text changed or was silently clipped"
            revisions = api.sql(f'SELECT count(*) FROM "{schema}".capture_revisions')
            process.terminate(); process.wait(timeout=10); process.stderr.close()
            process, address = api.start_api(environment, address)
            replayed = json.loads(command(["node", "--input-type=module", "-e", script], cwd=ROOT, timeout=60))
            assert replayed == observed
            command([str(ROOT / "bin/workgraph"), "sync"])
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_sessions') == str(len(ids)), "SDK/CLI/API restart duplicated source windows"
            assert api.sql(f'SELECT count(*) FROM "{schema}".capture_revisions') == revisions, "Unchanged native replay added a source revision"
            print(json.dumps({"result": "passed", "native": "OpenCode 1.19.5 + actual root SDK", "native_rows": count, "raw_source_bytes_over": 3 * 1024 * 1024, "sdk_pages": observed["sdk_pages"], "windows": observed["windows"], "publication": "explicit_only", "replay": "same_windows_after_cli_api_restart", "crash_recovery": crash}))
    finally:
        for child in (server, process):
            if child:
                if child.poll() is None:
                    child.terminate()
                    try:
                        child.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        child.kill(); child.wait(timeout=5)
                if child.stderr:
                    child.stderr.close()
        model.shutdown(); model.server_close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
