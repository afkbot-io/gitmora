#!/usr/bin/env python3
"""Real operator CLI and Compose rendering for two unrelated private installations; no services started."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location(
    "nginx_renderer", ROOT / "scripts/render-nginx.py"
)
renderer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(renderer)


def main():
    env = {k: v for k, v in os.environ.items() if k in {"PATH", "HOME", "LANG"}}
    with tempfile.TemporaryDirectory(prefix="gitmora-self-host-config-") as name:
        folder = Path(name)
        for number, host in enumerate(
            ["gitmora.alpha.example", "context.beta.example"]
        ):
            state = folder / f"private state {number}"
            policies = [
                "identity",
                "identity-auth",
                "github-com",
                "gitlab-custom",
                "github-enterprise.example",
                "gitlab-ca",
            ]
            overlays = [
                ROOT / "deploy" / f"compose.{policy}.yaml" for policy in policies
            ]
            init = [
                str(ROOT / "bin/gitmorad"),
                "deployment",
                "init",
                "--directory",
                str(state),
                "--compose",
                str(ROOT / "compose.yaml"),
                "--image",
                "gitmora:configuration-fixture",
                "--port",
                str(18080 + number),
            ]
            for overlay in overlays:
                init += ["--overlay", str(overlay)]
            subprocess.run(init, env=env, check=True, capture_output=True)
            # Only private compose.env supplies these values; no operator exports or checkout .env.
            bundle = folder / f"ca-{number}.pem"
            bundle.write_text("Synthetic public CA config placeholder")
            settings = dict(
                GITMORA_PUBLIC_HOST=host,
                GITMORA_REALM="gitmora",
                GITMORA_CLIENT_ID="gitmora",
                GITMORA_PRIVATE_SUBNET=f"10.247.{number}.0/24",
                GITMORA_PRIVATE_GATEWAY=f"10.247.{number}.1",
                GITMORA_GITLAB_URL="https://code.example.test:8443/gitlab",
                GITMORA_GITLAB_AUTHORITY="code.example.test:8443",
                GITMORA_GITLAB_VISIBILITY="private",
                GITMORA_GITHUB_ENTERPRISE_URL="https://hub.example.test:9443/code",
                GITMORA_GITHUB_ENTERPRISE_AUTHORITY="hub.example.test:9443",
                GITMORA_GITHUB_ENTERPRISE_VISIBILITY="private",
                GITMORA_GITLAB_CA_BUNDLE=str(bundle),
            )
            policy = state / "compose.env"
            policy.write_text("".join(k + "=" + v + "\n" for k, v in settings.items()))
            policy.chmod(0o600)
            (state / "runtime.env").write_text(
                (ROOT / "deploy/gitmora.runtime.env.example").read_text()
            )
            # Status resolves/validates its complete profile using the actual new CLI env-file path.
            subprocess.run(
                [
                    str(ROOT / "bin/gitmorad"),
                    "deployment",
                    "status",
                    "--directory",
                    str(state),
                ],
                env=env,
                check=True,
                capture_output=True,
            )
            command = [
                "docker",
                "compose",
                "--env-file",
                str(policy),
                "-f",
                str(ROOT / "compose.yaml"),
            ]
            for overlay in overlays:
                command += ["-f", str(overlay)]
            config_env = {
                **env,
                "WORKGRAPH_STATE_DIR": str(state),
                "WORKGRAPH_IMAGE": "gitmora:configuration-fixture",
                "WORKGRAPH_HTTP_PORT": str(18080 + number),
            }
            empty_policy = folder / "empty.env"
            empty_policy.write_text("")
            missing_host = subprocess.run(
                ["docker", "compose", "--env-file", str(empty_policy),
                 "-f", str(ROOT / "compose.yaml"),
                 "-f", str(ROOT / "deploy/compose.identity.yaml"),
                 "-f", str(ROOT / "deploy/compose.identity-auth.yaml"),
                 "config", "--quiet"],
                env=config_env, capture_output=True, text=True, timeout=30,
            )
            assert missing_host.returncode != 0 and "GITMORA_PUBLIC_HOST" in missing_host.stderr, (
                "Missing domain silently selected another installation"
            )
            profile = json.loads(
                subprocess.check_output(
                    [*command, "config", "--format", "json"],
                    env=config_env,
                    stderr=subprocess.PIPE,
                )
            )
            services = profile["services"]
            api = services["api"]
            worker = services["worker"]
            assert api["environment"]["WORKGRAPH_PUBLIC_URL"] == "https://" + host
            assert (
                api["environment"]["WORKGRAPH_OIDC_ISSUER"]
                == "https://" + host + "/auth/realms/gitmora"
            )
            assert (
                host + "=" + settings["GITMORA_PRIVATE_GATEWAY"] in api["extra_hosts"]
            )
            assert not worker.get("extra_hosts")
            assert "WORKGRAPH_OIDC_CLIENT_SECRET_FILE" not in worker["environment"]
            for role in [api, worker]:
                assert set(role["networks"]) == {"private"}
            for service, authority in [
                ("gitlab-egress", settings["GITMORA_GITLAB_AUTHORITY"]),
                (
                    "github-enterprise-egress",
                    settings["GITMORA_GITHUB_ENTERPRISE_AUTHORITY"],
                ),
            ]:
                relay = services[service]
                assert authority in relay["command"]
                assert (
                    not relay.get("secrets")
                    and not relay.get("volumes")
                    and not relay.get("ports")
                )
            assert profile["networks"]["private"]["internal"]
            assert (
                profile["networks"]["private"]["ipam"]["config"][0]["gateway"]
                == settings["GITMORA_PRIVATE_GATEWAY"]
            )
            rendered = renderer.render(
                host, 18081, 19081, str(folder / "acme"), str(folder / "public")
            )
            tls = rendered["gitmora.https.conf"]
            assert (
                "server_name " + host + ";" in tls
                and "127.0.0.1:18081;" in tls
                and "127.0.0.1:19081/auth/" in tls
            )
            assert (
                "gitmora.tasktopia.online" not in tls
                and "options-ssl-nginx" not in tls
                and "ssl-dhparams" not in tls
            )
            assert "/live/" + host + "/fullchain.pem" in tls
            assert "/live/" + host in rendered["20-gitmora-reload"]
            # Missing explicit provider policy fails, rather than opening a fallback destination.
            policy.write_text("GITMORA_PUBLIC_HOST=" + host + "\n")
            assert (
                subprocess.run(
                    [
                        str(ROOT / "bin/gitmorad"),
                        "deployment",
                        "status",
                        "--directory",
                        str(state),
                    ],
                    env=env,
                    capture_output=True,
                ).returncode
                != 0
            )
        for bad in [
            "https://example.com",
            "example.com/path",
            "example.com:8443",
            "*.example.com",
            "example.com;return 200;",
            "127.0.0.1",
        ]:
            try:
                renderer.hostname(bad)
            except Exception:
                pass
            else:
                raise AssertionError("Unsafe hostname accepted")
    print(
        json.dumps(
            dict(
                result="passed",
                independent_domains=2,
                private_env_persisted=True,
                custom_relays=3,
                nginx_domain_ports=True,
                missing_policy_denied=True,
                no_services_started=True,
            )
        )
    )


if __name__ == "__main__":
    main()
