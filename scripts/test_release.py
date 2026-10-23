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
    def fixture(self, root):
        info = {"schemaVersion": 1, "version": "v0.7.9", "channel": "github-release",
                "sourceRevision": "a" * 40, "manifestUrl": "https://github.com/hiono/taxion/releases/download/v0.7.9/release-manifest.json"}
        (root / ".release").mkdir()
        (root / ".release/release-info.json").write_text(json.dumps(info))
        dist = root / "dist"
        dist.mkdir()
        (dist / "metadata.json").write_text(json.dumps({
            "project_name": "taxion", "tag": "v0.7.9", "previous_tag": "v0.7.8",
            "version": "0.7.9", "commit": info["sourceRevision"], "date": "2026-10-23T00:00:00Z",
            "runtime": {"goos": "linux", "goarch": "amd64"},
        }))
        sums = []
        for system, arch in release.PLATFORMS:
            executable = "taxion.exe" if system == "windows" else "taxion"
            extension = "zip" if system == "windows" else "tar.gz"
            archive = dist / f"taxion_0.7.9_{system}_{arch}.{extension}"
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
