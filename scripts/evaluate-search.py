#!/usr/bin/env python3
"""Compare labeled lexical/hybrid search without retaining source text or credentials.

Case file: [{"name":"handoff", "query":{"project_id":"...","text":"..."},
             "expected":[{"session_id":"...","part":"messages/4"}]}].
Expected labels match source fields plus optional part; [] is an irrelevant query.
"""

import argparse
import json
import os
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


def evaluate(items, expected):
    """Return first relevant rank, label recall and false-positive status."""
    covered = set()
    first = 0
    for rank, hit in enumerate(items, 1):
        source = {**hit.get("source", {}), "part": hit.get("part")}
        for index, label in enumerate(expected):
            if all(source.get(key) == value for key, value in label.items()):
                covered.add(index)
                first = first or rank
    return {
        "reciprocal_rank": 1 / first if first else 0,
        "recall": len(covered) / len(expected) if expected else None,
        "negative_returned_hits": bool(items) if not expected else None,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server", required=True)
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--k", type=int, default=10)
    parser.add_argument("--samples", type=int, default=1)
    args = parser.parse_args()
    url = urllib.parse.urlsplit(args.server)
    if (
        url.scheme not in ("http", "https") or not url.netloc or url.username
        or url.password or url.path or url.query or url.fragment
        or url.scheme == "http" and url.hostname not in ("127.0.0.1", "::1", "localhost")
        or not 1 <= args.k <= 50 or not 1 <= args.samples <= 20
    ):
        parser.error("Use an exact HTTPS origin (HTTP only on loopback) and bounded k/samples")
    token = os.environ.get("GITMORA_BENCHMARK_TOKEN", "")
    if not token or len(token) > 256 or any(c in token for c in "\r\n"):
        parser.error("Set a short-lived scoped GITMORA_BENCHMARK_TOKEN")
    if args.cases.stat().st_size > 1024 * 1024:
        parser.error("Case file exceeds 1 MiB")
    cases = json.loads(args.cases.read_text())
    allowed = {"session_id", "repository_id", "commit_sha", "revision", "file_path", "part"}
    if not isinstance(cases, list) or not 1 <= len(cases) <= 200:
        parser.error("Expected 1–200 labeled cases")
    for case in cases:
        if (
            not isinstance(case, dict) or not isinstance(case.get("name"), str)
            or not isinstance(case.get("query"), dict)
            or not isinstance(case.get("expected"), list)
            or len(case["expected"]) > 100
            or any(not isinstance(label, dict) or not label or not set(label) <= allowed
                   for label in case["expected"])
        ):
            parser.error("Invalid case/expected label")

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *unused):
            return None

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    records = []
    for case in cases:
        for mode in ("lexical", "hybrid"):
            for _ in range(args.samples):
                query = {**case["query"], "mode": mode, "limit": args.k}
                query.pop("after", None)
                req = urllib.request.Request(
                    args.server + "/v1/context/search", json.dumps(query).encode(),
                    {"Authorization": "Bearer " + token, "Content-Type": "application/json"},
                )
                started = time.monotonic()
                try:
                    with opener.open(req, timeout=20) as response:
                        raw = response.read(2 * 1024 * 1024 + 1)
                    if len(raw) > 2 * 1024 * 1024:
                        raise ValueError("response exceeds budget")
                    value = json.loads(raw)
                    metrics = evaluate(value["items"], case["expected"])
                    records.append({
                        "case": case["name"], "mode": mode,
                        "actual_mode": value.get("search_mode", "lexical"),
                        "pending": value.get("semantic_pending", 0),
                        "failed": value.get("semantic_failed", 0),
                        "degraded": value.get("semantic_unavailable", False),
                        "ms": round((time.monotonic() - started) * 1000, 1), **metrics,
                    })
                except (OSError, ValueError, KeyError):
                    # Error bodies and request/token data never enter the report.
                    records.append({"case": case["name"], "mode": mode, "error": True})
    summary = {}
    for mode in ("lexical", "hybrid"):
        successful = [r for r in records if r["mode"] == mode and not r.get("error")]
        positive = [r for r in successful if r["recall"] is not None]
        times = sorted(r["ms"] for r in successful)
        summary[mode] = {
            "requests": sum(r["mode"] == mode for r in records), "successes": len(successful),
            "mrr": statistics.mean(r["reciprocal_rank"] for r in positive) if positive else None,
            "recall_at_k": statistics.mean(r["recall"] for r in positive) if positive else None,
            "p50_ms": statistics.median(times) if times else None,
            "p95_ms": times[min(len(times) - 1, int(len(times) * .95))] if times else None,
        }
    print(json.dumps({"k": args.k, "summary": summary, "records": records}, indent=2))


if __name__ == "__main__":
    main()
