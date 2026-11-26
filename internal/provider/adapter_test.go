package provider

import (
	"context"
	"errors"
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

type testEvidenceRegistry struct {
	items map[Capability]CapabilityEvidence
	err   error
}

func (r testEvidenceRegistry) Find(_ context.Context, _ Identity, capability Capability) (CapabilityEvidence, bool, error) {
	if r.err != nil {
		return CapabilityEvidence{}, false, r.err
	}
	item, ok := r.items[capability]
	return item, ok, nil
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
		Identity: identity, StartIdempotency: true, SendLookup: true, ReadOnlyContextEnforcement: true,
		Evidence: []CapabilityEvidence{
			evidence(identity, CapabilityStartIdempotency, EvidenceSynthetic),
			evidence(identity, CapabilitySendLookup, EvidenceSynthetic),
			evidence(identity, CapabilityReadOnlyContextEnforcement, EvidenceSynthetic),
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
	if !verified.Has(CapabilityStartIdempotency) || !verified.Has(CapabilitySendLookup) || !verified.Has(CapabilityReadOnlyContextEnforcement) {
		t.Fatal("trusted E2 test verifier did not preserve declared synthetic capabilities")
	}
	if verified.HasEvidenceAtLeast(CapabilityStartIdempotency, EvidenceLive) || verified.HasEvidenceAtLeast(CapabilityReadOnlyContextEnforcement, EvidenceLive) {
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

func TestRegistryCapabilityVerifierRequiresExactTrustedEvidence(t *testing.T) {
	identity := testIdentity()
	item := evidence(identity, CapabilityStartIdempotency, EvidenceSynthetic)
	verifier, err := NewRegistryCapabilityVerifier(testEvidenceRegistry{
		items: map[Capability]CapabilityEvidence{CapabilityStartIdempotency: item},
	})
	if err != nil {
		t.Fatalf("NewRegistryCapabilityVerifier: %v", err)
	}
	accepted, err := verifier.Verify(context.Background(), identity, item)
	if err != nil || !accepted {
		t.Fatalf("trusted evidence was not accepted: accepted=%v err=%v", accepted, err)
	}
	sameInstant := item
	sameInstant.VerifiedAt = item.VerifiedAt.In(time.FixedZone("equivalent-zero-offset", 0))
	accepted, err = verifier.Verify(context.Background(), identity, sameInstant)
	if err != nil || !accepted {
		t.Fatalf("equivalent evidence timestamp was rejected: accepted=%v err=%v", accepted, err)
	}
	tampered := item
	tampered.EvidenceSHA256 = strings.Repeat("c", 64)
	accepted, err = verifier.Verify(context.Background(), identity, tampered)
	if err != nil || accepted {
		t.Fatalf("tampered evidence was accepted: accepted=%v err=%v", accepted, err)
	}
	tampered = item
	tampered.Identity.SDKVersion = "other-sdk/2"
	accepted, err = verifier.Verify(context.Background(), identity, tampered)
	if err != nil || accepted {
		t.Fatalf("evidence from another SDK version was accepted: accepted=%v err=%v", accepted, err)
	}
}

func TestRegistryCapabilityVerifierFailsClosedWhenCatalogUnavailable(t *testing.T) {
	identity := testIdentity()
	item := evidence(identity, CapabilityStartIdempotency, EvidenceSynthetic)
	verifier, err := NewRegistryCapabilityVerifier(testEvidenceRegistry{err: errors.New("catalog unavailable")})
	if err != nil {
		t.Fatalf("NewRegistryCapabilityVerifier: %v", err)
	}
	if _, err := verifier.Verify(context.Background(), identity, item); err == nil {
		t.Fatal("unavailable evidence catalog was treated as verified")
	}
	if _, err := NewRegistryCapabilityVerifier(nil); err == nil {
		t.Fatal("nil evidence registry was accepted")
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
		PromptSHA256: sha256Hex(prompt), Prompt: prompt,
		ReadOnlyBoundary: ReadOnlyContextBoundary{Mode: "snapshot", Reference: "snapshot_123", SHA256: sha256Hex("authorized snapshot")},
		MaxTokens:        500, Deadline: time.Now().UTC().Add(time.Hour),
	}
	if err := turn.Validate(); err != nil {
		t.Fatalf("valid Turn request: %v", err)
	}
	turn.Prompt = "changed prompt"
	if err := turn.Validate(); err == nil {
		t.Fatal("Turn request accepted prompt content that differs from its immutable digest")
	}
}

func TestTurnLookupRequiresResultDigestForCompletedButNotFailed(t *testing.T) {
	request := LookupRequest{
		OperationID: "operation_turn_123",
		SessionID:   "session_turn_123",
		TurnID:      "turn_123",
	}
	failed := LookupResult{
		Found: true, OperationID: request.OperationID, Status: "FAILED",
		ReceiptID: "receipt_turn_123", SessionID: request.SessionID, TurnID: request.TurnID,
		UsageID: "usage_turn_123", EvidenceSHA256: strings.Repeat("a", 64),
	}
	if err := failed.Validate(request); err != nil {
		t.Fatalf("FAILED Turn Lookup without result digest: %v", err)
	}
	completed := failed
	completed.Status = "COMPLETED"
	if err := completed.Validate(request); err == nil {
		t.Fatal("COMPLETED Turn Lookup without result digest was accepted")
	}
	completed.ResultSHA256 = strings.Repeat("b", 64)
	if err := completed.Validate(request); err != nil {
		t.Fatalf("COMPLETED Turn Lookup with result digest: %v", err)
	}
}

func TestTurnRequestValidatesReadOnlyContextBoundary(t *testing.T) {
	prompt := "Answer using the authorized context only"
	request := TurnRequest{
		OperationID: "provider_turn_boundary", TurnID: "planning_turn_boundary",
		SessionID: "session_boundary", PromptSHA256: sha256Hex(prompt), Prompt: prompt,
		ReadOnlyBoundary: ReadOnlyContextBoundary{
			Mode: "snapshot", Reference: "snapshot_boundary", SHA256: strings.Repeat("c", 64),
		},
		MaxTokens: 100, Deadline: time.Now().UTC().Add(time.Hour),
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid read-only boundary: %v", err)
	}
	request.ReadOnlyBoundary.Mode = "write"
	if err := request.Validate(); err == nil {
		t.Fatal("Turn request accepted a non-read-only boundary mode")
	}
}

func TestLookupResultDigestRequirementIsExplicitAndScoped(t *testing.T) {
	request := LookupRequest{OperationID: "operation_1", SessionID: "session_1", TurnID: "turn_1"}
	result := LookupResult{
		Found: true, OperationID: request.OperationID, Status: "COMPLETED",
		ReceiptID: "receipt_1", SessionID: request.SessionID, TurnID: request.TurnID,
		EvidenceSHA256: strings.Repeat("a", 64),
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("legacy completed lookup without digest was rejected: %v", err)
	}
	request.RequireResultDigest = true
	if err := result.Validate(request); err == nil {
		t.Fatal("question-turn completed lookup without digest was accepted")
	}
	result.ResultSHA256 = strings.Repeat("b", 64)
	if err := result.Validate(request); err != nil {
		t.Fatalf("question-turn completed lookup with digest was rejected: %v", err)
	}
	if err := (LookupRequest{OperationID: "operation_1", RequireResultDigest: true}).Validate(); err == nil {
		t.Fatal("digest requirement without a TurnID was accepted")
	}
}
