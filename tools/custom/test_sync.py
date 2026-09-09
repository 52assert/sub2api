#!/usr/bin/env python3
"""Exercise sync against real temporary Git repositories, without network access."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("sync-upstream.sh").resolve()


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.env = dict(os.environ, GH_REPO="52assert/sub2api",
                        GIT_AUTHOR_NAME="Test", GIT_AUTHOR_EMAIL="test@example.invalid",
                        GIT_COMMITTER_NAME="Test", GIT_COMMITTER_EMAIL="test@example.invalid",
                        GH_LOG=str(self.root / "gh.log"), EXISTING_PR="")
        self.env.pop("GITHUB_STEP_SUMMARY", None)
        self.git(self.root, "init", "--bare", "upstream.git")
        self.git(self.root, "init", "--bare", "fork.git")
        self.git(self.root, "init", "-b", "main", "seed")
        self.seed = self.root / "seed"
        self.commit(self.seed, "base", "base")
        self.git(self.seed, "remote", "add", "upstream", str(self.root / "upstream.git"))
        self.git(self.seed, "remote", "add", "fork", str(self.root / "fork.git"))
        self.git(self.seed, "push", "upstream", "main")
        self.git(self.seed, "push", "fork", "main")
        self.git(self.seed, "switch", "-c", "custom")
        self.commit(self.seed, "custom", "keep this customization")
        self.git(self.seed, "push", "fork", "custom")
        self.git(self.root, "clone", "-b", "custom", str(self.root / "fork.git"), "work")
        self.work = self.root / "work"
        self.git(self.work, "config", "url." + str(self.root / "upstream.git") + ".insteadOf",
                 "https://github.com/Wei-Shaw/sub2api.git")
        self.custom_sha = self.git(self.work, "rev-parse", "HEAD")
        self.git(self.seed, "switch", "main")
        bindir = self.root / "bin"
        bindir.mkdir()
        gh = bindir / "gh"
        gh.write_text('''#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ["GH_LOG"], "a") as stream:
    stream.write(json.dumps(args) + "\\n")
if args[0] == "api" and any(arg.endswith("/pulls") for arg in args):
    print(os.environ["EXISTING_PR"] if "GET" in args else "7")
''')
        gh.chmod(0o755)
        self.env["PATH"] = str(bindir) + os.pathsep + self.env["PATH"]

    def git(self, cwd, *args):
        return subprocess.run(["git", *args], cwd=cwd, env=self.env, check=True,
                              capture_output=True, text=True).stdout.strip()

    def commit(self, cwd, name, content):
        (cwd / name).write_text(content)
        self.git(cwd, "add", name)
        self.git(cwd, "commit", "-m", name)

    def update_upstream(self):
        self.commit(self.seed, "official", "new official feature")
        self.git(self.seed, "push", "upstream", "main")

    def sync(self):
        return subprocess.run(["bash", str(SCRIPT)], cwd=self.work, env=self.env,
                              capture_output=True, text=True)

    def calls(self):
        path = self.root / "gh.log"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_mirror_create_pr_and_preserve_custom(self):
        self.update_upstream()
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git(self.root / "fork.git", "rev-parse", "main"),
                         self.git(self.seed, "rev-parse", "main"))
        self.assertEqual(self.git(self.root / "fork.git", "rev-parse", "custom"), self.custom_sha)
        self.assertTrue(any("POST" in call and "repos/52assert/sub2api/pulls" in call for call in self.calls()))
        self.assertTrue(any(call[:3] == ["workflow", "run", "fork-ci.yml"] for call in self.calls()))

    def test_existing_pr_is_reused(self):
        self.update_upstream()
        self.env["EXISTING_PR"] = "42"
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any("POST" in call and "repos/52assert/sub2api/pulls" in call for call in self.calls()))
        self.assertIn("pr_number=42", self.calls()[-1])

    def test_no_update_has_no_pr_or_dispatch(self):
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), [])

    def test_diverged_main_is_not_overwritten(self):
        self.commit(self.seed, "fork-only", "must survive")
        self.git(self.seed, "push", "fork", "main")
        before = self.git(self.root / "fork.git", "rev-parse", "main")
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("diverged", result.stderr)
        self.assertEqual(self.git(self.root / "fork.git", "rev-parse", "main"), before)
        self.assertEqual(self.calls(), [])


if __name__ == "__main__":
    unittest.main()
