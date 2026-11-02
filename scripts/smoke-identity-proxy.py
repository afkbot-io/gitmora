#!/usr/bin/env python3
"""Exercise the actual selected-host identity vhost with native nginx and fixture TLS/HTTP only."""
from http.client import HTTPConnection
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from contextlib import contextmanager, ExitStack
import json
import re
import os
from pathlib import Path
import shutil
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import uuid

from identity_proxy_fixture import owned_http_paths

ROOT = Path(__file__).resolve().parent.parent
HOST = "gitmora.example.test"
PG_IMAGE = "gitmora-postgres:18.6-security1"


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def execute(args):
    result = subprocess.run(args, capture_output=True, timeout=25)
    assert result.returncode == 0, "Fixture process failed: " + result.stderr.decode()[-2000:]


def filter_policy():
    value = subprocess.run(["iptables-save", "-t", "filter"], capture_output=True, check=True, timeout=15).stdout.decode()
    return "\n".join(re.sub(r"\[\d+:\d+\]", "[counters]", line) for line in value.splitlines() if not line.startswith("#"))


@contextmanager
def private_network():
    """Optional native Linux host-bridge proof owns only its new internal network/container."""
    probe = os.environ.get("GITMORA_IDENTITY_ROUTE_PROBE")
    if not probe:
        yield None
        return
    assert os.uname().sysname == "Linux", "Host-bridge route proof requires native Linux, not Docker Desktop"
    probe = Path(probe).resolve(strict=True)
    assert probe.is_file(), "Select the separately compiled Linux route probe"
    name = "gitmora-identity-route-" + uuid.uuid4().hex
    baseline = None
    if os.environ.get("GITMORA_IDENTITY_ROUTE_ALLOW_FIXTURE_PORT") == "1":
        assert os.geteuid() == 0, "Fixture filter policy requires root"
        baseline = filter_policy()
    for _ in range(4):
        octet = 1 + uuid.uuid4().int % 250
        gateway = f"10.248.{octet}.1"
        result = subprocess.run(["docker", "network", "create", "--internal", "--subnet", f"10.248.{octet}.0/24", "--gateway", gateway, name], capture_output=True, timeout=20)
        if result.returncode == 0:
            break
        assert b"overlap" in result.stderr.lower(), "Private fixture network creation failed"
    else:
        raise AssertionError("No free fixture subnet found")
    try:
        yield dict(name=name, gateway=gateway, subnet=f"10.248.{octet}.0/24", probe=probe)
    finally:
        subprocess.run(["docker", "rm", "--force", name + "-probe"], capture_output=True, timeout=20)
        execute(["docker", "network", "rm", name])
        if baseline is not None:
            assert filter_policy() == baseline, "Filter rules changed after owned network cleanup"
            print("Owned fixture network/rule removed; complete original filter policy preserved (timestamps/counters excluded).")


@contextmanager
def fixture_port(private, port):
    """Opt-in only: allow one new private fixture bridge to its own ephemeral host port; restore exact policy."""
    if os.environ.get("GITMORA_IDENTITY_ROUTE_ALLOW_FIXTURE_PORT") != "1":
        yield
        return
    assert private and os.geteuid() == 0, "Fixture firewall scope requires an owned private network and root"
    result = subprocess.run(["docker", "network", "inspect", private["name"]], capture_output=True, check=True, timeout=15)
    network = json.loads(result.stdout)[0]
    assert network["Name"] == private["name"] and network["Internal"] and network["Driver"] == "bridge"
    assert network["IPAM"]["Config"] == [{"Subnet": private["subnet"], "Gateway": private["gateway"]}]
    interface = network.get("Options", {}).get("com.docker.network.bridge.name", "br-" + network["Id"][:12])
    assert re.fullmatch(r"br-[0-9a-f]{12}", interface) and socket.if_nametoindex(interface)

    baseline = filter_policy()
    rule = ["INPUT", "-i", interface, "-s", private["subnet"], "-d", private["gateway"] + "/32", "-p", "tcp", "--dport", str(port),
            "-m", "comment", "--comment", private["name"], "-j", "ACCEPT"]
    try:
        execute(["iptables", "-w", "5", "-I", rule[0], "1", *rule[1:]])
        yield
    finally:
        # Inspect the exact owned rule even if insertion acknowledgement was uncertain.
        found = subprocess.run(["iptables", "-w", "5", "-C", *rule], capture_output=True, timeout=15)
        assert found.returncode in (0, 1), "Owned fixture rule state unavailable"
        if found.returncode == 0:
            execute(["iptables", "-w", "5", "-D", *rule])
        assert filter_policy() == baseline, "Filter policy changed beyond the owned fixture rule"


