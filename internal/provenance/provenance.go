// Package provenance enforces cooperative operation policy, not OS isolation.
package provenance

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const MaxArchiveBytes = 128 << 20

type Artifact struct {
	Name             string `json:"name"`
	OS               string `json:"os"`
	Architecture     string `json:"architecture"`
	Format           string `json:"format"`
	Executable       string `json:"executable"`
	SHA256           string `json:"sha256"`
	ExecutableSHA256 string `json:"executableSha256"`
}

type Manifest struct {
	SchemaVersion     int        `json:"schemaVersion"`
	Version           string     `json:"version"`
	Tag               string     `json:"tag"`
	SourceRevision    string     `json:"sourceRevision"`
	BuildTimestamp    string     `json:"buildTimestamp"`
	GoVersion         string     `json:"goVersion"`
	GoreleaserVersion string     `json:"goreleaserVersion"`
	Artifacts         []Artifact `json:"artifacts"`
	ChecksumsSHA256   string     `json:"checksumsSha256"`
	MiseIdentifier    string     `json:"miseIdentifier"`
	RollbackVersion   string     `json:"rollbackVersion"`
	RollbackPlatforms []string   `json:"rollbackPlatforms"`
	ReleaseURL        string     `json:"releaseUrl"`
	GeneratedBy       string     `json:"generatedBy"`
	CIRun             string     `json:"ciRun"`
	Dirty             *bool      `json:"dirty"`
	Channel           string     `json:"channel"`
}

type Policy struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Mode           string `json:"mode"`
	StoreDirectory string `json:"storeDirectory"`
	BinaryPath     string `json:"binaryPath,omitempty"`
	MiseDataDir    string `json:"miseDataDir,omitempty"`
	ManifestPath   string `json:"manifestPath,omitempty"`
	ManifestSHA256 string `json:"manifestSha256,omitempty"`
	ArchivePath    string `json:"archivePath,omitempty"`
	ChecksumsPath  string `json:"checksumsPath,omitempty"`
}

type Identity struct {
	Version, Revision, Channel, OS, Architecture, Executable string
	Dirty                                                    bool
}

type Report struct {
	Mode           string `json:"mode"`
	BinaryPath     string `json:"binaryPath,omitempty"`
	BinarySHA256   string `json:"binarySha256,omitempty"`
	ManifestSHA256 string `json:"manifestSha256,omitempty"`
	ArtifactSHA256 string `json:"artifactSha256,omitempty"`
	Version        string `json:"version,omitempty"`
	SourceRevision string `json:"sourceRevision,omitempty"`
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func Decode(data []byte, target any) error {
	// Reject duplicate keys as well as unknown fields: a pin must have one meaning.
	var walk func(*json.Decoder) error
	walk = func(d *json.Decoder) error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || keys[name] {
						return fmt.Errorf("duplicate or invalid JSON key")
					}
					keys[name] = true
					if err := walk(d); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(d); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unexpected JSON delimiter")
			}
			_, err = d.Token()
		}
		return err
	}
	if err := walk(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}

func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func Read(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("provenance input exceeds size limit")
	}
	return data, nil
}

func realDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func Verify(policy Policy, storeDirectory string, identity Identity) (Report, error) {
	report := Report{Mode: policy.Mode}
	actualStore, err := realDirectory(storeDirectory)
	if err != nil {
		return report, err
	}
	if policy.SchemaVersion != 1 || policy.StoreDirectory != actualStore ||
		(policy.Mode != "development" && policy.Mode != "product") {
		return report, fmt.Errorf("invalid operation mode or store binding")
	}
	if policy.Mode == "development" {
		if policy.BinaryPath != "" || policy.ManifestPath != "" || policy.ManifestSHA256 != "" ||
			policy.ArchivePath != "" || policy.MiseDataDir != "" || policy.ChecksumsPath != "" {
			return report, fmt.Errorf("development policy cannot contain product provenance")
		}
		return report, nil
	}
	if !versionPattern.MatchString(identity.Version) || identity.Version == "v0.7.6" ||
		!revisionPattern.MatchString(identity.Revision) || identity.Dirty ||
		identity.Channel != "github-release" {
		return report, fmt.Errorf("product mode requires an unmodified official release build")
	}
	binary, err := filepath.EvalSymlinks(identity.Executable)
	if err != nil {
		return report, err
	}
	ext := ""
	if identity.OS == "windows" {
		ext = ".exe"
	}
	expected := filepath.Join(policy.MiseDataDir, "installs", "github-hiono-taxion", strings.TrimPrefix(identity.Version, "v"), "taxion"+ext)
	if !filepath.IsAbs(policy.MiseDataDir) || binary != policy.BinaryPath ||
		filepath.Clean(policy.BinaryPath) != expected {
		return report, fmt.Errorf("active executable is not the pinned mise GitHub installation")
	}
	realExpected, err := filepath.EvalSymlinks(expected)
	if err != nil || realExpected != expected {
		return report, fmt.Errorf("pinned installation contains a changed symlink")
	}
	before, err := os.Stat(binary)
	if err != nil || !before.Mode().IsRegular() {
		return report, fmt.Errorf("active executable is not a regular file")
	}
	data, err := Read(binary, MaxArchiveBytes)
	if err != nil {
		return report, err
	}
	report.BinaryPath, report.BinarySHA256 = binary, Hash(data)
	manifestData, err := Read(policy.ManifestPath, 1<<20)
	if err != nil {
		return report, err
	}
	report.ManifestSHA256 = Hash(manifestData)
	if !digestPattern.MatchString(policy.ManifestSHA256) || report.ManifestSHA256 != policy.ManifestSHA256 {
		return report, fmt.Errorf("manifest does not match approved SHA-256 pin")
	}
	var manifest Manifest
	if err := Decode(manifestData, &manifest); err != nil {
		return report, err
	}
	_, timestampErr := time.Parse(time.RFC3339Nano, manifest.BuildTimestamp)
	if manifest.SchemaVersion != 2 || manifest.Dirty == nil || *manifest.Dirty || manifest.Channel != "github-release" ||
		manifest.Version != identity.Version || manifest.Tag != identity.Version ||
		manifest.SourceRevision != identity.Revision || timestampErr != nil ||
		manifest.GoVersion == "" || manifest.GoreleaserVersion != "2.18.3" ||
		manifest.GeneratedBy != "hiono/taxion/.github/workflows/release.yml" ||
		!regexp.MustCompile(`^https://github.com/hiono/taxion/actions/runs/[0-9]+$`).MatchString(manifest.CIRun) ||
		manifest.ReleaseURL != "https://github.com/hiono/taxion/releases/tag/"+identity.Version ||
		manifest.MiseIdentifier != "github:hiono/taxion@"+strings.TrimPrefix(identity.Version, "v") ||
		manifest.RollbackVersion == manifest.Version ||
		!validRollbackPolicy(manifest.RollbackVersion, manifest.Version, manifest.RollbackPlatforms) || len(manifest.Artifacts) != 6 {
		return report, fmt.Errorf("unsupported manifest or release identity mismatch")
	}
	checksums, err := Read(policy.ChecksumsPath, 1<<20)
	if err != nil || !digestPattern.MatchString(manifest.ChecksumsSHA256) ||
		Hash(checksums) != manifest.ChecksumsSHA256 {
		return report, fmt.Errorf("checksums file mismatch")
	}
	seen := map[string]bool{}
	var selected Artifact
	for _, artifact := range manifest.Artifacts {
		key := artifact.OS + "-" + artifact.Architecture
		if seen[key] || (artifact.OS != "linux" && artifact.OS != "darwin" && artifact.OS != "windows") ||
			(artifact.Architecture != "amd64" && artifact.Architecture != "arm64") ||
			!digestPattern.MatchString(artifact.SHA256) || !digestPattern.MatchString(artifact.ExecutableSHA256) {
			return report, fmt.Errorf("invalid or duplicate artifact platform")
		}
		seen[key] = true
		format, executable := "tar.gz", "taxion"
		if artifact.OS == "windows" {
			format, executable = "zip", "taxion.exe"
		}
		name := fmt.Sprintf("taxion_%s_%s_%s.%s", strings.TrimPrefix(identity.Version, "v"), artifact.OS, artifact.Architecture, format)
		if artifact.Name != name || artifact.Format != format || artifact.Executable != executable ||
			!strings.Contains(string(checksums), artifact.SHA256+"  "+name+"\n") {
			return report, fmt.Errorf("artifact naming or checksum mismatch")
		}
		if artifact.OS == identity.OS && artifact.Architecture == identity.Architecture {
			selected = artifact
		}
	}
	if selected.Name == "" || selected.ExecutableSHA256 != report.BinarySHA256 {
		return report, fmt.Errorf("unsupported platform or executable SHA-256 mismatch")
	}
	archive, err := Read(policy.ArchivePath, MaxArchiveBytes)
	if err != nil {
		return report, err
	}
	report.ArtifactSHA256 = Hash(archive)
	if report.ArtifactSHA256 != selected.SHA256 {
		return report, fmt.Errorf("archive SHA-256 mismatch; modified and repacked archives are not permitted")
	}
	if err := verifyArchive(archive, selected, identity); err != nil {
		return report, err
	}
	after, err := os.Stat(binary)
	resolved, resolveErr := filepath.EvalSymlinks(identity.Executable)
	if err != nil || resolveErr != nil || resolved != binary || !os.SameFile(before, after) ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return report, fmt.Errorf("executable changed during provenance verification")
	}
	report.Version, report.SourceRevision = identity.Version, identity.Revision
	return report, nil
}

