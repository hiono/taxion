package provenance

import "testing"

func TestExplicitUnavailableRollbackProvenance(t *testing.T) {
	policy, identity, manifest := releaseFixture(t, "v0.7.15")
	manifest.SchemaVersion = 3
	manifest.RollbackVersion = ""
	manifest.RollbackPlatforms = []string{}
	manifest.RollbackUnavailableReason = "prior-distributions-withdrawn"
	policy.ManifestSHA256 = Hash(writeJSON(t, policy.ManifestPath, manifest))
	report, err := Verify(policy, policy.StoreDirectory, identity)
	if err != nil || report.RollbackUnavailableReason != manifest.RollbackUnavailableReason {
		t.Fatal(report, err)
	}
	for _, name := range []string{"legacy-schema", "unknown-schema", "missing-reason", "unknown-reason",
		"target-present", "platform-present", "platform-missing", "historical-version", "future-version", "tampered-pin"} {
		t.Run(name, func(t *testing.T) {
			candidate, pin := manifest, policy
			switch name {
			case "legacy-schema":
				candidate.SchemaVersion = 2
			case "unknown-schema":
				candidate.SchemaVersion = 4
			case "missing-reason":
				candidate.RollbackUnavailableReason = ""
			case "unknown-reason":
				candidate.RollbackUnavailableReason = "network-error"
			case "target-present":
				candidate.RollbackVersion = "v0.7.14"
			case "platform-present":
				candidate.RollbackPlatforms = []string{"linux-amd64"}
			case "platform-missing":
				candidate.RollbackPlatforms = nil
			case "historical-version", "future-version":
				candidate.Version = "v0.7.14"
				if name == "future-version" {
					candidate.Version = "v0.7.16"
				}
				if validManifestRollback(candidate) {
					t.Fatal("unavailable policy accepted for another release")
				}
			}
			pin.ManifestSHA256 = Hash(writeJSON(t, pin.ManifestPath, candidate))
			if name == "tampered-pin" {
				pin.ManifestSHA256 = Hash([]byte("different approval"))
			}
			if _, err := Verify(pin, pin.StoreDirectory, identity); err == nil {
				t.Fatal("invalid unavailable rollback accepted")
			}
		})
	}
}

func TestPublishedRollbackPlatformPolicies(t *testing.T) {
	all := []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64"}
	if !validRollbackPolicy("v0.7.9", "v0.7.10", all) || !validRollbackPolicy("v0.7.8", "v0.7.9", []string{"linux-amd64"}) ||
		!validRollbackPolicy("v0.7.11", "v0.7.13", all) || !validRollbackPolicy("v0.7.12", "v0.8.0", all) {
		t.Fatal("published policy refused")
	}
	for _, p := range all[1:] {
		if validRollbackPolicy("v0.7.8", "v0.7.9", []string{p}) {
			t.Fatal("nonexistent historical artifact accepted", p)
		}
	}
	if validRollbackPolicy("v0.7.8", "v0.7.9", all) || validRollbackPolicy("v0.7.9", "v0.7.10", all[:5]) ||
		validRollbackPolicy("local", "v0.7.10", all) || validRollbackPolicy("v0.7.9", "v0.7.10", []string{"linux-amd64", "linux-amd64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64"}) ||
		validRollbackPolicy("v0.7.7", "v0.7.10", all) || validRollbackPolicy("v0.7.12-rc.1", "v0.7.13", all) ||
		validRollbackPolicy("v0.07.11", "v0.7.13", all) || validRollbackPolicy("v0.7.11", "v0.7.1234567890", all) {
		t.Fatal("unsupported rollback policy accepted")
	}
	for _, current := range []string{"v0.7.11", "v0.7.10", "local", "v0.7.11-rc.1"} {
		if validRollbackPolicy("v0.7.11", current, all) {
			t.Fatal("rollback not older than current accepted", current)
		}
	}
}
