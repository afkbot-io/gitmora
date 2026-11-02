import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


publish = module("publisher", "scripts/publish-cli.py")
renderer = module("renderer", "scripts/render-nginx.py")
configuration = module("configuration", "scripts/check-configuration.py")


class InstructionLinks(unittest.TestCase):
    def test_unicode_duplicates_and_fenced_example_headings(self):
        text = "# Вход: CLI/MCP\n## Повтор\n## Повтор\n```sh\n# Не заголовок\n```\n"
        self.assertEqual(configuration.heading_anchors(text), {"вход-climcp", "повтор", "повтор-1"})

    def test_missing_heading_file_and_outside_symlink_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "repo"
            root.mkdir()
            (root / "docs").mkdir()
            (root / "docs/guide.md").write_text("## Запустить вход\n")
            outside = Path(temporary) / "outside.md"
            outside.write_text("# Private\n")
            (root / "linked.md").symlink_to(outside)
            (root / "README.md").write_text(
                "[ok](docs/guide.md#запустить-вход)\n"
                "[broken](docs/guide.md#не-существует)\n"
                "[missing](docs/missing.md)\n"
                "[outside](linked.md)\n"
            )
            self.assertEqual(configuration.broken_instruction_links(root), [
                "README.md: docs/guide.md#не-существует", "README.md: docs/missing.md", "README.md: linked.md",
            ])


class Publication(unittest.TestCase):
    def fixture(self, folder, variant=""):
        folder.mkdir()
        artifacts = []
        for name in sorted(publish.ASSETS):
            raw = ("synthetic " + name + variant).encode()
            (folder / name).write_bytes(raw)
            _, system, arch = name.split("_")
            artifacts.append(
                dict(
                    name=name,
                    os=system,
                    arch=arch,
                    sha256=hashlib.sha256(raw).hexdigest(),
                    size=len(raw),
                )
            )
        (folder / "release.json").write_text(
            json.dumps(
                dict(
                    schema_version=1,
                    version="0.4.0",
                    revision="a" * 40,
                    go_version="go1.27.3",
                    artifacts=artifacts,
                )
            )
        )
        expected = {a["name"]: a["sha256"] for a in artifacts}
        expected["release.json"] = publish.digest(folder / "release.json")
        (folder / "SHA256SUMS").write_text(
            "".join(value + "  " + name + "\n" for name, value in expected.items())
        )

    def test_valid_repeat_is_immutable_and_corruption_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "release"
            public = root / "public"
            public.mkdir()
            self.fixture(source)
            self.assertEqual(
                publish.publish(source, public, "0.4.0")["result"], "published"
            )
            self.assertEqual(
                publish.publish(source, public, "0.4.0")["result"], "unchanged"
            )
            target = public / "downloads/cli/0.4.0"
            before = {p.name: p.read_bytes() for p in target.iterdir()}
            (source / "gitmora_linux_amd64").write_bytes(b"corrupted")
            with self.assertRaises(ValueError):
                publish.publish(source, public, "0.4.0")
            self.assertEqual(before, {p.name: p.read_bytes() for p in target.iterdir()})

    def test_valid_different_release_cannot_replace_published_version(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            public = root / "public"
            public.mkdir()
            first = root / "first"
            second = root / "second"
            self.fixture(first)
            self.fixture(second, " changed bytes")
            publish.publish(first, public, "0.4.0")
            target = public / "downloads/cli/0.4.0"
            before = {p.name: p.read_bytes() for p in target.iterdir()}
            with self.assertRaises(ValueError):
                publish.publish(second, public, "0.4.0")
            self.assertEqual(before, {p.name: p.read_bytes() for p in target.iterdir()})

    def test_duplicate_checksum_and_extra_secret_never_enter_public_root(self):
        for case in ["duplicate", "extra", "symlink"]:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                source = root / "release"
                public = root / "public"
                public.mkdir()
                self.fixture(source)
                if case == "duplicate":
                    path = source / "SHA256SUMS"
                    path.write_text(
                        path.read_text() + path.read_text().splitlines()[0] + "\n"
                    )
                if case == "extra":
                    (source / "operator.json").write_text("synthetic private material")
                if case == "symlink":
                    (source / "gitmora_linux_amd64").unlink()
                    (source / "gitmora_linux_amd64").symlink_to(
                        source / "gitmora_linux_arm64"
                    )
                with self.assertRaises(ValueError):
                    publish.publish(source, public, "0.4.0")
                self.assertFalse(list(public.iterdir()))


class Renderer(unittest.TestCase):
    def test_ports_are_replaced_once_and_nginx_variables_survive(self):
        value = renderer.render("context.company.example", 18081, 8080)[
            "gitmora.https.conf"
        ]
        self.assertIn("proxy_pass http://127.0.0.1:18081;", value)
        self.assertIn("proxy_pass http://127.0.0.1:8080/auth/realms/gitmora/;", value)
        self.assertIn("proxy_set_header Host $http_host;", value)
        self.assertIn("$request_uri", value)
        self.assertIn("root /srv/gitmora-public/acme;", value)
        self.assertNotIn("gitmora.tasktopia.online", value)


if __name__ == "__main__":
    unittest.main()
