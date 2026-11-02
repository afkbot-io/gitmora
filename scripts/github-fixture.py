"""Read-only GitHub API fixture backed by an owned real local Git repository; no production access."""

from __future__ import annotations

import base64
import datetime as dt
import http.server
import json
from pathlib import Path
import re
import subprocess
import urllib.parse

REPOSITORY_URL = "https://github.com/example/gitmora-fixture"


class GitHubFixture(http.server.BaseHTTPRequestHandler):
    repository: Path | None = None
    base_sha = ""
    token = "synthetic-github-read-token"
    requests: list[str] = []

    def log_message(self, *_):
        pass

    def respond(self, value, status=200):
        raw = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def git(self, *args):
        assert self.repository is not None
        return subprocess.run(
            ["git", "--git-dir", str(self.repository), *args],
            capture_output=True,
            timeout=10,
        )

    def do_GET(self):
        type(self).requests.append(urllib.parse.urlparse(self.path).path)
        auth = self.headers.get("Authorization", "")
        if auth and auth != "Bearer " + self.token:
            return self.respond({}, 401)
        path = urllib.parse.urlparse(self.path).path
        root = "/repos/example/gitmora-fixture"
        now = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1)).isoformat()
        if path in ["/repositories/99", root]:
            return self.respond(
                dict(
                    id=99,
                    private=False,
                    html_url=REPOSITORY_URL,
                    name="gitmora-fixture",
                )
            )
        if path == root + "/pulls":
            return self.respond(
                [dict(number=1, updated_at=now)] if self.repository else []
            )
        if path.startswith(root + "/pulls/1"):
            head = self.git("rev-parse", "refs/heads/main").stdout.decode().strip()
            parent = self.git("rev-parse", head + "^")
            base = self.base_sha or (
                parent.stdout.decode().strip() if parent.returncode == 0 else head
            )
            commits = (
                self.git("rev-list", "--reverse", base + ".." + head)
                .stdout.decode()
                .splitlines()
            )
            if path.endswith("/commits"):
                return self.respond([dict(sha=sha) for sha in commits])
            return self.respond(
                dict(
                    id=100,
                    number=1,
                    state="open",
                    title="Review fixture",
                    body="Synthetic review context",
                    html_url=REPOSITORY_URL + "/pull/1",
                    updated_at=now,
                    merged=False,
                    merge_commit_sha=None,
                    commits=len(commits),
                    head=dict(sha=head, repo=dict(id=99)),
                    base=dict(sha=base, repo=dict(id=99)),
                )
            )
        if path.startswith(root + "/compare/"):
            interval = path.split("/compare/", 1)[1]
            before, after = interval.split("...")
            result = self.git("merge-base", before, after)
            if result.returncode:
                return self.respond({}, 404)
            return self.respond(
                dict(
                    base_commit=dict(sha=before),
                    merge_base_commit=dict(sha=result.stdout.decode().strip()),
                )
            )
        if path == root + "/actions/runs":
            return self.respond(dict(workflow_runs=[]))
        match = re.fullmatch(re.escape(root) + r"/commits/([0-9a-f]{40})", path)
        if match:
            sha = match.group(1)
            result = self.git("cat-file", "-t", sha)
            if result.returncode or result.stdout.strip() != b"commit":
                return self.respond({"message": "No commit found for SHA"}, 422)
            files = (
                self.git(
                    "diff-tree", "--root", "--no-commit-id", "--name-only", "-r", sha
                )
                .stdout.decode()
                .splitlines()
            )
            query = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
            per_page = int(query.get("per_page", [50])[0])
            page = int(query.get("page", [1])[0])
            assert len(files) < per_page or per_page == 1, (
                "Use the dedicated bounded adapter tests for multi-page fixtures"
            )
            return self.respond(
                dict(
                    sha=sha,
                    files=[
                        dict(filename=name, status="added")
                        for name in files[(page - 1) * per_page : page * per_page]
                    ],
                )
            )
        match = re.fullmatch(
            re.escape(root) + r"/git/(commits|trees|blobs)/([0-9a-f]{40})", path
        )
        if match:
            kind, sha = match.groups()
            obj = self.git("cat-file", "-t", sha)
            if obj.returncode:
                return self.respond({}, 404)
            expected = dict(commits=b"commit", trees=b"tree", blobs=b"blob")[kind]
            if obj.stdout.strip() != expected:
                return self.respond({}, 404)
            if kind == "commits":
                tree = (
                    self.git("show", "--format=%T", "--no-patch", sha)
                    .stdout.decode()
                    .strip()
                )
                return self.respond(dict(sha=sha, tree=dict(sha=tree)))
            if kind == "blobs":
                raw = self.git("cat-file", "blob", sha).stdout
                return self.respond(
                    dict(
                        sha=sha,
                        size=len(raw),
                        encoding="base64",
                        content=base64.b64encode(raw).decode(),
                    )
                )
            entries = []
            for row in self.git("ls-tree", "-z", sha).stdout.split(b"\0"):
                if not row:
                    continue
                metadata, name = row.split(b"\t", 1)
                mode, entry_kind, digest = metadata.decode().split()
                size = (
                    int(self.git("cat-file", "-s", digest).stdout)
                    if entry_kind == "blob"
                    else 0
                )
                entries.append(
                    dict(
                        path=name.decode(),
                        mode=mode,
                        type=entry_kind,
                        sha=digest,
                        size=size,
                    )
                )
            return self.respond(dict(sha=sha, tree=entries, truncated=False))
        self.respond({}, 404)
