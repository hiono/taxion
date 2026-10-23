package provenance

import "testing"

func TestPublishedRollbackPlatformPolicies(t *testing.T) {
	all := []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64"}
	if !validRollbackPolicy("v0.7.9", all) || !validRollbackPolicy("v0.7.8", []string{"linux-amd64"}) {
		t.Fatal("published policy refused")
	}
	for _, p := range all[1:] {
		if validRollbackPolicy("v0.7.8", []string{p}) {
			t.Fatal("nonexistent historical artifact accepted", p)
		}
	}
	if validRollbackPolicy("v0.7.8", all) || validRollbackPolicy("v0.7.9", all[:5]) ||
		validRollbackPolicy("local", all) || validRollbackPolicy("v0.7.9", []string{"linux-amd64", "linux-amd64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64"}) {
		t.Fatal("unsupported rollback policy accepted")
	}
}
