"""Exercise mise's binary backend with no Go toolchain on the child PATH."""

import functools
import hashlib
import http.server
import json
import os
import pathlib
import platform
import shutil
import subprocess
import tempfile
import threading

from release import archive_contents


def run(arguments, env, directory):
    result = subprocess.run(arguments, env=env, cwd=directory, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"{arguments}: {result.stdout}\n{result.stderr}")
    return result.stdout.strip()


def main():
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
        if snapshot:
            # A snapshot has no public release or SLSA statement; its URL and checksum are pinned below.
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
            rollback = "NOT_AVAILABLE"
            if (system, arch) == ("linux", "amd64"):
                config = root / "mise.toml"
                current = config.read_text()
                config.write_text(
                    '[tools]\n"github:hiono/taxion" = { version = "0.7.8", '
                    'asset_pattern = "taxion-v0.7.8-linux-amd64", format = "raw", bin = "taxion", '
                    'checksum = "sha256:d0084c4da30ef42b016dfff0c3b1ce1db516f1934584495cbc5c63b66f492753" }\n'
                )
                run([mise, "install"], env, root)
                if run([mise, "exec", "--", "taxion", "--version"], env, root) != "taxion v0.7.8":
                    raise ValueError("immutable v0.7.8 rollback binary was not selected")
                config.write_text(current)
                run([mise, "install"], env, root)
                if run([mise, "exec", "--", executable, "--version"], env, root) != expected:
                    raise ValueError("upgrade back to pinned archive failed")
                rollback = "PASS_BINARY_SWITCH_ONLY"
            print(json.dumps({
                "platform": system + "-" + arch, "backend": "github",
                "nativeMiseSmoke": "PASS", "goToolchainOnPath": False,
                "sourceBuildUsed": False, "version": version, "binaryPath": binary,
                "selection": "snapshot-native-url" if snapshot else "published-release-autodetection",
                "productProvenance": "NOT_RUN" if snapshot else "PASS",
                "copiedBinaryRefusedWithImmutableStore": "NOT_RUN" if snapshot else "PASS",
                "rollbackTo078": rollback,
            }))
        finally:
            if server:
                server.shutdown()


if __name__ == "__main__":
    main()