def private_route_checks(private, folder, port):
    """The real Go RP adapter resolves a private host address and uses its ordinary Linux CA store."""
    for host, address, ca, expected in [(HOST, private["gateway"], "/verification/ca.pem", "accepted"),
                                      (HOST, private["gateway"], "/etc/ssl/certs/ca-certificates.crt", "rejected"),
                                      ("wrong.invalid", private["gateway"], "/verification/ca.pem", "rejected"),
                                      (HOST, "1.1.1.1", "/verification/ca.pem", "rejected")]:
        args = ["docker", "run", "--rm", "--name", private["name"] + "-probe", "--network", private["name"],
                "--user", "65532:65532", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "128m", "--pids-limit", "32",
                "--add-host", host + ":" + address, "--env", "SSL_CERT_FILE=" + ca,
                "--mount", f"type=bind,source={private['probe']},target=/verification/route-probe,readonly",
                "--mount", f"type=bind,source={folder / 'ca.pem'},target=/verification/ca.pem,readonly",
                "--entrypoint", "/verification/route-probe", PG_IMAGE, f"https://{host}:{port}/auth/realms/gitmora", expected]
        result = subprocess.run(args, capture_output=True, timeout=25)
        assert result.returncode == 0, "Private Linux OIDC route probe failed: " + result.stderr.decode()[-1000:]
        assert json.loads(result.stdout)["status"] == expected
    print("Actual unprivileged disconnected Linux RP: private host mapping, nginx HTTPS and actual OIDC discovery/PKCE accepted; wrong CA, wrong hostname and public destination rejected.")


def certificates(folder):
    """Create a fixture CA and an exact-host leaf without changing host trust."""
    execute(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
             "-subj", "/CN=Gitmora proxy fixture CA", "-keyout", str(folder / "ca.key"),
             "-out", str(folder / "ca.pem"), "-addext", "basicConstraints=critical,CA:TRUE",
             "-addext", "keyUsage=critical,keyCertSign,cRLSign", "-addext", "subjectKeyIdentifier=hash"])
    execute(["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-subj", "/CN=" + HOST,
             "-keyout", str(folder / "leaf.key"), "-out", str(folder / "leaf.csr")])
    extensions = folder / "leaf.ext"
    extensions.write_text("basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid,issuer\nsubjectAltName=DNS:" + HOST + "\n")
    execute(["openssl", "x509", "-req", "-in", str(folder / "leaf.csr"), "-CA", str(folder / "ca.pem"),
             "-CAkey", str(folder / "ca.key"), "-CAcreateserial", "-out", str(folder / "leaf.pem"),
             "-days", "1", "-extfile", str(extensions)])
    execute(["openssl", "genpkey", "-genparam", "-algorithm", "DH", "-pkeyopt", "group:ffdhe2048", "-out", str(folder / "dh.pem")])


def request(port, path, *, tls=None, hostname=HOST, method="GET", body=None, headers=None):
    connection = HTTPConnection("127.0.0.1", port, timeout=5)
    if tls:
        connection.sock = tls.wrap_socket(socket.create_connection(("127.0.0.1", port), timeout=5), server_hostname=hostname)
    try:
        connection.request(method, path, body=body, headers={"Host": HOST, **(headers or {})})
        response = connection.getresponse()
        return response.status, dict(response.getheaders()), response.read()
    finally:
        connection.close()


