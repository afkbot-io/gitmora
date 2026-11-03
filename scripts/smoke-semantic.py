#!/usr/bin/env python3
"""Opt-in real CPU model + built API/CLI/MCP acceptance in a disposable DB schema.

Provision Ollama embeddinggemma:300m, then run with --embedding-url on loopback.
No production server or provider credentials are used. Reports contain no source text.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import urllib.error
import urllib.request
import uuid

from native_context_fixture import verify as verify_native

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--embedding-url", default="http://127.0.0.1:11436")
    parser.add_argument("--model", default="embeddinggemma:300m")
    parser.add_argument("--dimensions", type=int, default=768)
    args = parser.parse_args()
    # The test fixture uses an explicit operator-owned endpoint; never a source URL.
    with urllib.request.urlopen(args.embedding_url + "/api/tags", timeout=10) as response:
        tags = json.load(response)
    digest = next(m["digest"] for m in tags["models"] if m["name"] == args.model)
    warm = urllib.request.Request(args.embedding_url + "/api/embed", json.dumps({
        "model": args.model, "input": "task: search result | query: session renewal",
        "truncate": False, "keep_alive": "30m", "options": {"num_thread": 2},
    }).encode(), {"Content-Type": "application/json"})
    with urllib.request.urlopen(warm, timeout=60) as response:
        assert len(json.load(response)["embeddings"][0]) == args.dimensions
    schema = "semantic_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    env = {k: v for k, v in os.environ.items() if k in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}}
    env.update({
        "WORKGRAPH_DATABASE_URL": api.BASE_DSN + "&search_path=" + schema,
        "WORKGRAPH_EMBEDDING_URL": args.embedding_url,
        "WORKGRAPH_EMBEDDING_MODEL": args.model,
        "WORKGRAPH_EMBEDDING_REVISION": digest,
        "WORKGRAPH_EMBEDDING_DIMENSIONS": str(args.dimensions),
        "WORKGRAPH_EMBEDDING_QUERY_PREFIX": "task: search result | query: ",
        "WORKGRAPH_EMBEDDING_PASSAGE_PREFIX": "title: none | text: ",
    })
    process = None
    try:
        assert api.command(["migrate"], env).returncode == 0
        setup = api.command(["bootstrap", "--project", "Semantic acceptance", "--repository", "example",
                             "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], env)
        assert setup.returncode == 0
        identity = json.loads(setup.stdout)
        process, address = api.start_api(env)
        base = "http://" + address

        def request(route, body):
            req = urllib.request.Request(base + route, json.dumps(body).encode(), {
                "Authorization": "Bearer " + identity["token"], "Content-Type": "application/json",
            })
            try:
                response = urllib.request.urlopen(req, timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                return response.status, json.load(response)

        session = str(uuid.uuid4())
        a, b = "a" * 40, "b" * 40
        texts = [
            "Нужно сохранять вход пользователя, когда заканчивается срок доступа.",
            "We keep the existing login valid by rotating the renewal credential. refreshToken replaces the expired access token without signing the person out.",
            "The edit to src/auth.go adds background credential rotation and retries an expired access token once.",
            "The authentication decision is complete. The next change belongs to another commit.",
            "For exporting invoices, stream CSV rows instead of buffering the whole report in memory.",
            "The database query now has an index on invoice_date to avoid scanning every bill.",
            "The deployment uses a health probe before forwarding HTTP requests to a new container.",
            "Use exponential delay and jitter after the provider returns a rate limit response.",
        ]
        snapshot = {
            "schema_version": 1, "repository_id": identity["repository_id"], "session_id": session,
            "revision": 1, "started_at": "2026-11-03T10:00:00Z",
            "agent": {"name": "opencode", "model": "unknown"},
            "messages": [{"role": "assistant", "text": text, "model": "alpha" if i < 4 else "beta",
                          "provider": "fixture", "native_id": f"native-{i}"} for i, text in enumerate(texts)],
            "files": [], "commits": [a, b],
            "commit_ranges": [{"commit_sha": a, "start": 0, "end": 4, "basis": "observed_checkpoint"},
                              {"commit_sha": b, "start": 4, "end": 8, "basis": "observed_checkpoint"}],
        }
        status, accepted = request("/v1/captures", snapshot)
        assert status == 201, f"synthetic session ingestion failed: {status} {accepted}"
        api.sql(f'UPDATE "{schema}".commits SET confirmed_at=now()')
        assert api.command(["worker", "--once"], env).returncode == 0
        for _ in range(20):
            step = api.command(["embedding-worker", "--once"], env)
            assert step.returncode == 0, "embedding batch failed"
            if not json.loads(step.stdout)["worked"]:
                break
        else:
            raise AssertionError("embedding queue did not finish")
        query = {"project_id": identity["project_id"], "repository_id": identity["repository_id"],
                 "text": "Как продлить действующую авторизацию без повторного ввода пароля?", "limit": 5}
        status, lexical = request("/v1/context/search", {**query, "mode": "lexical"})
        assert status == 200 and not lexical["items"], "paraphrase fixture unexpectedly shares lexical terms"
        status, semantic = request("/v1/context/search", query)
        assert status == 200 and semantic["search_mode"] == "hybrid" and not semantic.get("semantic_pending"), "semantic search incomplete"
        hit = next((h for h in semantic["items"][:3] if h["part"] == "messages/1"), None)
        assert hit is not None, "real model did not retrieve the labeled decision in top three"
        assert hit["metadata"]["model"] == "alpha" and hit["metadata"]["native_id"] == "native-1"
        status, expanded = request("/v1/context/expand", {"evidence_id": hit["id"], "limit": 2048})
        assert status == 200 and expanded["content"] == texts[1] and expanded["source"]["digest"] == accepted["digest"]
        status, scoped = request("/v1/context/search", {**query, "commit_sha": a})
        assert status == 200 and all(int(h["part"].split("/")[1]) < 4 for h in scoped["items"])
        status, exact = request("/v1/context/search", {**query, "text": a})
        assert status == 200 and exact["search_mode"] == "lexical" and len(exact["items"]) == 4

        client_env = {**env, "WORKGRAPH_TOKEN": identity["token"]}
        cli = subprocess.run([str(ROOT / "bin/gitmora"), "search", query["text"], "--server", base,
                              "--project", identity["project_id"]], env=client_env, cwd=ROOT,
                             capture_output=True, text=True, timeout=30)
        assert cli.returncode == 0 and identity["token"] not in cli.stdout + cli.stderr
        assert hit["id"] in {h["id"] for h in json.loads(cli.stdout)["items"]}
        cases = [{"name": "why-and-handoff", "query": query, "expected": [{"session_id": session, "part": "messages/1"}]},
                 {"name": "invoice-memory", "query": {**query, "text": "Как выгрузить счета без большого расхода оперативки?"}, "expected": [{"session_id": session, "part": "messages/4"}]},
                 {"name": "retry-backoff", "query": {**query, "text": "How to wait after too many requests?"}, "expected": [{"session_id": session, "part": "messages/7"}]},
                 {"name": "exact-identifier", "query": {**query, "text": "refreshToken"}, "expected": [{"session_id": session, "part": "messages/1"}]},
                 {"name": "unrelated", "query": {**query, "text": "Рецепт клубничного пирога в духовке"}, "expected": []}]
        report_dir = ROOT / "build/semantic"
        report_dir.mkdir(parents=True, exist_ok=True)
        fixture = report_dir / "cases.json"
        fixture.write_text(json.dumps(cases))
        evaluated = subprocess.run(["python3", str(ROOT / "scripts/evaluate-search.py"), "--server", base,
                                    "--cases", str(fixture), "--samples", "3", "--k", "3"],
                                   env={**env, "GITMORA_BENCHMARK_TOKEN": identity["token"]},
                                   capture_output=True, text=True, timeout=180)
        assert evaluated.returncode == 0 and identity["token"] not in evaluated.stdout + evaluated.stderr
        report = json.loads(evaluated.stdout)
        (report_dir / "quality.json").write_text(json.dumps(report, indent=2) + "\n")
        # Real native stdio bridge exercises the same model-aware schema and revocation.
        verify_native(ROOT, base, identity["token"], {**query, "limit": 1}, hit["id"],
                      lambda: api.sql(f'UPDATE "{schema}".api_tokens SET revoked_at=now()'))
        print(json.dumps({"result": "passed", "model": args.model, "dimensions": args.dimensions,
                          "semantic_paraphrase": True, "exact_expansion": True, "commit_scope": True,
                          "cli": True, "native_mcp": True, "revocation": True, "quality": report["summary"]}))
    finally:
        if process:
            process.terminate()
            process.wait(timeout=10)
            process.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
