#!/usr/bin/env python3
"""Exercise a built API process against isolated PostgreSQL without exposing credentials."""

from __future__ import annotations

import datetime as dt
import json
import os
from pathlib import Path
import re
import select
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parent.parent
BASE_DSN = "postgres://workgraph_test:workgraph_test_only@127.0.0.1:55439/workgraph_test?sslmode=disable"


def sql(statement: str) -> str:
    """Execute synthetic test SQL only inside the dedicated Compose database."""
    result = subprocess.run(
        ["docker", "compose", "-f", "compose.test.yaml", "exec", "-T", "postgres",
         "psql", "-U", "workgraph_test", "-d", "workgraph_test", "-v", "ON_ERROR_STOP=1", "-At", "-c", statement],
        cwd=ROOT, capture_output=True, text=True, timeout=15, check=True,
    )
    return result.stdout.strip()


def command(arguments: list[str], environment: dict[str, str]) -> subprocess.CompletedProcess[str]:
    """Capture one-time setup output in memory; never echo its bearer secret."""
    return subprocess.run([str(ROOT / "bin/workgraphd"), *arguments], cwd=ROOT,
                          env=environment, capture_output=True, text=True, timeout=35)


def start_api(environment: dict[str, str], listen: str = "127.0.0.1:0") -> tuple[subprocess.Popen[str], str]:
    """Start a real API process and return its observed bound address, cleaning failed starts."""
    process = subprocess.Popen([str(ROOT / "bin/workgraphd"), "serve", "--listen", listen],
                               cwd=ROOT, env=environment, stdout=subprocess.DEVNULL,
                               stderr=subprocess.PIPE, text=True)
    assert process.stderr is not None
    try:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise AssertionError("API process exited during startup")
            if select.select([process.stderr], [], [], 0.1)[0]:
                match = re.search(r"address=(127\.0\.0\.1:\d+)", process.stderr.readline())
                if match:
                    return process, match.group(1)
        raise AssertionError("API did not announce its listener")
    except BaseException:
        if process.poll() is None:
            process.kill()
        process.wait(timeout=5)
        process.stderr.close()
        raise


