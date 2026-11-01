#!/usr/bin/env python3
"""Real Git/CLI/offline queue/API/PostgreSQL/index/MCP with a Git-backed GitHub protocol fixture."""

from __future__ import annotations

import base64
import http.server
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    assert spec and spec.loader
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


api = module("github_api", "scripts/smoke-api.py")
github = module("github_protocol", "scripts/github-fixture.py")


def main():
    schema = "github_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    process = provider = None
    environment = {
        k: v
        for k, v in os.environ.items()
        if k in {"PATH", "TMPDIR", "USER", "LANG", "SHELL"}
    }
    environment.update(
        WORKGRAPH_DATABASE_URL=api.BASE_DSN + "&search_path=" + schema,
        WORKGRAPH_GITHUB_ENABLED="true",
        WORKGRAPH_ENCRYPTION_KEYS=json.dumps(
            {"test": base64.b64encode(bytes([7]) * 32).decode()}
        ),
        WORKGRAPH_ENCRYPTION_ACTIVE_KEY="test",
        GIT_CONFIG_GLOBAL=os.devnull,
        GIT_CONFIG_NOSYSTEM="1",
        GIT_AUTHOR_NAME="GitHub fixture",
        GIT_AUTHOR_EMAIL="fixture@example.invalid",
        GIT_COMMITTER_NAME="GitHub fixture",
        GIT_COMMITTER_EMAIL="fixture@example.invalid",
    )
    try:
        with tempfile.TemporaryDirectory(prefix="gitmora-github-") as temporary:
            folder = Path(temporary)
            repository = folder / "repo"
            remote = folder / "remote.git"
            repository.mkdir()

            def git(*args):
                result = subprocess.run(
                    ["git", *map(str, args)],
                    cwd=repository,
                    env=environment,
                    capture_output=True,
                    text=True,
                    timeout=40,
                )
                assert result.returncode == 0, "Synthetic Git operation failed"
                return result.stdout.strip()

            git("init", "--initial-branch=main")
            git("init", "--bare", remote)
            git("remote", "add", "origin", remote)
            (repository / "README.md").write_text("Synthetic GitHub source\n")
            git("add", "README.md")
            git("commit", "-m", "Initial fixture")
            git("push", "-u", "origin", "main")
            github.GitHubFixture.repository = remote
            github.GitHubFixture.base_sha = git("rev-parse", "HEAD")
            provider = http.server.ThreadingHTTPServer(
                ("127.0.0.1", 0), github.GitHubFixture
            )
            threading.Thread(target=provider.serve_forever, daemon=True).start()
            environment["WORKGRAPH_GITHUB_API_URL"] = (
                f"http://127.0.0.1:{provider.server_port}"
            )
            assert api.command(["migrate"], environment).returncode == 0
            setup = api.command(
                [
                    "bootstrap",
                    "--project",
                    "GitHub integration",
                    "--repository",
                    "Bootstrap fixture",
                    "--gitlab-url",
                    "https://gitlab.internal",
                    "--gitlab-project-id",
                    "42",
                ],
                environment,
            )
            assert setup.returncode == 0
            identity = json.loads(setup.stdout)
            issued = api.command(
                [
                    "issue-token",
                    "--user-id",
                    identity["user_id"],
                    "--scopes",
                    "directory:read,directory:manage,platform:manage,integration:read,integration:manage,capture:read,capture:write",
                ],
                environment,
            )
            assert issued.returncode == 0
            token = json.loads(issued.stdout)["token"]
            environment["WORKGRAPH_TOKEN"] = token
            process, address = api.start_api(environment)
            base = "http://" + address
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

            def request(path, body=None, method=None, extra=None):
                req = urllib.request.Request(
                    base + path,
                    data=json.dumps(body).encode() if body is not None else None,
                    method=method,
                    headers={
                        "Content-Type": "application/json",
                        "Authorization": "Bearer " + token,
                        **(extra or {}),
                    },
                )
                try:
                    response = opener.open(req, timeout=15)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    raw = response.read(4 * 1024 * 1024)
                    return response.status, json.loads(raw) if raw else None

            repo = str(uuid.uuid4())
            assert (
                request(
                    f"/v1/projects/{identity['project_id']}/repositories",
                    dict(
                        request_id=str(uuid.uuid4()),
                        id=repo,
                        name="GitHub fixture",
                        provider="github",
                        provider_url=github.REPOSITORY_URL,
                        provider_repository_id=99,
                    ),
                )[0]
                == 201
            )
            assert (
                request(
                    f"/v1/repositories/{repo}/forge",
                    dict(anonymous=True, webhook_mode="disabled"),
                    "PUT",
                )[0]
                == 200
            )
            install = folder / "bin"
            install.mkdir(mode=0o700)
            result = subprocess.run(
                [
                    str(ROOT / "bin/gitmora"),
                    "self",
                    "install",
                    "--directory",
                    str(install),
                ],
                env=environment,
                capture_output=True,
                text=True,
                timeout=30,
            )
            assert result.returncode == 0
            installed = json.loads(result.stdout)
            client = Path(installed["path"])

            def cli(*args, data=None):
                result = subprocess.run(
                    [str(client), *args],
                    cwd=repository,
                    env=environment,
                    input=json.dumps(data) if data is not None else None,
                    capture_output=True,
                    text=True,
                    timeout=40,
                )
                assert result.returncode == 0, "Native collector operation failed"
                return json.loads(result.stdout) if result.stdout.strip() else None

            cli("init", "--server", base, "--repository-id", repo)
            fake_secret = "github_pat_" + "a" * 32
            observation = dict(
                schema_version=1,
                source_session_id="github-review",
                event_id="review-recorded",
                agent=dict(name="review-fixture", model="synthetic-no-model"),
                messages=[
                    dict(
                        role="assistant",
                        text="GitHubReviewMarker: exact source saved. " + fake_secret,
                    )
                ],
                changed_files=["review.txt"],
            )
            captured = cli("capture", data=observation)
            session = captured["session_id"]
            assert request(f"/v1/repositories/{repo}/sessions/{session}")[0] == 404, (
                "Private context uploaded before consent"
            )
            (repository / "review.txt").write_text(
                "GitHubReviewMarker: durable code source.\n"
            )
            git("add", "review.txt")
            git("commit", "-m", "Review source fixture")
            sha = git("rev-parse", "HEAD")
            process.terminate()
            process.wait(timeout=10)
            process.stderr.close()
            process = None
            git("push", "origin", "main")
            assert cli("status")["queue"]["pending"] > 0, (
                "Offline push lost queued context"
            )
            process, address = api.start_api(environment, address)
            cli("sync", "--retry")
            path = f"/v1/repositories/{repo}/sessions/{session}"
            status, stored = request(path)
            assert (
                status == 200
                and stored["snapshot"]["commits"] == [sha]
                and stored["confirmed_commits"] == []
            )
            assert fake_secret not in json.dumps(stored) and "[REDACTED]" in json.dumps(
                stored
            )
            for _ in range(80):
                result = api.command(["worker", "--once"], environment)
                assert result.returncode == 0, "GitHub worker failed"
                status, stored = request(path)
                _, code = request(f"/v1/repositories/{repo}/code/commits/{sha}")
                _, found = request(
                    "/v1/context/search",
                    dict(project_id=identity["project_id"], text="GitHubReviewMarker"),
                )
                if (
                    stored["confirmed_commits"] == [sha]
                    and code.get("stored") == 1
                    and found.get("items")
                ):
                    _, mr = request(f"/v1/repositories/{repo}/forge/merge-requests/1")
                    if mr.get("commits_complete"):
                        break
            else:
                raise AssertionError(
                    json.dumps(
                        dict(
                            error="GitHub reconciliation incomplete",
                            confirmed=stored.get("confirmed_commits"),
                            code=code,
                            mr=mr if "mr" in locals() else None,
                            requests=github.GitHubFixture.requests[-12:],
                        )
                    )
                )
            assert (
                mr["model"]["head"] == sha
                and mr["source_commits"] == [sha]
                and mr["model"]["merge"] == ""
            )
            status, file = request(
                f"/v1/repositories/{repo}/code/file",
                dict(commit_sha=sha, file_path="review.txt"),
            )
            assert (
                status == 200
                and file["content"] == "GitHubReviewMarker: durable code source.\n"
            )
            status, pack = request(
                "/v1/context/pack",
                dict(
                    project_id=identity["project_id"],
                    repository_id=repo,
                    session_id=session,
                ),
            )
            assert (
                status == 200
                and pack["merge_requests"]["items"]
                and pack["merge_requests"]["items"][0]["url"]
                == github.REPOSITORY_URL + "/pull/1"
            )
            meta = {
                "io.modelcontextprotocol/protocolVersion": "2026-07-28",
                "io.modelcontextprotocol/clientInfo": {
                    "name": "github-fixture",
                    "version": "1",
                },
                "io.modelcontextprotocol/clientCapabilities": {},
            }
            status, mcp = request(
                "/mcp",
                dict(
                    jsonrpc="2.0",
                    id=1,
                    method="tools/call",
                    params=dict(
                        name="context.search",
                        arguments=dict(
                            project_id=identity["project_id"], text="GitHubReviewMarker"
                        ),
                        _meta=meta,
                    ),
                ),
                extra={
                    "Accept": "application/json, text/event-stream",
                    "MCP-Protocol-Version": "2026-07-28",
                    "Mcp-Method": "tools/call",
                    "Mcp-Name": "context.search",
                },
            )
            assert (
                status == 200
                and not mcp["result"].get("isError")
                and mcp["result"]["structuredContent"]["result"]["items"]
            )
            before = request(path)[1]["snapshot"]["revision"]
            cli("sync", "--retry")
            assert request(path)[1]["snapshot"]["revision"] == before
            (repository / "unrelated.txt").write_text("Later human work\n")
            git("add", "unrelated.txt")
            git("commit", "-m", "Unrelated human work")
            git("push", "origin", "main")
            assert request(path)[1]["snapshot"]["commits"] == [sha], (
                "Later human work broadened sealed review attribution"
            )
            process.terminate()
            process.wait(timeout=10)
            process.stderr.close()
            process, address = api.start_api(environment, address)
            assert request(path)[1]["confirmed_commits"] == [sha]
            assert request(f"/v1/repositories/{repo}/forge", method="DELETE")[0] == 204
            assert (
                request(
                    "/v1/context/search",
                    dict(
                        project_id=identity["project_id"],
                        text="GitHubReviewMarker",
                        kinds=["code"],
                    ),
                )[1]["items"]
                == []
            ), "Disconnected provider source remained visible"
            assert request(path)[0] == 200, "Disconnect erased user capture history"
            cli("disable")
            print(
                json.dumps(
                    dict(
                        result="passed",
                        provider="GitHub protocol fixture",
                        real_git_objects=True,
                        standalone_install=True,
                        offline_push=True,
                        exact_commit_confirmation=True,
                        raw_blob_hash=True,
                        source_index=True,
                        pull_request_context=True,
                        http_mcp=True,
                        replay_and_restart=True,
                        unrelated_commit_excluded=True,
                        disconnect_hides_code=True,
                    )
                )
            )
    finally:
        if process is not None:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=10)
            if process.stderr:
                process.stderr.close()
        if provider is not None:
            provider.shutdown()
            provider.server_close()
        github.GitHubFixture.repository = None
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
