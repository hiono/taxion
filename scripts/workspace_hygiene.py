"""Read-only source-workspace release gate. Never removes files or Git refs."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys


def git(root, *args):
    return subprocess.check_output(
        ["git", "-C", str(root), *args], text=True,
        env=dict(os.environ, GIT_OPTIONAL_LOCKS="0"),
    ).strip()


def inspect(root, ownership=None):
    root = Path(root).resolve(strict=True)
    registered = git(root, "worktree", "list", "--porcelain", "-z")
    entries = []
    for block in registered.split("\0\0"):
        fields = dict(line.split(" ", 1) if " " in line else (line, True)
                      for line in block.split("\0") if line)
        if "worktree" in fields:
            entries.append(fields)
    if not entries:
        raise ValueError("no registered canonical worktree")
    canonical = Path(entries[0]["worktree"]).resolve()
    reasons = []
    if canonical != root or Path(git(root, "rev-parse", "--show-toplevel")).resolve() != root:
        reasons.append("NON_CANONICAL_BUILD_SOURCE")
    head = git(root, "rev-parse", "HEAD")
    merged = subprocess.run(
        ["git", "-C", str(root), "merge-base", "--is-ancestor", head, "origin/main"],
        capture_output=True,
    )
    if merged.returncode not in (0, 1):
        raise ValueError("cannot establish release source ancestry")
    if merged.returncode:
        reasons.append("RELEASE_BRANCH_NOT_MERGED")
    worktrees = []
    for fields in entries:
        path = Path(fields["worktree"])
        item = {"path": str(path), "head": fields.get("HEAD"), "branch": fields.get("branch"),
                "detached": bool(fields.get("detached")), "orphaned": not path.is_dir() or "prunable" in fields}
        if item["orphaned"]:
            reasons.append("ORPHANED_WORKTREE")
            item["status"] = None
            item["merged"] = False
        else:
            item["status"] = git(path, "status", "--porcelain=v1", "--untracked-files=all")
            ancestry = subprocess.run(
                ["git", "-C", str(root), "merge-base", "--is-ancestor", item["head"], "origin/main"],
                capture_output=True,
            )
            if ancestry.returncode not in (0, 1):
                raise ValueError("cannot establish worktree ancestry")
            item["merged"] = ancestry.returncode == 0
            if item["status"]:
                reasons.append("DIRTY_WORKTREE")
        if path.resolve() != canonical:
            owner = (ownership or {}).get(str(path.resolve()), {})
            item["ownershipVerified"] = (
                bool(owner.get("owner")) and owner.get("head") == item["head"]
                and bool(owner.get("evidence")) and owner.get("disposition") in ("retained", "remove")
                and owner.get("purpose") in ("durable", "review", "repair", "candidate")
                and type(owner.get("activeReleaseCandidate")) is bool
            )
            if not item["ownershipVerified"]:
                reasons.append("UNKNOWN_WORKTREE_OWNERSHIP")
            if item["detached"]:
                reasons.append("DETACHED_TEMPORARY_WORKTREE")
            if owner.get("activeReleaseCandidate"):
                reasons.append("ACTIVE_RELEASE_CANDIDATE")
            if owner.get("purpose") in ("review", "repair", "candidate"):
                reasons.append("TEMPORARY_WORKTREE_REQUIRES_DISPOSITION")
            # Merged auxiliary worktrees must be resolved before publication,
            # even when ownership is known. Evidence retention is not deletion authority.
            if item["merged"]:
                reasons.append("MERGED_WORKTREE_REQUIRES_DISPOSITION")
            item["cleanupCandidate"] = item["orphaned"] or item["merged"] or item["detached"]
        worktrees.append(item)
    staging = [str(root / name) for name in ("dist", ".release", "taxion", "taxion.exe", "bin/taxion", "bin/taxion.exe")
               if (root / name).exists() or (root / name).is_symlink()]
    staging += sorted({str(path) for pattern in (".git/*/dist", ".git/*/stage*", ".git/*/candidate*", ".git/*/artifacts")
                       for path in root.glob(pattern) if path.is_dir() or path.is_symlink()})
    if staging:
        reasons.append("EXISTING_BUILD_ARTIFACT_OR_STAGING")
    branches = git(root, "for-each-ref", "--format=%(refname) %(objectname)", "--merged=origin/main", "refs/heads").splitlines()
    return {
        "schemaVersion": 1, "sourceRevision": head, "canonicalWorktree": str(canonical),
        "canonicalWorktreeClean": not worktrees[0]["orphaned"] and not worktrees[0]["status"],
        "releaseBranchMerged": merged.returncode == 0, "worktrees": worktrees,
        "mergedBranches": branches, "stagingDirectoriesAndArtifacts": staging,
        "mergedWorktreesDetected": sum(w.get("merged", False) for w in worktrees[1:]),
        "mergedWorktreesRemoved": 0, "orphanedWorktreesDetected": sum(w["orphaned"] for w in worktrees),
        "orphanedWorktreesRemoved": 0, "detachedWorktreesRemaining": sum(w["detached"] for w in worktrees[1:]),
        "buildArtifactsCleaned": False, "releaseSourceVerified": not reasons,
        "rejections": sorted(set(reasons)), "decision": "REJECT" if reasons else "PASS",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--canonical", default=".")
    parser.add_argument("--ownership", help="private exact-path ownership and evidence inventory")
    args = parser.parse_args()
    owners = json.loads(Path(args.ownership).read_text()) if args.ownership else None
    result = inspect(args.canonical, owners)
    print(json.dumps(result, indent=2))
    return 0 if result["decision"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
