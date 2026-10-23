import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import workspace_hygiene


class WorkspaceHygieneTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "canonical"
        self.root.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "user.name", "Fixture")
        (self.root / "source.txt").write_text("verified source\n")
        self.git("add", "source.txt")
        self.git("commit", "-qm", "source")
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.root), *args],
                                       text=True, stderr=subprocess.STDOUT).strip()

    def test_clean_and_isolated_canonical_detached_checkout(self):
        self.assertEqual(workspace_hygiene.inspect(self.root)["decision"], "PASS")
        self.git("checkout", "--detach", "-q")
        self.assertEqual(workspace_hygiene.inspect(self.root)["decision"], "PASS")

    def test_dirty_artifacts_and_unmerged_source_reject(self):
        (self.root / "dist").mkdir()
        (self.root / "dist" / "binary").write_bytes(b"candidate")
        result = workspace_hygiene.inspect(self.root)
        self.assertIn("DIRTY_WORKTREE", result["rejections"])
        self.assertIn("EXISTING_BUILD_ARTIFACT_OR_STAGING", result["rejections"])
        self.git("add", ".")
        self.git("commit", "-qm", "unmerged")
        self.assertIn("RELEASE_BRANCH_NOT_MERGED", workspace_hygiene.inspect(self.root)["rejections"])

    def test_unknown_merged_detached_and_noncanonical_reject_without_cleanup(self):
        other = Path(self.temp.name) / "review"
        self.git("worktree", "add", "--detach", str(other), "HEAD")
        result = workspace_hygiene.inspect(self.root)
        self.assertIn("UNKNOWN_WORKTREE_OWNERSHIP", result["rejections"])
        self.assertIn("DETACHED_TEMPORARY_WORKTREE", result["rejections"])
        self.assertEqual(result["mergedWorktreesDetected"], 1)
        self.assertEqual(result["mergedWorktreesRemoved"], 0)
        self.assertTrue(other.exists())
        self.assertIn("NON_CANONICAL_BUILD_SOURCE", workspace_hygiene.inspect(other)["rejections"])
        # Simulate an externally missing tree; no prune or deletion is performed.
        moved = Path(self.temp.name) / "retained"
        other.rename(moved)
        self.assertIn("ORPHANED_WORKTREE", workspace_hygiene.inspect(self.root)["rejections"])
        self.assertTrue(moved.exists())

    def test_exact_ownership_does_not_authorize_merged_worktree_deletion(self):
        other = Path(self.temp.name) / "review"
        self.git("worktree", "add", "-b", "review", str(other), "HEAD")
        owners = {str(other): {"head": self.git("rev-parse", "HEAD"), "owner": "fixture",
                              "evidence": ["review:fixture"], "disposition": "remove",
                              "purpose": "review", "activeReleaseCandidate": False}}
        result = workspace_hygiene.inspect(self.root, owners)
        self.assertNotIn("UNKNOWN_WORKTREE_OWNERSHIP", result["rejections"])
        self.assertIn("MERGED_WORKTREE_REQUIRES_DISPOSITION", result["rejections"])
        self.assertTrue(other.exists())


if __name__ == "__main__":
    unittest.main()
