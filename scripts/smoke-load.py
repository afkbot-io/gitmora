#!/usr/bin/env python3
"""Bounded synthetic API/MCP load against an owned PostgreSQL schema and real API/index worker.

Never accepts a production origin, database or credential. Results describe this dataset/host,
not an unqualified capacity guarantee. All credentials remain in process memory.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import importlib.util
import json
import math
import os
from pathlib import Path
import statistics
import subprocess
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("load_api", ROOT / "scripts/smoke-api.py")
assert spec and spec.loader
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sessions", type=int, default=1000)
    parser.add_argument("--seconds", type=int, default=20)
    parser.add_argument("--clients", type=int, nargs="+", default=[1, 10, 25, 50])
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    assert 50 <= args.sessions <= 5000 and 2 <= args.seconds <= 60
    assert args.clients and all(1 <= n <= 50 for n in args.clients)
    schema = "load_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {
        key: value
        for key, value in os.environ.items()
        if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}
    }
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = worker = None
    failures: list[str] = []
    try:
        assert api.command(["migrate"], environment).returncode == 0, "Migration failed"
        setup = api.command(
            [
                "bootstrap",
                "--project",
                "Synthetic load",
                "--repository",
                "Load source",
                "--gitlab-url",
                "https://gitlab.internal",
                "--gitlab-project-id",
                "42",
            ],
            environment,
        )
        assert setup.returncode == 0, "Synthetic bootstrap failed"
        identity = json.loads(setup.stdout)
        repository, project = identity["repository_id"], identity["project_id"]
        process, address = api.start_api(environment)
        base = "http://" + address
        actor_count = max(args.clients)
        actors = []
        # Fixture membership is separate for every actor; the token goes through the real authentication store.
        for _ in range(actor_count):
            user = str(uuid.uuid4())
            api.sql(
                f"INSERT INTO \"{schema}\".users(id,display_name)VALUES('{user}','Load actor');INSERT INTO \"{schema}\".memberships(project_id,user_id,role)VALUES('{project}','{user}','reader')"
            )
            issued = api.command(
                [
                    "issue-token",
                    "--user-id",
                    user,
                    "--label",
                    "Synthetic load",
                    "--scopes",
                    "capture:read,directory:read,integration:read",
                    "--ttl",
                    "1h",
                ],
                environment,
            )
            assert issued.returncode == 0, "Synthetic credential setup failed"
            actors.append((user, json.loads(issued.stdout)["token"]))

        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

        def request(path: str, body=None, token: str | None = None, headers=None):
            selected = {
                "Authorization": "Bearer " + (token or identity["token"]),
                "Content-Type": "application/json",
                **(headers or {}),
            }
            req = urllib.request.Request(
                base + path,
                data=json.dumps(body).encode() if body is not None else None,
                headers=selected,
            )
            try:
                response = opener.open(req, timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read(4 * 1024 * 1024 + 1)
                assert len(raw) <= 4 * 1024 * 1024, "Oversized response"
                return response.status, json.loads(raw) if raw else None

        sessions = [str(uuid.uuid4()) for _ in range(args.sessions)]
        marker = "gitmoraLoad" + uuid.uuid4().hex[:12]
        content = (
            marker
            + " retry commit evidence queue exact source. "
            + "Контекст разработки. 🧪\n"
        ) * 32
        snapshots = [
            dict(
                schema_version=1,
                repository_id=repository,
                session_id=session,
                revision=1,
                started_at="2026-11-01T00:00:00Z",
                agent=dict(name="load-fixture", model="synthetic-no-model"),
                messages=[
                    dict(role="user", text=content),
                    dict(role="assistant", text=content),
                ],
                files=["src/queue.go"],
                commits=["a" * 40],
            )
            for session in sessions
        ]

        def seed(snapshot):
            status, result = request("/v1/captures", snapshot)
            assert status == 201 and result["created"], "Synthetic ingestion failed"

        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            list(pool.map(seed, snapshots))
        repeated, receipt = request("/v1/captures", snapshots[0])
        assert repeated == 200 and not receipt["created"], (
            "ACK replay duplicated a revision"
        )
        worker = subprocess.Popen(
            [str(ROOT / "bin/gitmorad"), "worker"],
            cwd=ROOT,
            env=environment,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            pending = int(
                api.sql(
                    f'SELECT count(*) FROM "{schema}".retrieval_jobs WHERE completed_at IS NULL'
                )
            )
            if pending == 0:
                break
            assert worker.poll() is None, "Index worker exited"
            time.sleep(0.2)
        else:
            raise AssertionError("Indexing did not complete")
        worker.terminate()
        worker.wait(timeout=10)
        worker = None
        rows = int(api.sql(f'SELECT count(*) FROM "{schema}".retrieval_chunks'))
        status, found = request(
            "/v1/context/search", dict(project_id=project, text=marker), actors[0][1]
        )
        assert (
            status == 200
            and found["items"]
            and found["pending_sources_in_project"] == 0
        )
        evidence = found["items"][0]
        assert (
            evidence["source"]["project_id"] == project
            and not evidence["confirmed_commits"]
        ), "Unverified SHA became provider-confirmed"
        calls = [
            (
                "search",
                "/v1/context/search",
                dict(project_id=project, text=marker, limit=10),
            ),
            (
                "browse",
                "/v1/context/browse",
                dict(
                    project_id=project,
                    repository_id=repository,
                    kind="session",
                    limit=10,
                ),
            ),
            (
                "expand",
                "/v1/context/expand",
                dict(evidence_id=evidence["id"], limit=120),
            ),
            (
                "pack",
                "/v1/context/pack",
                dict(
                    project_id=project,
                    repository_id=repository,
                    session_id=sessions[0],
                    limit=2,
                ),
            ),
        ]
        headers = {
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": "2026-07-28",
            "Mcp-Method": "tools/call",
            "Mcp-Name": "context.search",
        }
        meta = {
            "io.modelcontextprotocol/protocolVersion": "2026-07-28",
            "io.modelcontextprotocol/clientInfo": {
                "name": "bounded-load",
                "version": "1",
            },
            "io.modelcontextprotocol/clientCapabilities": {},
        }
        mcp_body = dict(
            jsonrpc="2.0",
            id=1,
            method="tools/call",
            params=dict(
                name="context.search",
                arguments=dict(project_id=project, text=marker, limit=5),
                _meta=meta,
            ),
        )
        phases = []
        for clients in args.clients:
            start = threading.Barrier(clients + 1)
            deadline_holder = [0.0]

            def actor(index):
                token = actors[index][1]
                samples: dict[str, list[float]] = {
                    kind: []
                    for kind in ["search", "browse", "expand", "pack", "mcp_search"]
                }
                local_failures = []
                start.wait()
                n = index
                while time.monotonic() < deadline_holder[0]:
                    kind, path, body = (
                        calls[n % len(calls)]
                        if n % 5 != 4
                        else ("mcp_search", "/mcp", mcp_body)
                    )
                    before = time.perf_counter()
                    try:
                        status, value = request(
                            path, body, token, headers if kind == "mcp_search" else None
                        )
                        assert status == 200, (
                            "HTTP "
                            + str(status)
                            + " "
                            + str(
                                value.get("error", {}).get("code", "")
                                if isinstance(value, dict)
                                and isinstance(value.get("error"), dict)
                                else ""
                            )
                        )
                        if kind == "mcp_search":
                            assert not value.get("error") and not value["result"].get(
                                "isError"
                            )
                            result = value["result"]["structuredContent"]["result"]
                            assert result["items"] and all(
                                hit["source"]["project_id"] == project
                                for hit in result["items"]
                            )
                        elif kind == "search":
                            assert value["items"] and all(
                                hit["source"]["project_id"] == project
                                for hit in value["items"]
                            )
                        elif kind == "browse":
                            assert value["items"] and all(
                                hit["query"]["project_id"] == project
                                for hit in value["items"]
                            )
                        elif kind == "expand":
                            assert (
                                value["source"]["digest"]
                                == evidence["source"]["digest"]
                                and value["content"]
                                == content[value["start"] : value["end"]]
                            )
                        else:
                            assert (
                                value["captures"]["items"][0]["source"]["session_id"]
                                == sessions[0]
                            )
                    except Exception as error:
                        local_failures.append(
                            kind
                            + ": "
                            + (
                                str(error)
                                if isinstance(error, AssertionError)
                                else type(error).__name__
                            )
                        )
                    samples[kind].append((time.perf_counter() - before) * 1000)
                    n += 1
                return samples, local_failures

            with concurrent.futures.ThreadPoolExecutor(max_workers=clients) as pool:
                tasks = [pool.submit(actor, i) for i in range(clients)]
                began = time.monotonic()
                deadline_holder[0] = began + args.seconds
                start.wait()
                results = [task.result() for task in tasks]
            elapsed = time.monotonic() - began
            metrics = {}
            for kind in ["search", "browse", "expand", "pack", "mcp_search"]:
                values = sorted(v for samples, _ in results for v in samples[kind])
                if values:
                    metrics[kind] = dict(
                        requests=len(values),
                        p50_ms=round(statistics.median(values), 2),
                        p95_ms=round(values[math.ceil(len(values) * 0.95) - 1], 2),
                        p99_ms=round(values[math.ceil(len(values) * 0.99) - 1], 2),
                        max_ms=round(values[-1], 2),
                    )
            errors = [item for _, items in results for item in items]
            phase = dict(
                clients=clients,
                seconds=round(elapsed, 2),
                requests=sum(m["requests"] for m in metrics.values()),
                errors=len(errors),
                error_examples=sorted(set(errors))[:10],
                operations=metrics,
            )
            phase["requests_per_second"] = round(phase["requests"] / elapsed, 2)
            phases.append(phase)
            print(json.dumps(phase), flush=True)
            failures.extend(errors)
        # Check durable data after API restart and deny a removed actor before any result is returned.
        process.terminate()
        process.wait(timeout=10)
        process.stderr.close()
        process, address = api.start_api(environment, address)
        assert request(
            "/v1/context/search", dict(project_id=project, text=marker), actors[0][1]
        )[1]["items"]
        api.sql(
            f"DELETE FROM \"{schema}\".memberships WHERE project_id='{project}' AND user_id='{actors[0][0]}'"
        )
        assert (
            request(
                "/v1/context/search",
                dict(project_id=project, text=marker),
                actors[0][1],
            )[0]
            == 403
        )
        assert (
            int(api.sql(f'SELECT count(*) FROM "{schema}".capture_revisions'))
            == args.sessions
        )
        result = dict(
            result="passed" if not failures else "failed",
            dataset=dict(
                sessions=args.sessions,
                indexed_chunks=rows,
                actors=actor_count,
                canonical_bytes=sum(len(json.dumps(s).encode()) for s in snapshots),
            ),
            phases=phases,
            restart_durable=True,
            membership_revocation=True,
            duplicate_replay_safe=True,
            unconfirmed_sha_not_promoted=True,
            production_target=False,
        )
        if args.output:
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(json.dumps(result, indent=2) + "\n")
        assert not failures, (
            "Bounded load contained failures; inspect the content-free report"
        )
    finally:
        for child in [worker, process]:
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
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
