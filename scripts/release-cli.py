#!/usr/bin/env python3
"""Build immutable standalone native clients from clean, pinned source; never publish or install them."""
from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent
TARGETS = ("darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64")
STAMP = "workgraph.local/platform/internal/distribution/buildinfo"


def execute(args: list[str], *, environment: dict[str, str] | None = None, directory: Path = ROOT) -> str:
    """Retain build output locally; exceptions never dump environment or build diagnostics."""
    result = subprocess.run(args, cwd=directory, env=environment, capture_output=True, text=True, timeout=600)
    if result.returncode:
        raise RuntimeError("Native release subprocess failed; inspect the selected toolchain/source locally")
    return result.stdout


def export_source(repository: Path, revision: str, destination: Path) -> None:
    """Export only regular committed files; ignored compiler/embed input never enters the build tree."""
    result = subprocess.run(["git", "--no-replace-objects", "archive", "--format=tar", revision], cwd=repository, capture_output=True, timeout=60)
    if result.returncode:
        raise RuntimeError("Committed source export failed")
    with tarfile.open(fileobj=io.BytesIO(result.stdout), mode="r:") as archive:
        for member in archive.getmembers():
            path = Path(member.name)
            if path.is_absolute() or ".." in path.parts or not (member.isdir() or member.isfile()):
                raise RuntimeError("Source archive requires explicit path/link review")
            target = destination / path
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with target.open("xb") as output:
                    output.write(archive.extractfile(member).read())
                target.chmod(member.mode & 0o777)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--target", action="append", choices=TARGETS)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[a-z0-9]+(?:[.-][a-z0-9]+)*)?", args.version):
        parser.error("Use a canonical numeric version, optionally with a lowercase prerelease")
    if execute(["git", "status", "--porcelain", "--untracked-files=normal"]).strip():
        parser.error("Release builds require clean committed source")
    revision = execute(["git", "rev-parse", "HEAD"]).strip()
    assert re.fullmatch(r"[0-9a-f]{40}", revision), "Unexpected source revision"
    output = args.output.absolute()
    assert output.parent.is_dir(), "Output parent must exist"
    output.mkdir(mode=0o755)  # Refuse overwriting/reusing an existing artifact directory.
    targets = sorted(set(args.target or TARGETS))
    with tempfile.TemporaryDirectory(prefix="gitmora-committed-client-source-") as name:
        source = Path(name)
        export_source(ROOT, revision, source)
        build_release(args.version, revision, output, targets, source)
    print(json.dumps(dict(version=args.version, revision=revision, output=str(output), targets=targets)))


def build_release(version: str, revision: str, output: Path, targets: list[str], source: Path) -> None:
    """Build from one owned committed export, then emit metadata only after every target validates."""
    execute(["go", "mod", "verify"], directory=source)
    required_go = re.search(r"(?m)^go ([0-9.]+)$", (source / "go.mod").read_text()).group(1)
    artifacts = []
    for target in targets:
        operating_system, architecture = target.split("/")
        name = f"gitmora_{operating_system}_{architecture}"
        environment = os.environ.copy()
        environment.update({"CGO_ENABLED": "0", "GOOS": operating_system, "GOARCH": architecture,
                            "GOFLAGS": "", "GOWORK": "off", "GOENV": "off", "GOTOOLCHAIN": "auto",
                            "GOEXPERIMENT": "", "GOAMD64": "v1", "GOARM64": "v8.0"})
        flags = f"-buildid= -X {STAMP}.version={version} -X {STAMP}.revision={revision}"
        execute(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", flags,
                 "-o", str(output / name), "./cmd/workgraph"], environment=environment, directory=source)
        info = json.loads(execute(["go", "version", "-m", "-json", str(output / name)]))
        settings = {setting["Key"]: setting["Value"] for setting in info["Settings"]}
        assert info["GoVersion"] == "go" + required_go, "Build used a different toolchain"
        assert settings["GOOS"] == operating_system and settings["GOARCH"] == architecture and settings["CGO_ENABLED"] == "0"
        # Go intentionally omits -ldflags from build info under -trimpath (it can contain host paths).
        assert settings["-trimpath"] == "true" and info["Path"] == "workgraph.local/platform/cmd/workgraph"
        native_os = {"Darwin": "darwin", "Linux": "linux"}.get(platform.system())
        native_arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine())
        if (operating_system, architecture) == (native_os, native_arch):
            observed = json.loads(execute([str(output / name), "version"]))
            assert observed == dict(schema_version=1, version=version, revision=revision,
                                    go_version="go" + required_go, os=operating_system, arch=architecture)
        raw = (output / name).read_bytes()
        artifacts.append(dict(name=name, os=operating_system, arch=architecture,
                              sha256=hashlib.sha256(raw).hexdigest(), size=len(raw)))
        (output / name).chmod(0o755)
    assert execute(["git", "rev-parse", "HEAD"]).strip() == revision, "Source revision moved during build"
    assert not execute(["git", "status", "--porcelain", "--untracked-files=normal"]).strip(), "Source changed during build"
    manifest = dict(schema_version=1, version=version, revision=revision, go_version="go" + required_go, artifacts=artifacts)
    (output / "release.json").write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
    for name in ("release.json",):
        (output / name).chmod(0o644)
    sums = artifacts + [dict(name="release.json", sha256=hashlib.sha256((output / "release.json").read_bytes()).hexdigest())]
    (output / "SHA256SUMS").write_text("".join(f'{item["sha256"]}  {item["name"]}\n' for item in sums))
    (output / "SHA256SUMS").chmod(0o644)


if __name__ == "__main__":
    main()