func verifyArchive(data []byte, artifact Artifact, identity Identity) error {
	seen := map[string]bool{}
	check := func(name string, reader io.Reader) error {
		if seen[name] || (name != artifact.Executable && name != "LICENSE" && name != "release-info.json" && name != "THIRD_PARTY_NOTICES.txt") {
			return fmt.Errorf("unexpected or duplicate archive member")
		}
		seen[name] = true
		content, err := io.ReadAll(io.LimitReader(reader, MaxArchiveBytes+1))
		if err != nil || len(content) > MaxArchiveBytes {
			return fmt.Errorf("invalid archive member size")
		}
		if name == artifact.Executable && Hash(content) != artifact.ExecutableSHA256 {
			return fmt.Errorf("archived executable mismatch")
		}
		if name == "release-info.json" {
			var info struct {
				SchemaVersion  int    `json:"schemaVersion"`
				Version        string `json:"version"`
				SourceRevision string `json:"sourceRevision"`
				ManifestURL    string `json:"manifestUrl"`
				Channel        string `json:"channel"`
			}
			if err := Decode(content, &info); err != nil {
				return err
			}
			if info.SchemaVersion != 1 || info.Version != identity.Version ||
				info.SourceRevision != identity.Revision || info.Channel != "github-release" ||
				info.ManifestURL != "https://github.com/hiono/taxion/releases/download/"+identity.Version+"/release-manifest.json" {
				return fmt.Errorf("archived release metadata mismatch")
			}
		}
		return nil
	}
	if artifact.Format == "zip" {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, file := range archive.File {
			if !file.Mode().IsRegular() {
				return fmt.Errorf("non-regular archive entry")
			}
			reader, err := file.Open()
			if err != nil {
				return err
			}
			err = check(file.Name, reader)
			reader.Close()
			if err != nil {
				return err
			}
		}
	} else {
		compressed, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer compressed.Close()
		archive := tar.NewReader(compressed)
		for {
			header, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if header.Typeflag != tar.TypeReg || header.Size > MaxArchiveBytes {
				return fmt.Errorf("non-regular or oversized archive entry")
			}
			if err := check(header.Name, archive); err != nil {
				return err
			}
		}
	}
	if len(seen) != 4 {
		return fmt.Errorf("archive must contain executable, LICENSE, notices and release-info.json")
	}
	return nil
}
