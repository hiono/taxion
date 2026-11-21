package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type testBroker struct {
	secret   []byte
	acquires int
	closed   int
}

func (b *testBroker) Acquire(_ context.Context, _ Identity) (CredentialLease, error) {
	b.acquires++
	return &trackingLease{secret: append([]byte(nil), b.secret...), onClose: func() { b.closed++ }}, nil
}

type trackingLease struct {
	secret  []byte
	closed  bool
	onClose func()
}

func (l *trackingLease) WithSecret(use func([]byte) error) error {
	if l.closed {
		return ErrCredentialLeaseClosed
	}
	copy := append([]byte(nil), l.secret...)
	defer zero(copy)
	return use(copy)
}

func (l *trackingLease) Close() error {
	if !l.closed {
		zero(l.secret)
		l.closed = true
		l.onClose()
	}
	return nil
}

type testAdapter struct {
	identity     Identity
	capabilities Capabilities
	startCalls   int
	sendCalls    int
	stopCalls    int
	resumeCalls  int
	lookupCalls  int
	resultCalls  int
	usageCalls   int
	eventCalls   int
	startErr     error
}

func (a *testAdapter) Identity() Identity                                 { return a.identity }
func (a *testAdapter) Capabilities(context.Context) (Capabilities, error) { return a.capabilities, nil }
func (a *testAdapter) Start(_ context.Context, _ StartRequest, lease CredentialLease) (StartResult, error) {
	a.startCalls++
	if err := lease.WithSecret(func(secret []byte) error {
		if string(secret) != "runtime-test-secret" {
			return errors.New("credential mismatch")
		}
		return nil
	}); err != nil {
		return StartResult{}, err
	}
	return StartResult{SessionID: "session_1", ReceiptID: "receipt_1", EvidenceSHA256: strings.Repeat("a", 64)}, a.startErr
}
func (a *testAdapter) Send(context.Context, TurnRequest, CredentialLease) (TurnResult, error) {
	a.sendCalls++
	return TurnResult{ReceiptID: "receipt_2", Status: "COMPLETED", TokensUsed: 10, EvidenceSHA256: strings.Repeat("b", 64)}, nil
}
func (a *testAdapter) Stop(context.Context, StopRequest, CredentialLease) (StopResult, error) {
	a.stopCalls++
	return StopResult{Stopped: true, EvidenceSHA256: strings.Repeat("c", 64)}, nil
}
func (a *testAdapter) Resume(context.Context, ResumeRequest, CredentialLease) (ResumeResult, error) {
	a.resumeCalls++
	return ResumeResult{Attached: true, EvidenceSHA256: strings.Repeat("d", 64)}, nil
}
func (a *testAdapter) Lookup(context.Context, LookupRequest, CredentialLease) (LookupResult, error) {
	a.lookupCalls++
	return LookupResult{Found: true, Status: "COMPLETED", EvidenceSHA256: strings.Repeat("e", 64)}, nil
}
func (a *testAdapter) Result(context.Context, ResultRequest, CredentialLease) (Result, error) {
	a.resultCalls++
	return Result{Content: "result", ResultSHA256: strings.Repeat("f", 64), EvidenceSHA256: strings.Repeat("1", 64)}, nil
}
func (a *testAdapter) Usage(context.Context, UsageRequest, CredentialLease) (Usage, error) {
	a.usageCalls++
	return Usage{UsageID: "usage_1", Tokens: 10, EvidenceSHA256: strings.Repeat("2", 64)}, nil
}
func (a *testAdapter) ReplayEvents(_ context.Context, request ReplayEventsRequest, _ CredentialLease) (EventPage, error) {
	a.eventCalls++
	event := ProviderEvent{
		EventID: "event_1", SessionID: request.SessionID, OperationID: "provider_op_1",
		Kind: EventOperationAccepted, PreviousCursor: request.AfterCursor, Cursor: "cursor_1",
		ObservedAt: time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC),
		EvidenceSHA256: strings.Repeat("3", 64),
	}
	return EventPage{
		SessionID: request.SessionID, FromCursor: request.AfterCursor, NextCursor: event.Cursor,
		Events: []ProviderEvent{event}, EvidenceSHA256: strings.Repeat("4", 64),
	}, nil
}

func liveEvidence(identity Identity, capability Capability, level EvidenceLevel) CapabilityEvidence {
	return CapabilityEvidence{
		Capability: capability, Identity: identity, TestID: "runtime-test",
		EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: level,
		VerifiedAt: time.Now().UTC(),
	}
}

