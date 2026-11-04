#!/usr/bin/env python3
"""Actual source-built image, private Compose stack, nonroot runtime, persistence and outage/drift checks."""
import datetime as dt
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


def main():
    with tempfile.TemporaryDirectory(prefix="gitmora compose ") as temporary:
        state = Path(temporary) / "installation"
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        def cli(args):
            result = subprocess.run([str(ROOT / "bin/gitmorad"), "deployment", *args], cwd=ROOT, capture_output=True, text=True, timeout=130)
            assert result.returncode == 0, "Deployment CLI command failed"
            return json.loads(result.stdout) if result.stdout.strip() else None

        installation = cli(["init", "--directory", str(state), "--compose", str(ROOT / "compose.yaml"), "--port", str(port), "--image", os.environ.get("GITMORA_DEPLOYMENT_TEST_IMAGE", "gitmora:local")])
        environment = {**os.environ, "WORKGRAPH_STATE_DIR": str(state), "WORKGRAPH_HTTP_PORT": str(port), "WORKGRAPH_IMAGE": installation["image"]}
        prefix = ["docker", "compose", "--project-name", "gitmora-" + installation["id"].replace("-", ""), "--project-directory", str(ROOT), "--file", str(ROOT / "compose.yaml")]

        def compose(args, data=None, check=True, timeout=120):
            result = subprocess.run([*prefix, *args], env=environment, cwd=ROOT, input=data, capture_output=True, timeout=timeout)
            assert not check or result.returncode == 0, "Isolated Compose operation failed"
            return result

        def sql(statement):
            return compose(["exec", "-T", "postgres", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "workgraph", "-d", "workgraph", "-At"], statement.encode()).stdout.decode().strip()

        def request(path, token=None, body=None):
            headers = {"Content-Type": "application/json"}
            if token:
                headers["Authorization"] = "Bearer " + token
            request = urllib.request.Request(f"http://127.0.0.1:{port}" + path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
            try:
                response = urllib.request.urlopen(request, timeout=5)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                return response.status, json.load(response)

        started = False
        try:
            normalized = json.loads(compose(["config", "--format", "json"]).stdout)
            assert normalized["networks"]["private"]["internal"]
            for service in ["migrate", "api", "worker", "gateway"]:
                config = normalized["services"][service]
                assert config["user"] == "65532:65532" and config["read_only"]
                assert config["cap_drop"] == ["ALL"] and len(config["tmpfs"]) == 1, "Invalid application isolation/tmpfs configuration"
                assert "WORKGRAPH_DATABASE_URL" not in config.get("environment", {})
            assert not normalized["services"]["postgres"].get("ports") and not normalized["services"]["worker"].get("ports")
            assert not normalized["services"]["api"].get("ports")
            assert normalized["services"]["gateway"]["ports"][0]["host_ip"] == "127.0.0.1"
            assert not normalized["services"]["gateway"].get("secrets") and not normalized["services"]["gateway"].get("environment")
            started = True
            cli(["up", "--directory", str(state)])
            assert request("/health/ready")[0] == 200
            bootstrap = cli(["admin", "--directory", str(state), "--", "init-admin", "--name", "Deployment operator"])
            token = bootstrap["token"]
            assert request("/v1/projects", token)[1]["items"] == [], "Production initializer invented catalog/provider identities"
            bootstrap.update(project_id=str(uuid.uuid4()), repository_id=str(uuid.uuid4()))
            assert request("/v1/projects", token, dict(request_id=str(uuid.uuid4()), id=bootstrap["project_id"], name="Synthetic deployment verification"))[0] == 201
            assert request(f'/v1/projects/{bootstrap["project_id"]}/repositories', token, dict(request_id=str(uuid.uuid4()), id=bootstrap["repository_id"], name="Fixture", gitlab_url="https://gitlab.internal", gitlab_project_id=42))[0] == 201
            api_id = compose(["ps", "-q", "api"]).stdout.decode().strip()
            inspected = json.loads(subprocess.check_output(["docker", "inspect", api_id]))[0]
            assert inspected["Config"]["User"] == "65532:65532" and inspected["HostConfig"]["ReadonlyRootfs"]
            assert all(not value.startswith("WORKGRAPH_DATABASE_URL=") for value in inspected["Config"]["Env"])
            assert subprocess.run(["docker", "exec", api_id, "/bin/sh", "-c", "true"], capture_output=True).returncode != 0
            logs = compose(["logs", "--no-color", "api"]).stdout.decode()
            assert token not in logs, "First-only bootstrap credential entered container logs"
            assert sql("SELECT NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication FROM pg_roles WHERE rolname='workgraph'") == "t"
            session = str(uuid.uuid4())
            capture = dict(schema_version=1, repository_id=bootstrap["repository_id"], session_id=session, revision=1, started_at=dt.datetime.now(dt.timezone.utc).isoformat(), agent=dict(name="local-deployment-verification", model="synthetic"), messages=[dict(role="assistant", text="GitmoraDeploymentSentinel проверяем постоянное хранилище")], files=["src/retained.go"])
            assert request("/v1/captures", token, capture)[0] == 201
            source = f'/v1/repositories/{bootstrap["repository_id"]}/sessions/{session}'
            assert request(source, token)[0] == 200
            indexed = False
            for _ in range(30):
                status, value = request("/v1/context/search", token, dict(project_id=bootstrap["project_id"], text="GitmoraDeploymentSentinel"))
                if status == 200 and value["items"]:
                    indexed = True
                    break
                time.sleep(0.2)
            assert indexed, "Independent deployed worker did not index durable capture"
            cli(["down", "--directory", str(state)])
            cli(["up", "--directory", str(state)])
            assert request(source, token)[0] == 200, "Container recreation lost persistent database data"
            compose(["stop", "postgres"])
            assert request("/health/live")[0] == 200 and request("/health/ready")[0] == 503
            compose(["start", "postgres"])
            # Shared runners can take longer to restart PostgreSQL than the old
            # six-second polling window. Verify an authenticated read too: one
            # ready connection does not drain every stale pooled connection.
            deadline = time.monotonic() + 30
            recovered = 0
            while time.monotonic() < deadline:
                ready = request("/health/ready")[0]
                assert ready in (200, 503), "Unexpected readiness response during recovery"
                status = request(source, token)[0] if ready == 200 else 503
                assert status in (200, 503), "Stored capture/authentication changed during outage"
                recovered = recovered + 1 if status == 200 else 0
                if recovered == 3:
                    break
                time.sleep(0.2)
            assert recovered == 3, "API did not reconnect after database outage"
            original = sql("SELECT checksum FROM schema_migrations WHERE name='migrations/0001_capture.sql'")
            sql("UPDATE schema_migrations SET checksum=repeat('f',64) WHERE name='migrations/0001_capture.sql'")
            assert request("/health/ready")[0] == 503
            drifted = compose(["run", "--rm", "--no-deps", "-T", "migrate"], check=False)
            assert drifted.returncode != 0, "Migrator accepted checksum drift"
            sql("UPDATE schema_migrations SET checksum='" + original + "' WHERE name='migrations/0001_capture.sql'")
            assert request("/health/ready")[0] == 200
            print("Actual Gitmora Compose: pinned source image, nonroot/read-only/no-shell runtime, role isolation, private file secrets, no bootstrap secret logs, worker indexing, persistent recreation, database outage/reconnect and migration drift rejection passed.")
        except Exception:
            diagnostics = compose(["logs", "--no-color", "--tail", "30"], check=False).stdout.decode(errors="replace")
            diagnostics += compose(["ps", "--all"], check=False).stdout.decode(errors="replace")
            for secret in (state / "secrets").iterdir():
                value = secret.read_text().strip()
                if value:
                    diagnostics = diagnostics.replace(value, "[redacted]")
            log = ROOT / "build/deployment-debug.log"
            log.write_text(diagnostics)
            log.chmod(0o600)
            raise
        finally:
            if started:
                compose(["down", "--volumes", "--remove-orphans"], check=False)


if __name__ == "__main__":
    main()
