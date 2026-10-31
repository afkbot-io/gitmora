#!/usr/bin/env python3
"""Real logical dump/restore into a different private volume, offline quarantine and authorized API/index recovery.

This acceptance harness owns two synthetic installations. It is not the production backup/restore coordinator.
"""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


def uid():
    return str(uuid.uuid4())


def run(argv, data=None, env=None, check=True):
    result = subprocess.run(argv, cwd=ROOT, input=data, env=env, capture_output=True, timeout=150)
    assert not check or result.returncode == 0, "Synthetic recovery subprocess failed; private diagnostics suppressed"
    return result


class Stack:
    def __init__(self, parent, name):
        self.state = parent / name
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.port = listener.getsockname()[1]
        value = json.loads(self.cli(["init", "--directory", str(self.state), "--compose", str(ROOT / "compose.yaml"), "--port", str(self.port), "--image", os.environ.get("GITMORA_DEPLOYMENT_TEST_IMAGE", "gitmora:local")]).stdout)
        self.environment = {**os.environ, "WORKGRAPH_STATE_DIR": str(self.state), "WORKGRAPH_HTTP_PORT": str(self.port), "WORKGRAPH_IMAGE": value["image"]}
        self.prefix = ["docker", "compose", "--project-name", "gitmora-" + value["id"].replace("-", ""), "--project-directory", str(ROOT), "--file", str(ROOT / "compose.yaml")]

    def cli(self, args):
        return run([str(ROOT / "bin/gitmorad"), "deployment", *args])

    def compose(self, args, data=None, check=True):
        return run([*self.prefix, *args], data, self.environment, check)

    def sql(self, statement):
        return self.compose(["exec", "-T", "postgres", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "workgraph", "-d", "workgraph", "-At"], statement.encode()).stdout.decode().strip()

    def admin(self, args):
        return json.loads(self.cli(["admin", "--directory", str(self.state), "--", *args]).stdout)

    def api(self, path, token=None, body=None, method=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        request = urllib.request.Request(f"http://127.0.0.1:{self.port}" + path, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method)
        try:
            response = urllib.request.urlopen(request, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, json.load(response)

    def up(self):
        self.cli(["up", "--directory", str(self.state)])

    def close(self):
        result = self.compose(["down", "--volumes", "--remove-orphans"], check=False)
        assert result.returncode == 0, "Could not clean this synthetic recovery installation"


def main():
    with tempfile.TemporaryDirectory(prefix="gitmora recovery ") as directory:
        parent = Path(directory)
        source = target = None
        try:
            source = Stack(parent, "source")
            source.up()
            initial = source.admin(["init-admin", "--name", "Synthetic source operator"])
            token = initial["token"]
            project, repository, session = uid(), uid(), uid()
            assert source.api("/v1/projects", token, dict(request_id=uid(), id=project, name="Recovery fixture"))[0] == 201
            assert source.api(f"/v1/projects/{project}/repositories", token, dict(request_id=uid(), id=repository, name="Fixture", gitlab_url="https://gitlab.internal", gitlab_project_id=42))[0] == 201
            capture = dict(schema_version=1, repository_id=repository, session_id=session, revision=1, started_at=dt.datetime.now(dt.timezone.utc).isoformat(), agent=dict(name="recovery-verification", model="synthetic"), messages=[dict(role="assistant", text="GitmoraRecoverySentinel проверяем восстановление")])
            assert source.api("/v1/captures", token, capture)[0] == 201
            source_route = f"/v1/repositories/{repository}/sessions/{session}"
            canonical = source.api(source_route, token)[1]
            # Simulate an interrupted local indexing lease at the snapshot boundary.
            source.compose(["stop", "worker"])
            source.sql("DELETE FROM retrieval_chunks; UPDATE retrieval_jobs SET completed_at=NULL,lease_owner=gen_random_uuid(),lease_until=now()+interval '1 hour'")
            metadata = json.loads(source.compose(["exec", "-T", "api", "/gitmorad", "recovery", "metadata"]).stdout)
            archive = source.compose(["exec", "-T", "postgres", "pg_dump", "--username", "workgraph", "--dbname", "workgraph", "--format", "custom", "--no-owner", "--no-acl"]).stdout
            assert archive.startswith(b"PGDMP") and len(archive) > 10000
            checksum = hashlib.sha256(archive).hexdigest()
            target = Stack(parent, "target")
            keyring = target.state / "secrets/encryption_keys"
            keyring.chmod(0o600)
            shutil.copyfile(source.state / "secrets/encryption_keys", keyring)
            keyring.chmod(0o444)
            target.compose(["up", "--detach", "--no-build", "--pull", "never", "--wait", "postgres"])
            assert target.compose(["ps", "-q", "api", "worker", "gateway"]).stdout.strip() == b"", "Unquarantined destination served traffic"
            restore = ["exec", "-T", "postgres", "pg_restore", "--username", "workgraph", "--dbname", "workgraph", "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl"]
            assert target.compose(restore, archive[:len(archive)//2], check=False).returncode != 0
            assert target.sql("SELECT count(*)FROM pg_tables WHERE schemaname='public'") == "0", "Failed restore left a partially usable schema"
            assert hashlib.sha256(archive).hexdigest() == checksum
            target.compose(restore, archive)
            recovery_path = target.state / "recovery.json"
            plan = json.loads(run([str(ROOT / "bin/gitmorad"), "recovery", "prepare", "--output", str(recovery_path), "--backup-id", uid(), "--schema", metadata["schema"], "--name", "Synthetic recovery operator"]).stdout)
            preparation = json.loads(recovery_path.read_text())
            recovery_key = preparation["token"]
            assert recovery_key not in json.dumps(plan) and recovery_path.stat().st_mode & 0o777 == 0o600
            quarantine = ["run", "--rm", "--no-deps", "-T", "api", "recovery", "quarantine"]
            wrong = {**plan, "schema": "f" * 64}
            assert target.compose(quarantine, json.dumps(wrong).encode(), check=False).returncode != 0
            assert target.sql("SELECT count(*)FROM users WHERE enabled") == "1", "Rejected quarantine mutated the destination"
            receipt = json.loads(target.compose(quarantine, json.dumps(plan).encode()).stdout)
            assert json.loads(target.compose(quarantine, json.dumps(plan).encode()).stdout) == receipt
            assert receipt["user_id"] == plan["user_id"] and receipt["counts"]["memberships"] == 1
            target.up()
            assert target.api("/v1/me", token)[0] == 401
            status, current = target.api("/v1/me", recovery_key)
            assert status == 200 and current["user"]["operator"] and "capture:read" not in current["scopes"]
            assert target.api(source_route, recovery_key)[0] == 403
            assert target.api(f"/v1/projects/{project}/members/{plan['user_id']}", recovery_key, dict(request_id=uid(), expected_version=0, role="reader"), "PUT")[0] == 200
            assert target.api(source_route, recovery_key)[0] == 403, "Metadata-only key acquired content capability implicitly"
            explicit = target.admin(["issue-token", "--user-id", plan["user_id"], "--scopes", "capture:read", "--ttl", "1h"])["token"]
            assert target.api(source_route, explicit)[1] == canonical, "Restored immutable API content differs"
            for _ in range(40):
                status, hits = target.api("/v1/context/search", explicit, dict(project_id=project, text="GitmoraRecoverySentinel"))
                if status == 200 and hits["items"]:
                    break
                time.sleep(0.25)
            assert status == 200 and hits["items"], "Restored independent worker did not resume indexing"
            assert target.sql("SELECT count(*)FROM retrieval_jobs WHERE completed_at IS NULL") == "0"
            assert source.api(source_route, token)[1] == canonical and source.api("/v1/me", token)[0] == 200, "Recovery changed the source installation"
            assert recovery_key not in target.compose(["logs", "--no-color"]).stdout.decode(errors="replace"), "Fresh recovery bearer entered container logs"
            print("Actual recovery: distinct private volume, truncated-archive atomic rejection, custom dump/restore, exact schema, hash-only quarantine/replay, old-key denial, explicit fresh authority, identical capture API and independent index recovery passed. Production archive/coordinator/PITR remain separate work.")
        finally:
            try:
                if target:
                    target.close()
            finally:
                if source:
                    source.close()


if __name__ == "__main__":
    main()
