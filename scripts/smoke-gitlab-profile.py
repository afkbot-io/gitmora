#!/usr/bin/env python3
"""Validate custom GitLab/CA profiles and their actual bind policy with an owned stopped probe."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid

ROOT = Path(__file__).resolve().parent.parent
PG_IMAGE = "gitmora-postgres:18.6-security1"


def verify_bind(folder, mount):
    """Check engine read-only state and missing-source refusal, not optional JSON defaults."""
    project = "gitmora-ca-probe-" + uuid.uuid4().hex
    config = folder / "probe.json"
    probe = {"services": {"probe": {"image": PG_IMAGE, "network_mode": "none", "volumes": [mount]}}}
    command = ["docker", "compose", "--project-name", project, "-f", str(config)]
    subprocess.run(["docker", "image", "inspect", PG_IMAGE], check=True, capture_output=True)
    try:
        config.write_text(json.dumps(probe))
        subprocess.run([*command, "create", "--no-build", "--pull", "never"], check=True, capture_output=True)
        container = subprocess.check_output([*command, "ps", "--all", "-q", "probe"], text=True).strip()
        inspected = json.loads(subprocess.check_output(["docker", "inspect", container]))[0]
        selected = [v for v in inspected["Mounts"] if v["Destination"] == mount["target"]]
        assert len(selected) == 1 and selected[0]["RW"] is False, "Engine allowed writable CA mount"
        subprocess.run([*command, "down", "--volumes"], check=True, capture_output=True)
        missing = folder / "missing-ca.pem"
        probe["services"]["probe"]["volumes"] = [{**mount, "source": str(missing)}]
        config.write_text(json.dumps(probe))
        rejected = subprocess.run([*command, "create", "--no-build", "--pull", "never"], capture_output=True)
        assert rejected.returncode != 0 and not missing.exists(), "Missing CA source was created or accepted"
    finally:
        subprocess.run([*command, "down", "--volumes"], check=True, capture_output=True)


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
                assert len(selected["volumes"]) == 1
                mount = selected["volumes"][0]
                assert mount["type"] == "bind" and mount["source"] == str(folder / "ca.pem")
                assert mount["target"] == "/etc/gitmora/trusted-ca.pem" and mount["read_only"] is True
                # Older Compose omits false/empty bind options in canonical JSON.
                assert mount.get("bind", {}).get("create_host_path", False) is False
        verify_bind(folder, mount)
        for required in ("GITMORA_GITLAB_URL", "GITMORA_GITLAB_AUTHORITY", "GITMORA_GITLAB_CA_BUNDLE"):
            absent = {key: value for key, value in environment.items() if key != required}
            assert subprocess.run(command, env=absent, capture_output=True).returncode != 0, "Missing operator policy silently defaulted"
        print(json.dumps(dict(smoke="custom_gitlab_compose", result="passed", policies=["private", "public"], port=8443, prefix="/gitlab", ca_mount=True, engine_read_only=True, missing_ca_rejected=True, secret_isolation=True)))


if __name__ == "__main__":
    main()
