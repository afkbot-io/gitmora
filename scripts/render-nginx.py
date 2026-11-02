#!/usr/bin/env python3
"""Render reviewed single-domain nginx/renewal files; never install, reload or request a certificate."""

import argparse
import ipaddress
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent.parent


def hostname(value):
    if len(value) > 253 or value != value.lower() or len(value.split(".")) < 2:
        raise argparse.ArgumentTypeError(
            "Use a lowercase DNS hostname without scheme, path or port"
        )
    if any(
        not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", part)
        for part in value.split(".")
    ):
        raise argparse.ArgumentTypeError("Invalid DNS hostname")
    try:
        ipaddress.ip_address(value)
    except ValueError:
        return value
    raise argparse.ArgumentTypeError("Use a DNS hostname for the public certificate")


def absolute(value):
    if (
        not Path(value).is_absolute()
        or Path(value) == Path("/")
        or any(c in value for c in " \t\r\n\"'$;{}\\")
        or ".." in Path(value).parts
    ):
        raise argparse.ArgumentTypeError(
            "Use a plain absolute path without whitespace or nginx/shell syntax"
        )
    return value.rstrip("/")


def render(
    domain,
    api_port=8080,
    identity_port=18081,
    acme="/srv/gitmora-public/acme",
    downloads="/srv/gitmora-public",
    realm="gitmora",
):
    hostname(domain)
    if api_port == identity_port or not all(
        1 <= port <= 65535 for port in [api_port, identity_port]
    ):
        raise ValueError("Choose two distinct loopback ports in 1..65535")
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,62}", realm) or realm == "master":
        raise ValueError("Invalid public realm")
    absolute(acme)
    absolute(downloads)
    result = {}
    for name in ["gitmora.http.conf", "gitmora.https.conf", "20-gitmora-reload"]:
        text = (ROOT / "deploy/nginx" / name).read_text()
        replacements = {
            "gitmora.example.com": domain,
            "127.0.0.1:8080": f"127.0.0.1:{api_port}",
            "127.0.0.1:18081": f"127.0.0.1:{identity_port}",
            "/srv/gitmora/acme": acme,
            "/srv/gitmora-public": downloads,
            "/realms/gitmora/": f"/realms/{realm}/",
        }
        text = re.sub(
            "|".join(re.escape(key) for key in replacements),
            lambda match: replacements[match.group()],
            text,
        )
        result[name] = text
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--domain", required=True, type=hostname)
    parser.add_argument(
        "--output",
        required=True,
        type=Path,
        help="New output directory under an existing parent",
    )
    parser.add_argument("--api-port", type=int, default=8080)
    parser.add_argument("--identity-port", type=int, default=18081)
    parser.add_argument("--realm", default="gitmora")
    parser.add_argument(
        "--acme-root", type=absolute, default="/srv/gitmora-public/acme"
    )
    parser.add_argument(
        "--downloads-root", type=absolute, default="/srv/gitmora-public"
    )
    args = parser.parse_args()
    try:
        files = render(
            args.domain,
            args.api_port,
            args.identity_port,
            args.acme_root,
            args.downloads_root,
            args.realm,
        )
    except ValueError as error:
        parser.error(str(error))
    args.output.mkdir(mode=0o700)
    for name, text in files.items():
        path = args.output / name
        path.write_text(text)
        path.chmod(0o700 if name == "20-gitmora-reload" else 0o600)
    print(f"Rendered {len(files)} files in {args.output}; review before installing")


if __name__ == "__main__":
    main()
