"""Loopback HTTP rendering of the actual shared-host nginx routes; TLS is verified by smoke-identity-proxy."""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import time

ROOT = Path(__file__).resolve().parent.parent


@contextmanager
def loopback_proxy(folder, public_port, api_port, identity_port, *, download_root=None):
    """Run only an owned nginx process; preserve the production route policy for same-origin browser acceptance."""
    nginx = shutil.which(os.environ.get("GITMORA_NGINX", "nginx"))
    assert nginx, "Install native nginx for shared-origin identity verification"
    source = (ROOT / "deploy/nginx/gitmora.https.conf").read_text()
    pieces = source.split("\nserver {")
    assert len(pieces) == 3, "Selected vhost structure changed; review fixture rendering"
    source = pieces[0] + "\nserver {" + pieces[2]
    lines = []
    for line in source.splitlines():
        if line.strip().startswith(("listen [::]:443", "ssl_certificate", "include /etc/letsencrypt/options", "ssl_dhparam")):
            continue
        lines.append(line.replace("listen 443 ssl http2;", f"listen 127.0.0.1:{public_port};"))
    source = "\n".join(lines)
    source = source.replace("https://gitmora.tasktopia.online", f"http://127.0.0.1:{public_port}")
    source = source.replace("gitmora.tasktopia.online", "127.0.0.1")
    source = source.replace("127.0.0.1:8080", f"127.0.0.1:{api_port}").replace("127.0.0.1:18081", f"127.0.0.1:{identity_port}")
    source = source.replace("/var/log/nginx/gitmora.access.log", str(folder / "edge-access.log")).replace("/var/log/nginx/gitmora.error.log", str(folder / "edge-error.log"))
    if download_root is not None:
        source = source.replace("root /srv/gitmora-public;", "root " + json.dumps(str(download_root)) + ";")
    config = folder / "edge-nginx.conf"
    user = "user root;\n" if os.geteuid() == 0 else ""
    config.write_text(user + "worker_processes 1;\npid " + str(folder / "edge-nginx.pid") + ";\nerror_log " + str(folder / "edge-bootstrap.log") + " warn;\nevents { worker_connections 128; }\nhttp {\n" + source + "\n}\n")
    checked = subprocess.run([nginx, "-p", str(folder), "-c", str(config), "-t"], capture_output=True, timeout=10)
    assert checked.returncode == 0, "Shared-origin fixture nginx configuration invalid"
    process = subprocess.Popen([nginx, "-p", str(folder), "-c", str(config), "-g", "daemon off;"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(100):
            assert process.poll() is None, "Owned shared-origin nginx exited"
            try:
                with socket.create_connection(("127.0.0.1", public_port), timeout=0.1):
                    break
            except OSError:
                time.sleep(0.05)
        else:
            raise AssertionError("Owned shared-origin nginx did not start")
        yield
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=10)
