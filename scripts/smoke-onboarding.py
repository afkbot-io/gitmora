#!/usr/bin/env python3
"""Execute the actual UI installer against an owned HTTPS fixture and native CLI bytes."""

from __future__ import annotations

import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import ssl
import subprocess
import tempfile
import threading

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--client", type=Path, default=ROOT / "bin/gitmora")
    args = parser.parse_args()
    missing = subprocess.run(
        ["sh", str(ROOT / "web/public/install.sh")],
        capture_output=True,
        text=True,
        timeout=5,
    )
    assert missing.returncode == 2 and "--server" in missing.stderr, (
        "Installer silently selected another instance"
    )
    systems = {"Darwin": "darwin", "Linux": "linux"}
    architectures = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}
    filename = (
        f"gitmora_{systems[platform.system()]}_{architectures[platform.machine()]}"
    )
    client = args.client.resolve()
    raw = client.read_bytes()
    version = json.loads(subprocess.check_output([str(client), "version"], text=True))
    manifest = json.dumps(version).encode()
    sums = (
        f"{hashlib.sha256(raw).hexdigest()}  {filename}\n"
        f"{hashlib.sha256(manifest).hexdigest()}  release.json\n"
    ).encode()
    bodies = {}

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            body = bodies.get(self.path)
            self.send_response(200 if body is not None else 404)
            self.end_headers()
            if body is not None:
                self.wfile.write(body)

    with tempfile.TemporaryDirectory(prefix="gitmora-onboarding-") as name:
        folder = Path(name)
        certificate, key = folder / "ca.pem", folder / "key.pem"
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
                "/CN=127.0.0.1",
                "-addext",
                "subjectAltName=IP:127.0.0.1",
                "-keyout",
                str(key),
                "-out",
                str(certificate),
            ],
            check=True,
            capture_output=True,
        )
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(certificate, key)
        server.socket = tls.wrap_socket(server.socket, server_side=True)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            base = f"https://127.0.0.1:{server.server_port}"
            module = (ROOT / "build/web-tests/web/src/domain/onboarding.js").as_uri()
            code = f"import {{clientInstallCommand,publicClientVersion}} from {json.dumps(module)}; console.log(JSON.stringify({{version:publicClientVersion,command:clientInstallCommand(process.argv[1],process.argv[2])}}));"
            generated = json.loads(
                subprocess.check_output(
                    ["node", "--input-type=module", "-e", code, base, filename],
                    text=True,
                )
            )
            prefix = f"/downloads/cli/{generated['version']}/"
            cases = (
                "corrupt-binary",
                "corrupt-manifest",
                "missing-checksum",
                "duplicate-checksum",
                "duplicate-binary-only",
                "valid",
            )
            for case in cases:
                bodies.clear()
                bodies.update(
                    {
                        "/install.sh": (ROOT / "web/public/install.sh").read_bytes(),
                        prefix + filename: raw,
                        prefix + "release.json": manifest,
                        prefix + "SHA256SUMS": sums,
                    }
                )
                if case == "corrupt-binary":
                    bodies[prefix + filename] = b"not executable downloaded bytes"
                if case == "corrupt-manifest":
                    bodies[prefix + "release.json"] = b'{"corrupt":true}'
                if case == "missing-checksum":
                    bodies[prefix + "SHA256SUMS"] = sums.splitlines(keepends=True)[0]
                if case == "duplicate-checksum":
                    bodies[prefix + "SHA256SUMS"] = (
                        sums + sums.splitlines(keepends=True)[0]
                    )
                if case == "duplicate-binary-only":
                    bodies[prefix + "SHA256SUMS"] = (
                        sums.splitlines(keepends=True)[0] * 2
                    )
                home = folder / case
                home.mkdir(mode=0o700)
                temporary = home / "tmp"
                temporary.mkdir(mode=0o700)
                environment = {
                    k: v for k, v in os.environ.items() if k in {"PATH", "LANG"}
                }
                environment.update(
                    HOME=str(home),
                    TMPDIR=str(temporary),
                    CURL_CA_BUNDLE=str(certificate),
                )
                result = subprocess.run(
                    ["/bin/sh", "-c", generated["command"]],
                    env=environment,
                    capture_output=True,
                    text=True,
                    timeout=90,
                )
                installed = home / ".local/bin/gitmora"
                if case == "valid":
                    if result.returncode != 0 or not installed.is_file():
                        (ROOT / "build/onboarding-failure.log").write_text(
                            result.stdout + "\n" + result.stderr
                        )
                    assert result.returncode == 0 and installed.is_file(), (
                        "Verified copied installer failed; see build/onboarding-failure.log"
                    )
                    assert (
                        hashlib.sha256(installed.read_bytes()).digest()
                        == hashlib.sha256(raw).digest()
                    )
                    observed = json.loads(
                        subprocess.check_output(
                            [str(installed), "version"], env=environment, text=True
                        )
                    )
                    assert observed == version
                else:
                    assert result.returncode != 0 and not installed.exists(), (
                        "Invalid downloaded bytes reached installation"
                    )
            print(
                json.dumps(
                    dict(
                        smoke="copied_ui_installer",
                        result="passed",
                        platform=filename,
                        cases=len(cases),
                        client_version=version["version"],
                    )
                )
            )
        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
