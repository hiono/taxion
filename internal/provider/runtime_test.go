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

type failingAcquireBroker struct {
	lease CredentialLease
}

func (b failingAcquireBroker) Acquire(context.Context, Identity) (CredentialLease, error) {
	return b.lease, errors.New("credential broker unavailable")
}

type failingCloseLease struct {
	closeCalls int
}

func (l *failingCloseLease) WithSecret(use func([]byte) error) error {
	return use([]byte("temporary"))
}

func (l *failingCloseLease) Close() error {
	l.closeCalls++
	return errors.New("raw lease cleanup detail")
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
	identity       Identity
	capabilities   Capabilities
	startCalls     int
	sendCalls      int
	stopCalls      int
	resumeCalls    int
	lookupCalls    int
	resultCalls    int
	usageCalls     int
	eventCalls     int
	startErr       error
	startPanic     bool
	stopQuiescent  bool
	resultFinal    bool
	usageComplete  bool
	startSessionID string
	startDeadline  time.Time
	sendOutput     string
	sendResult     TurnResult
	resultContent  string
	lookupResult   LookupResult
}

func (a *testAdapter) Identity() Identity { return a.identity }
func (a *testAdapter) Capabilities(context.Context) (Capabilities, error) {
	return a.capabilities.Clone(), nil
}
func (a *testAdapter) Start(ctx context.Context, request StartRequest, lease CredentialLease) (StartResult, error) {
	a.startCalls++
	a.startDeadline, _ = ctx.Deadline()
	if err := lease.WithSecret(func(secret []byte) error {
		if string(secret) != "runtime-test-secret" {
			return errors.New("credential mismatch")
		}
		return nil
	}); err != nil {
		return StartResult{}, err
	}
	if a.startPanic {
		panic("raw Provider panic containing runtime-test-secret")
	}
	sessionID := a.startSessionID
	if sessionID == "" {
		sessionID = "session_1"
	}
	return StartResult{OperationID: request.OperationID, SessionID: sessionID, ReceiptID: "receipt_1", EvidenceSHA256: strings.Repeat("a", 64)}, a.startErr
}
func (a *testAdapter) Send(_ context.Context, request TurnRequest, _ CredentialLease) (TurnResult, error) {
	a.sendCalls++
	if a.sendResult.Status != "" {
		result := a.sendResult
		result.OperationID = request.OperationID
		result.TurnID = request.TurnID
		result.SessionID = request.SessionID
		return result, nil
	}
	return TurnResult{
		OperationID: request.OperationID, TurnID: request.TurnID, SessionID: request.SessionID,
		ReceiptID: "receipt_2", Status: "COMPLETED", TokensUsed: 10,
		Output: a.sendOutput, EvidenceSHA256: strings.Repeat("b", 64),
	}, nil
}
func (a *testAdapter) Stop(_ context.Context, request StopRequest, _ CredentialLease) (StopResult, error) {
	a.stopCalls++
	return StopResult{OperationID: request.OperationID, SessionID: request.SessionID, Stopped: true, Quiescent: a.stopQuiescent, EvidenceSHA256: strings.Repeat("c", 64)}, nil
}
func (a *testAdapter) Resume(_ context.Context, request ResumeRequest, _ CredentialLease) (ResumeResult, error) {
	a.resumeCalls++
	return ResumeResult{OperationID: request.OperationID, SessionID: request.SessionID, Attached: true, EvidenceSHA256: strings.Repeat("d", 64)}, nil
}
func (a *testAdapter) Lookup(_ context.Context, request LookupRequest, _ CredentialLease) (LookupResult, error) {
	a.lookupCalls++
	if a.lookupResult.Status != "" {
		result := a.lookupResult
		result.OperationID = request.OperationID
		if request.SessionID != "" {
			result.SessionID = request.SessionID
		}
		if request.TurnID != "" {
			result.TurnID = request.TurnID
		}
		return result, nil
	}
	sessionID := request.SessionID
	if sessionID == "" {
		sessionID = "session_1"
	}
	return LookupResult{
		Found: true, OperationID: request.OperationID, Status: "COMPLETED", ReceiptID: "receipt_lookup",
		SessionID: sessionID, TurnID: request.TurnID, EvidenceSHA256: strings.Repeat("e", 64),
	}, nil
}
func (a *testAdapter) Result(_ context.Context, request ResultRequest, _ CredentialLease) (Result, error) {
	a.resultCalls++
	content := a.resultContent
	if content == "" {
		content = "result"
	}
	return Result{
		OperationID: request.OperationID, SessionID: request.SessionID, TurnID: request.TurnID,
		Content: content, ResultSHA256: sha256Hex(content), Final: a.resultFinal, EvidenceSHA256: strings.Repeat("1", 64),
	}, nil
}
func (a *testAdapter) Usage(_ context.Context, request UsageRequest, _ CredentialLease) (Usage, error) {
	a.usageCalls++
	return Usage{
		OperationID: request.OperationID, SessionID: request.SessionID, TurnID: request.TurnID,
		UsageID: "usage_1", Tokens: 10, Complete: a.usageComplete, EvidenceSHA256: strings.Repeat("2", 64),
	}, nil
}
func (a *testAdapter) ReplayEvents(_ context.Context, request ReplayEventsRequest, _ CredentialLease) (EventPage, error) {
	a.eventCalls++
	event := ProviderEvent{
		EventID: "event_1", SessionID: request.SessionID, OperationID: "provider_op_1",
		Kind: EventOperationAccepted, PreviousCursor: request.AfterCursor, Cursor: "cursor_1",
		ObservedAt:     time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC),
		EvidenceSHA256: strings.Repeat("3", 64),
	}
	return EventPage{
		SessionID: request.SessionID, FromCursor: request.AfterCursor, NextCursor: event.Cursor,
		Events: []ProviderEvent{event}, EvidenceSHA256: strings.Repeat("4", 64),
	}, nil
}

