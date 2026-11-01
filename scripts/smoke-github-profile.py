#!/usr/bin/env python3
"""Inspect the actual GitHub Compose policy without creating services or using credentials."""

import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def main():
    with tempfile.TemporaryDirectory(prefix="gitmora-github-profile-") as name:
        folder = Path(name)
        (folder / "runtime.env").write_text("")
        env = {k: v for k, v in os.environ.items() if k in {"PATH", "HOME"}}
        env.update(
            WORKGRAPH_STATE_DIR=str(folder), WORKGRAPH_IMAGE="gitmora:profile-fixture"
        )
        config = json.loads(
            subprocess.check_output(
                [
                    "docker",
                    "compose",
                    "-f",
                    str(ROOT / "compose.yaml"),
                    "-f",
                    str(ROOT / "deploy/compose.github-com.yaml"),
                    "config",
                    "--format",
                    "json",
                ],
                env=env,
                stderr=subprocess.PIPE,
            )
        )
        services = config["services"]
        relay = services["github-egress"]
        assert relay["command"][-2:] == ["--target", "api.github.com:443"]
        assert (
            not relay.get("secrets")
            and not relay.get("volumes")
            and not relay.get("ports")
        )
        assert set(relay["networks"]) == {"private", "github_outbound"}
        assert config["networks"]["private"]["internal"] is True
        for role in ["api", "worker"]:
            service = services[role]
            assert set(service["networks"]) == {"private"}
            assert service["environment"]["WORKGRAPH_GITHUB_ENABLED"] == "true"
            assert (
                service["environment"]["WORKGRAPH_GITHUB_PROXY_URL"]
                == "http://github-egress:8080"
            )
        print(
            json.dumps(
                dict(
                    result="passed",
                    provider="GitHub.com",
                    private_api_worker=True,
                    credential_free_fixed_relay=True,
                )
            )
        )


if __name__ == "__main__":
    main()
