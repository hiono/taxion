package provider

import (
	"context"
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

func evidence(identity Identity, capability Capability, level EvidenceLevel) CapabilityEvidence {
	return CapabilityEvidence{
		Capability: capability, Identity: identity, TestID: "adapter-contract",
		EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: level,
		VerifiedAt: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
	}
}

type syntheticCapabilityVerifier struct{}

func (syntheticCapabilityVerifier) Verify(_ context.Context, identity Identity, item CapabilityEvidence) (bool, error) {
	return item.Identity == identity && item.TestID != "" &&
		item.EvidenceLevel == EvidenceSynthetic, nil
}

type minimumCapabilityVerifier struct {
	minimum map[Capability]EvidenceLevel
}

func (v minimumCapabilityVerifier) Verify(_ context.Context, identity Identity, item CapabilityEvidence) (bool, error) {
	minimum, ok := v.minimum[item.Capability]
	if !ok || item.Identity != identity {
		return false, nil
	}
	return evidenceRank(item.EvidenceLevel) >= evidenceRank(minimum), nil
}

func TestCapabilitiesRequireIdentityBoundEvidence(t *testing.T) {
	identity := testIdentity()
	capabilities := Capabilities{
		Identity: identity, StartIdempotency: true, SendLookup: true,
		Evidence: []CapabilityEvidence{
			evidence(identity, CapabilityStartIdempotency, EvidenceSynthetic),
			evidence(identity, CapabilitySendLookup, EvidenceSynthetic),
		},
	}
	if err := capabilities.Validate(identity); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if capabilities.SupportsSafeStart() || capabilities.SupportsSafeSend() {
		t.Fatal("synthetic evidence was treated as live Provider capability")
	}
	verified, err := capabilities.Verified(context.Background(), identity, syntheticCapabilityVerifier{})
	if err != nil {
		t.Fatalf("Verified: %v", err)
	}
	if !verified.Has(CapabilityStartIdempotency) || !verified.Has(CapabilitySendLookup) {
		t.Fatal("trusted E2 test verifier did not preserve declared synthetic capabilities")
	}
	if verified.HasEvidenceAtLeast(CapabilityStartIdempotency, EvidenceLive) {
		t.Fatal("synthetic evidence was promoted to live capability")
	}
}

func TestCapabilityVerificationIsPerCapability(t *testing.T) {
	identity := testIdentity()
	capabilities := Capabilities{
		Identity: identity, StartIdempotency: true, EventReplay: true,
		Evidence: []CapabilityEvidence{
			evidence(identity, CapabilityStartIdempotency, EvidenceLive),
			evidence(identity, CapabilityEventReplay, EvidenceSynthetic),
		},
	}
	verified, err := capabilities.Verified(context.Background(), identity, minimumCapabilityVerifier{
		minimum: map[Capability]EvidenceLevel{
			CapabilityStartIdempotency: EvidenceLive,
			CapabilityEventReplay:      EvidenceDurable,
		},
	})
	if err != nil {
		t.Fatalf("Verified: %v", err)
	}
	if !verified.HasEvidenceAtLeast(CapabilityStartIdempotency, EvidenceLive) {
		t.Fatal("verified Start capability was disabled by unrelated EventReplay evidence")
	}
	if verified.Has(CapabilityEventReplay) {
		t.Fatal("unverified EventReplay capability remained enabled")
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
		Evidence: []CapabilityEvidence{evidence(other, CapabilityStartIdempotency, EvidenceSynthetic)},
	}
	if err := mismatched.Validate(identity); err == nil {
		t.Fatal("evidence from another configuration was accepted")
	}
}

func TestCapabilitiesRejectUnverifiableAndDuplicateEvidence(t *testing.T) {
	identity := testIdentity()
	unverifiable := evidence(identity, CapabilityStartIdempotency, "E1")
	if err := (Capabilities{Identity: identity, StartIdempotency: true, Evidence: []CapabilityEvidence{unverifiable}}).Validate(identity); err == nil {
		t.Fatal("E1 evidence was accepted as Provider capability verification")
	}

	item := evidence(identity, CapabilityStartIdempotency, EvidenceSynthetic)
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

func TestCrossProcessReattachmentRequiresDurableEvidence(t *testing.T) {
	identity := testIdentity()
	item := evidence(identity, CapabilitySessionReattach, EvidenceLive)
	capabilities := Capabilities{Identity: identity, CrossProcessSessionReattach: true, Evidence: []CapabilityEvidence{item}}
	if capabilities.HasEvidenceAtLeast(CapabilitySessionReattach, EvidenceDurable) {
		t.Fatal("live-only evidence was accepted for durable reattachment")
	}
	capabilities.Evidence[0].EvidenceLevel = EvidenceDurable
	if !capabilities.HasEvidenceAtLeast(CapabilitySessionReattach, EvidenceDurable) {
		t.Fatal("durable reattachment capability was not recognized")
	}
}

func TestOperationErrorNeverIncludesUnconstrainedMessage(t *testing.T) {
	err, createErr := NewOperationError(FailureAmbiguous, OperationCodeTimeout)
	if createErr != nil {
		t.Fatalf("NewOperationError: %v", createErr)
	}
	if err.Error() != "Provider operation failed (timeout)" {
		t.Fatalf("safe operation error = %q", err.Error())
	}
	unsafe := &OperationError{Class: FailureAmbiguous, Code: "sk-secret-token"}
	if strings.Contains(unsafe.Error(), "sk-secret-token") {
		t.Fatal("unsafe operation code leaked through Error")
	}
	if _, createErr := NewOperationError(FailureAmbiguous, "Bearer-secret-token"); createErr == nil {
		t.Fatal("unsafe operation error code was accepted")
	}
}

func TestStartAndTurnRequestsValidateImmutablePromptAndContextDigests(t *testing.T) {
	prompt := "Create a bounded plan"
	contextContent := "read-only source"
	document := ContextDocument{ID: "docs/README.md", SHA256: sha256Hex(contextContent), Content: contextContent}
	start := StartRequest{
		OperationID: "provider_start_123", JobID: "job_123", RoleExecutionID: "role_exec_123",
		RequestSHA256: strings.Repeat("a", 64), PromptSHA256: sha256Hex(prompt), Prompt: prompt,
		Context: []ContextDocument{document}, MaxTurns: 3, MaxTokens: 1000,
		Deadline: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC),
	}
	if err := start.Validate(); err != nil {
		t.Fatalf("valid Start request: %v", err)
	}
	start.Context[0].Content = "tampered source"
	if err := start.Validate(); err == nil {
		t.Fatal("Start request accepted context content that differs from its immutable digest")
	}

	start.Context[0] = document
	start.Context = append(start.Context, document)
	if err := start.Validate(); err == nil {
		t.Fatal("Start request accepted duplicate context document identity")
	}

	turn := TurnRequest{
		OperationID: "provider_turn_123", TurnID: "planning_turn_123", SessionID: "session_123",
		PromptSHA256: sha256Hex(prompt), Prompt: prompt, MaxTokens: 500,
		Deadline: time.Now().UTC().Add(time.Hour),
	}
	if err := turn.Validate(); err != nil {
		t.Fatalf("valid Turn request: %v", err)
	}
	turn.Prompt = "changed prompt"
	if err := turn.Validate(); err == nil {
		t.Fatal("Turn request accepted prompt content that differs from its immutable digest")
	}
}