func liveEvidence(identity Identity, capability Capability) CapabilityEvidence {
	return CapabilityEvidence{
		Capability: capability, Identity: identity, TestID: "runtime-test",
		EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: EvidenceSynthetic,
		VerifiedAt: time.Now().UTC(),
	}
}

type runtimeTestVerifier struct{}

func (runtimeTestVerifier) Verify(_ context.Context, identity Identity, evidence CapabilityEvidence) (bool, error) {
	return evidence.Identity == identity && evidence.TestID == "runtime-test" &&
		evidence.EvidenceLevel == EvidenceSynthetic, nil
}

func newTestRuntime(adapter Adapter, broker CredentialBroker) (*Runtime, error) {
	return newRuntimeWithEvidence(adapter, broker, runtimeTestVerifier{}, EvidenceSynthetic, EvidenceSynthetic)
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
		declaration.Evidence = append(declaration.Evidence, liveEvidence(identity, capability))
	}
	return &testAdapter{
		identity: identity, capabilities: declaration,
		stopQuiescent: true, resultFinal: true, usageComplete: true,
	}
}

func validStartRequest() StartRequest {
	prompt := "Create a safe plan"
	return StartRequest{
		OperationID: "provider_op_1", JobID: "job_1", RoleExecutionID: "role_1",
		RequestSHA256: sha256Hex("request"), PromptSHA256: sha256Hex(prompt),
		Prompt: prompt, MaxTurns: 3, MaxTokens: 100, Deadline: time.Now().UTC().Add(time.Hour),
	}
}

func validTurnRequest() TurnRequest {
	prompt := "Continue planning"
	return TurnRequest{
		OperationID: "provider_turn_1", TurnID: "turn_1", SessionID: "session_1",
		PromptSHA256: sha256Hex(prompt), Prompt: prompt, MaxTokens: 20,
		Deadline: time.Now().UTC().Add(time.Hour),
	}
}

func validStopRequest() StopRequest {
	return StopRequest{OperationID: "provider_stop_1", SessionID: "session_1"}
}

func validResumeRequest() ResumeRequest {
	return ResumeRequest{OperationID: "provider_resume_1", SessionID: "session_1", Deadline: time.Now().UTC().Add(time.Hour)}
}

func validResultRequest() ResultRequest {
	return ResultRequest{OperationID: "provider_turn_1", SessionID: "session_1", TurnID: "turn_1"}
}

func TestRuntimeRejectsExpiredStartDeadlineBeforeCredentialAcquisition(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("newTestRuntime: %v", err)
	}
	request := validStartRequest()
	request.Deadline = time.Now().UTC().Add(-time.Second)
	if _, err := runtime.Start(context.Background(), request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired Start deadline error = %v", err)
	}
	if broker.acquires != 0 || adapter.startCalls != 0 {
		t.Fatalf("expired request acquired credential or called Adapter: acquires=%d starts=%d", broker.acquires, adapter.startCalls)
	}
}

func TestRuntimeUsesEarlierContextAndRequestDeadline(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("newTestRuntime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	contextDeadline, _ := ctx.Deadline()
	request := validStartRequest()
	request.Deadline = contextDeadline.Add(time.Hour)
	if _, err := runtime.Start(ctx, request); err != nil {
		t.Fatalf("Runtime.Start: %v", err)
	}
	if !adapter.startDeadline.Equal(contextDeadline) {
		t.Fatalf("Adapter deadline = %v, want earlier context deadline %v", adapter.startDeadline, contextDeadline)
	}
}

func TestRuntimeRejectsCanceledContextBeforeCredentialAcquisition(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("newTestRuntime: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Start(ctx, validStartRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Start context error = %v", err)
	}
	if broker.acquires != 0 || adapter.startCalls != 0 {
		t.Fatalf("canceled request acquired credential or called Adapter: acquires=%d starts=%d", broker.acquires, adapter.startCalls)
	}
}

