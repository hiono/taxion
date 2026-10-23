import os
import hashlib
import json
import tarfile
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

    def test_unknown_reference_coverage_and_noncanonical_reject_without_cleanup(self):
        other = Path(self.temp.name) / "review"
        self.git("worktree", "add", "--detach", str(other), "HEAD")
        result = workspace_hygiene.inspect(self.root)
        self.assertEqual(result["classificationCounts"]["REQUIRES_OWNER_DECISION"], 1)
        self.assertIn("REQUIRES_OWNER_DECISION", result["rejections"])
        self.assertEqual(result["mergedWorktreesDetected"], 1)
        self.assertEqual(result["mergedWorktreesRemoved"], 0)
        self.assertTrue(other.exists())
        self.assertIn("NON_CANONICAL_BUILD_SOURCE", workspace_hygiene.inspect(other)["rejections"])
        # Simulate an externally missing tree; no prune or deletion is performed.
        moved = Path(self.temp.name) / "retained"
        other.rename(moved)
        self.assertIn(str(other), workspace_hygiene.inspect(self.root)["blockingWorktrees"])
        self.assertTrue(moved.exists())

    def test_exact_ownership_does_not_authorize_merged_worktree_deletion(self):
        other = Path(self.temp.name) / "review"
        self.git("worktree", "add", "-b", "review", str(other), "HEAD")
        owners = {str(other): {"head": self.git("rev-parse", "HEAD"), "owner": "fixture",
                              "evidence": ["review:fixture"], "disposition": "remove",
                              "purpose": "review", "activeReleaseCandidate": False}}
        result = workspace_hygiene.inspect(self.root, owners)
        self.assertIn("REQUIRES_OWNER_DECISION", result["rejections"])
        self.assertTrue(other.exists())

    def evidence(self, other, kind="review"):
        path = Path(self.temp.name) / (kind + ".json")
        path.write_text(json.dumps({"worktree": str(other), "head": self.git("rev-parse", "HEAD"), "decision": "APPROVE"}))
        return {"records": [{"path": str(path), "kind": kind, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}]}

    def test_merged_detached_evidence_is_retained_without_blocking(self):
        other = Path(self.temp.name) / "review"
        self.git("worktree", "add", "--detach", str(other), "HEAD")
        for kind in ("review", "release"):
            result = workspace_hygiene.inspect(self.root, evidence=self.evidence(other, kind))
            self.assertEqual(result["decision"], "PASS")
            self.assertEqual(result["classificationCounts"]["PRESERVED_EVIDENCE"], 1)
            self.assertEqual(result["blockingWorktrees"], [])
            self.assertFalse(result["releaseSourceAmbiguity"])
            self.assertTrue(result["worktrees"][1][kind + "Evidence"])
            self.assertTrue(other.exists())

    def test_safe_candidate_requires_reachable_clean_merged_and_complete_reference_inventory(self):
        other = Path(self.temp.name) / "merged"
        self.git("worktree", "add", "-b", "merged", str(other), "HEAD")
        result = workspace_hygiene.inspect(self.root, evidence={"referenceInventoryComplete": True})
        self.assertEqual(result["decision"], "PASS")
        self.assertEqual(result["worktrees"][1]["classification"], "SAFE_TO_DELETE")
        self.assertTrue(result["worktrees"][1]["reachableRefs"])
        self.assertTrue(result["worktrees"][1]["cleanupCandidate"])
        self.assertTrue(other.exists())
        (other / "source.txt").write_text("not preserved\n")
        result = workspace_hygiene.inspect(self.root, evidence={"referenceInventoryComplete": True})
        self.assertEqual(result["worktrees"][1]["classification"], "REQUIRES_OWNER_DECISION")

    def test_explicit_active_unmerged_work_is_retained(self):
        other = Path(self.temp.name) / "active"
        self.git("worktree", "add", "-b", "active", str(other), "HEAD")
        (other / "source.txt").write_text("in progress\n")
        owners = {str(other): {"head": self.git("rev-parse", "HEAD"), "owner": "fixture", "activeReleaseCandidate": True}}
        result = workspace_hygiene.inspect(self.root, owners)
        self.assertEqual(result["decision"], "PASS")
        self.assertEqual(result["classificationCounts"]["ACTIVE"], 2)
        self.assertFalse(result["worktrees"][1]["cleanupCandidate"])

    def test_dirty_snapshot_exact_match_required_and_tampering_rejected(self):
        other = Path(self.temp.name) / "preserved"
        self.git("worktree", "add", "-b", "preserved", str(other), "HEAD")
        (other / "source.txt").write_text("preserved change\n")
        (other / "extra.txt").write_bytes(b"untracked evidence")
        folder = Path(self.temp.name) / "snapshot"
        folder.mkdir()
        metadata = {"path": str(other), "head": self.git("rev-parse", "HEAD"),
                    "untrackedFiles": {"extra.txt": {"sha256": hashlib.sha256((other / "extra.txt").read_bytes()).hexdigest()}}}
        for name, args in (("status.txt", ("status", "--porcelain=v1", "--untracked-files=all")),
                           ("staged.patch", ("diff", "--cached", "--binary", "--full-index")),
                           ("unstaged.patch", ("diff", "--binary", "--full-index"))):
            (folder / name).write_bytes(subprocess.check_output(["git", "-C", str(other), *args]))
        with tarfile.open(folder / "untracked.tar", "w") as archive:
            archive.add(other / "extra.txt", arcname="extra.txt")
        metadata["savedFiles"] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in folder.iterdir()}
        (folder / "metadata.json").write_text(json.dumps(metadata))
        evidence = {"snapshots": {str(other): str(folder)}}
        result = workspace_hygiene.inspect(self.root, evidence=evidence)
        self.assertEqual(result["decision"], "PASS")
        self.assertEqual(result["classificationCounts"]["PRESERVED_EVIDENCE"], 1)
        self.assertTrue(result["worktrees"][1]["snapshotVerified"])
        (other / "extra.txt").write_bytes(b"new unpreserved bytes")
        result = workspace_hygiene.inspect(self.root, evidence=evidence)
        self.assertEqual(result["worktrees"][1]["classification"], "REQUIRES_OWNER_DECISION")
        self.assertTrue((other / "source.txt").exists())

    def test_changed_evidence_is_not_silently_accepted(self):
        other = Path(self.temp.name) / "review"
        self.git("worktree", "add", "--detach", str(other), "HEAD")
        evidence = self.evidence(other)
        Path(evidence["records"][0]["path"]).write_text("changed")
        with self.assertRaisesRegex(ValueError, "evidence changed"):
            workspace_hygiene.inspect(self.root, evidence=evidence)

    def test_unmerged_clean_without_evidence_is_not_a_delete_candidate(self):
        other = Path(self.temp.name) / "unmerged"
        self.git("worktree", "add", "-b", "unmerged", str(other), "HEAD")
        (other / "source.txt").write_text("unmerged commit\n")
        subprocess.run(["git", "-C", str(other), "commit", "-qam", "unmerged"], check=True)
        result = workspace_hygiene.inspect(self.root, evidence={"referenceInventoryComplete": True})
        item = result["worktrees"][1]
        self.assertFalse(item["merged"])
        self.assertTrue(item["reachableRefs"])
        self.assertTrue(item["canonicalDiff"])
        self.assertEqual(item["classification"], "REQUIRES_OWNER_DECISION")
        self.assertFalse(item["cleanupCandidate"])

    def test_default_private_evidence_used_and_staging_is_separate_from_source_ambiguity(self):
        other = Path(self.temp.name) / "review"
        self.git("worktree", "add", "--detach", str(other), "HEAD")
        evidence = self.evidence(other)
        (self.root / ".git" / "release-workspace-evidence.json").write_text(json.dumps(evidence))
        result = workspace_hygiene.inspect(self.root)
        self.assertEqual(result["decision"], "PASS")
        self.assertEqual(result["classificationCounts"]["PRESERVED_EVIDENCE"], 1)
        (self.root / ".gitignore").write_text("dist/\n")
        self.git("add", ".gitignore")
        self.git("commit", "-qm", "ignore staging")
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        (self.root / "dist").mkdir()
        result = workspace_hygiene.inspect(self.root)
        self.assertIn("EXISTING_BUILD_ARTIFACT_OR_STAGING", result["rejections"])
        self.assertFalse(result["releaseSourceAmbiguity"])
        self.assertEqual(result["blockingWorktrees"], [])


if __name__ == "__main__":
    unittest.main()
