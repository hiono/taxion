"""Exercise mise's binary backend with no Go toolchain on the child PATH."""

import argparse
import functools
import hashlib
import http.server
import json
import os
import pathlib
import platform
import re
import shutil
import subprocess
import tempfile
import threading
import urllib.request

from release import archive_contents, formal_version


def run(arguments, env, directory):
    result = subprocess.run(arguments, env=env, cwd=directory, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"{arguments}: {result.stdout}\n{result.stderr}")
    return result.stdout.strip()


def rollback_artifact(manifest, system, arch):
    version = manifest["version"]
    target = manifest["rollbackVersion"]
    platforms = manifest["rollbackPlatforms"]
    reason = manifest.get("rollbackUnavailableReason", "")
    if manifest["schemaVersion"] == 3:
        if (reason != "prior-distributions-withdrawn" or target or platforms != [] or
                not formal_version(version) or formal_version(version) < (0, 7, 15)):
            raise ValueError("invalid unavailable rollback policy")
        return None
    if (manifest["schemaVersion"] != 2 or reason or not formal_version(target) or
            not formal_version(version) or formal_version(target) >= formal_version(version) or
            system + "-" + arch not in platforms):
        raise ValueError("invalid exact-platform rollback policy")
    if target == "v0.7.8":
        if (system, arch) != ("linux", "amd64"):
            raise ValueError("historical rollback has no exact-platform artifact")
        return target, {
            "name": "taxion-v0.7.8-linux-amd64",
            "sha256": "d0084c4da30ef42b016dfff0c3b1ce1db516f1934584495cbc5c63b66f492753",
            "executableSha256": "d0084c4da30ef42b016dfff0c3b1ce1db516f1934584495cbc5c63b66f492753",
        }
    base = "https://github.com/hiono/taxion/releases/download/" + target + "/"
    with urllib.request.urlopen(base + "release-manifest.json", timeout=60) as response:
        data = response.read(1048577)
    with urllib.request.urlopen(base + "release-manifest.sha256", timeout=60) as response:
        checksum = response.read(1025).decode().split()
    if (len(data) > 1048576 or checksum != [hashlib.sha256(data).hexdigest(), "release-manifest.json"]):
        raise ValueError("rollback manifest checksum mismatch")
    old = json.loads(data)
    if old["version"] != target or old["tag"] != target:
        raise ValueError("rollback manifest version mismatch")
    candidates = [a for a in old["artifacts"] if a["os"] == system and a["architecture"] == arch]
    if len(candidates) != 1:
        raise ValueError("no exact-platform rollback artifact")
    artifact = candidates[0]
    extension = "zip" if system == "windows" else "tar.gz"
    if (artifact["name"] != f"taxion_{target[1:]}_{system}_{arch}.{extension}" or
            not re.fullmatch("[a-f0-9]{64}", artifact["sha256"]) or
            not re.fullmatch("[a-f0-9]{64}", artifact["executableSha256"])):
        raise ValueError("invalid rollback artifact identity")
    return target, artifact


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--staged-release", action="store_true")
    args = parser.parse_args()
    system = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}[platform.system()]
    arch = {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64", "ARM64": "arm64"}[platform.machine()]
    dist = pathlib.Path("dist").resolve()
    archives = list(dist.glob(f"taxion_*_{system}_{arch}.*"))
    if len(archives) != 1:
        raise ValueError("native archive missing or ambiguous")
    archive = archives[0]
    info = json.loads(archive_contents(archive)["release-info.json"])
    version = info["version"].removeprefix("v")
    mise = shutil.which("mise")
    if not mise:
        raise ValueError("pinned mise executable is required")
    with tempfile.TemporaryDirectory() as temporary:
        root = pathlib.Path(temporary).resolve()
        env = {key: value for key, value in os.environ.items() if not key.startswith(("MISE_", "__MISE_"))}
        # Explicitly remove every PATH entry containing a Go compiler. No Go backend
        # is selected; a fake source-build attempt would also fail this check.
        env["PATH"] = os.pathsep.join(
            entry for entry in os.environ["PATH"].split(os.pathsep)
            if entry and not (pathlib.Path(entry) / ("go.exe" if system == "windows" else "go")).is_file()
        )
        if shutil.which("go", path=env["PATH"]):
            raise ValueError("Go compiler remains on installation PATH")
        env.update({
            "MISE_CONFIG_DIR": str(root / "config"), "MISE_DATA_DIR": str(root / "data"),
            "MISE_CACHE_DIR": str(root / "cache"), "MISE_STATE_DIR": str(root / "state"),
            "MISE_GLOBAL_CONFIG_FILE": str(root / "global.toml"),
            "MISE_SYSTEM_CONFIG_FILE": os.devnull, "MISE_YES": "1",
            "MISE_TRUSTED_CONFIG_PATHS": str(root),
        })
        tool = "github:hiono/taxion"
        snapshot = info["channel"] == "development"
        server = None
        if snapshot or args.staged_release:
            # Unpublished fixtures have no public SLSA statement; pin their URL and checksum below.
            env["MISE_GITHUB_SLSA"] = "0"
            handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(dist))
            server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            url = f"http://127.0.0.1:{server.server_port}/{archive.name}"
            mise_os = {"darwin": "macos"}.get(system, system)
            mise_arch = {"amd64": "x64"}.get(arch, arch)
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            config = (
                f'[tools."{tool}"]\nversion = "{version}"\n'
                f'[tools."{tool}".platforms.{mise_os}-{mise_arch}]\n'
                f'url = "{url}"\nchecksum = "sha256:{digest}"\n'
            )
            (root / "mise.toml").write_text(config)
        else:
            run([mise, "use", "--pin", tool + "@" + version], env, root)
        try:
            run([mise, "install"], env, root)
            binary = run([mise, "which", "taxion"], env, root)
            expected = "taxion " + info["version"]
            if run([binary, "--version"], env, root) != expected:
                raise ValueError("installed version mismatch")
            executable = "taxion.exe" if system == "windows" else "taxion"
            if pathlib.Path(binary).read_bytes() != archive_contents(archive)[executable]:
                raise ValueError("mise installed different executable bytes")
            if not snapshot and version not in (root / "mise.toml").read_text():
                raise ValueError("version not pinned")
            if run([mise, "exec", "--", executable, "--version"], env, root) != expected:
                raise ValueError("mise PATH did not select the pinned binary")
            if not snapshot:
                store = root / "product-fixture"
                store.mkdir()
                policy = {
                    "schemaVersion": 1, "mode": "product", "storeDirectory": str(store),
                    "binaryPath": str(pathlib.Path(binary).resolve()), "miseDataDir": str(root / "data"),
                    "manifestPath": str(dist / "release-manifest.json"),
                    "manifestSha256": hashlib.sha256((dist / "release-manifest.json").read_bytes()).hexdigest(),
                    "archivePath": str(archive), "checksumsPath": str(dist / "SHA256SUMS"),
                }
                request = root / "policy.json"
                request.write_text(json.dumps(policy))
                prefix = [binary, "--store", str(store / "ledger.json")]
                run(prefix + ["provenance", "configure", "--file", str(request)], env, root)
                report = json.loads(run(prefix + ["provenance", "verify"], env, root))
                if report["mode"] != "product":
                    raise ValueError("formal product provenance did not pass")
                database = store / "workspace.db"
                database.write_bytes(b"unchanged synthetic store sentinel")
                rogue = root / executable
                shutil.copy2(binary, rogue)
                before = {p.name: p.read_bytes() for p in store.iterdir()}
                refusal = subprocess.run([str(rogue), "--store", str(store / "ledger.json"), "task", "graph"],
                                         env=env, cwd=root, capture_output=True, text=True)
                if refusal.returncode == 0 or "provenance-rejected" not in refusal.stdout:
                    raise ValueError("PATH/copy hijack was not rejected")
                if before != {p.name: p.read_bytes() for p in store.iterdir()}:
                    raise ValueError("provenance refusal changed synthetic store bytes")
            rollback = "NOT_RUN_SNAPSHOT"
            rollback_version = ""
            if not snapshot:
                manifest = json.loads((dist / "release-manifest.json").read_bytes())
                selected = rollback_artifact(manifest, system, arch)
                rollback = "NOT_AVAILABLE_PRIOR_DISTRIBUTIONS_WITHDRAWN"
            if not snapshot and selected is not None:
                rollback_version, old = selected
                config = root / "mise.toml"
                current = config.read_text()
                mise_os = {"darwin": "macos"}.get(system, system)
                mise_arch = {"amd64": "x64"}.get(arch, arch)
                raw = '\nformat = "raw"\nbin = "taxion"' if rollback_version == "v0.7.8" else ""
                config.write_text(
                    f'[tools."{tool}"]\nversion = "{rollback_version[1:]}"\n'
                    f'[tools."{tool}".platforms.{mise_os}-{mise_arch}]\n'
                    f'url = "https://github.com/hiono/taxion/releases/download/{rollback_version}/{old["name"]}"\n'
                    f'checksum = "sha256:{old["sha256"]}"{raw}\n'
                )
                run([mise, "install"], env, root)
                old_binary = run([mise, "which", "taxion"], env, root)
                if (run([old_binary, "--version"], env, root) != "taxion " + rollback_version or
                        hashlib.sha256(pathlib.Path(old_binary).read_bytes()).hexdigest() != old["executableSha256"]):
                    raise ValueError("rollback did not select the verified executable")
                config.write_text(current)
                run([mise, "install"], env, root)
                if run([mise, "exec", "--", executable, "--version"], env, root) != expected:
                    raise ValueError("upgrade back to pinned archive failed")
                rollback = "PASS_BINARY_SWITCH_ONLY"
            config = root / "mise.toml"
            pinned = config.read_text()
            config.write_text("[tools]\n")
            unselected = subprocess.run([mise, "which", "taxion"], env=env, cwd=root,
                                        capture_output=True, text=True)
            if unselected.returncode == 0:
                raise ValueError("removing the tool configuration did not disable selection")
            config.write_text(pinned)
            run([mise, "uninstall", tool + "@" + version], env, root)
            if pathlib.Path(binary).exists():
                raise ValueError("uninstall left the pinned executable installed")
            run([mise, "install"], env, root)
            binary = run([mise, "which", "taxion"], env, root)
            if run([binary, "--version"], env, root) != expected:
                raise ValueError("reinstallation did not restore the pinned version")
            if pathlib.Path(binary).read_bytes() != archive_contents(archive)[executable]:
                raise ValueError("reinstallation changed the pinned executable bytes")
            print(json.dumps({
                "platform": system + "-" + arch, "backend": "github",
                "nativeMiseSmoke": "PASS", "goToolchainOnPath": False,
                "sourceBuildUsed": False, "version": version, "binaryPath": binary,
                "selection": "snapshot-native-url" if snapshot else
                             ("staged-release-native-url" if args.staged_release else "published-release-autodetection"),
                "productProvenance": "NOT_RUN" if snapshot else "PASS",
                "copiedBinaryRefusedWithImmutableStore": "NOT_RUN" if snapshot else "PASS",
                "rollback": rollback, "rollbackVersion": rollback_version,
                "disableSelection": "PASS", "uninstall": "PASS", "reinstallPinnedBytes": "PASS",
            }))
        finally:
            if server:
                server.shutdown()


if __name__ == "__main__":
    main()
