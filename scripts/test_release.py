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
            "tag": "v0.7.9", "commit": info["sourceRevision"], "date": "2026-10-23T00:00:00Z",
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
            with contextlib.chdir(root):
                (dist / "SHA256SUMS").write_text("tampered")
                with self.assertRaisesRegex(ValueError, "checksum"):
                    release.manifest(dist, "v0.7.9")
                with self.assertRaisesRegex(ValueError, "matching tagged"):
                    release.manifest(dist, "v0.7.10")


if __name__ == "__main__":
    unittest.main()
