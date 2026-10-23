"""Verify private retention evidence without changing any worktree."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile


def git_bytes(path, *args):
    return subprocess.check_output(["git", "-C", str(path), *args],
                                   env=dict(os.environ, GIT_OPTIONAL_LOCKS="0"))


def links(item, records):
    result = {"review": [], "release": []}
    for record in records:
        if record["kind"] not in result:
            raise ValueError("unsupported workspace evidence kind")
        path = Path(record["path"])
        data = path.read_bytes()
        if hashlib.sha256(data).hexdigest() != record["sha256"]:
            raise ValueError("workspace evidence changed: " + str(path))
        # A path reference requires retaining that location; a revision reference
        # requires retaining that source, even if a different worktree holds it.
        if item["path"].encode() in data or item["head"].encode() in data:
            result[record["kind"]].append(str(path))
    return result


def snapshot_matches(item, directory):
    if not directory or item["orphaned"]:
        return False
    directory = Path(directory)
    metadata = json.loads((directory / "metadata.json").read_text())
    if metadata["path"] != item["path"] or metadata["head"] != item["head"]:
        return False
    for name in ("status.txt", "staged.patch", "unstaged.patch", "untracked.tar"):
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != metadata["savedFiles"][name]:
            return False
    path = Path(item["path"])
    commands = (
        ("status.txt", ("status", "--porcelain=v1", "--untracked-files=all")),
        ("staged.patch", ("diff", "--cached", "--binary", "--full-index")),
        ("unstaged.patch", ("diff", "--binary", "--full-index")),
    )
    if any((directory / name).read_bytes() != git_bytes(path, *args) for name, args in commands):
        return False
    names = set(filter(None, git_bytes(path, "ls-files", "--others", "--exclude-standard", "-z").decode().split("\0")))
    if names != set(metadata["untrackedFiles"]):
        return False
    with tarfile.open(directory / "untracked.tar") as archive:
        if names != set(archive.getnames()):
            return False
        for name, expected in metadata["untrackedFiles"].items():
            if Path(name).is_absolute() or ".." in Path(name).parts:
                raise ValueError("unsafe preserved snapshot path")
            source = path / name
            member = archive.getmember(name)
            if "symlink" in expected:
                if not source.is_symlink() or os.readlink(source) != expected["symlink"] or not member.issym() or member.linkname != expected["symlink"]:
                    return False
            elif (source.is_symlink() or not source.is_file() or not member.isfile()
                  or hashlib.sha256(source.read_bytes()).hexdigest() != expected["sha256"]
                  or hashlib.sha256(archive.extractfile(member).read()).hexdigest() != expected["sha256"]):
                return False
    return (git_bytes(path, "rev-parse", "HEAD").decode().strip() == item["head"]
            and all((directory / name).read_bytes() == git_bytes(path, *args) for name, args in commands))


def classify(item, canonical, owner, evidence):
    records = evidence.get("records", [])
    bound = links(item, records)
    item["reviewEvidence"] = bound["review"]
    item["releaseEvidence"] = bound["release"]
    snapshot = evidence.get("snapshots", {}).get(item["path"])
    item["snapshotVerified"] = snapshot_matches(item, snapshot)
    item["referenceInventoryComplete"] = evidence.get("referenceInventoryComplete") is True
    if item["path"] == str(canonical):
        return "ACTIVE", "canonical source"
    if owner.get("head") == item["head"] and owner.get("owner") and owner.get("activeReleaseCandidate") is True:
        return "ACTIVE", "explicit active operation; not a release source"
    if item["orphaned"]:
        return "REQUIRES_OWNER_DECISION", "registered worktree is missing"
    if item["status"] and not item["snapshotVerified"]:
        return "REQUIRES_OWNER_DECISION", "uncommitted content lacks an exact verified snapshot"
    if item["snapshotVerified"] or bound["review"] or bound["release"]:
        return "PRESERVED_EVIDENCE", "verified snapshot or bound review/release evidence; retain"
    if item["merged"] and item["reachableRefs"] and item["referenceInventoryComplete"]:
        return "SAFE_TO_DELETE", "clean merged source retained by named refs; no references in complete supplied inventory"
    return "REQUIRES_OWNER_DECISION", "source is unmerged, unreferenced or reference coverage is incomplete"
