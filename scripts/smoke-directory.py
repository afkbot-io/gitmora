#!/usr/bin/env python3
"""Built directory/account API against isolated PostgreSQL; no provider/network workflow mutation."""
import importlib.util
import json
import os
from pathlib import Path
import urllib.error
import urllib.request
import uuid

spec = importlib.util.spec_from_file_location("api_smoke", Path(__file__).resolve().parent / "smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def uid():
    return str(uuid.uuid4())


def main():
    schema = "directory_smoke_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {k: v for k, v in os.environ.items() if k in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = None
    try:
        boot = api.command(["bootstrap", "--project", "Initial", "--repository", "Initial", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert boot.returncode == 0
        identity = json.loads(boot.stdout)

        def issue(user):
            result = api.command(["issue-token", "--user-id", user, "--scopes", "directory:read,directory:manage,platform:manage,credentials:manage,capture:read,capture:write"], environment)
            assert result.returncode == 0
            return json.loads(result.stdout)["token"]

        admin = issue(identity["user_id"])
        process, address = api.start_api(environment)

        def request(path, body=None, method=None, token=None):
            req = urllib.request.Request("http://" + address + path, data=json.dumps(body).encode() if body is not None else None, method=method, headers={"Authorization": "Bearer " + (token or admin), "Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(req, timeout=15) as response:
                    return response.status, json.load(response)
            except urllib.error.HTTPError as error:
                return error.code, json.load(error)

        status, me = request("/v1/me", token=identity["token"])
        assert status == 200 and me["user"]["operator"] and len(me["scopes"]) == 4
        assert request("/v1/projects", token=identity["token"])[0] == 403
        other = uid()
        create_user = dict(request_id=uid(), id=other, display_name="Synthetic teammate")
        assert request("/v1/admin/users", create_user)[0] == 201
        view = request("/v1/admin/users/" + other)[1]
        grant = dict(request_id=uid(), expected_version=view["version"], operator=True)
        assert request("/v1/admin/users/" + other + "/operator", grant, "PUT")[0] == 200
        peer = issue(other)
        project, repository = uid(), uid()
        create_project = dict(request_id=uid(), id=project, name="Second project")
        status, receipt = request("/v1/projects", create_project, token=peer)
        assert status == 201
        process.terminate(); process.wait(timeout=10); process.stderr.close()
        process, address = api.start_api(environment)
        status, replay = request("/v1/projects", create_project, token=peer)
        assert status == 200 and replay == receipt
        create_repo = dict(request_id=uid(), id=repository, name="Fixture", gitlab_url="https://gitlab.internal/", gitlab_project_id=99)
        assert request(f"/v1/projects/{project}/repositories", create_repo, token=peer)[0] == 201
        repo = request("/v1/repositories/" + repository)[1]
        assert repo["gitlab_url"] == "https://gitlab.internal" and repo["gitlab_project_id"] == 99
        snapshot = dict(schema_version=1, repository_id=repository, session_id=uid(), revision=1, started_at="2026-10-30T00:00:00Z", agent=dict(name="fixture", model="none"), messages=[dict(role="assistant", text="Synthetic implementation report")])
        assert request("/v1/captures", snapshot, token=peer)[0] == 201
        source_route = f'/v1/repositories/{repository}/sessions/{snapshot["session_id"]}'
        assert request(source_route)[0] == 403  # Global metadata authority grants no content membership.
        assert request("/v1/projects/" + project)[1].get("role", "") == ""
        membership = dict(request_id=uid(), role="admin", expected_version=0)
        status, member_receipt = request(f'/v1/projects/{project}/members/{identity["user_id"]}', membership, "PUT")
        assert status == 200 and request(source_route)[0] == 200
        operator = request("/v1/admin/users/" + other)[1]
        assert request("/v1/admin/users/" + other + "/operator", dict(request_id=uid(), expected_version=operator["version"], operator=False), "PUT")[0] == 200
        assert request("/v1/admin/users", dict(request_id=uid(), id=uid(), display_name="Denied"), token=peer)[0] == 403
        rename = dict(request_id=uid(), expected_version=repo["version"], name="Renamed")
        path = f"/v1/projects/{project}/repositories/{repository}"
        assert request(path, {**rename, "gitlab_project_id":100}, "PATCH", peer)[0] == 400
        status, renamed = request(path, rename, "PATCH", peer)
        assert status == 200
        assert request(path, {**rename, "request_id":uid()}, "PATCH", peer)[0] == 409
        assert request("/v1/projects?limit=1&limit=2")[0] == 422
        root = request("/v1/me")[1]
        assert request('/v1/admin/users/' + identity["user_id"], dict(request_id=uid(), expected_version=root["user"]["version"], enabled=False), "PATCH")[1]["error"]["code"] == "last_installation_operator"

        child_input = dict(request_id=uid(), label="Delegated", scopes=["capture:read","credentials:manage"], ttl_seconds=600)
        status, child = request("/v1/me/tokens", child_input)
        assert status == 201 and child["secret"] and child["token"]["parent_id"] == root["token_id"]
        again = request("/v1/me/tokens", child_input)[1]
        assert not again["created"] and "secret" not in again and again["token"]["id"] == child["token"]["id"]
        status, descendant = request("/v1/me/tokens", dict(request_id=uid(), label="Child", scopes=["capture:read"], ttl_seconds=120), token=child["secret"])
        assert status == 201
        listed = request("/v1/me/tokens")[1]
        assert child["secret"] not in json.dumps(listed) and "secret_hash" not in json.dumps(listed)
        assert request('/v1/me/tokens/' + child["token"]["id"] + '/revoke', dict(request_id=uid()))[0] == 200
        assert request("/v1/me", token=child["secret"])[0] == 401 and request("/v1/me", token=descendant["secret"])[0] == 401
        peer_view = request("/v1/admin/users/" + other)[1]
        assert request("/v1/admin/users/" + other, dict(request_id=uid(), expected_version=peer_view["version"], enabled=False), "PATCH")[0] == 200
        assert request("/v1/me", token=peer)[0] == 401
        disabled = request("/v1/admin/users/" + other)[1]
        assert request("/v1/admin/users/" + other, dict(request_id=uid(), expected_version=disabled["version"], enabled=True), "PATCH")[0] == 200
        assert request("/v1/me", token=peer)[0] == 401  # Enabling an account does not resurrect revoked tokens.
        current = request("/v1/admin/users/" + other)[1]
        assert request("/v1/admin/users/" + other, dict(request_id=uid(), expected_version=current["version"]), "DELETE")[0] == 200
        tombstone = request("/v1/admin/users/" + other)[1]
        assert tombstone["display_name"] == "Deleted user" and tombstone["deleted_at"] and not tombstone["enabled"]
        remove = dict(request_id=uid(), expected_version=renamed["version"])
        status, deleted = request(path, remove, "DELETE")
        assert status == 200 and request(source_route)[0] == 403
        assert request(path, remove, "DELETE")[1] == deleted
        assert request(f"/v1/projects/{project}/repositories", {**create_repo,"request_id":uid()})[0] == 410
        project_view = request("/v1/projects/" + project)[1]
        deletion = dict(request_id=uid(), expected_version=project_view["version"])
        assert request("/v1/projects/" + project, deletion, "DELETE")[0] == 200
        assert request("/v1/projects/" + project, deletion, "DELETE")[0] == 200
        assert request("/v1/projects", {**create_project,"request_id":uid()})[0] == 410
        events = request("/v1/directory/events?limit=100")[1]
        assert len(events["items"]) > 10 and "Synthetic teammate" not in json.dumps(events)
        assert api.command(["grant-operator","--user-id",identity["user_id"]], environment).returncode == 0
        print("PASS built directory/account API: restart/replay, immutable provider IDs, independent operator/project authority, delegated-key revocation, account disable/delete, version conflicts, cascade deletion, tombstones and journal")
    finally:
        if process is not None and process.poll() is None:
            process.terminate(); process.wait(timeout=10); process.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
