#!/usr/bin/env python3
"""Actual browser + embedded API + signed local IdP + isolated PostgreSQL; synthetic source only."""
import argparse
import hashlib
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
    parser.add_argument("--search",action="store_true",help="Exercise search pagination, metadata and code provenance")
    args = parser.parse_args()
    assert not(args.history and args.search)
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
            capture["messages"] += [dict(role="tool", tool_name="bash", text=f"QuotedToolMessage {i}") for i in range(64)]
            capture["messages"][1] = dict(role="tool", tool_name="Bash", text=json.dumps(dict(cmd="git status --short", output="M web/src/views/history/SessionTranscript.tsx", exit_code=0)))
        if args.history:
            capture["messages"][0] = dict(role="user", text="Проверь авторизацию и обнови CLI. Сохрани совместимость.\n\n<img src=x onerror=window.untrustedExecuted=true>\n[unsafe](javascript:alert(1))\n![remote image](https://tracking.invalid/pixel)")
            capture["messages"][2] = dict(role="assistant", text="## Проверка refreshToken\n\nПроверю настройки сервера и запись сессий. Результаты будут привязаны к коммиту.\n\n- Вход через браузер\n- Отправка истории через CLI\n\n```sh\ngitmora auth status --server https://gitmora.example.com\n```\n\n| Компонент | Результат |\n| --- | --- |\n| CLI | Готово |\n| MCP | Готово |")
        if args.history:
            capture["commits"]=["a"*40,"b"*40]
            capture["commit_ranges"]=[dict(commit_sha="a"*40,start=0,end=20,basis="observed_checkpoint"),dict(commit_sha="b"*40,start=20,end=65,basis="observed_checkpoint")]
            capture["messages"][20]=dict(role="user",text="PR4BoundaryMarker: обнови поиск только для этого изменения\n" + "Условия проверки 🧪 "*1800 + "FullUserTailMarker")
            capture["messages"][21]=dict(role="assistant",text="Обновлю поиск и проверю границы PR 4.")
            capture["messages"][22]=dict(role="tool",tool_name="Bash",text=json.dumps(dict(command="git diff --stat",output="x"*50000+"FullToolTailMarker",exit_code=0)))
        assert call("/v1/captures", capture)[0] == 201
        if args.history:
            digest = call(f'/v1/repositories/{identity["repository_id"]}/sessions/{session}')[1]["digest"]
            previous_session = session
            previous_revision = 2
            for index in range(1, 42):
                extra = {**capture, "revision": 1, "session_id": f"eeeeeeee-eeee-4eee-8eee-{index:012x}", "messages": [dict(role="assistant", text=f"Archive marker {index}")]}
                extra.pop("commit_ranges",None)
                if index <= 30:
                    extra["continuation"] = dict(root_session_id=session, previous_session_id=previous_session, previous_revision=previous_revision, previous_digest=digest, segment=index+1, reason="window")
                else:
                    extra["agent"] = dict(name=f"Archive window {index}", model="synthetic-local")
                status, accepted = call("/v1/captures", extra)
                assert status == 201
                if index <= 30:
                    previous_session, previous_revision, digest = extra["session_id"], 1, accepted["digest"]
        if args.search:
            repo=identity["repository_id"];owner=identity["user_id"]
            search_session="dddddddd-dddd-4ddd-8ddd-000000000001"
            messages=[]
            for index in range(45):
                message=dict(role="assistant",text=f"SearchScenarioMarker visible answer {index}",model="model-alpha" if index<25 else "model-beta",provider="fixture",native_id=f"message-{index}")
                if index%3==0:
                    message.update(role="tool",tool_name="write",changes=[dict(path="src/search.ts" if index<25 else "src/other.ts")])
                messages.append(message)
            messages[5]["text"]="SearchScenarioMarker "+"long exposed text "*300+" SearchScenarioTail"
            messages[0]["text"]+=" https://docs.example.test/installation"
            tracked=dict(schema_version=1,repository_id=repo,session_id=search_session,revision=1,started_at="2026-11-03T10:00:00Z",agent=dict(name="opencode",model="model-beta",model_basis="message_metadata"),messages=messages,files=["src/search.ts","src/other.ts"],commits=["a"*40,"b"*40],commit_ranges=[dict(commit_sha="a"*40,start=0,end=25,basis="observed_checkpoint"),dict(commit_sha="b"*40,start=25,end=45,basis="observed_checkpoint")])
            assert call("/v1/captures",tracked)[0]==201
            code_text="export function SearchScenarioCode() { return true; }\n"
            content=code_text.encode();blob=hashlib.sha1(f"blob {len(content)}\0".encode()+content).hexdigest()
            file=dict(identity=dict(repository_id=repo,provider_base_url="https://gitlab.internal",provider_project_id=42,commit_sha="a"*40,path="src/search.ts",blob_id=blob,raw_size=len(content)),raw_sha256=hashlib.sha256(content).hexdigest(),content=code_text,redaction_policy="code-lines-v1",redacted=False)
            raw=json.dumps(file,separators=(',',':'));digest=hashlib.sha256(raw.encode()).hexdigest()
            api.sql(f"UPDATE \"{schema}\".commits SET confirmed_at=now(); INSERT INTO \"{schema}\".forge_connections(repository_id,webhook_mode,secret_envelope,updated_by,last_synced_at,code_checked_at,next_sync_at,code_next_at,code_probe_at) VALUES('{repo}','signature',decode(repeat('00',32),'hex'),'{owner}',now(),now(),now()+interval '1 day',now()+interval '1 day',now()+interval '1 day'); INSERT INTO \"{schema}\".forge_code_runs(repository_id,sha,discovery_ended) VALUES('{repo}','{'a'*40}',true) ON CONFLICT DO NOTHING; INSERT INTO \"{schema}\".forge_code_paths(repository_id,sha,path,status) VALUES('{repo}','{'a'*40}','src/search.ts','stored'); INSERT INTO \"{schema}\".forge_code_snapshots(repository_id,sha,path,digest,blob_id,provider_base_url,provider_project_id,model) VALUES('{repo}','{'a'*40}','src/search.ts','{digest}','{blob}','https://gitlab.internal',42,$fixture${raw}$fixture$); INSERT INTO \"{schema}\".retrieval_jobs(id,source_kind,project_id,repository_id,code_sha,code_path,code_blob_id,source_digest,source_observed_at) VALUES('{uid()}','code','{identity['project_id']}','{repo}','{'a'*40}','src/search.ts','{blob}','{digest}',now());")
        for _ in range(30):
            indexed = api.command(["worker", "--once"], environment)
            assert indexed.returncode == 0, "Synthetic index worker failed"
            if not json.loads(indexed.stdout)["worked"]:
                break
        assert call("/v1/context/search", dict(project_id=identity["project_id"], text="refreshToken"))[1]["items"]
        if args.history:
            repo,owner=identity["repository_id"],identity["user_id"]
            api.sql(f"UPDATE \"{schema}\".commits SET confirmed_at=now(); INSERT INTO \"{schema}\".forge_connections(repository_id,webhook_mode,secret_envelope,updated_by,last_synced_at) VALUES('{repo}','signature',decode(repeat('00',32),'hex'),'{owner}',now());")
            for iid,sha in [(3,"a"*40),(4,"b"*40)]:
                model=dict(id=100+iid,iid=iid,project_id=42,source_project_id=42,state="merged",title=f"PR boundary fixture {iid}",description="Scoped synthetic provider observation",url=f"https://gitlab.internal/-/merge_requests/{iid}",head=sha,base="c"*40,start="d"*40,merge="",squash="",updated_at="2026-10-30T10:00:00Z")
                raw=json.dumps(model,separators=(',',':'));digest=hashlib.sha256(raw.encode()).hexdigest()
                code=dict(Project=42,Source=42,IID=iid,State="merged",Head=sha,Base="c"*40,Start="d"*40,Merge="",Squash="")
                key=hashlib.sha256(json.dumps(code,separators=(',',':')).encode()).hexdigest()
                api.sql(f"INSERT INTO \"{schema}\".forge_mr_codes(repository_id,iid,code_key,complete) VALUES('{repo}',{iid},'{key}',true); INSERT INTO \"{schema}\".forge_mr_observations(repository_id,iid,digest,code_key,model,provider_updated_at) VALUES('{repo}',{iid},'{digest}','{key}',$fixture${raw}$fixture$,now()); INSERT INTO \"{schema}\".forge_merge_requests(repository_id,iid,digest,provider_updated_at) VALUES('{repo}',{iid},'{digest}',now()); INSERT INTO \"{schema}\".forge_mr_commits(repository_id,iid,code_key,source_project_id,sha) VALUES('{repo}',{iid},'{key}',42,'{sha}');")
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
            if args.search:
                payload["search_session"]=search_session
                scoped=api.command(["issue-token","--user-id",user,"--scopes","directory:read,capture:read,integration:read"],environment)
                assert scoped.returncode==0
                payload["read_token"]=json.loads(scoped.stdout)["token"]
            script = "scripts/smoke-search.mjs" if args.search else "scripts/smoke-history.mjs" if args.history else "scripts/smoke-web.mjs"
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
