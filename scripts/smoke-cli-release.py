#!/usr/bin/env python3
"""Verify standalone install/update through real Git/API/SQLite/PostgreSQL; all identities are synthetic."""
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("api_smoke", ROOT / "scripts/smoke-api.py")
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)
STAMP = "workgraph.local/platform/internal/distribution/buildinfo"


def execute(args, *, environment, directory, data=None, success=True):
    result = subprocess.run([str(value) for value in args], cwd=directory, env=environment,
                            input=json.dumps(data) if data is not None else None,
                            capture_output=True, text=True, timeout=120)
    assert (result.returncode == 0) == success, "Standalone process outcome mismatch (diagnostics retained in process memory)"
    return result.stdout


def verify_release(directory):
    """Inspect complete immutable package data before selecting/executing the native artifact."""
    manifest = json.loads((directory / "release.json").read_text())
    assert manifest["schema_version"] == 1 and len(manifest["revision"]) == 40
    expected_names = set()
    for item in manifest["artifacts"]:
        assert item["os"] in ("linux", "darwin") and item["arch"] in ("arm64", "amd64")
        assert item["name"] == f'gitmora_{item["os"]}_{item["arch"]}' and item["name"] not in expected_names
        expected_names.add(item["name"])
        file = directory / item["name"]
        assert file.is_file() and not file.is_symlink()
        raw = file.read_bytes()
        assert len(raw) == item["size"] and hashlib.sha256(raw).hexdigest() == item["sha256"]
    checked = set()
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split("  ")
        assert name in expected_names | {"release.json"} and name not in checked
        checked.add(name)
        assert hashlib.sha256((directory / name).read_bytes()).hexdigest() == digest
    assert checked == expected_names | {"release.json"}
    assert set(path.name for path in directory.iterdir()) == expected_names | {"release.json", "SHA256SUMS"}
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release", type=Path)
    args = parser.parse_args()
    schema = "client_release_" + uuid.uuid4().hex
    api.sql(f'CREATE SCHEMA "{schema}"')
    environment = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR", "USER", "LANG", "SHELL"}}
    environment.update(WORKGRAPH_DATABASE_URL=api.BASE_DSN + "&search_path=" + schema,
                       GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_AUTHOR_NAME="Client fixture",
                       GIT_COMMITTER_NAME="Client fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid", GIT_COMMITTER_EMAIL="fixture@example.invalid")
    process = None
    try:
        assert api.command(["migrate"], environment).returncode == 0
        boot = api.command(["bootstrap", "--project", "Standalone fixture", "--repository", "Client fixture", "--gitlab-url", "https://gitlab.internal", "--gitlab-project-id", "42"], environment)
        assert boot.returncode == 0
        identity = json.loads(boot.stdout)
        environment["WORKGRAPH_TOKEN"] = identity["token"]
        process, address = api.start_api(environment)
        base = "http://" + address
        with tempfile.TemporaryDirectory(prefix="gitmora standalone space's-") as name:
            folder = Path(name)
            install = folder / "stable bin"
            repository = folder / "repository"
            install.mkdir(); repository.mkdir()
            first, second = folder / "first-client", folder / "second-client"
            for version, path in (("first", first), ("second", second)):
                if version == "first" and args.release:
                    manifest = verify_release(args.release.resolve())
                    operating_system = {"Darwin": "darwin", "Linux": "linux"}[platform.system()]
                    architecture = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}[platform.machine()]
                    first = args.release.resolve() / f"gitmora_{operating_system}_{architecture}"
                    info = json.loads(execute([first, "version"], environment=environment, directory=folder))
                    assert info["revision"] == manifest["revision"] and info["version"] == manifest["version"]
                else:
                    build_env = environment | {"CGO_ENABLED": "0"}
                    execute(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", f"-X {STAMP}.version=0.0.0-fixture.{version}", "-o", path, "./cmd/workgraph"], environment=build_env, directory=ROOT)
            receipt = json.loads(execute([first, "self", "install", "--directory", install], environment=environment, directory=folder))
            client = Path(receipt["path"])
            assert client.is_file() and not client.is_symlink() and receipt["changed"]

            def cli(*arguments, data=None, success=True):
                raw = execute([client, *arguments], environment=environment, directory=repository, data=data, success=success)
                return json.loads(raw) if raw else {}

            def git(*arguments):
                return execute(["git", *arguments], environment=environment, directory=repository).strip()

            git("init", "-b", "main")
            git("init", "--bare", str(folder / "remote.git"))
            git("remote", "add", "origin", str(folder / "remote.git"))
            cli("init", "--server", base, "--repository-id", identity["repository_id"])
            agent = folder / "agent"
            agent.mkdir()
            cli("agent", "enable", "opencode", "--config-dir", str(agent))
            plugin_before = {path.relative_to(agent): path.read_bytes() for path in agent.rglob("*") if path.is_file()}
            hooks = repository / ".git/workgraph-hook-install.json"
            hooks_before = hooks.read_bytes()
            assert json.loads(hooks_before)["executable"] == str(client)
            observation = dict(schema_version=1, source_session_id="standalone-session", event_id="first",
                               agent=dict(name="standalone-fixture", model="no-model"),
                               messages=[dict(role="assistant", text="Standalone client captured this synthetic report.")], changed_files=["change.txt"])
            captured = cli("capture", data=observation)
            (repository / "change.txt").write_text("first change\n")
            git("add", "change.txt"); git("commit", "-m", "first fixture change")
            first_sha = git("rev-parse", "HEAD")
            process.terminate(); assert process.wait(timeout=12) == 0; process.stderr.close()
            git("push", "origin", "main")
            assert cli("status")["queue"]["pending"] > 0
            execute([second, "self", "install", "--directory", install], environment=environment, directory=folder, success=False)
            assert hashlib.sha256(client.read_bytes()).hexdigest() == receipt["sha256"]
            updated = json.loads(execute([second, "self", "install", "--directory", install, "--replace-sha256", receipt["sha256"]], environment=environment, directory=folder))
            assert updated["changed"] and updated["path"] == str(client) and updated["previous_sha256"] == receipt["sha256"]
            assert cli("version")["version"] == "0.0.0-fixture.second"
            assert hooks.read_bytes() == hooks_before and {path.relative_to(agent): path.read_bytes() for path in agent.rglob("*") if path.is_file()} == plugin_before
            # Previously published source stays sealed; a new agent report authorizes new work.
            next_capture = cli("capture", data=observation | {"source_session_id": "standalone-second", "event_id": "second"})
            (repository / "change.txt").write_text("second change\n")
            git("add", "change.txt"); git("commit", "-m", "second fixture change")
            second_sha = git("rev-parse", "HEAD")
            process, restarted = api.start_api(environment, address)
            assert restarted == address
            git("push", "origin", "main")
            if cli("status")["queue"]["pending"]:
                cli("sync", "--retry")
            assert cli("status")["queue"]["pending"] == 0
            req = urllib.request.Request(base + f'/v1/repositories/{identity["repository_id"]}/sessions/{captured["session_id"]}', headers={"Authorization": "Bearer " + identity["token"]})
            with urllib.request.urlopen(req, timeout=5) as response:
                stored = json.load(response)
            assert set(stored["snapshot"]["commits"]) == {first_sha}, "Later work widened sealed source attribution"
            assert stored["confirmed_commits"] == []
            req = urllib.request.Request(base + f'/v1/repositories/{identity["repository_id"]}/sessions/{next_capture["session_id"]}', headers={"Authorization": "Bearer " + identity["token"]})
            with urllib.request.urlopen(req, timeout=5) as response:
                assert set(json.load(response)["snapshot"]["commits"]) == {second_sha}
            cli("disable")
            cli("agent", "disable", "opencode", "--config-dir", str(agent))
            print(json.dumps(dict(smoke="standalone_client", result="passed", release="actual_manifest" if args.release else "explicit_build_fixtures", verified=["install", "exact_update", "stable_hook_plugin_paths", "offline_queue", "ordinary_push_after_update", "api_restart", "exact_revision_ack"])))
    finally:
        if process:
            if process.poll() is None:
                process.terminate(); process.wait(timeout=12)
            process.stderr.close()
        api.sql(f'DROP SCHEMA "{schema}" CASCADE')


if __name__ == "__main__":
    main()
