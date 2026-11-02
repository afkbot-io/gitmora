#!/usr/bin/env python3
"""Keep documented production env inputs and local instruction links in sync with source."""

from pathlib import Path
import re
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parent.parent


def heading_anchors(text: str) -> set[str]:
    """GitHub-style anchors for authored ATX headings, excluding fenced examples."""
    anchors: set[str] = set()
    fence = ""
    for line in text.splitlines():
        marker = re.match(r"^ {0,3}(`{3,}|~{3,})", line)
        if fence:
            if marker and marker[1][0] == fence[0] and len(marker[1]) >= len(fence):
                fence = ""
            continue
        if marker:
            fence = marker[1]
            continue
        heading = re.match(r"^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$", line)
        if not heading:
            continue
        title = re.sub(r"<[^>]*>", "", heading[1]).lower()
        slug = re.sub(r"[^\w -]", "", title).replace(" ", "-")
        anchor, number = slug, 0
        while anchor in anchors:
            number += 1
            anchor = f"{slug}-{number}"
        anchors.add(anchor)
    return anchors


def broken_instruction_links(root: Path) -> list[str]:
    """Validate local file and heading targets without reading outside the repository."""
    root = root.resolve()
    broken = []
    for p in [root / "README.md", *(root / "docs").glob("*.md")]:
        if p.is_symlink() or not p.is_file():
            broken.append(str(p.relative_to(root)) + ": expected a regular authored file")
            continue
        for link in re.findall(r"\]\(([^\s)]+)\)", p.read_text()):
            if re.match(r"(?:https?://|mailto:)", link):
                continue
            target, _, fragment = link.partition("#")
            resolved = (p.parent / unquote(target)).resolve() if target else p
            if not resolved.is_relative_to(root) or not resolved.is_file():
                broken.append(str(p.relative_to(root)) + ": " + link)
            elif (
                fragment and resolved.suffix == ".md"
                and unquote(fragment) not in heading_anchors(resolved.read_text())
            ):
                broken.append(str(p.relative_to(root)) + ": " + link)
    return broken


def main():
    names = set()
    for folder in ["cmd", "internal"]:
        for p in (ROOT / folder).rglob("*.go"):
            if (
                p.name.endswith("_test.go")
                or "testdata" in p.parts
                or "/webui/assets/" in str(p)
            ):
                continue
            names.update(
                re.findall(r'"((?:WORKGRAPH|GITMORA)_[A-Z][A-Z0-9_]*)"', p.read_text())
            )
    for p in [ROOT / "compose.yaml", *(ROOT / "deploy").rglob("*.yaml")]:
        text = p.read_text()
        names.update(re.findall(r"\$\{((?:WORKGRAPH|GITMORA)_[A-Z][A-Z0-9_]*)", text))
        names.update(
            re.findall(r"^\s+((?:WORKGRAPH|GITMORA)_[A-Z][A-Z0-9_]*):", text, re.M)
        )
    names.update(
        name + "_FILE"
        for name in [
            "WORKGRAPH_DATABASE_URL",
            "WORKGRAPH_ENCRYPTION_KEYS",
            "WORKGRAPH_OIDC_CLIENT_SECRET",
        ]
    )
    reference = (ROOT / "docs/configuration.md").read_text()
    missing = sorted(name for name in names if name not in reference)
    if missing:
        raise SystemExit("Undocumented production inputs: " + ", ".join(missing))
    for p in [ROOT / ".env.example", ROOT / "deploy/gitmora.runtime.env.example"]:
        for name in re.findall(
            r"^\s*(?:#\s*)?((?:WORKGRAPH|GITMORA)_[A-Z][A-Z0-9_]*)=",
            p.read_text(),
            re.M,
        ):
            if name not in names:
                raise SystemExit("Example uses unsupported input: " + name)
    ui_version = re.search(
        r'publicClientVersion\s*=\s*["\']([^"\']+)',
        (ROOT / "web/src/domain/onboarding.ts").read_text(),
    )
    installer_version = re.search(
        r"^gitmora_version=([^\n]+)$",
        (ROOT / "web/public/install.sh").read_text(),
        re.M,
    )
    if not ui_version or not installer_version or ui_version[1] != installer_version[1]:
        raise SystemExit("UI and installer pin different CLI download versions")
    broken = broken_instruction_links(ROOT)
    if broken:
        raise SystemExit("Broken local instruction links:\n" + "\n".join(broken))
    print(
        f"Configuration reference covers {len(names)} production inputs; examples and local instruction links passed."
    )


if __name__ == "__main__":
    main()
