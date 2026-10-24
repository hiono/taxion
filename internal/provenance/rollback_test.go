package provenance

import "testing"

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
