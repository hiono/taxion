package provenance

import (
	"regexp"
	"strconv"
	"strings"
)

var formalVersion = regexp.MustCompile(`^v(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})$`)

func validManifestRollback(manifest Manifest) bool {
	if manifest.SchemaVersion == 3 {
		return manifest.RollbackUnavailableReason == "prior-distributions-withdrawn" &&
			manifest.RollbackVersion == "" && manifest.RollbackPlatforms != nil &&
			len(manifest.RollbackPlatforms) == 0 && manifest.Version == "v0.7.15"
	}
	return manifest.SchemaVersion == 2 && manifest.RollbackUnavailableReason == "" &&
		validRollbackPolicy(manifest.RollbackVersion, manifest.Version, manifest.RollbackPlatforms)
}

// Historical releases retain their original policy. Only published platform
// combinations are valid; no source-build or cross-platform fallback exists.
// Publication itself is checked when the manifest is generated.
func validRollbackPolicy(version, current string, platforms []string) bool {
	if !olderVersion(version, current) {
		return false
	}
	if version == "v0.7.8" {
		return len(platforms) == 1 && platforms[0] == "linux-amd64"
	}
	if !olderVersion("v0.7.8", version) || len(platforms) != 6 {
		return false
	}
	seen := map[string]bool{}
	for _, platform := range platforms {
		switch platform {
		case "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64":
		default:
			return false
		}
		if seen[platform] {
			return false
		}
		seen[platform] = true
	}
	return true
}

func olderVersion(a, b string) bool {
	if !ValidReleaseVersion(b) {
		return false
	}
	b, _, _ = strings.Cut(b, "-rc.")
	x, y := formalVersion.FindStringSubmatch(a), formalVersion.FindStringSubmatch(b)
	if x == nil || y == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		p, errP := strconv.Atoi(x[i])
		q, errQ := strconv.Atoi(y[i])
		if errP != nil || errQ != nil {
			return false
		}
		if p != q {
			return p < q
		}
	}
	return false
}