func newTestAdapter(capabilities ...Capability) *testAdapter {
	identity := testIdentity()
	declaration := Capabilities{Identity: identity}
	for _, capability := range capabilities {
		switch capability {
		case CapabilityStartIdempotency:
			declaration.StartIdempotency = true
		case CapabilityStartLookup:
			declaration.StartLookup = true
		case CapabilitySendIdempotency:
			declaration.SendIdempotency = true
		case CapabilitySendLookup:
			declaration.SendLookup = true
		case CapabilitySessionReattach:
			declaration.CrossProcessSessionReattach = true
		case CapabilityEventReplay:
			declaration.EventReplay = true
		case CapabilityStopProof:
			declaration.StopProof = true
		case CapabilityFinalOutput:
			declaration.FinalOutputRetrieval = true
		case CapabilityUsageIdentity:
			declaration.UsageIdentity = true
		case CapabilityUsageCompleteness:
			declaration.UsageCompleteness = true
		}
		level := EvidenceLive
		if capability == CapabilitySessionReattach || capability == CapabilityEventReplay {
			level = EvidenceDurable
		}
		declaration.Evidence = append(declaration.Evidence, liveEvidence(identity, capability, level))
	}
	return &testAdapter{identity: identity, capabilities: declaration}
}

func TestRuntimeStartRequiresLiveSafeCapabilityAndClosesCredential(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	result, err := runtime.Start(context.Background(), StartRequest{OperationID: "provider_op_1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.SessionID != "session_1" || adapter.startCalls != 1 || broker.acquires != 1 || broker.closed != 1 {
		t.Fatalf("Start result / calls: %#v adapter=%d acquired=%d closed=%d", result, adapter.startCalls, broker.acquires, broker.closed)
	}
}

func TestRuntimeRejectsSyntheticCapabilityBeforeCredentialAcquisition(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	adapter.capabilities.Evidence[0].EvidenceLevel = EvidenceSynthetic
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.Start(context.Background(), StartRequest{}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("Start error = %v", err)
	}
	if adapter.startCalls != 0 || broker.acquires != 0 {
		t.Fatalf("unsupported capability caused side effects: adapter=%d broker=%d", adapter.startCalls, broker.acquires)
	}
}

func TestRuntimeRequiresDurableEvidenceForResume(t *testing.T) {
	adapter := newTestAdapter(CapabilitySessionReattach)
	adapter.capabilities.Evidence[0].EvidenceLevel = EvidenceLive
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.Resume(context.Background(), ResumeRequest{}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("Resume error = %v", err)
	}
	if adapter.resumeCalls != 0 || broker.acquires != 0 {
		t.Fatalf("non-durable reattachment caused side effects: adapter=%d broker=%d", adapter.resumeCalls, broker.acquires)
	}
}

func TestRuntimeSanitizesAdapterErrors(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	adapter.startErr = errors.New("request failed with Bearer secret-value")
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	_, err = runtime.Start(context.Background(), StartRequest{})
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("Adapter error was not safely sanitized: %v", err)
	}
}

func TestRuntimeStopRequiresStopProof(t *testing.T) {
	adapter := newTestAdapter()
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.Stop(context.Background(), StopRequest{}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("Stop error = %v", err)
	}
	if adapter.stopCalls != 0 || broker.acquires != 0 {
		t.Fatalf("unverified stop caused side effects: adapter=%d broker=%d", adapter.stopCalls, broker.acquires)
	}
}

func TestRuntimeEventReplayRequiresDurableCapabilityAndValidCursorChain(t *testing.T) {
	adapter := newTestAdapter(CapabilityEventReplay)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	page, err := runtime.ReplayEvents(context.Background(), ReplayEventsRequest{SessionID: "session_1"})
	if err != nil {
		t.Fatalf("ReplayEvents: %v", err)
	}
	if len(page.Events) != 1 || page.NextCursor != "cursor_1" || adapter.eventCalls != 1 || broker.acquires != 1 || broker.closed != 1 {
		t.Fatalf("ReplayEvents result / calls: %#v adapter=%d acquired=%d closed=%d", page, adapter.eventCalls, broker.acquires, broker.closed)
	}

	broken := page
	broken.Events = append([]ProviderEvent(nil), page.Events...)
	broken.Events[0].PreviousCursor = "cursor_gap"
	if err := broken.Validate(ReplayEventsRequest{SessionID: "session_1"}); err == nil {
		t.Fatal("event page with a cursor gap was accepted")
	}
}

func TestRuntimeRejectsEventReplayWithoutDurableEvidenceBeforeCredentialAcquisition(t *testing.T) {
	adapter := newTestAdapter(CapabilityEventReplay)
	adapter.capabilities.Evidence[0].EvidenceLevel = EvidenceLive
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.ReplayEvents(context.Background(), ReplayEventsRequest{SessionID: "session_1"}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("ReplayEvents error = %v", err)
	}
	if adapter.eventCalls != 0 || broker.acquires != 0 {
		t.Fatalf("unverified event replay caused side effects: adapter=%d broker=%d", adapter.eventCalls, broker.acquires)
	}
}
