#!/usr/bin/env python3
"""Actual optimized Keycloak/PostgreSQL -> RP/PostgreSQL -> browser/native consent; synthetic accounts only."""
import importlib.util
import hashlib
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import threading
import urllib.error
import urllib.parse
import urllib.request
import uuid
from contextlib import ExitStack
from identity_proxy_fixture import loopback_proxy
from registration_fixture import create_user

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("keycloak_api", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)


def port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def main():
    selected = None
    if os.environ.get("GITMORA_KEYCLOAK_CLIENT"):
        assert os.environ.get("GITMORA_KEYCLOAK_NATIVE") == "1", "An explicit client requires native verification"
        selected = json.loads(os.environ["GITMORA_KEYCLOAK_CLIENT"])
        assert set(selected) == {"path", "version", "revision", "sha256"}
        executable = Path(selected["path"])
        assert executable.is_absolute() and executable.is_file() and not executable.is_symlink() and os.access(executable, os.X_OK)
        assert hashlib.sha256(executable.read_bytes()).hexdigest() == selected["sha256"], "Selected client bytes changed"
    schema = "keycloak_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    allowed = {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL", "GITMORA_UI_AUDIT", "GITMORA_UI_AUDIT_LABEL", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "DISPLAY"}
    environment = {k: v for k, v in os.environ.items() if k in allowed}
    environment["WORKGRAPH_DATABASE_URL"] = api.BASE_DSN + "&search_path=" + schema
    process = control = None
    with tempfile.TemporaryDirectory(prefix="gitmora-keycloak-") as temporary, ExitStack() as edges:
        root = Path(temporary) / "identity"
        public = "http://127.0.0.1:" + str(port())
        identity_port = port()
        shared = os.environ.get("GITMORA_KEYCLOAK_DISTINCT_HOST") != "1"
        prefix = "/auth" if shared else ""
        identity_url = public + prefix if shared else "http://localhost:" + str(identity_port)
        identity_origin = public if shared else identity_url
        api_address = "127.0.0.1:" + str(port()) if shared else public.removeprefix("http://")
        profile = None

        def cli(args):
            result = subprocess.run([str(ROOT / "bin/gitmorad"), "identity-server", *args],
                env=environment, cwd=ROOT, capture_output=True, text=True, timeout=260)
            assert result.returncode == 0, "Identity operator command failed; synthetic diagnostics remain private"
            return json.loads(result.stdout) if result.stdout.strip() else None

        def compose_environment():
            config = json.loads((root / "identity.json").read_text())
            return {**environment, "GITMORA_IDENTITY_STATE": str(root), "GITMORA_IDENTITY_HTTP_PORT": str(identity_port), "GITMORA_IDENTITY_REALM": "gitmora",
                    "GITMORA_IDENTITY_IMAGE": profile["profile"]["identity_image"], "GITMORA_IDENTITY_GATEWAY_IMAGE": profile["profile"]["gateway_image"],
                    "GITMORA_IDENTITY_PUBLIC_URL": identity_url, "GITMORA_IDENTITY_PUBLIC_ORIGIN": identity_origin, "GITMORA_IDENTITY_PREFIX": prefix,
                    "GITMORA_IDENTITY_CONFIG": "keycloak-active.conf" if config["phase"] == "active" else "keycloak-bootstrap.conf",
                    "GITMORA_IDENTITY_IMPORT": "runtime" if config["phase"] == "active" else "bootstrap"}

        try:
            profile = cli(["init", "--directory", str(root), "--compose", str(ROOT / "deploy/identity/compose.yaml"),
                "--public-url", identity_url, "--application-url", public,
                "--identity-image", os.environ.get("GITMORA_KEYCLOAK_IMAGE", "gitmora-identity:26.8.1-auth" if shared else "gitmora-identity:26.8.1-root"),
                "--gateway-image", os.environ.get("GITMORA_IDENTITY_GATEWAY_IMAGE", "gitmora:local"),
                "--port", str(identity_port)])
            cli(["up", "--directory", str(root)])
            print("Actual identity provider bootstrap is healthy", flush=True)
            finalized = cli(["finalize", "--directory", str(root)])
            assert finalized["phase"] == "active" and finalized["master_user_id"]
            assert not list((root / "bootstrap").iterdir()), "Retired passwords remain in a mounted import directory"
            material = json.loads((root / "operator.json").read_text())
            cli(["finalize", "--directory", str(root)])
            print("Permanent administration and replay verified; bootstrap retired", flush=True)
            # Normal recreation must preserve the actual provider/database while excluding bootstrap inputs.
            cli(["down", "--directory", str(root)])
            cli(["up", "--directory", str(root)])
            # Inspect actual provider state without printing credential bodies or tokens.
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            token_request = urllib.request.Request("http://127.0.0.1:" + str(identity_port) + prefix + "/realms/master/protocol/openid-connect/token",
                data=urllib.parse.urlencode(dict(grant_type="password", client_id="admin-cli", username="gitmora.operator", password=material["master_password"])).encode())
            with opener.open(token_request, timeout=15) as response:
                management_token = json.load(response)["access_token"]
            # Ordinary restart must not silently reset durable registration policy.
            environment["WORKGRAPH_REGISTRATION_ENABLED"] = "true"
            cli(["configure", "--directory", str(root)])
            environment.pop("WORKGRAPH_REGISTRATION_ENABLED")
            cli(["up", "--directory", str(root)])
            realm_request = urllib.request.Request("http://127.0.0.1:" + str(identity_port) + prefix + "/admin/realms/gitmora", headers={"Authorization": "Bearer " + management_token})
            with opener.open(realm_request, timeout=15) as response:
                assert json.load(response)["registrationAllowed"] is True, "Restart closed existing signup policy"
            environment["WORKGRAPH_REGISTRATION_ENABLED"] = "false"
            cli(["configure", "--directory", str(root)])
            environment.pop("WORKGRAPH_REGISTRATION_ENABLED")
            user_request = urllib.request.Request("http://127.0.0.1:" + str(identity_port) + prefix + "/admin/realms/gitmora/users/" + profile["user_id"], headers={"Authorization": "Bearer " + management_token})
            with opener.open(user_request, timeout=15) as response:
                user = json.load(response)
                assert user["enabled"] and user.get("requiredActions") == ["UPDATE_PASSWORD"]
                print(json.dumps({"provider_user_enabled": user["enabled"], "required_actions": user.get("requiredActions", [])}), flush=True)
            boot = api.command(["bootstrap", "--project", "Actual Keycloak", "--repository", "Synthetic identity context",
                               "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
            assert boot.returncode == 0
            identity = json.loads(boot.stdout)
            issued = api.command(["issue-token", "--user-id", identity["user_id"], "--scopes",
                                  "directory:read,platform:manage,credentials:manage,capture:read,capture:write"], environment)
            assert issued.returncode == 0
            admin = json.loads(issued.stdout)["token"]
            registration = os.environ.get("GITMORA_KEYCLOAK_REGISTRATION") == "1"
            operator_user = None
            if registration:
                operator_password = secrets.token_urlsafe(32)
                operator_user = create_user(ROOT, root, environment, "cli.engineer", operator_password)
            environment.update(WORKGRAPH_PUBLIC_URL=public, WORKGRAPH_OIDC_ISSUER=identity_url + "/realms/gitmora",
                WORKGRAPH_OIDC_CLIENT_ID="gitmora", WORKGRAPH_OIDC_CLIENT_SECRET_FILE=str(root / "secrets/oidc_client_secret"))
            process, _ = api.start_api(environment, api_address)
            if shared:
                edges.enter_context(loopback_proxy(Path(temporary), int(urllib.parse.urlparse(public).port), int(api_address.rsplit(":", 1)[1]), identity_port))
            nonce = secrets.token_urlsafe(32)

            class Control(BaseHTTPRequestHandler):
                def log_message(self, *_):
                    pass

                def do_POST(self):
                    nonlocal process
                    if self.path not in {"/restart", "/registration/true", "/registration/false"} or not secrets.compare_digest(self.headers.get("X-Verification-Nonce", ""), nonce):
                        self.send_response(403); self.end_headers(); return
                    if self.path.startswith("/registration/"):
                        if not registration:
                            self.send_response(403); self.end_headers(); return
                        environment["WORKGRAPH_REGISTRATION_ENABLED"] = self.path.rsplit("/", 1)[1]
                        cli(["configure", "--directory", str(root)])
                    process.terminate(); process.wait(timeout=10); process.stderr.close()
                    process, _ = api.start_api(environment, api_address)
                    self.send_response(204); self.end_headers()

            control = ThreadingHTTPServer(("127.0.0.1", 0), Control)
            threading.Thread(target=control.serve_forever, daemon=True).start()
            payload = dict(url=public, issuer=identity_url + "/realms/gitmora", user=identity["user_id"], admin=admin,
                repository=identity["repository_id"], binding=str(uuid.uuid4()), native=os.environ.get("GITMORA_KEYCLOAK_NATIVE") == "1", same_origin=shared, context_cli=selected is None or os.environ.get("GITMORA_KEYCLOAK_CONTEXT") == "1",
                control_url=f"http://127.0.0.1:{control.server_port}", control_nonce=nonce,
                keycloak=dict(password=material["user_password"], replacement=secrets.token_urlsafe(32), subject=profile["user_id"]))
            if registration:
                payload.update(operator_account={**operator_user, "password": operator_password}, project=identity["project_id"])
            if selected:
                payload.update(cli_binary=selected["path"], cli_release=dict(version=selected["version"], revision=selected["revision"]))
            child_environment = {k: v for k, v in environment.items() if k in allowed}
            result = subprocess.run(["node", "scripts/smoke-registration.mjs" if registration else "scripts/smoke-cli-auth.mjs"], input=json.dumps(payload),
                cwd=ROOT, env=child_environment, capture_output=True, text=True, timeout=600 if os.environ.get("GITMORA_UI_AUDIT") == "1" else 300)
            if result.returncode:
                private = ROOT / "build/keycloak-browser-debug.log"
                diagnostic = result.stdout + result.stderr
                for value in [*material.values(), admin, payload["keycloak"]["replacement"], *([operator_password] if registration else [])]:
                    diagnostic = diagnostic.replace(value, "[redacted]")
                private.write_text(diagnostic); private.chmod(0o600)
            assert result.returncode == 0, "Actual provider/browser/native verification failed; private synthetic diagnostics retained"
            print(result.stdout.strip())
            print(json.dumps(dict(result="passed", provider="actual optimized Keycloak 26.8.1", storage="distinct PostgreSQL volume",
                bootstrap="retired", import_source="unmounted and directory emptied", restart="provider and RP", native=payload["native"], same_origin=shared)))
        except BaseException:
            if profile:
                command = ["docker", "compose", "--project-name", "gitmora-identity-" + profile["id"].replace("-", ""),
                    "--project-directory", str(ROOT / "deploy/identity"), "--file", str(ROOT / "deploy/identity/compose.yaml")]
                logs = subprocess.run([*command, "logs", "--no-color", "--tail", "40"], env=compose_environment(), capture_output=True, text=True, timeout=20)
                diagnostic = logs.stdout + logs.stderr
                for value in json.loads((root / "operator.json").read_text()).values():
                    diagnostic = diagnostic.replace(value, "[redacted]")
                log = ROOT / "build/keycloak-containers-debug.log"; log.write_text(diagnostic); log.chmod(0o600)
            raise
        finally:
            if control:
                control.shutdown(); control.server_close()
            if process:
                if process.poll() is None:
                    process.terminate(); process.wait(timeout=10)
                if process.stderr and not process.stderr.closed:
                    process.stderr.close()
            if profile:
                subprocess.run(["docker", "compose", "--project-name", "gitmora-identity-" + profile["id"].replace("-", ""),
                    "--project-directory", str(ROOT / "deploy/identity"), "--file", str(ROOT / "deploy/identity/compose.yaml"),
                    "down", "--volumes", "--remove-orphans"], env=compose_environment(), capture_output=True, timeout=90, check=True)
            api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
