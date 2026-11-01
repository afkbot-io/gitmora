#!/usr/bin/env python3
"""Verify and atomically publish only a complete immutable CLI release to an own download root.

Does not execute downloaded binaries, build code, read private installation state or reload nginx.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import tempfile

ASSETS = {
    f"gitmora_{system}_{arch}"
    for system in ["darwin", "linux"]
    for arch in ["arm64", "amd64"]
}
FILES = ASSETS | {"release.json", "SHA256SUMS"}


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("Duplicate manifest field")
        value[key] = item
    return value


def verify(folder, version):
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[a-z0-9]+(?:[.-][a-z0-9]+)*)?", version):
        raise ValueError("Invalid release version")
    if (
        folder.is_symlink()
        or not folder.is_dir()
        or {p.name for p in folder.iterdir()} != FILES
    ):
        raise ValueError(
            "Select a directory containing exactly four binaries and both manifests"
        )
    for p in folder.iterdir():
        if (
            p.is_symlink()
            or not p.is_file()
            or p.stat().st_size < 1
            or p.stat().st_size > 128 * 1024 * 1024
        ):
            raise ValueError("Invalid release file")
    if (folder / "release.json").stat().st_size > 65536 or (
        folder / "SHA256SUMS"
    ).stat().st_size > 16384:
        raise ValueError("Oversized release metadata")
    manifest = json.loads(
        (folder / "release.json").read_text(), object_pairs_hook=unique_object
    )
    if (
        set(manifest)
        != {"schema_version", "version", "revision", "go_version", "artifacts"}
        or not re.fullmatch(r"go[0-9]+\.[0-9]+\.[0-9]+", manifest.get("go_version", ""))
        or manifest.get("schema_version") != 1
        or manifest.get("version") != version
        or not re.fullmatch("[0-9a-f]{40}", manifest.get("revision", ""))
    ):
        raise ValueError("Manifest version/revision mismatch")
    expected = {}
    artifacts = manifest.get("artifacts", [])
    if len(artifacts) != 4:
        raise ValueError("Four native targets are required")
    for asset in artifacts:
        name = asset["name"]
        if (
            set(asset) != {"name", "os", "arch", "sha256", "size"}
            or name not in ASSETS
            or name in expected
            or name != f"gitmora_{asset['os']}_{asset['arch']}"
        ):
            raise ValueError("Unexpected/duplicate artifact")
        if (
            type(asset["size"]) is not int
            or not re.fullmatch("[0-9a-f]{64}", asset["sha256"])
            or asset["size"] != (folder / name).stat().st_size
        ):
            raise ValueError("Invalid artifact metadata")
        expected[name] = asset["sha256"]
    expected["release.json"] = digest(folder / "release.json")
    sums = {}
    for line in (folder / "SHA256SUMS").read_text().splitlines():
        parts = line.split("  ")
        if len(parts) != 2 or parts[1] not in expected or parts[1] in sums:
            raise ValueError("Unexpected/duplicate checksum")
        sums[parts[1]] = parts[0]
    if sums != expected or any(
        digest(folder / name) != value for name, value in expected.items()
    ):
        raise ValueError("Release checksum mismatch")
    return manifest


def publish(source, public_root, version):
    manifest = verify(source, version)
    if (
        not public_root.is_absolute()
        or public_root.is_symlink()
        or not public_root.is_dir()
    ):
        raise ValueError("Choose an existing absolute public directory")
    base = public_root / "downloads/cli"
    for parent in [public_root / "downloads", base]:
        if parent.is_symlink():
            raise ValueError("Public download path must not use symlinks")
        parent.mkdir(mode=0o755, exist_ok=True)
    target = base / version
    if target.exists() or target.is_symlink():
        verify(target, version)
        if any(digest(source / name) != digest(target / name) for name in FILES):
            raise ValueError("An immutable release already exists with different bytes")
        return dict(
            result="unchanged",
            version=version,
            revision=manifest["revision"],
            directory=str(target),
        )
    stage = Path(tempfile.mkdtemp(prefix=".publish-", dir=base))
    try:
        for name in FILES:
            shutil.copyfile(source / name, stage / name)
            (stage / name).chmod(0o644)
        if verify(stage, version) != manifest:
            raise ValueError("Source changed during publication")
        for name in FILES:
            with (stage / name).open("rb") as stream:
                os.fsync(stream.fileno())
        stage.chmod(0o755)
        descriptor = os.open(stage, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        os.rename(stage, target)
        descriptor = os.open(base, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        if stage.exists():
            shutil.rmtree(stage)
    return dict(
        result="published",
        version=version,
        revision=manifest["revision"],
        directory=str(target),
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release", type=Path, required=True)
    parser.add_argument("--public-root", type=Path, required=True)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    try:
        receipt = publish(args.release, args.public_root, args.version)
    except (ValueError, KeyError, OSError, TypeError):
        parser.error(
            "Release verification/publication failed; check the selected local files and destination"
        )
    print(json.dumps(receipt))


if __name__ == "__main__":
    main()
