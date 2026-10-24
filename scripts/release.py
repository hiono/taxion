"""Deterministic release metadata and archive verification; standard library only."""

import argparse
import hashlib
import json
import os
import pathlib
import platform
import re
import subprocess
import tarfile
import zipfile
from workspace_hygiene import inspect as inspect_release_workspace

PLATFORMS = [(os, arch) for os in ("linux", "darwin", "windows") for arch in ("amd64", "arm64")]
GORELEASER = "2.18.3"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def command(*args):
    return subprocess.check_output(args, text=True).strip()

def require_absent_release(tag):
    result = subprocess.run(
        ["gh", "api", "--include", "repos/hiono/taxion/releases/tags/" + tag],
        capture_output=True, text=True,
    )
    status = re.findall(r"^HTTP/\S+\s+(\d{3})\b", result.stdout, re.MULTILINE)
    if not status or status[-1] != "404" or result.returncode == 0:
        raise ValueError("release absence requires an authenticated HTTP 404; existing releases and other errors are refused")


def published_assets(dist, tag):
    return [dist / f"taxion_{tag[1:]}_{system}_{arch}.{'zip' if system == 'windows' else 'tar.gz'}"
            for system, arch in PLATFORMS] + [
                dist / "SHA256SUMS", dist / "release-manifest.json", dist / "release-manifest.sha256",
            ]


def published_release(tag):
    return json.loads(command("gh", "api", "repos/hiono/taxion/releases/tags/" + tag))


def require_published_rollback(tag):
    release = published_release(tag)
    names = {asset["name"] for asset in release.get("assets", [])}
    if tag == "v0.7.8":
        required = {"taxion-v0.7.8-linux-amd64", "SHA256SUMS"}
    else:
        required = {path.name for path in published_assets(pathlib.Path(), tag)}
    if release.get("tag_name") != tag or release.get("draft") or release.get("prerelease") or not required <= names:
        raise ValueError("rollback target is not a published formal release with all required assets: " + tag)


def verify_published(dist, public, tag):
    for original in published_assets(dist, tag):
        if original.read_bytes() != (public / original.name).read_bytes():
            raise ValueError("published asset differs from packaged bytes: " + original.name)


def archive_contents(path):
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            return {n: archive.read(n) for n in archive.namelist() if not n.endswith("/")}
    with tarfile.open(path, "r:gz") as archive:
        if any(m.issym() or m.islnk() or m.name.startswith("/") or ".." in pathlib.PurePosixPath(m.name).parts for m in archive):
            raise ValueError("unsafe archive member")
        return {m.name: archive.extractfile(m).read() for m in archive if m.isfile()}


def prepare(dist, tag, snapshot=False):
    if not snapshot:
        hygiene = inspect_release_workspace(pathlib.Path.cwd())
        if hygiene["decision"] != "PASS":
            raise ValueError("release workspace hygiene rejected: " + ", ".join(hygiene["rejections"]))
    if command("git", "status", "--porcelain", "--untracked-files=normal"):
        raise ValueError("release requires clean source")
    version = pathlib.Path("VERSION").read_text().strip()
    if tag != "v" + version:
        raise ValueError("tag differs from VERSION")
    if not snapshot and command("git", "rev-parse", tag + "^{commit}") != command("git", "rev-parse", "HEAD"):
        raise ValueError("tag is not HEAD")
    dist.mkdir(exist_ok=True)
    metadata = {
        "schemaVersion": 1, "version": tag + ("-snapshot" if snapshot else ""),
        "sourceRevision": command("git", "rev-parse", "HEAD"),
        "manifestUrl": f"https://github.com/hiono/taxion/releases/download/{tag}/release-manifest.json",
        "channel": "development" if snapshot else "github-release",
    }
    pathlib.Path(".release").mkdir(exist_ok=True)
    pathlib.Path(".release/release-info.json").write_text(json.dumps(metadata, indent=2) + "\n")
    notices = []
    modules = set()
    for target_os, target_arch in PLATFORMS:
        env = dict(os.environ, GOOS=target_os, GOARCH=target_arch, CGO_ENABLED="0")
        output = subprocess.check_output(
            ["go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}", "./cmd/taxion"],
            env=env, text=True,
        )
        modules.update(line for line in output.splitlines() if line and not line.startswith("github.com/hiono/taxion|"))
    for module in sorted(modules):
        name, version, directory = module.split("|")
        files = sorted({p for pattern in ("LICENSE*", "COPYING*", "NOTICE*", "PATENTS") for p in pathlib.Path(directory).glob(pattern) if p.is_file()})
        if not files:
            raise ValueError("license not found: " + name)
        notices.append("\n" + name + " " + version + "\n")
        notices.extend("\n" + p.name + "\n" + p.read_text() for p in files)
    notices.append("\nGo standard library\n" + (pathlib.Path(command("go", "env", "GOROOT")) / "LICENSE").read_text())
    pathlib.Path(".release/THIRD_PARTY_NOTICES.txt").write_text("\n".join(notices))


