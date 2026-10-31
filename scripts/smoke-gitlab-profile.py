#!/usr/bin/env python3
"""Validate the actual custom GitLab/CA Compose profiles without creating any service."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def main():
    with tempfile.TemporaryDirectory(prefix="gitmora-custom-gitlab-profile-") as name:
        folder = Path(name)
        (folder / "runtime.env").write_text("")
        (folder / "ca.pem").write_text("synthetic config-only public CA placeholder")
        environment = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME"}}
        environment.update(WORKGRAPH_STATE_DIR=str(folder), WORKGRAPH_IMAGE="gitmora:profile-fixture",
                           GITMORA_GITLAB_URL="https://code.example.test:8443/gitlab",
                           GITMORA_GITLAB_AUTHORITY="code.example.test:8443",
                           GITMORA_GITLAB_CA_BUNDLE=str(folder / "ca.pem"))
        command = ["docker", "compose", "-f", str(ROOT / "compose.yaml"), "-f", str(ROOT / "deploy/compose.gitlab-custom.yaml"),
                   "-f", str(ROOT / "deploy/compose.gitlab-ca.yaml"), "config", "--format", "json"]
        for visibility in ("private", "public"):
            environment["GITMORA_GITLAB_VISIBILITY"] = visibility
            config = json.loads(subprocess.check_output(command, env=environment, stderr=subprocess.PIPE, text=True))
            services = config["services"]
            relay = services["gitlab-egress"]
            assert relay["command"][-4:] == ["--target", "code.example.test:8443", "--visibility", visibility]
            assert not relay.get("secrets") and not relay.get("volumes") and not relay.get("ports")
            assert set(relay["networks"]) == {"private", "gitlab_outbound"}
            assert config["networks"]["private"]["internal"] is True
            for service in ("api", "worker"):
                selected = services[service]
                assert set(selected["networks"]) == {"private"}
                assert selected["environment"]["WORKGRAPH_GITLAB_ALLOWLIST"] == environment["GITMORA_GITLAB_URL"]
                assert selected["environment"]["SSL_CERT_FILE"] == "/etc/gitmora/trusted-ca.pem"
                assert selected["volumes"] == [{"type": "bind", "source": str(folder / "ca.pem"), "target": "/etc/gitmora/trusted-ca.pem", "read_only": True, "bind": {"create_host_path": False}}]
        for required in ("GITMORA_GITLAB_URL", "GITMORA_GITLAB_AUTHORITY", "GITMORA_GITLAB_CA_BUNDLE"):
            absent = {key: value for key, value in environment.items() if key != required}
            assert subprocess.run(command, env=absent, capture_output=True).returncode != 0, "Missing operator policy silently defaulted"
        print(json.dumps(dict(smoke="custom_gitlab_compose", result="passed", policies=["private", "public"], port=8443, prefix="/gitlab", ca_mount=True, secret_isolation=True)))


if __name__ == "__main__":
    main()
