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
	if capabilities.SupportsSafeStart() || capabilities.SupportsSafeSend() {
		t.Fatal("synthetic evidence was treated as live Provider capability")
	}
	for i := range capabilities.Evidence {
		capabilities.Evidence[i].EvidenceLevel = EvidenceLive
	}
	if err := capabilities.ValidateForLiveExecution(identity); err != nil {
		t.Fatalf("ValidateForLiveExecution: %v", err)
	}
	if !capabilities.SupportsSafeStart() || !capabilities.SupportsSafeSend() {
		t.Fatal("live idempotency / lookup capability was not recognized")
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

func TestLiveExecutionRejectsSDKOnlyAndSyntheticCapabilities(t *testing.T) {
	identity := testIdentity()
	capabilities := Capabilities{
		Identity: identity, StartIdempotency: true,
		Evidence: []CapabilityEvidence{evidence(identity, CapabilityStartIdempotency)},
	}
	capabilities.Evidence[0].EvidenceLevel = EvidenceSDK
	if err := capabilities.ValidateForLiveExecution(identity); err == nil {
		t.Fatal("SDK-only evidence was accepted for live execution")
	}
	capabilities.Evidence[0].EvidenceLevel = EvidenceLive
	if err := capabilities.ValidateForLiveExecution(identity); err != nil {
		t.Fatalf("live evidence was rejected: %v", err)
	}
}

func TestCrossProcessReattachmentRequiresDurableEvidence(t *testing.T) {
	identity := testIdentity()
	item := evidence(identity, CapabilitySessionReattach)
	item.EvidenceLevel = EvidenceLive
	capabilities := Capabilities{Identity: identity, CrossProcessSessionReattach: true, Evidence: []CapabilityEvidence{item}}
	if err := capabilities.ValidateForLiveExecution(identity); err == nil {
		t.Fatal("live-only evidence was accepted for durable reattachment")
	}
	capabilities.Evidence[0].EvidenceLevel = EvidenceDurable
	if err := capabilities.ValidateForLiveExecution(identity); err != nil {
		t.Fatalf("durable evidence was rejected: %v", err)
	}
	if !capabilities.SupportsCrossProcessReattachment() {
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
	}
	if err := turn.Validate(); err != nil {
		t.Fatalf("valid Turn request: %v", err)
	}
	turn.Prompt = "changed prompt"
	if err := turn.Validate(); err == nil {
		t.Fatal("Turn request accepted prompt content that differs from its immutable digest")
	}
}