def main() -> None:
    """Verify migration, bootstrap, authenticated capture, conflicts and graceful shutdown."""
    schema = "smoke_" + uuid.uuid4().hex
    sql(f'CREATE SCHEMA "{schema}"')
    environment = os.environ.copy()
    environment["WORKGRAPH_DATABASE_URL"] = BASE_DSN + "&search_path=" + schema
    process: subprocess.Popen[str] | None = None
    try:
        migration = command(["migrate"], environment)
        assert migration.returncode == 0, "migration failed"
        setup_args = ["bootstrap", "--project", "Smoke team", "--repository", "Smoke repository",
                      "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"]
        setup = command(setup_args, environment)
        assert setup.returncode == 0, "initial setup failed"
        identity = json.loads(setup.stdout)
        assert command(setup_args, environment).returncode != 0, "second bootstrap accepted"
        process, address = start_api(environment)
        base = "http://" + address

        def request(method: str, path: str, body: object = None, authenticated: bool = True) -> tuple[int, dict]:
            raw = json.dumps(body).encode() if body is not None else None
            headers = {"Content-Type": "application/json"}
            if authenticated:
                headers["Authorization"] = "Bearer " + identity["token"]
            request = urllib.request.Request(base + path, data=raw, headers=headers, method=method)
            try:
                response = urllib.request.urlopen(request, timeout=5)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                return response.status, json.load(response)

        assert request("GET", "/health/live", authenticated=False)[0] == 200
        assert request("GET", "/health/ready", authenticated=False)[0] == 200
        session_id = str(uuid.uuid4())
        standalone_credential = "wg_" + "A" * 42 + "-"
        snapshot = {
            "schema_version": 1, "repository_id": identity["repository_id"],
            "session_id": session_id, "revision": 1,
            "agent": {"name": "smoke-adapter", "model": "synthetic-local"},
            "started_at": dt.datetime.now(dt.timezone.utc).isoformat(),
            "messages": [{"role": "user", "text": "DB_PASSWORD=synthetic-do-not-persist\nObserved credential: " + standalone_credential}],
            "files": ["src/queue.go"], "commits": ["a" * 40],
        }
        assert request("POST", "/v1/captures", snapshot, authenticated=False)[0] == 401
        status, created = request("POST", "/v1/captures", snapshot)
        assert status == 201 and created["created"] is True
        status, retry = request("POST", "/v1/captures", snapshot)
        assert status == 200 and retry["created"] is False and retry["digest"] == created["digest"]
        path = f'/v1/repositories/{identity["repository_id"]}/sessions/{session_id}'
        status, stored = request("GET", path)
        assert status == 200 and stored["confirmed_commits"] == []
        assert "synthetic-do-not-persist" not in json.dumps(stored)
        assert standalone_credential not in json.dumps(stored), "Standalone URL-safe credential reached persisted capture"
        assert "[REDACTED]" in json.dumps(stored)
        snapshot["messages"][0]["text"] = "Updated observation"
        assert request("POST", "/v1/captures", snapshot)[0] == 409
        snapshot["revision"] = 2
        assert request("POST", "/v1/captures", snapshot)[0] == 201
        assert request("GET", path)[1]["snapshot"]["revision"] == 2
        assert request("GET", path + "?revision=1")[1]["snapshot"]["revision"] == 1
        assert sql(f'SELECT count(*) FROM "{schema}".capture_revisions') == "2"
        assert sql(f'SELECT count(*) FROM "{schema}".outbox_events') == "2"

        # Exercise the built collector and ordinary Git hooks through the actual API/database.
        with tempfile.TemporaryDirectory(prefix="workgraph Git space's-") as temporary:
            repository = Path(temporary) / "repository"
            remote = Path(temporary) / "remote.git"
            repository.mkdir()
            collector_environment = environment.copy()
            collector_environment.update({
                "WORKGRAPH_TOKEN": identity["token"], "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Smoke",
                "GIT_AUTHOR_EMAIL": "smoke@example.invalid", "GIT_COMMITTER_NAME": "Smoke",
                "GIT_COMMITTER_EMAIL": "smoke@example.invalid",
            })

            def git(*arguments: str) -> str:
                result = subprocess.run(["git", "-C", str(repository), *arguments],
                                        env=collector_environment, capture_output=True,
                                        text=True, timeout=15, check=True)
                return result.stdout.strip()

            def collector(*arguments: str, observation: object = None) -> dict:
                result = subprocess.run([str(ROOT / "bin/workgraph"), *arguments], cwd=repository,
                                        env=collector_environment, input=json.dumps(observation) if observation else None,
                                        capture_output=True, text=True, timeout=15, check=True)
                return json.loads(result.stdout) if result.stdout.strip() else {}

            git("init", "-b", "main")
            git("init", "--bare", str(remote))
            git("remote", "add", "origin", str(remote))
            collector("init", "--server", base, "--repository-id", identity["repository_id"])
            (repository / "a.go").write_text("first change\n")
            (repository / "b.go").write_text("second change\n")
            local = collector("capture", observation={
                "schema_version": 1, "source_session_id": "synthetic-local-agent", "event_id": "turn-1",
                "agent": {"name": "generic-local", "model": "synthetic-local"},
                "messages": [{"role": "user", "text": "DB_PASSWORD=collector-never-persist"}],
                "changed_files": ["a.go", "b.go"],
            })
            git("add", "a.go")
            git("commit", "-m", "first observed change")
            first = git("rev-parse", "HEAD")
            git("add", "b.go")
            git("commit", "-m", "second observed change")
            second = git("rev-parse", "HEAD")
            assert collector("status")["queue"]["pending"] == 2
            process.terminate()
            assert process.wait(timeout=12) == 0
            assert process.stderr is not None
            process.stderr.close()
            git("push", "origin", "main")
            assert collector("status")["queue"]["pending"] == 2, "offline push lost context"
            assert git("--git-dir", str(remote), "rev-parse", "refs/heads/main") == second
            process, restarted_address = start_api(environment, address)
            assert restarted_address == address
            assert collector("sync", "--retry")["sent"] == 2
            assert collector("status")["queue"]["pending"] == 0
            local_path = f'/v1/repositories/{identity["repository_id"]}/sessions/{local["session_id"]}'
            status, context = request("GET", local_path)
            assert status == 200 and context["snapshot"]["revision"] == 2
            assert set(context["snapshot"]["commits"]) == {first, second}
            assert "task" not in context["snapshot"]
            assert "collector-never-persist" not in json.dumps(context)
            assert context["confirmed_commits"] == [], "local push was confused with provider confirmation"
            collector("disable")
            assert sql(f'SELECT count(*) FROM "{schema}".capture_revisions') == "4"
            assert sql(f'SELECT count(*) FROM "{schema}".outbox_events') == "4"
        process.terminate()
        assert process.wait(timeout=12) == 0, "unclean API shutdown"
        print(json.dumps({"result": "passed", "scope": "built API process + PostgreSQL",
                          "verified": ["migrate", "single bootstrap", "health", "authentication",
                                       "upload", "idempotency", "redaction", "pending commits",
                                       "conflict", "revision history", "atomic outbox", "collector immutable source identity",
                                       "ordinary multi-commit Git hooks", "offline push", "collector retry after API restart",
                                       "exact context ACK", "graceful shutdown"]}))
    finally:
        if process is not None:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            if process.stderr is not None:
                process.stderr.close()
        sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
