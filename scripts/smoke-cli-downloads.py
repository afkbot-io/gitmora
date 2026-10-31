#!/usr/bin/env python3
"""Exercise actual shared-host nginx download policy with isolated bytes and fixture backends."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import json
from pathlib import Path
import socket
import tempfile
import threading
import urllib.error
import urllib.request

from identity_proxy_fixture import loopback_proxy

def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


class Backend(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        raw = b"fixture-application"
        self.send_response(200)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main():
    with tempfile.TemporaryDirectory(prefix="gitmora-downloads-") as name:
        folder = Path(name)
        selected = folder / "downloads/cli/0.1.0-fixture"
        selected.mkdir(parents=True)
        raw = b"synthetic-native-client\x00\x01"
        (selected / "gitmora_linux_amd64").write_bytes(raw)
        (selected / "unsafe-link").symlink_to("gitmora_linux_amd64")
        fixture = ThreadingHTTPServer(("127.0.0.1", 0), Backend)
        threading.Thread(target=fixture.serve_forever, daemon=True).start()
        port = free_port()
        try:
            with loopback_proxy(folder, port, fixture.server_port, fixture.server_port, download_root=folder):
                base = f"http://127.0.0.1:{port}"

                def request(path, method="GET"):
                    req = urllib.request.Request(base + path, method=method)
                    try:
                        response = urllib.request.urlopen(req, timeout=5)
                    except urllib.error.HTTPError as error:
                        response = error
                    with response:
                        return response.status, response.headers, response.read()

                path = "/downloads/cli/0.1.0-fixture/gitmora_linux_amd64"
                status, headers, body = request(path)
                assert status == 200 and body == raw, "Download did not serve exact local artifact bytes"
                assert headers["Content-Type"] == "application/octet-stream"
                assert headers["Content-Disposition"] == "attachment" and headers["X-Content-Type-Options"] == "nosniff"
                assert "immutable" in headers["Cache-Control"] and not headers.get("Set-Cookie")
                assert request(path, "HEAD")[2] == b""
                for method in ("POST", "PUT", "DELETE"):
                    assert request(path, method)[0] in (403, 405), "Download accepted mutation"
                assert request("/downloads/cli/0.1.0-fixture/")[0] in (403, 404), "Directory listing exposed"
                assert request("/downloads/cli/0.1.0-fixture/unsafe-link")[0] in (403, 404), "Artifact symlink followed"
                assert request("/downloads/cli/0.1.0-fixture/missing")[0] == 404
                assert request("/health/ready")[2] == b"fixture-application", "Application route changed"
                assert request("/auth/realms/gitmora/.well-known/openid-configuration")[2] == b"fixture-application", "Identity route changed"
                assert request("/auth/admin/")[0] == 404
                print(json.dumps(dict(smoke="cli_downloads", result="passed", sha256=hashlib.sha256(raw).hexdigest())))
        finally:
            fixture.shutdown()
            fixture.server_close()


if __name__ == "__main__":
    main()