def main():
    nginx = shutil.which(os.environ.get("GITMORA_NGINX", "nginx"))
    assert nginx, "Install native nginx or set GITMORA_NGINX to its explicit binary"
    source = (ROOT / "deploy/nginx/gitmora.https.conf").read_text().replace("gitmora.example.com", HOST)
    with tempfile.TemporaryDirectory(prefix="gitmora-identity-proxy-") as temporary, private_network() as private, ExitStack() as resources:
        folder = Path(temporary)
        certificates(folder)
        http_port, tls_port = free_port(), free_port()
        resources.enter_context(fixture_port(private, tls_port))
        calls, lock = [], threading.Lock()

        class Backend(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                self.reply()

            def do_POST(self):
                self.reply()

            def reply(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                record = dict(backend=self.server.role, method=self.command, path=self.path, body=body.decode(), headers=dict(self.headers))
                with lock:
                    calls.append(record)
                if self.server.role == "provider" and self.path == "/auth/realms/gitmora/.well-known/openid-configuration":
                    issuer = f"https://{HOST}:{tls_port}/auth/realms/gitmora"
                    record.update(issuer=issuer, authorization_endpoint=issuer + "/protocol/openid-connect/auth", token_endpoint=issuer + "/protocol/openid-connect/token",
                                  jwks_uri=issuer + "/protocol/openid-connect/certs", id_token_signing_alg_values_supported=["RS256"], response_types_supported=["code"], subject_types_supported=["public"])
                raw = json.dumps(record).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        backend, application = ThreadingHTTPServer(("127.0.0.1", 0), Backend), ThreadingHTTPServer(("127.0.0.1", 0), Backend)
        backend.role, application.role = "provider", "application"
        workers = []
        for server in [backend, application]:
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            workers.append(worker)
        process = None
        try:
            acme = folder / "acme/.well-known/acme-challenge"
            acme.mkdir(parents=True)
            (acme / "probe").write_text("owned ACME challenge")
            replacements = {
                "listen 80;": f"listen 127.0.0.1:{http_port};", "listen [::]:80;": "",
                "listen 443 ssl http2;": f"listen 127.0.0.1:{tls_port} ssl http2;" + (f"\nlisten {private['gateway']}:{tls_port} ssl http2;" if private else ""),
                "listen [::]:443 ssl http2;": "",
                "/etc/letsencrypt/live/" + HOST + "/fullchain.pem": str(folder / "leaf.pem"),
                "/etc/letsencrypt/live/" + HOST + "/privkey.pem": str(folder / "leaf.key"),
                "/srv/gitmora/acme": str(folder / "acme"), "127.0.0.1:18081": "127.0.0.1:" + str(backend.server_port),
                "127.0.0.1:8080": "127.0.0.1:" + str(application.server_port),
                "/var/log/nginx/gitmora.access.log": str(folder / "access.log"), "/var/log/nginx/gitmora.error.log": str(folder / "proxy-error.log"),
            }
            for before, after in replacements.items():
                assert before in source, "Deployment vhost substitution no longer matches: " + before
                source = source.replace(before, after)
            config = folder / "nginx.conf"
            user = "user root;\n" if os.geteuid() == 0 else ""
            config.write_text(user + "worker_processes 1;\npid " + str(folder / "nginx.pid") + ";\nerror_log " + str(folder / "error.log") + " warn;\nevents { worker_connections 128; }\nhttp {\n" + owned_http_paths(folder) + source + "\n}\n")
            execute([nginx, "-e", "stderr", "-p", str(folder), "-c", str(config), "-t"])
            process = subprocess.Popen([nginx, "-e", "stderr", "-p", str(folder), "-c", str(config), "-g", "daemon off;"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            for _ in range(100):
                assert process.poll() is None, "Owned nginx exited"
                try:
                    with socket.create_connection(("127.0.0.1", tls_port), timeout=0.1):
                        break
                except OSError:
                    time.sleep(0.05)
            else:
                raise AssertionError("Owned nginx did not start")
            trusted = ssl.create_default_context(cafile=str(folder / "ca.pem"))
            for entry, code, destination in [("/auth", 308, "/auth/"), ("/auth/", 303, "/v1/auth/login")]:
                status, headers, _ = request(tls_port, entry, tls=trusted)
                assert status == code and headers["Location"] == "https://" + HOST + destination, "Identity entry does not reach the actual RP login route"
            status, _, raw = request(http_port, "/.well-known/acme-challenge/probe")
            assert status == 200 and raw == b"owned ACME challenge"
            status, headers, _ = request(http_port, "/auth/realms/gitmora/?state=fixture-secret", headers={"Host": "attacker.invalid"})
            assert status == 308 and headers["Location"] == "https://" + HOST + "/auth/realms/gitmora/?state=fixture-secret"
            for context, hostname in [(ssl.create_default_context(), HOST), (trusted, "wrong.invalid")]:
                try:
                    request(tls_port, "/auth/realms/gitmora/", tls=context, hostname=hostname)
                except ssl.SSLCertVerificationError:
                    pass
                else:
                    raise AssertionError("Untrusted CA or wrong TLS hostname accepted")
            status, _, _ = request(tls_port, "/auth/realms/gitmora/", tls=trusted, headers={"Host": "attacker.invalid"})
            assert status == 421, "Foreign HTTP host accepted"
            for path in ["/", "/v1/me", "/assets/example.css"]:
                status, _, raw = request(tls_port, path, tls=trusted)
                assert status == 200 and json.loads(raw)["backend"] == "application", "Application routing changed"
            for path in ["/auth/realms/gitmora/.well-known/openid-configuration", "/auth/resources/version/login/theme.css"]:
                status, _, raw = request(tls_port, path, tls=trusted)
                assert status == 200 and json.loads(raw)["path"] == path and json.loads(raw)["backend"] == "provider"
            status, _, raw = request(tls_port, "/auth/realms/gitmora/protocol/openid-connect/token?code=fixture-secret", tls=trusted,
                method="POST", body="fixture-body-secret", headers={"Authorization": "Basic fixture-header-secret", "Cookie": "fixture-cookie-secret",
                "X-Forwarded-Host": "attacker.invalid", "X-Forwarded-Proto": "http", "X-Forwarded-Port": "81", "X-Forwarded-Prefix": "/evil"})
            forwarded = json.loads(raw)
            assert status == 200 and forwarded["body"] == "fixture-body-secret"
            assert forwarded["headers"]["Host"] == HOST and forwarded["headers"]["X-Forwarded-Proto"] == "https"
            assert forwarded["headers"]["X-Forwarded-Host"] == HOST and forwarded["headers"]["X-Forwarded-Port"] == str(tls_port)
            assert "X-Forwarded-Prefix" not in forwarded["headers"]
            with lock:
                permitted_calls = sum(row["backend"] == "provider" for row in calls)
            for path in ["/auth/admin/", "/auth/realms/master/.well-known/openid-configuration", "/auth/metrics", "/auth/health/ready",
                         "/auth/realms/gitmora/../../admin/", "/auth/realms/gitmora/../master/.well-known/openid-configuration",
                         "/auth/realms/gitmora/%2e%2e/master/", "/auth/resources/%2e%2e/admin/", "/auth/realms/gitmora%2f..%2fmaster/", "/auth/%61dmin/",
                         "/auth/realms/gitmora/%252e%252e/master/", "/auth/realms/gitmora/%5c..%5cadmin/", "/auth/resources/%255cadmin/"]:
                status, _, _ = request(tls_port, path, tls=trusted)
                assert status == 404, "Forbidden provider path forwarded: " + path
            # A normalized escape into the RP still routes only to the RP, never to the provider's private API.
            status, _, raw = request(tls_port, "/auth/realms/gitmora/../../../admin/", tls=trusted)
            assert status == 200 and json.loads(raw)["backend"] == "application"
            with lock:
                assert sum(row["backend"] == "provider" for row in calls) == permitted_calls, "A forbidden route reached provider"
            logs = "".join(path.read_text(errors="replace") for path in folder.rglob("*.log"))
            assert "fixture-secret" not in logs and all(value not in logs for value in ["fixture-body-secret", "fixture-header-secret", "fixture-cookie-secret"])
            if private:
                private_route_checks(private, folder, tls_port)
            print("Actual shared-host nginx: application routing preserved, /auth realm/resources, private provider administration, trusted TLS/wrong CA/hostname denial, normalized route confinement, fixed authority and proof-free auth logs passed.")
        finally:
            if process and process.poll() is None:
                process.terminate()
                process.wait(timeout=10)
            for server in [backend, application]:
                server.shutdown()
                server.server_close()
            for worker in workers:
                worker.join(timeout=5)


if __name__ == "__main__":
    def interrupt(_number, _frame):
        raise KeyboardInterrupt("Owned fixture interrupted; unwinding resources")
    for number in [signal.SIGTERM, signal.SIGHUP]:
        signal.signal(number, interrupt)
    main()
