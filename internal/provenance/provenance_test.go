package provenance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func fixture(t *testing.T) (Policy, Identity, Manifest) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "installs", "github-hiono-taxion", "0.7.9", "taxion")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("synthetic-release-bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	id := Identity{Version: "v0.7.9", Revision: strings.Repeat("a", 40),
		Channel: "github-release", OS: "linux", Architecture: "amd64", Executable: binary}
	metadata, _ := json.Marshal(map[string]any{"schemaVersion": 1, "version": id.Version,
		"sourceRevision": id.Revision, "channel": id.Channel,
		"manifestUrl": "https://github.com/hiono/taxion/releases/download/v0.7.9/release-manifest.json"})
	var packed bytes.Buffer
	compressed := gzip.NewWriter(&packed)
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"taxion", []byte("synthetic-release-bytes")}, {"LICENSE", []byte("synthetic license")}, {"release-info.json", metadata}, {"THIRD_PARTY_NOTICES.txt", []byte("synthetic notices")}} {
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Size: int64(len(entry.data)), Mode: 0755, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	policy := Policy{SchemaVersion: 1, Mode: "product", StoreDirectory: root,
		BinaryPath: binary, MiseDataDir: root, ManifestPath: filepath.Join(root, "manifest.json"),
		ArchivePath: filepath.Join(root, "archive.tar.gz"), ChecksumsPath: filepath.Join(root, "SHA256SUMS")}
	if err := os.WriteFile(policy.ArchivePath, packed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: 2, Version: id.Version, Tag: id.Version, SourceRevision: id.Revision,
		BuildTimestamp: "2026-10-23T00:00:00Z", GoVersion: "go1.27.2", GoreleaserVersion: "2.18.3",
		MiseIdentifier: "github:hiono/taxion@0.7.9", RollbackVersion: "v0.7.8",
		ReleaseURL:  "https://github.com/hiono/taxion/releases/tag/v0.7.9",
		GeneratedBy: "hiono/taxion/.github/workflows/release.yml",
		CIRun:       "https://github.com/hiono/taxion/actions/runs/12345", Channel: "github-release"}
	var sums strings.Builder
	for _, osName := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			format, executable := "tar.gz", "taxion"
			if osName == "windows" {
				format, executable = "zip", "taxion.exe"
			}
			a := Artifact{Name: "taxion_0.7.9_" + osName + "_" + arch + "." + format,
				OS: osName, Architecture: arch, Format: format, Executable: executable,
				SHA256: Hash(packed.Bytes()), ExecutableSHA256: Hash([]byte("synthetic-release-bytes"))}
			manifest.Artifacts = append(manifest.Artifacts, a)
			sums.WriteString(a.SHA256 + "  " + a.Name + "\n")
		}
	}
	if err := os.WriteFile(policy.ChecksumsPath, []byte(sums.String()), 0600); err != nil {
		t.Fatal(err)
	}
	manifest.ChecksumsSHA256 = Hash([]byte(sums.String()))
	policy.ManifestSHA256 = Hash(writeJSON(t, policy.ManifestPath, manifest))
	return policy, id, manifest
}

func TestProductProvenanceAndImmutableRefusals(t *testing.T) {
	policy, identity, _ := fixture(t)
	report, err := Verify(policy, policy.StoreDirectory, identity)
	if err != nil || report.BinarySHA256 != Hash([]byte("synthetic-release-bytes")) {
		t.Fatal(report, err)
	}
	for _, mutation := range []string{"local-build", "go-install", "dirty", "version", "revision", "channel",
		"unsupported-platform", "path-hijack", "manifest-pin", "store-binding", "mode", "unknown-schema", "v076"} {
		t.Run(mutation, func(t *testing.T) {
			p, id := policy, identity
			switch mutation {
			case "local-build":
				id.Version, id.Revision, id.Channel = "", "", ""
			case "go-install":
				id.Version, id.Channel = "v0.7.9", ""
			case "dirty":
				id.Dirty = true
			case "version":
				id.Version = "v0.7.10"
			case "revision":
				id.Revision = strings.Repeat("b", 40)
			case "channel":
				id.Channel = "candidate"
			case "unsupported-platform":
				id.Architecture = "386"
			case "path-hijack":
				id.Executable = filepath.Join(p.StoreDirectory, "other-taxion")
			case "manifest-pin":
				p.ManifestSHA256 = strings.Repeat("0", 64)
			case "store-binding":
				p.StoreDirectory = filepath.Join(p.StoreDirectory, "other-store")
			case "mode":
				p.Mode = ""
			case "unknown-schema":
				p.SchemaVersion = 99
			case "v076":
				id.Version = "v0.7.6"
			}
			before, _ := os.ReadFile(policy.ManifestPath)
			for i := 0; i < 2; i++ {
				if _, err := Verify(p, policy.StoreDirectory, id); err == nil {
					t.Fatal("invalid provenance accepted")
				}
			}
			after, _ := os.ReadFile(policy.ManifestPath)
			if !bytes.Equal(before, after) {
				t.Fatal("verification changed persistence")
			}
		})
	}
}

func TestTamperingAndRepackedArchives(t *testing.T) {
	for _, name := range []string{"binary", "manifest", "archive", "checksums", "symlink", "missing", "duplicate-platform"} {
		t.Run(name, func(t *testing.T) {
			p, id, manifest := fixture(t)
			switch name {
			case "binary":
				if err := os.WriteFile(p.BinaryPath, []byte("altered"), 0700); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				writeJSON(t, p.ManifestPath, map[string]bool{"dirty": false})
			case "archive":
				f, err := os.OpenFile(p.ArchivePath, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Write([]byte("repacked"))
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "checksums":
				writeJSON(t, p.ChecksumsPath, "changed")
			case "symlink":
				target := filepath.Join(p.StoreDirectory, "target")
				if err := os.Rename(p.BinaryPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p.BinaryPath); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "missing":
				if err := os.Remove(p.ManifestPath); err != nil {
					t.Fatal(err)
				}
			case "duplicate-platform":
				manifest.Artifacts[1] = manifest.Artifacts[0]
				p.ManifestSHA256 = Hash(writeJSON(t, p.ManifestPath, manifest))
			}
			if _, err := Verify(p, p.StoreDirectory, id); err == nil {
				t.Fatal("tampered provenance accepted")
			}
		})
	}
}

func TestExplicitDevelopmentModeAndStrictDecode(t *testing.T) {
	p, id, _ := fixture(t)
	development := Policy{SchemaVersion: 1, Mode: "development", StoreDirectory: p.StoreDirectory}
	if _, err := Verify(development, p.StoreDirectory, Identity{}); err != nil {
		t.Fatal(err)
	}
	before := p
	if _, err := Verify(p, p.StoreDirectory, Identity{}); err == nil {
		t.Fatal("local build connected to product store")
	}
	if !reflect.DeepEqual(before, p) {
		t.Fatal("verification changed mode")
	}
	for _, text := range []string{`{"mode":"product","mode":"development"}`, `{"unknown":true}`, `{}`, `null`, `{} {}`} {
		var candidate Policy
		err := Decode([]byte(text), &candidate)
		if err == nil {
			_, err = Verify(candidate, p.StoreDirectory, id)
		}
		if err == nil {
			t.Fatal("ambiguous or incomplete policy accepted", text)
		}
	}
}