func TestRuntimeStartUsesVerifiedCapabilityAndClosesCredential(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	result, err := runtime.Start(context.Background(), validStartRequest())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.SessionID != "session_1" || adapter.startCalls != 1 || broker.acquires != 1 || broker.closed != 1 {
		t.Fatalf("Start result / calls: %#v adapter=%d acquired=%d closed=%d", result, adapter.startCalls, broker.acquires, broker.closed)
	}
}

func TestRuntimeClosesCredentialAndReturnsUnknownWhenAdapterPanics(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	adapter.startPanic = true
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	_, err = runtime.Start(context.Background(), validStartRequest())
	var operationError *OperationError
	if !errors.As(err, &operationError) || operationError.Class != FailureAmbiguous ||
		strings.Contains(err.Error(), "runtime-test-secret") {
		t.Fatalf("Adapter panic was not sanitized as UNKNOWN: %v", err)
	}
	if broker.closed != 1 {
		t.Fatalf("credential lease was not closed after panic: closed=%d", broker.closed)
	}
}

func TestRuntimeRejectsSyntheticCapabilityBeforeCredentialAcquisition(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker, runtimeTestVerifier{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.Start(context.Background(), validStartRequest()); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("Start error = %v", err)
	}
	if adapter.startCalls != 0 || broker.acquires != 0 {
		t.Fatalf("unsupported capability caused side effects: adapter=%d broker=%d", adapter.startCalls, broker.acquires)
	}
}

func TestRuntimeRequiresDurableEvidenceForResume(t *testing.T) {
	adapter := newTestAdapter(CapabilitySessionReattach)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker, runtimeTestVerifier{})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.Resume(context.Background(), validResumeRequest()); !errors.Is(err, ErrUnsupportedCapability) {
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
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	_, err = runtime.Start(context.Background(), validStartRequest())
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("Adapter error was not safely sanitized: %v", err)
	}
}

func TestRuntimeStopRequiresStopProof(t *testing.T) {
	adapter := newTestAdapter()
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.Stop(context.Background(), validStopRequest()); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("Stop error = %v", err)
	}
	if adapter.stopCalls != 0 || broker.acquires != 0 {
		t.Fatalf("unverified stop caused side effects: adapter=%d broker=%d", adapter.stopCalls, broker.acquires)
	}
}

func TestRuntimeSyntheticEventReplayContract(t *testing.T) {
	adapter := newTestAdapter(CapabilityEventReplay)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
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
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := NewRuntime(adapter, broker, runtimeTestVerifier{})
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

func TestRuntimeSendPreservesUnknownAndRejectsCredentialBearingOutput(t *testing.T) {
	adapter := newTestAdapter(CapabilitySendIdempotency)
	adapter.sendResult = TurnResult{Status: "UNKNOWN"}
	adapter.sendOutput = "Provider echoed runtime-test-secret"
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	result, err := runtime.Send(context.Background(), validTurnRequest())
	if err != nil {
		t.Fatalf("Send UNKNOWN: %v", err)
	}
	if result.Status != "UNKNOWN" || result.ReceiptID != "" || result.EvidenceSHA256 != "" {
		t.Fatalf("UNKNOWN result was rewritten: %#v", result)
	}

	adapter.sendResult = TurnResult{}
	_, err = runtime.Send(context.Background(), validTurnRequest())
	var operationError *OperationError
	if !errors.As(err, &operationError) || operationError.Class != FailureAmbiguous ||
		strings.Contains(err.Error(), "runtime-test-secret") {
		t.Fatalf("credential-bearing Provider output was not rejected as UNKNOWN: %v", err)
	}
	if adapter.sendCalls != 2 || broker.closed != 2 {
		t.Fatalf("Send calls / credential lease: adapter=%d closed=%d", adapter.sendCalls, broker.closed)
	}
}

func TestRuntimeRejectsCredentialInFinalResultAsUnknown(t *testing.T) {
	adapter := newTestAdapter(CapabilityFinalOutput)
	adapter.resultContent = "final result included runtime-test-secret"
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	_, err = runtime.Result(context.Background(), validResultRequest())
	var operationError *OperationError
	if !errors.As(err, &operationError) || operationError.Class != FailureAmbiguous ||
		strings.Contains(err.Error(), "runtime-test-secret") {
		t.Fatalf("credential-bearing final result was not rejected as UNKNOWN: %v", err)
	}
}

func TestRuntimeRejectsCredentialInProviderSessionIdentity(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	adapter.startSessionID = "runtime-test-secret"
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	_, err = runtime.Start(context.Background(), validStartRequest())
	var operationError *OperationError
	if !errors.As(err, &operationError) || operationError.Class != FailureAmbiguous ||
		strings.Contains(err.Error(), "runtime-test-secret") {
		t.Fatalf("credential-bearing session identity was not rejected safely: %v", err)
	}
}

func TestRuntimeLookupRequiresEvidenceBackedNotFoundResult(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartLookup)
	adapter.lookupResult = LookupResult{
		Status: LookupStatusNotFound, EvidenceSHA256: strings.Repeat("e", 64),
	}
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	result, err := runtime.Lookup(context.Background(), LookupRequest{OperationID: "provider_start_1"})
	if err != nil {
		t.Fatalf("Lookup not found: %v", err)
	}
	if result.Found || result.Status != LookupStatusNotFound || adapter.lookupCalls != 1 || broker.closed != 1 {
		t.Fatalf("Lookup absence result / calls: %#v adapter=%d closed=%d", result, adapter.lookupCalls, broker.closed)
	}

	adapter.lookupResult.Status = "COMPLETED"
	_, err = runtime.Lookup(context.Background(), LookupRequest{OperationID: "provider_start_2"})
	var operationError *OperationError
	if !errors.As(err, &operationError) || operationError.Class != FailureAmbiguous {
		t.Fatalf("inconsistent Lookup absence was not treated as UNKNOWN: %v", err)
	}
}

