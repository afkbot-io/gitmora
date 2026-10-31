"""Actual Git export regression: clean status must never admit ignored compiler/embed input."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

spec=importlib.util.spec_from_file_location("release_cli",Path(__file__).resolve().parents[1]/"release-cli.py")
release=importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class CommittedSourceExport(unittest.TestCase):
    def test_ignored_and_later_worktree_bytes_are_not_build_input(self):
        with tempfile.TemporaryDirectory(prefix="gitmora-export-test-") as name:
            root=Path(name); repository=root/"repository"; repository.mkdir(); exported=root/"exported"; exported.mkdir()
            def git(*arguments):
                return subprocess.check_output(["git","-C",str(repository),*arguments],stderr=subprocess.DEVNULL,text=True).strip()
            git("init","-q")
            (repository/".gitignore").write_text("ignored*\n")
            (repository/"main.go").write_text("committed source\n")
            git("add",".");git("-c","user.name=Fixture","-c","user.email=fixture@example.invalid","commit","-qm","synthetic source")
            revision=git("rev-parse","HEAD")
            (repository/"ignored.go").write_text("uncommitted compiler input must not be built\n")
            self.assertEqual(git("status","--porcelain"),"")
            (repository/"main.go").write_text("later uncommitted bytes\n")
            release.export_source(repository,revision,exported)
            self.assertEqual((exported/"main.go").read_text(),"committed source\n")
            self.assertEqual({item.name for item in exported.iterdir()},{"main.go",".gitignore"})
            self.assertEqual((repository/"main.go").read_text(),"later uncommitted bytes\n")
            git("add","main.go");git("-c","user.name=Fixture","-c","user.email=fixture@example.invalid","commit","-qm","replacement fixture")
            git("replace",revision,git("rev-parse","HEAD"))
            replacement_export=root/"replacement-export";replacement_export.mkdir()
            release.export_source(repository,revision,replacement_export)
            self.assertEqual((replacement_export/"main.go").read_text(),"committed source\n")


if __name__=="__main__":
    unittest.main()
