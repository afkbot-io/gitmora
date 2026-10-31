#!/usr/bin/env python3
"""Built RP + real signed local OIDC fixture + PostgreSQL + browser cookie jar; synthetic data only."""
import http.cookiejar
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def browser():
    jar = http.cookiejar.CookieJar()
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar), NoRedirect()), jar


def main():
    schema = "identity_smoke_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    process = fixture = None
    environment = {k: v for k, v in os.environ.items() if k in {"PATH","HOME","TMPDIR","USER","LANG","SHELL"}}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    public = f"http://127.0.0.1:{port}"
    try:
        boot = api.command(["bootstrap","--project","Identity fixture","--repository","Fixture","--gitlab-url","https://gitlab.internal","--gitlab-project-id","42"], environment)
        assert boot.returncode == 0
        identity = json.loads(boot.stdout)
        issued = api.command(["issue-token","--user-id",identity["user_id"],"--scopes","directory:read,directory:manage,platform:manage,credentials:manage,capture:read,capture:write"], environment)
        assert issued.returncode == 0
        admin = json.loads(issued.stdout)["token"]
        fixture = subprocess.Popen([str(ROOT / "bin/verification/oidc-fixture"),"--callback",public+"/v1/auth/oidc/callback"], cwd=ROOT,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        issuer = json.loads(fixture.stdout.readline())["issuer"]
        oidc_environment = {**environment,"WORKGRAPH_OIDC_ISSUER":issuer,"WORKGRAPH_OIDC_CLIENT_ID":"workgraph","WORKGRAPH_OIDC_CLIENT_SECRET":"synthetic-oidc-client-secret","WORKGRAPH_PUBLIC_URL":public}
        anonymous, _ = browser()

        def call(path, body=None, method=None, token=None, opener=None, origin=None, csrf=None):
            target = path if path.startswith("http") else public+path
            headers = {"Content-Type":"application/json","Accept":"application/json, text/event-stream"}
            if token: headers["Authorization"] = "Bearer "+token
            if origin: headers["Origin"] = origin
            if csrf: headers["X-Workgraph-CSRF"] = csrf
            request = urllib.request.Request(target,data=json.dumps(body).encode() if body is not None else None,method=method,headers=headers)
            try:
                response = (opener or anonymous).open(request,timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                data = json.loads(raw) if raw and response.headers.get_content_type()=="application/json" else None
                return response.code, data, response.headers

        def control(mode):
            assert call(issuer+"/control",{"mode":mode})[0] == 200

        control("down")
        process, _ = api.start_api(oidc_environment, f"127.0.0.1:{port}")
        assert call("/v1/me",token=admin)[0] == 200  # Startup/capture do not depend on discovery.
        assert call("/v1/auth/login")[0] == 503
        control("good")

        def begin(opener):
            status, _, headers = call("/v1/auth/login",opener=opener)
            assert status == 303
            status, _, response = call(headers["Location"],opener=opener)
            assert status == 303
            return response["Location"]

        missing, _ = browser()
        assert call(begin(missing),opener=missing)[0] == 400
        user = str(uuid.uuid4())
        assert call("/v1/admin/users",{"request_id":str(uuid.uuid4()),"id":user,"display_name":"OIDC reader"},token=admin)[0] == 201
        assert call(f'/v1/projects/{identity["project_id"]}/members/{user}',{"request_id":str(uuid.uuid4()),"expected_version":0,"role":"reader"},"PUT",admin)[0] == 200
        bind = {"request_id":str(uuid.uuid4()),"id":str(uuid.uuid4()),"user_id":user,"subject":"subject-reader"}
        status, receipt, _ = call("/v1/admin/identities",bind,token=admin)
        assert status == 201
        binding_version = receipt["version"]
        assert call("/v1/admin/identities",bind,token=admin)[0] == 200
        principal, jar = browser()
        callback = begin(principal)
        wrong, _ = browser()
        before = call(issuer+"/stats")[1]["token_calls"]
        assert call(callback,opener=wrong)[0] == 400
        assert call(issuer+"/stats")[1]["token_calls"] == before
        status, _, headers = call(callback,opener=principal)
        assert status == 303 and headers["Location"] == public+"/"
        assert call(callback,opener=principal)[0] == 400
        assert call(issuer+"/stats")[1]["token_calls"] == before+1

        def cookie(name):
            return next(value.value for value in jar if value.name == name)

        csrf = cookie("workgraph_csrf_dev")
        session_cookie = cookie("workgraph_session_dev")
        session = call("/v1/auth/session",opener=principal)[1]
        assert session["user_id"] == user
        assert call("/v1/me",opener=principal)[1]["user"]["operator"] is False
        assert call("/v1/admin/users",opener=principal)[0] == 403  # ID token's admin groups/email do not grant roles.
        assert call("/mcp",{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}},opener=principal)[0] == 401
        assert call("/v1/me",token=session_cookie)[0] == 401
        query = {"project_id":identity["project_id"],"text":"fixture"}
        assert call("/v1/context/search",query,opener=principal)[0] == 403
        assert call("/v1/context/search",query,opener=principal,origin="http://attacker.invalid",csrf=csrf)[0] == 403
        assert call("/v1/context/search",query,opener=principal,origin=public,csrf=csrf)[0] == 200
        capture = {"schema_version":1,"repository_id":identity["repository_id"],"session_id":str(uuid.uuid4()),"revision":1,"started_at":"2026-10-30T00:00:00Z","agent":{"name":"fixture","model":"none"},"messages":[{"role":"assistant","text":"Denied writer attempt"}]}
        assert call("/v1/captures",capture,opener=principal,origin=public,csrf=csrf)[0] == 403
        child_request = {"request_id":str(uuid.uuid4()),"label":"Browser delegated key","scopes":["capture:read"],"ttl_seconds":600}
        status, child, _ = call("/v1/me/tokens",child_request,opener=principal,origin=public,csrf=csrf)
        assert status == 201 and child["token"]["kind"] == "api"
        child_secret = child["secret"]
        assert call("/v1/me",token=child_secret)[0] == 200
        process.terminate(); process.wait(timeout=10); process.stderr.close()
        process, _ = api.start_api(oidc_environment,f"127.0.0.1:{port}")
        assert call("/v1/me",opener=principal)[0] == 200
        assert call(f'/v1/admin/identities/{bind["id"]}',{"request_id":str(uuid.uuid4()),"expected_version":binding_version,"enabled":False},"PUT",admin)[0] == 200
        assert call("/v1/me",opener=principal)[0] == 401 and call("/v1/me",token=child_secret)[0] == 401
        current = call("/v1/admin/identities",token=admin)[1]["items"][0]
        assert call(f'/v1/admin/identities/{bind["id"]}',{"request_id":str(uuid.uuid4()),"expected_version":current["version"],"enabled":True},"PUT",admin)[0] == 200
        assert call("/v1/me",token=child_secret)[0] == 401
        assert call(begin(principal),opener=principal)[0] == 303
        csrf = cookie("workgraph_csrf_dev")
        control("bad-nonce")
        assert call(begin(principal),opener=principal)[0] == 400
        assert call("/v1/me",opener=principal)[0] == 200  # Failed relogin leaves the valid original session alone.
        control("good")
        assert call("/v1/auth/logout",{},opener=principal)[0] == 403
        assert call("/v1/auth/logout",{},opener=principal,origin=public,csrf=csrf)[0] == 200
        assert call("/v1/me",opener=principal)[0] == 401
        assert call(begin(principal),opener=principal)[0] == 303
        process.terminate(); process.wait(timeout=10); process.stderr.close()
        process, _ = api.start_api(environment,f"127.0.0.1:{port}")
        assert call("/v1/auth/config")[1]["enabled"] is False
        assert call("/v1/me",opener=principal)[0] == 401 and call("/v1/me",token=admin)[0] == 200
        process.terminate(); process.wait(timeout=10); process.stderr.close()
        process, _ = api.start_api(oidc_environment,f"127.0.0.1:{port}")
        assert call("/v1/me",opener=principal)[0] == 401  # Same configuration restored cannot resurrect old grants.
        assert call(begin(principal),opener=principal)[0] == 303
        view = call("/v1/admin/users/"+user,token=admin)[1]
        assert call("/v1/admin/users/"+user,{"request_id":str(uuid.uuid4()),"expected_version":view["version"],"enabled":False},"PATCH",admin)[0] == 200
        assert call("/v1/me",opener=principal)[0] == 401
        print("PASS built OIDC RP + signed local provider + cookie jar/PostgreSQL: explicit binding, PKCE/nonce/state, CSRF, bearer/MCP separation, restart, logout, account/binding/configuration revocation without resurrection; provider outage leaves bearer API available")
    finally:
        if process is not None and process.poll() is None:
            process.terminate(); process.wait(timeout=10); process.stderr.close()
        if fixture is not None:
            fixture.terminate(); fixture.wait(timeout=10); fixture.stdout.close(); fixture.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