def build_metadata(dist, tag, snapshot=False):
    info = json.loads(pathlib.Path(".release/release-info.json").read_text())
    build = json.loads((dist / "metadata.json").read_text())
    suffix = "-snapshot" if snapshot else ""
    if (info["version"] != tag + suffix or info["channel"] != ("development" if snapshot else "github-release") or
            build["commit"] != info["sourceRevision"] or build["version"] != tag[1:] + suffix or
            not snapshot and build["tag"] != tag):
        raise ValueError("formal manifest requires matching tagged release metadata")
    return info, build


def manifest(dist, tag):
    info, build = build_metadata(dist, tag)
    rollback = build.get("previous_tag", "")
    if not re.fullmatch(r"v\d+\.\d+\.\d+", rollback) or rollback == tag:
        raise ValueError("rollback requires the previous formal release tag")
    require_published_rollback(rollback)
    artifacts = []
    sums = (dist / "SHA256SUMS").read_text()
    for target_os, arch in PLATFORMS:
        ext = "zip" if target_os == "windows" else "tar.gz"
        path = dist / f"taxion_{tag[1:]}_{target_os}_{arch}.{ext}"
        data = path.read_bytes()
        digest = sha(data)
        if f"{digest}  {path.name}" not in sums:
            raise ValueError("GoReleaser archive checksum missing or incorrect")
        executable = "taxion.exe" if target_os == "windows" else "taxion"
        contents = archive_contents(path)
        if set(contents) != {executable, "LICENSE", "release-info.json", "THIRD_PARTY_NOTICES.txt"}:
            raise ValueError(f"unexpected archive contents: {path.name}: {list(contents)}")
        if json.loads(contents["release-info.json"]) != info:
            raise ValueError("archive metadata mismatch")
        artifacts.append({
            "name": path.name, "os": target_os, "architecture": arch, "format": ext,
            "executable": executable, "sha256": digest,
            "executableSha256": sha(contents[executable]),
        })
    result = {
        "schemaVersion": 2, "version": tag, "tag": tag,
        "sourceRevision": info["sourceRevision"],
        "buildTimestamp": build["date"],
        "goVersion": command("go", "env", "GOVERSION"),
        "goreleaserVersion": GORELEASER, "artifacts": artifacts,
        "checksumsSha256": sha((dist / "SHA256SUMS").read_bytes()),
        "miseIdentifier": "github:hiono/taxion@" + tag[1:],
        "rollbackVersion": rollback,
        "rollbackPlatforms": ["linux-amd64"] if rollback == "v0.7.8" else [system + "-" + arch for system, arch in PLATFORMS],
        "releaseUrl": "https://github.com/hiono/taxion/releases/tag/" + tag,
        "generatedBy": "hiono/taxion/.github/workflows/release.yml",
        "ciRun": "https://github.com/hiono/taxion/actions/runs/" + os.environ["GITHUB_RUN_ID"],
        "dirty": False, "channel": "github-release",
    }
    path = dist / "release-manifest.json"
    path.write_text(json.dumps(result, indent=2) + "\n")
    (dist / "release-manifest.sha256").write_text(sha(path.read_bytes()) + "  " + path.name + "\n")


def smoke(dist):
    os = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}[platform.system()]
    arch = {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64", "ARM64": "arm64"}[platform.machine()]
    paths = list(dist.glob(f"taxion_*_{os}_{arch}.*"))
    if len(paths) != 1:
        raise ValueError(f"expected exactly one native archive: {paths}")
    contents = archive_contents(paths[0])
    executable = "taxion.exe" if os == "windows" else "taxion"
    info = json.loads(contents["release-info.json"])
    import tempfile
    with tempfile.TemporaryDirectory() as temp:
        binary = pathlib.Path(temp) / executable
        binary.write_bytes(contents[executable])
        binary.chmod(0o755)
        if command(str(binary), "--version") != "taxion " + info["version"]:
            raise ValueError("native archive version mismatch")
        command(str(binary), "--help")
    print(json.dumps({"os": os, "arch": arch, "nativeArchiveSmoke": "PASS"}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("prepare", "manifest", "smoke", "require-absent", "verify-published", "verify-metadata"))
    parser.add_argument("--dist", type=pathlib.Path, default=pathlib.Path("dist"))
    parser.add_argument("--tag")
    parser.add_argument("--public", type=pathlib.Path, default=pathlib.Path("public"))
    parser.add_argument("--snapshot", action="store_true")
    args = parser.parse_args()
    if args.action == "require-absent":
        require_absent_release(args.tag)
    elif args.action == "verify-published":
        verify_published(args.dist, args.public, args.tag)
    elif args.action == "verify-metadata":
        build_metadata(args.dist, args.tag, args.snapshot)
    elif args.action == "smoke":
        smoke(args.dist)
    elif args.action == "prepare":
        prepare(args.dist, args.tag, args.snapshot)
    else:
        manifest(args.dist, args.tag)