func TestRuntimeCapabilitiesReturnsDetachedEvidenceSlice(t *testing.T) {
	adapter := newTestAdapter(CapabilityStartIdempotency)
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	capabilities.Evidence[0].EvidenceLevel = EvidenceSynthetic
	if _, err := runtime.Start(context.Background(), validStartRequest()); err != nil {
		t.Fatalf("mutating returned capability evidence changed runtime authorization: %v", err)
	}
}

func TestRuntimeDoesNotTreatStoppedWithoutQuiescenceAsConfirmed(t *testing.T) {
	adapter := newTestAdapter(CapabilityStopProof)
	adapter.stopQuiescent = false
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, err := runtime.Stop(context.Background(), validStopRequest()); !errors.Is(err, ErrStopUnconfirmed) {
		t.Fatalf("Stop without quiescence error = %v", err)
	}
	if adapter.stopCalls != 1 || broker.closed != 1 {
		t.Fatalf("Stop calls / lease close: calls=%d closed=%d", adapter.stopCalls, broker.closed)
	}
}

func TestRuntimeRejectsNonFinalResult(t *testing.T) {
	adapter := newTestAdapter(CapabilityFinalOutput)
	adapter.resultFinal = false
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	_, err = runtime.Result(context.Background(), validResultRequest())
	var operationError *OperationError
	if !errors.As(err, &operationError) || operationError.Class != FailureAmbiguous {
		t.Fatalf("non-final result was not treated as UNKNOWN: %v", err)
	}
}

func TestRuntimePreservesIncompleteUsageWithoutFinalizingIt(t *testing.T) {
	adapter := newTestAdapter(CapabilityUsageIdentity, CapabilityUsageCompleteness)
	adapter.usageComplete = false
	broker := &testBroker{secret: []byte("runtime-test-secret")}
	runtime, err := newTestRuntime(adapter, broker)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	usage, err := runtime.Usage(context.Background(), UsageRequest{OperationID: "provider_op_1", SessionID: "session_1"})
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if usage.Complete {
		t.Fatal("partial Usage was treated as complete")
	}
}

func TestWithCredentialReportsAcquireAndLeaseCleanupFailureWithoutCallingProvider(t *testing.T) {
	lease := &failingCloseLease{}
	called := false
	_, err := withCredential(context.Background(), failingAcquireBroker{lease: lease}, testIdentity(),
		func(CredentialLease) (struct{}, error) {
			called = true
			return struct{}{}, nil
		})
	if !errors.Is(err, ErrCredentialUnavailable) || !errors.Is(err, ErrCredentialLeaseCleanupFailed) {
		t.Fatalf("credential cleanup failure = %v", err)
	}
	if called || lease.closeCalls != 1 {
		t.Fatalf("Provider callback or lease cleanup count is wrong: called=%v closeCalls=%d", called, lease.closeCalls)
	}
	if strings.Contains(err.Error(), "raw lease cleanup detail") {
		t.Fatal("raw credential cleanup error escaped")
	}
}
