import contextlib
import io
import json
import os
import pathlib
import tarfile
import tempfile
import unittest
import zipfile
from unittest import mock

import release


class ManifestTests(unittest.TestCase):
    def setUp(self):
        def published(tag):
            if tag == "v0.7.8":
                names = ["taxion-v0.7.8-linux-amd64", "SHA256SUMS"]
            else:
                names = [path.name for path in release.published_assets(pathlib.Path(), tag)]
            return {"tag_name": tag, "draft": False, "prerelease": False, "assets": [{"name": n} for n in names]}
        patcher = mock.patch.object(release, "published_release", side_effect=published)
        self.published = patcher.start()
        self.addCleanup(patcher.stop)
        tags = mock.patch.object(release, "published_formal_tags", return_value=["v0.7.11", "v0.7.10", "v0.7.9", "v0.7.8"])
        self.tags = tags.start()
        self.addCleanup(tags.stop)

    def fixture(self, root, version="0.7.9", previous="v0.7.8"):
        info = {"schemaVersion": 1, "version": "v" + version, "channel": "github-release",
                "sourceRevision": "a" * 40, "manifestUrl": f"https://github.com/hiono/taxion/releases/download/v{version}/release-manifest.json"}
        (root / ".release").mkdir()
        (root / ".release/release-info.json").write_text(json.dumps(info))
        dist = root / "dist"
        dist.mkdir()
        (dist / "metadata.json").write_text(json.dumps({
            "project_name": "taxion", "tag": "v" + version, "previous_tag": previous,
            "version": version, "commit": info["sourceRevision"], "date": "2026-10-23T00:00:00Z",
            "runtime": {"goos": "linux", "goarch": "amd64"},
        }))
        sums = []
        for system, arch in release.PLATFORMS:
            executable = "taxion.exe" if system == "windows" else "taxion"
            extension = "zip" if system == "windows" else "tar.gz"
            archive = dist / f"taxion_{version}_{system}_{arch}.{extension}"
            files = {executable: b"synthetic fixture, not a runnable native executable",
                     "LICENSE": b"fixture license", "THIRD_PARTY_NOTICES.txt": b"fixture notices",
                     "release-info.json": json.dumps(info).encode()}
            if system == "windows":
                with zipfile.ZipFile(archive, "w") as output:
                    for name, content in files.items():
                        output.writestr(name, content)
            else:
                with tarfile.open(archive, "w:gz") as output:
                    for name, content in files.items():
                        header = tarfile.TarInfo(name)
                        header.size = len(content)
                        output.addfile(header, io.BytesIO(content))
            sums.append(release.sha(archive.read_bytes()) + "  " + archive.name)
        (dist / "SHA256SUMS").write_text("\n".join(sums) + "\n")
        return dist

    def test_manifest_all_platforms_and_self_hash(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            dist = self.fixture(root)
            with contextlib.chdir(root), mock.patch.dict(os.environ, GITHUB_RUN_ID="12345"), mock.patch.object(release, "command", return_value="go1.27.2"):
                release.manifest(dist, "v0.7.9")
            document = json.loads((dist / "release-manifest.json").read_text())
            self.assertEqual(document["schemaVersion"], 2)
            self.assertEqual(document["buildTimestamp"], "2026-10-23T00:00:00Z")
            self.assertFalse(document["dirty"])
            self.assertEqual(document["rollbackVersion"], "v0.7.8")
            self.assertEqual(document["rollbackPlatforms"], ["linux-amd64"])
            self.assertEqual({(a["os"], a["architecture"]) for a in document["artifacts"]}, set(release.PLATFORMS))
            self.assertEqual(release.sha((dist / "release-manifest.json").read_bytes()),
                             (dist / "release-manifest.sha256").read_text().split()[0])
            public = root / "public"
            public.mkdir()
            for path in release.published_assets(dist, "v0.7.9"):
                (public / path.name).write_bytes(path.read_bytes())
            self.assertFalse((public / "metadata.json").exists())
            release.verify_published(dist, public, "v0.7.9")
            (public / "release-manifest.json").write_text("tampered")
            with self.assertRaisesRegex(ValueError, "differs"):
                release.verify_published(dist, public, "v0.7.9")
            with contextlib.chdir(root):
                metadata = json.loads((dist / "metadata.json").read_text())
                metadata["version"] = "0.7.9-snapshot"
                (dist / "metadata.json").write_text(json.dumps(metadata))
                with self.assertRaisesRegex(ValueError, "matching tagged"):
                    release.manifest(dist, "v0.7.9")
                metadata["version"] = "0.7.9"
                (dist / "metadata.json").write_text(json.dumps(metadata))
                (dist / "SHA256SUMS").write_text("tampered")
                with self.assertRaisesRegex(ValueError, "checksum"):
                    release.manifest(dist, "v0.7.9")
                with self.assertRaisesRegex(ValueError, "matching tagged"):
                    release.manifest(dist, "v0.7.10")

    def test_new_patch_rollback_policy_uses_all_published_079_platforms(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            dist = self.fixture(root, "0.7.10", "v0.7.9")
            with contextlib.chdir(root), mock.patch.dict(os.environ, GITHUB_RUN_ID="12345"), mock.patch.object(release, "command", return_value="go1.27.2"):
                release.manifest(dist, "v0.7.10")
            document = json.loads((dist / "release-manifest.json").read_text())
            self.assertEqual(document["rollbackVersion"], "v0.7.9")
            self.assertEqual(set(document["rollbackPlatforms"]), {os + "-" + arch for os, arch in release.PLATFORMS})

    def test_rollback_skips_unpublished_tags_and_uses_newest_older_release(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            dist = self.fixture(root, "0.7.13", "v0.7.12")
            self.tags.return_value = ["v0.7.9", "v0.7.11", "v0.7.14", "v0.7.10"]
            with contextlib.chdir(root), mock.patch.dict(os.environ, GITHUB_RUN_ID="12345"), mock.patch.object(release, "command", return_value="go1.27.2"):
                release.manifest(dist, "v0.7.13")
            document = json.loads((dist / "release-manifest.json").read_text())
            self.assertEqual(document["rollbackVersion"], "v0.7.11")
            self.published.assert_called_with("v0.7.11")

    def test_rollback_rejects_missing_older_formal_release(self):
        for tags in ([], ["v0.7.12"], ["v0.7.12-rc.1", "v0.8.0", "local", "v0.07.11", "v00.7.1"]):
            with self.subTest(tags=tags), tempfile.TemporaryDirectory() as temporary:
                root = pathlib.Path(temporary)
                dist = self.fixture(root, "0.7.12", "v0.7.11")
                self.tags.return_value = tags
                with contextlib.chdir(root), mock.patch.dict(os.environ, GITHUB_RUN_ID="12345"), mock.patch.object(release, "command", return_value="go1.27.2"):
                    with self.assertRaisesRegex(ValueError, "previous formal release"):
                        release.manifest(dist, "v0.7.12")
                self.assertFalse((dist / "release-manifest.json").exists())

    def test_rollback_rejects_unpublished_or_incomplete_previous_release(self):
        complete = [{"name": p.name} for p in release.published_assets(pathlib.Path(), "v0.7.11")]
        for target in ({"tag_name": "v0.7.11", "draft": True, "prerelease": False, "assets": complete},
                       {"tag_name": "v0.7.11", "draft": False, "prerelease": True, "assets": complete},
                       {"tag_name": "v0.7.11", "draft": False, "prerelease": False, "assets": complete[1:]}):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as temporary:
                root = pathlib.Path(temporary)
                dist = self.fixture(root, "0.7.12", "v0.7.11")
                self.published.side_effect = None
                self.published.return_value = target
                with contextlib.chdir(root), mock.patch.dict(os.environ, GITHUB_RUN_ID="12345"), mock.patch.object(release, "command", return_value="go1.27.2"):
                    with self.assertRaisesRegex(ValueError, "published formal release"):
                        release.manifest(dist, "v0.7.12")
                self.assertFalse((dist / "release-manifest.json").exists())

    def test_workspace_rejection_precedes_all_release_writes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            with mock.patch.object(release, "inspect_release_workspace", return_value={
                "decision": "REJECT", "rejections": ["UNKNOWN_WORKTREE_OWNERSHIP"],
            }):
                with self.assertRaisesRegex(ValueError, "workspace hygiene rejected"):
                    release.prepare(root / "dist", "v0.7.10")
            self.assertFalse((root / "dist").exists())

    def test_release_absence_uses_http_status_not_json_body(self):
        for response, returncode, allowed in (
            ('HTTP/2.0 404 Not Found\n\n{"message":"Not Found"}', 1, True),
            ('HTTP/2.0 200 OK\n\n{}', 0, False),
            ('HTTP/2.0 403 Forbidden\n\n{"status":"404"}', 1, False),
            ('HTTP/2.0 500 Internal Server Error\n\n{}', 1, False),
            ('', 1, False),
            ('HTTP/2.0 404 Not Found\n\n{}', 0, False),
        ):
            with self.subTest(response=response, returncode=returncode):
                result = mock.Mock(stdout=response, returncode=returncode)
                with mock.patch.object(release.subprocess, "run", return_value=result):
                    if allowed:
                        release.require_absent_release("v0.7.9")
                    else:
                        with self.assertRaisesRegex(ValueError, "HTTP 404"):
                            release.require_absent_release("v0.7.9")


if __name__ == "__main__":
    unittest.main()
