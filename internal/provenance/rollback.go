package provenance

// Historical releases retain their original policy. Only published platform
// combinations are valid; no source-build or cross-platform fallback exists.
func validRollbackPolicy(version string, platforms []string) bool {
	if version == "v0.7.8" {
		return len(platforms) == 1 && platforms[0] == "linux-amd64"
	}
	if version != "v0.7.9" || len(platforms) != 6 {
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
