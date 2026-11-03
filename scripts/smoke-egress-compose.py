#!/usr/bin/env python3
"""Actual selected-origin Compose profile; optional unauthenticated GitLab metadata over verified TLS."""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
PG_IMAGE = "gitmora-postgres:18.6-vector1"


def run(args, env=None, check=True):
    result = subprocess.run(args, cwd=ROOT, env=env, capture_output=True, timeout=150)
    if check and result.returncode:
        # This harness owns synthetic state and no provider credential; keep bounded diagnostics private.
        log = ROOT / "build/egress-compose-debug.log"
        log.write_bytes((result.stderr + result.stdout)[-8192:])
        log.chmod(0o600)
    assert not check or result.returncode == 0, "Selected-provider Compose verification failed; private diagnostics suppressed"
    return result


def main():
    with tempfile.TemporaryDirectory(prefix="gitmora egress ") as folder:
        state = Path(folder) / "state"
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        overlays = [ROOT / "deploy/compose.gitlab-com.yaml", ROOT / "deploy/compose.identity.yaml"]
        args = [str(ROOT / "bin/gitmorad"), "deployment", "init", "--directory", str(state), "--compose", str(ROOT / "compose.yaml"), "--port", str(port), "--image", os.environ.get("GITMORA_DEPLOYMENT_TEST_IMAGE", "gitmora:local")]
        for path in overlays:
            args += ["--overlay", str(path)]
        installation = json.loads(run(args).stdout)
        shutil.copyfile(ROOT / "deploy/gitmora.runtime.env.example", state / "runtime.env")
        project = "gitmora-" + installation["id"].replace("-", "")
        environment = {**os.environ, "WORKGRAPH_STATE_DIR": str(state), "WORKGRAPH_HTTP_PORT": str(port), "WORKGRAPH_IMAGE": installation["image"], "GITMORA_PUBLIC_HOST": "gitmora.example.test", "GITMORA_PRIVATE_SUBNET": "10.249.88.0/24", "GITMORA_PRIVATE_GATEWAY": "10.249.88.1"}
        prefix = ["docker", "compose", "--project-name", project, "--project-directory", str(ROOT), "--file", str(ROOT / "compose.yaml")]
        for path in overlays:
            prefix += ["--file", str(path)]
        manifest = json.loads(run([*prefix, "config", "--format", "json"], environment).stdout)
        services = manifest["services"]
        assert manifest["networks"]["private"]["internal"]
        assert "gitmora.example.test=10.249.88.1" in services["api"].get("extra_hosts", []), "Private RP has no selected identity host mapping"
        assert not any(value.startswith("gitmora.example.test=") for value in services["worker"].get("extra_hosts", [])), "Ordinary worker received an unnecessary IdP route"
        activated = json.loads(run([*prefix, "--file", str(ROOT / "deploy/compose.identity-auth.yaml"), "config", "--format", "json"], environment).stdout)["services"]
        assert activated["api"]["environment"]["WORKGRAPH_OIDC_ISSUER"] == "https://gitmora.example.test/auth/realms/gitmora"
        assert activated["api"]["environment"]["WORKGRAPH_OIDC_CLIENT_ID"] == "gitmora"
        assert activated["api"]["environment"]["WORKGRAPH_OIDC_CLIENT_SECRET_FILE"] == "/run/secrets/oidc_client_secret"
        assert not any(key.startswith("WORKGRAPH_OIDC_") for key in activated["worker"]["environment"]), "Provider authentication policy leaked into ordinary worker"
        for name in ["api", "worker"]:
            assert set(services[name]["networks"]) == {"private"}
            assert services[name]["environment"]["WORKGRAPH_GITLAB_PROXY_URL"] == "http://gitlab-egress:8080"
            assert services[name]["environment"]["WORKGRAPH_GITLAB_ALLOWLIST"] == "https://gitlab.com"
            assert set(services[name]["depends_on"]) == {"migrate", "gitlab-egress"}
        relay = services["gitlab-egress"]
        assert set(relay["networks"]) == {"private", "gitlab_outbound"} and not relay.get("ports")
        assert not relay.get("secrets") and not relay.get("environment") and not relay.get("volumes")
        assert relay["user"] == "65532:65532" and relay["read_only"] and relay["cap_drop"] == ["ALL"]
        assert relay["command"][-1] == "gitlab.com:443"
        prepared_probe = os.environ.get("GITMORA_EGRESS_PROBE")
        probe_binary = Path(prepared_probe).resolve() if prepared_probe else ROOT / "bin/verification/egress-probe"
        if prepared_probe:
            assert probe_binary.is_file() and os.access(probe_binary, os.X_OK), "Explicit verified probe binary is required"
        else:
            arch = run(["docker", "version", "--format", "{{.Server.Arch}}"]).stdout.decode().strip()
            run(["go", "build", "-trimpath", "-o", str(probe_binary), "./internal/platform/egress/testdata/probe"], {**os.environ, "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": arch})
        try:
            run([*prefix, "up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "90"], environment)
            relay_id = run([*prefix, "ps", "-q", "gitlab-egress"], environment).stdout.decode().strip()
            actual = json.loads(run(["docker", "inspect", relay_id]).stdout)[0]
            assert actual["Config"]["User"] == "65532:65532" and actual["HostConfig"]["ReadonlyRootfs"]
            assert all(not value.startswith(("WORKGRAPH_DATABASE_URL", "WORKGRAPH_ENCRYPTION_KEYS", "WORKGRAPH_OIDC_CLIENT_SECRET", "WORKGRAPH_MODEL_TOKEN")) for value in actual["Config"]["Env"])
            for name in ["api", "worker"]:
                container_id = run([*prefix, "ps", "-q", name], environment).stdout.decode().strip()
                actual_role = json.loads(run(["docker", "inspect", container_id]).stdout)[0]
                assert set(actual_role["NetworkSettings"]["Networks"]) == {project + "_private"}
            probe = ["docker", "run", "--rm", "--network", project + "_private", "--user", "65532:65532", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--mount", f"type=bind,source={probe_binary},target=/verification/egress-probe,readonly", "--entrypoint", "/verification/egress-probe", PG_IMAGE]
            offline = json.loads(run([*probe, "offline"]).stdout)
            assert offline["status"] == "private route and denied authority verified"
            if os.environ.get("GITMORA_EGRESS_LIVE") == "1":
                live = json.loads(run([*probe, "live"]).stdout)
                assert live["status"] == "verified GitLab TLS route" and live["authenticated"] is False
                print("Live selected GitLab HTTPS metadata status:", live["http_status"], "(unauthenticated; no provider token or project ID claim).")
            print("Actual selected-origin Compose profile: ordered overlays, API-only private IdP host mapping, healthy API/worker/relay, private-only application network, credential-free nonroot/read-only relay, denied other authority and absent direct external route passed.")
        finally:
            run([*prefix, "down", "--volumes", "--remove-orphans"], environment)


if __name__ == "__main__":
    main()
