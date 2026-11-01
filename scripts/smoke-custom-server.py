#!/usr/bin/env python3
"""Built Linux CLI/API/PostgreSQL/MCP through real nginx HTTPS on an unrelated DNS hostname.

Uses synthetic credentials in a private env file, an ephemeral fixture CA and Docker-only
hostname mapping. Never changes host DNS, OS trust, user agent config or production state.
"""

import argparse
import datetime
import importlib.util
import json
import os
from pathlib import Path
import select
import shutil
import socket
import subprocess
import tempfile
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


api = module("custom_server_api", "scripts/smoke-api.py")
renderer = module("custom_nginx", "scripts/render-nginx.py")
HOST = "gitmora.custom.example.test"


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--client",
        type=Path,
        help="Optional reviewed Linux client for the Docker image architecture",
    )
    args = parser.parse_args()
    nginx = shutil.which("nginx")
    assert nginx, "Install nginx for the actual custom HTTPS gate"
    schema = "custom_server_" + uuid.uuid4().hex
    api.sql('CREATE SCHEMA "' + schema + '"')
    environment = {
        k: v for k, v in os.environ.items() if k in {"PATH", "HOME", "LANG", "TMPDIR"}
    }
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = proxy = bridge = None
    with tempfile.TemporaryDirectory(prefix="gitmora-custom-server-") as name:
        folder = Path(name)
        port = free_port()
        origin = "https://" + HOST + ":" + str(port)
        try:
            setup = api.command(
                [
                    "bootstrap",
                    "--project",
                    "Custom server team",
                    "--repository",
                    "Custom server repository",
                    "--gitlab-url",
                    "https://code.example.test",
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
                    "directory:read,capture:read,integration:read",
                ],
                environment,
            )
            assert issued.returncode == 0
            token = json.loads(issued.stdout)["token"]
            environment["WORKGRAPH_PUBLIC_URL"] = origin
            process, address = api.start_api(environment)
            marker = "CustomOriginEvidence" + uuid.uuid4().hex[:8]
            snapshot = dict(
                schema_version=1,
                repository_id=identity["repository_id"],
                session_id=str(uuid.uuid4()),
                revision=1,
                started_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                agent=dict(name="custom-server-fixture", model="no-model"),
                messages=[dict(role="user", text=marker + " portable domain context")],
            )
            request = urllib.request.Request(
                "http://" + address + "/v1/captures",
                data=json.dumps(snapshot).encode(),
                headers={
                    "Authorization": "Bearer " + identity["token"],
                    "Content-Type": "application/json",
                },
            )
            with urllib.request.urlopen(request, timeout=10) as response:
                assert response.status == 201
            assert api.command(["worker", "--once"], environment).returncode == 0
            cert, key = folder / "ca.pem", folder / "key.pem"
            subprocess.run(
                [
                    "openssl",
                    "req",
                    "-x509",
                    "-newkey",
                    "rsa:2048",
                    "-nodes",
                    "-days",
                    "1",
                    "-subj",
                    "/CN=" + HOST,
                    "-addext",
                    "subjectAltName=DNS:" + HOST,
                    "-keyout",
                    str(key),
                    "-out",
                    str(cert),
                ],
                check=True,
                capture_output=True,
            )
            conf = renderer.render(HOST, int(address.rsplit(":", 1)[1]), free_port())[
                "gitmora.https.conf"
            ]
            http_port = free_port()
            replacements = {
                "listen 80;": "listen 127.0.0.1:" + str(http_port) + ";",
                "listen [::]:80;": "",
                "listen 443 ssl http2;": "listen " + str(port) + " ssl;",
                "listen [::]:443 ssl http2;": "",
                "/etc/letsencrypt/live/" + HOST + "/fullchain.pem": str(cert),
                "/etc/letsencrypt/live/" + HOST + "/privkey.pem": str(key),
                "/var/log/nginx/gitmora.access.log": str(folder / "access.log"),
                "/var/log/nginx/gitmora.error.log": str(folder / "proxy-error.log"),
            }
            for before, after in replacements.items():
                assert before in conf
                conf = conf.replace(before, after)
            config = folder / "nginx.conf"
            config.write_text(
                "worker_processes 1;\npid "
                + str(folder / "nginx.pid")
                + ";\nerror_log "
                + str(folder / "error.log")
                + ";\nevents {worker_connections 128;}\nhttp {\n"
                + conf
                + "\n}\n"
            )
            subprocess.run(
                [nginx, "-p", str(folder), "-c", str(config), "-t"],
                check=True,
                capture_output=True,
            )
            proxy = subprocess.Popen(
                [nginx, "-p", str(folder), "-c", str(config), "-g", "daemon off;"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            # Host nginx listens on an ephemeral port so Docker Desktop's VM can reach
            # the owned fixture. The only accepted HTTPS identity is this synthetic host.
            architecture = subprocess.check_output(
                [
                    "docker",
                    "image",
                    "inspect",
                    "--format",
                    "{{.Architecture}}",
                    "gitmora-postgres:18.6-security1",
                ],
                text=True,
            ).strip()
            assert architecture in ["arm64", "amd64"]
            binary = folder / "gitmora"
            build = {
                **environment,
                "GOOS": "linux",
                "GOARCH": architecture,
                "CGO_ENABLED": "0",
            }
            if args.client:
                selected = args.client.resolve(strict=True)
                assert selected.is_file() and not args.client.is_symlink()
                shutil.copyfile(selected, binary)
            else:
                subprocess.run(
                    ["go", "build", "-trimpath", "-o", str(binary), "./cmd/workgraph"],
                    cwd=ROOT,
                    env=build,
                    check=True,
                    capture_output=True,
                )
            binary.chmod(0o755)
            cert.chmod(0o444)
            private = folder / "client.env"
            private.write_text(
                "WORKGRAPH_TOKEN=" + token + "\nSSL_CERT_FILE=/fixture-ca.pem\n"
            )
            private.chmod(0o600)
            docker = [
                "docker",
                "run",
                "-i",
                "--rm",
                "--read-only",
                "--user",
                "65532:65532",
                "--cap-drop",
                "ALL",
                "--security-opt",
                "no-new-privileges",
                "--add-host",
                HOST + ":host-gateway",
                "--env-file",
                str(private),
                "--mount",
                "type=bind,source=" + str(binary) + ",target=/gitmora,readonly",
                "--mount",
                "type=bind,source=" + str(cert) + ",target=/fixture-ca.pem,readonly",
                "--entrypoint",
                "/gitmora",
                "gitmora-postgres:18.6-security1",
            ]

            def cli(*args, success=True):
                result = subprocess.run(
                    [*docker, *args], capture_output=True, text=True, timeout=40
                )
                assert (result.returncode == 0) == success, (
                    "Custom HTTPS CLI result mismatch"
                )
                assert token not in result.stdout + result.stderr, (
                    "Credential reached CLI output"
                )
                return json.loads(result.stdout) if success else None

            client_version = cli("version")
            assert (
                client_version["os"] == "linux"
                and client_version["arch"] == architecture
            )
            projects = cli("api", "get", "/v1/projects", "--server", origin)
            assert projects["body"]["items"][0]["id"] == identity["project_id"]
            search = dict(project_id=identity["project_id"], text=marker)
            result = subprocess.run(
                [
                    *docker,
                    "api",
                    "post",
                    "/v1/context/search",
                    "--server",
                    origin,
                    "--input",
                    "-",
                ],
                input=json.dumps(search),
                capture_output=True,
                text=True,
                timeout=40,
            )
            assert result.returncode == 0 and token not in result.stdout + result.stderr
            hit = json.loads(result.stdout)["body"]["items"][0]
            bridge = subprocess.Popen(
                [*docker, "mcp", "--server", origin],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                bufsize=1,
            )
            sequence = 0

            def rpc(method, params):
                nonlocal sequence
                sequence += 1
                bridge.stdin.write(
                    json.dumps(
                        dict(jsonrpc="2.0", id=sequence, method=method, params=params)
                    )
                    + "\n"
                )
                bridge.stdin.flush()
                assert select.select([bridge.stdout], [], [], 20)[0], (
                    "Custom HTTPS MCP response timed out"
                )
                raw = bridge.stdout.readline()
                if not raw:
                    diagnostic = bridge.stderr.read().replace(token, "[redacted]")
                    target = ROOT / "build/live-review/self-host-mcp-diagnostic.log"
                    target.write_text(diagnostic)
                    target.chmod(0o600)
                    raise AssertionError(
                        "Custom MCP bridge closed; inspect the private synthetic diagnostic"
                    )
                assert token not in raw
                value = json.loads(raw)
                assert value.get("id") == sequence and "error" not in value
                return value["result"]

            rpc(
                "initialize",
                dict(
                    protocolVersion="2025-11-25",
                    capabilities={},
                    clientInfo=dict(name="custom-origin-verification", version="1"),
                ),
            )
            bridge.stdin.write(
                json.dumps(dict(jsonrpc="2.0", method="notifications/initialized"))
                + "\n"
            )
            bridge.stdin.flush()
            tools = rpc("tools/list", {})["tools"]
            assert {t["name"] for t in tools} == {
                "context.browse",
                "context.search",
                "context.pack",
                "context.expand",
                "context.task",
                "context.file",
            }

            def tool(name, args):
                value = rpc("tools/call", dict(name=name, arguments=args))
                assert not value.get("isError")
                return value["structuredContent"]["result"]

            assert tool(
                "context.browse",
                dict(project_id=identity["project_id"], kind="session"),
            )["items"]
            found = tool("context.search", search)
            assert found["items"][0]["source"]["project_id"] == identity["project_id"]
            assert (
                marker in tool("context.expand", dict(evidence_id=hit["id"]))["content"]
            )
            packed = tool(
                "context.pack",
                dict(
                    project_id=identity["project_id"],
                    repository_id=identity["repository_id"],
                    session_id=snapshot["session_id"],
                ),
            )
            assert (
                packed["captures"]["items"][0]["source"]["session_id"]
                == snapshot["session_id"]
            )
            assert not tool(
                "context.task",
                dict(
                    project_id=identity["project_id"],
                    country_id=str(uuid.uuid4()),
                    task_id=str(uuid.uuid4()),
                ),
            )["items"]
            unavailable = rpc(
                "tools/call",
                dict(
                    name="context.file",
                    arguments=dict(
                        repository_id=identity["repository_id"],
                        commit_sha="a" * 40,
                        file_path="src/absent.go",
                    ),
                ),
            )
            assert unavailable.get("isError") and "evidence_unavailable" in json.dumps(
                unavailable
            )

            bridge.stdin.close()
            assert bridge.wait(timeout=15) == 0
            assert token not in bridge.stderr.read()
            # A trusted fixture CA never authorizes another server hostname.
            wrong = list(docker)
            i = wrong.index("--env-file")
            wrong[i:i] = ["--add-host", "wrong.custom.example.test:host-gateway"]
            rejected = subprocess.run(
                [
                    *wrong,
                    "api",
                    "get",
                    "/v1/projects",
                    "--server",
                    "https://wrong.custom.example.test:" + str(port),
                ],
                capture_output=True,
                text=True,
                timeout=20,
            )
            assert (
                rejected.returncode != 0
                and token not in rejected.stdout + rejected.stderr
            )
            print(
                json.dumps(
                    dict(
                        result="passed",
                        custom_hostname=True,
                        actual_nginx_tls=True,
                        linux_cli=True,
                        client_version=client_version["version"],
                        cli_api=True,
                        stdio_mcp_tools=6,
                        all_six_tools_called=True,
                        unconfigured_code_explicit=True,
                        wrong_tls_hostname_rejected=True,
                        production_target=False,
                    )
                )
            )
        except Exception:
            trace = folder / "access.log"
            if trace.exists():
                diagnostic = ROOT / "build/live-review/self-host-custom-tls-access.log"
                diagnostic.write_text(trace.read_text().replace(token, "[redacted]"))
                diagnostic.chmod(0o600)
            raise
        finally:
            for child in [bridge, proxy, process]:
                if child is not None and child.poll() is None:
                    child.terminate()
                    try:
                        child.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        child.wait(timeout=5)
            api.sql('DROP SCHEMA "' + schema + '" CASCADE')


if __name__ == "__main__":
    main()
