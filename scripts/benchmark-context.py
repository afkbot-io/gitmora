#!/usr/bin/env python3
"""Read-only context latency probe. Supply a scoped token in GITMORA_BENCHMARK_TOKEN."""
import argparse
import json
import math
import os
import re
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args, **_kwargs):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--server', required=True)
    parser.add_argument('--project', required=True)
    parser.add_argument('--repository', required=True)
    parser.add_argument('--session')
    parser.add_argument('--query', action='append', required=True)
    parser.add_argument('--samples', type=int, default=12)
    args = parser.parse_args()
    url = urllib.parse.urlsplit(args.server)
    if url.scheme not in {'https', 'http'} or not url.hostname or url.username or url.password or url.query or url.fragment or url.path not in {'', '/'}:
        parser.error('Use the HTTPS origin of your Gitmora server')
    if url.scheme == 'http' and url.hostname not in {'localhost', '127.0.0.1', '::1'}:
        parser.error('HTTP is supported only on loopback')
    for value in [args.project, args.repository, *([args.session] if args.session else [])]:
        if not re.fullmatch(r'[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}', value):
            parser.error('Expected UUID identifiers')
    token = os.environ.get('GITMORA_BENCHMARK_TOKEN', '')
    if not token or not 1 <= args.samples <= 50 or len(args.query) > 10:
        parser.error('Provide a scoped token, 1–50 samples and at most ten queries')
    opener = urllib.request.build_opener(NoRedirect())
    checks = [(f'search_{i+1}', '/v1/context/search', dict(project_id=args.project, repository_id=args.repository, text=query, limit=20)) for i, query in enumerate(args.query)]
    if args.session:
        checks.append(('session', f'/v1/repositories/{args.repository}/sessions/{args.session}', None))
    results = {}
    try:
        for name, path, body in checks:
            elapsed, sizes = [], []
            for _ in range(args.samples):
                request = urllib.request.Request(args.server.rstrip('/') + path, data=json.dumps(body).encode() if body is not None else None, headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
                start = time.perf_counter()
                with opener.open(request, timeout=30) as response:
                    raw = response.read(4 * 1024 * 1024 + 1)
                if len(raw) > 4 * 1024 * 1024:
                    raise ValueError('Response exceeded probe budget')
                value = json.loads(raw)
                if not isinstance(value, dict):
                    raise ValueError('Invalid response')
                elapsed.append((time.perf_counter() - start) * 1000)
                sizes.append(len(raw))
            results[name] = dict(samples=len(elapsed), p50_ms=round(statistics.median(elapsed), 1), p95_ms=round(sorted(elapsed)[math.ceil(len(elapsed) * .95) - 1], 1), response_bytes=max(sizes))
    except (OSError, ValueError):
        parser.exit(1, 'Probe failed. Check server, permissions and availability; no response content or token was logged.\n')
    print(json.dumps(results, indent=2))


if __name__ == '__main__':
    main()
