package provider

import (
	"strings"
	"testing"
	"time"
)

func testIdentity() Identity {
	return Identity{
		ProviderID: "provider.test", AccountID: "account.primary",
		SDKVersion: "test-sdk/1", ConfigurationSHA256: strings.Repeat("a", 64),
	}
}

func evidence(identity Identity, capability Capability) CapabilityEvidence {
	return CapabilityEvidence{
		Capability: capability, Identity: identity, TestID: "adapter-contract",
		EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: EvidenceSynthetic,
		VerifiedAt: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
	}
}

func TestCapabilitiesRequireIdentityBoundEvidence(t *testing.T) {
	identity := testIdentity()
	capabilities := Capabilities{
		Identity: identity, StartIdempotency: true, SendLookup: true,
		Evidence: []CapabilityEvidence{
			evidence(identity, CapabilityStartIdempotency),
			evidence(identity, CapabilitySendLookup),
		},
	}
	if err := capabilities.Validate(identity); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !capabilities.SupportsSafeStart() || !capabilities.SupportsSafeSend() {
		t.Fatal("idempotency / lookup capability was not recognized")
	}
}

func TestCapabilitiesRejectMissingOrMismatchedEvidence(t *testing.T) {
	identity := testIdentity()
	missing := Capabilities{Identity: identity, StartLookup: true}
	if err := missing.Validate(identity); err == nil {
		t.Fatal("enabled capability without evidence was accepted")
	}

	other := identity
	other.ConfigurationSHA256 = strings.Repeat("c", 64)
	mismatched := Capabilities{
		Identity: identity, StartIdempotency: true,
		Evidence: []CapabilityEvidence{evidence(other, CapabilityStartIdempotency)},
	}
	if err := mismatched.Validate(identity); err == nil {
		t.Fatal("evidence from another configuration was accepted")
	}
}

func TestCapabilitiesRejectUnverifiableAndDuplicateEvidence(t *testing.T) {
	identity := testIdentity()
	unverifiable := evidence(identity, CapabilityStartIdempotency)
	unverifiable.EvidenceLevel = "E1"
	if err := (Capabilities{Identity: identity, StartIdempotency: true, Evidence: []CapabilityEvidence{unverifiable}}).Validate(identity); err == nil {
		t.Fatal("E1 evidence was accepted as Provider capability verification")
	}

	item := evidence(identity, CapabilityStartIdempotency)
	duplicate := Capabilities{
		Identity: identity, StartIdempotency: true,
		Evidence: []CapabilityEvidence{item, item},
	}
	if err := duplicate.Validate(identity); err == nil {
		t.Fatal("duplicate capability evidence was accepted")
	}
}

func TestCapabilitiesDoNotInferUnsupportedOperations(t *testing.T) {
	identity := testIdentity()
	capabilities := Capabilities{Identity: identity}
	if err := capabilities.Validate(identity); err != nil {
		t.Fatalf("Validate empty capabilities: %v", err)
	}
	if capabilities.SupportsSafeStart() || capabilities.SupportsSafeSend() ||
		capabilities.Has(CapabilitySessionReattach) || capabilities.Has(CapabilityStopProof) {
		t.Fatal("unsupported capability was inferred")
	}
}
