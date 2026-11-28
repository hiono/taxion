package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
	"golang.org/x/oauth2"
)

type providerTestBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *providerTestBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *providerTestBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func newProviderTestPrincipal(t *testing.T) auth.AuthenticatedPrincipal {
	t.Helper()
	trustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC signing key: %v", err)
	}
	oidcServer := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{
		PublicKey: trustedKey.Public(), KeyID: "taxion-provider-test-key", Algorithm: oidc.RS256,
	}}}
	var mu sync.Mutex
	var challenge, nonce string
	mux := http.NewServeMux()
	mux.Handle("/.well-known/openid-configuration", oidcServer)
	mux.Handle("/keys", oidcServer)
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		mu.Lock()
		challenge, nonce = query.Get("code_challenge"), query.Get("nonce")
		mu.Unlock()
		if query.Get("code_challenge_method") != "S256" || challenge == "" || nonce == "" || query.Get("state") == "" {
			http.Error(w, "invalid authorization request", http.StatusBadRequest)
			return
		}
		callback, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		values := callback.Query()
		values.Set("code", "one-time-code")
		values.Set("state", query.Get("state"))
		callback.RawQuery = values.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid token request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		expectedChallenge, expectedNonce := challenge, nonce
		mu.Unlock()
		if r.Form.Get("code_verifier") == "" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != expectedChallenge {
			http.Error(w, "PKCE verification failed", http.StatusBadRequest)
			return
		}
		now := time.Now()
		claims, err := json.Marshal(map[string]any{
			"iss": "https://" + r.Host, "aud": "taxion-provider-test", "sub": "subject-provider-test",
			"exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": expectedNonce,
		})
		if err != nil {
			http.Error(w, "encode claims", http.StatusInternalServerError)
			return
		}
		token := oidctest.SignIDToken(trustedKey, "taxion-provider-test-key", oidc.RS256, string(claims))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token", "token_type": "Bearer", "expires_in": 300, "id_token": token,
		})
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	oidcServer.SetIssuer(server.URL)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	originalTransport := http.DefaultTransport
	transport, ok := originalTransport.(*http.Transport)
	if !ok {
		t.Fatal("default HTTP transport has an unexpected type")
	}
	trustedTransport := transport.Clone()
	trustedTransport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	http.DefaultTransport = trustedTransport
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
		trustedTransport.CloseIdleConnections()
	})
	authenticator, err := auth.NewOIDCAuthenticator(auth.OIDCConfig{
		Issuer: server.URL, ClientID: "taxion-provider-test", Audience: "taxion-provider-test",
		RedirectURI: "http://127.0.0.1:0/callback", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}
	output := &providerTestBuffer{}
	type authResult struct {
		principal auth.AuthenticatedPrincipal
		err       error
	}
	done := make(chan authResult, 1)
	go func() {
		principal, err := authenticator.Authenticate(context.Background(), output)
		done <- authResult{principal: principal, err: err}
	}()
	var authorizationURL string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(output.String(), "\n") {
			if strings.HasPrefix(line, server.URL+"/auth?") {
				authorizationURL = strings.TrimSpace(line)
				break
			}
		}
		if authorizationURL != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if authorizationURL == "" {
		t.Fatal("OIDC authorization URL was not produced")
	}
	response, err := server.Client().Get(authorizationURL)
	if err != nil {
		t.Fatalf("complete fake OIDC authorization: %v", err)
	}
	_ = response.Body.Close()
	select {
	case completed := <-done:
		if completed.err != nil {
			t.Fatalf("Authenticate: %v", completed.err)
		}
		return completed.principal
	case <-time.After(5 * time.Second):
		t.Fatal("OIDC authentication did not complete")
		return auth.AuthenticatedPrincipal{}
	}
}

type fakePlanningProviderRuntime struct {
	capabilitiesHook func()
	startClaimErr    error
	startMu          sync.Mutex

	identity     provider.Identity
	capabilities provider.Capabilities
	startResult  provider.StartResult
	startErr     error
	startCalls   int
	lookupCalls  int
	lookupResult provider.LookupResult
	lookupErr    error
	sendResult   provider.TurnResult
	sendErr      error
	sendCalls    int
	sendHook     func(context.Context, provider.TurnRequest) error
	replayPage   provider.EventPage
	replayErr    error
	replayCalls  int
	stopResult   provider.StopResult
	stopErr      error
	stopCalls    int
	resultResult provider.Result
	resultErr    error
	resultCalls  int
	usageResult  provider.Usage
	usageErr     error
	usageCalls   int
	resumeResult provider.ResumeResult
	resumeErr    error
	resumeCalls  int
	resumeHook   func(context.Context, provider.ResumeRequest) error
	stopHook     func(context.Context, provider.StopRequest) error
	startHook    func(context.Context, provider.StartRequest) error
}

func (f *fakePlanningProviderRuntime) Identity() provider.Identity { return f.identity }

func (f *fakePlanningProviderRuntime) Capabilities(context.Context) (provider.Capabilities, error) {
	if f.capabilitiesHook != nil {
		f.capabilitiesHook()
	}
	return f.capabilities.Clone(), nil
}

func (f *fakePlanningProviderRuntime) StartClaimed(ctx context.Context, request provider.StartRequest, claim func() error) (provider.StartResult, error) {
	if f.startClaimErr != nil {
		return provider.StartResult{}, f.startClaimErr
	}
	if err := claim(); err != nil {
		return provider.StartResult{}, err
	}
	return f.Start(ctx, request)
}

func (f *fakePlanningProviderRuntime) Start(ctx context.Context, request provider.StartRequest) (provider.StartResult, error) {
	if f.identity.SDKVersion != "test-sdk/1" {
		return provider.StartResult{}, provider.ErrUnsupportedCapability
	}
	f.startMu.Lock()
	f.startCalls++
	f.startMu.Unlock()
	if f.startHook != nil {
		if err := f.startHook(ctx, request); err != nil {
			return provider.StartResult{}, err
		}
	}
	if f.startErr != nil {
		return provider.StartResult{}, f.startErr
	}
	result := f.startResult
	if result.OperationID == "" {
		result = provider.StartResult{
			OperationID: request.OperationID, SessionID: "session_1",
			ReceiptID: "receipt_1", EvidenceSHA256: strings.Repeat("d", 64),
		}
	}
	return result, nil
}

func (f *fakePlanningProviderRuntime) Lookup(_ context.Context, request provider.LookupRequest) (provider.LookupResult, error) {
	f.lookupCalls++
	if f.lookupErr != nil {
		return provider.LookupResult{}, f.lookupErr
	}
	result := f.lookupResult
	if result.OperationID == "" {
		result = provider.LookupResult{
			Found: true, OperationID: request.OperationID, Status: "RUNNING",
			ReceiptID: "receipt_1", SessionID: "session_1", EvidenceSHA256: strings.Repeat("e", 64),
		}
	}
	return result, nil
}

func (f *fakePlanningProviderRuntime) Send(ctx context.Context, request provider.TurnRequest) (provider.TurnResult, error) {
	f.sendCalls++
	if f.sendHook != nil {
		if err := f.sendHook(ctx, request); err != nil {
			return provider.TurnResult{}, err
		}
	}
	if f.sendErr != nil {
		return provider.TurnResult{}, f.sendErr
	}
	result := f.sendResult
	if result.OperationID == "" {
		result = provider.TurnResult{
			OperationID: request.OperationID, TurnID: request.TurnID, SessionID: request.SessionID,
			ReceiptID: "receipt_turn_1", Status: domain.PlanningTurnCompleted,
			Output: "turn output", EvidenceSHA256: strings.Repeat("f", 64),
		}
	}
	return result, nil
}

func (f *fakePlanningProviderRuntime) Resume(ctx context.Context, request provider.ResumeRequest) (provider.ResumeResult, error) {
	f.resumeCalls++
	if f.resumeHook != nil {
		if err := f.resumeHook(ctx, request); err != nil {
			return provider.ResumeResult{}, err
		}
	}
	if f.resumeErr != nil {
		return provider.ResumeResult{}, f.resumeErr
	}
	result := f.resumeResult
	if result.OperationID == "" {
		result = provider.ResumeResult{
			OperationID: request.OperationID, SessionID: request.SessionID,
			Attached: true, EvidenceSHA256: strings.Repeat("a", 64),
		}
	}
	return result, nil
}

func (f *fakePlanningProviderRuntime) Stop(ctx context.Context, request provider.StopRequest) (provider.StopResult, error) {
	f.stopCalls++
	if f.stopHook != nil {
		if err := f.stopHook(ctx, request); err != nil {
			return provider.StopResult{}, err
		}
	}
	if f.stopErr != nil {
		return provider.StopResult{}, f.stopErr
	}
	result := f.stopResult
	if result.OperationID == "" {
		result = provider.StopResult{
			OperationID: request.OperationID, SessionID: request.SessionID,
			Stopped: true, Quiescent: true, EvidenceSHA256: strings.Repeat("d", 64),
		}
	}
	return result, nil
}

func (f *fakePlanningProviderRuntime) ReplayEvents(_ context.Context, _ provider.ReplayEventsRequest) (provider.EventPage, error) {
	f.replayCalls++
	if f.replayErr != nil {
		return provider.EventPage{}, f.replayErr
	}
	return f.replayPage, nil
}

func (f *fakePlanningProviderRuntime) Result(_ context.Context, request provider.ResultRequest) (provider.Result, error) {
	f.resultCalls++
	if f.resultErr != nil {
		return provider.Result{}, f.resultErr
	}
	result := f.resultResult
	if result.OperationID == "" {
		content := "final provider result"
		result = provider.Result{
			OperationID: request.OperationID, SessionID: request.SessionID, TurnID: request.TurnID,
			Content: content, ResultSHA256: sha256String(content), Final: true,
			EvidenceSHA256: strings.Repeat("d", 64),
		}
	}
	return result, nil
}

func (f *fakePlanningProviderRuntime) Usage(_ context.Context, request provider.UsageRequest) (provider.Usage, error) {
	f.usageCalls++
	if f.usageErr != nil {
		return provider.Usage{}, f.usageErr
	}
	result := f.usageResult
	if result.UsageID == "" {
		result = provider.Usage{
			UsageID: "usage_1", Tokens: 12, Complete: true, EvidenceSHA256: strings.Repeat("e", 64),
		}
	}
	if result.OperationID == "" {
		result.OperationID = request.OperationID
	}
	if result.SessionID == "" {
		result.SessionID = request.SessionID
	}
	if result.TurnID == "" {
		result.TurnID = request.TurnID
	}
	return result, nil
}

func newProviderControllerFixture(t *testing.T, runtime *fakePlanningProviderRuntime, paths ...string) (*ProviderController, *store.Store, auth.AuthenticatedPrincipal, string, string, []provider.ContextDocument) {
	t.Helper()
	ctx := context.Background()
	principal := newProviderTestPrincipal(t)
	path := filepath.Join(t.TempDir(), "provider-controller.sqlite")
	if len(paths) > 0 {
		path = paths[0]
	}
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	jobID := "job_provider_controller"
	createCommand := "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Plan this request.", createCommand, principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	content := "immutable planning snapshot"
	digest := sha256String(content)
	now := time.Now().UTC()
	grantCommand := "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	request := domain.PlanningAuthorizationRequest{
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1", SHA256: digest,
		},

		Deadline: now.Add(time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommand, request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grantCommand, grant, principal); err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	runtime.identity = provider.Identity{
		ProviderID: grant.ProviderID, AccountID: grant.AccountID,
		SDKVersion: "test-sdk/1", ConfigurationSHA256: grant.ConfigurationSHA256,
	}
	runtime.capabilities = provider.Capabilities{
		Identity: runtime.identity, StartIdempotency: true, StartLookup: true,
		ReadOnlyContextEnforcement: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStartLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityReadOnlyContextEnforcement, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("e", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		},
	}
	controller, err := NewProviderController(database, runtime)
	if err != nil {
		t.Fatalf("NewProviderController: %v", err)
	}
	documents := []provider.ContextDocument{{ID: "snapshot_1", SHA256: digest, Content: content}}
	return controller, database, principal, jobID, grant.ID, documents
}

type providerClaimBarrierStore struct {
	*store.Store
	mu      sync.Mutex
	reads   int
	release chan struct{}
}

func (s *providerClaimBarrierStore) GetJob(ctx context.Context, jobID string) (domain.Job, error) {
	job, err := s.Store.GetJob(ctx, jobID)
	if err != nil {
		return domain.Job{}, err
	}
	s.mu.Lock()
	s.reads++
	read := s.reads
	if read == 2 {
		close(s.release)
	}
	release := s.release
	s.mu.Unlock()
	if read <= 2 {
		select {
		case <-release:
		case <-ctx.Done():
			return domain.Job{}, ctx.Err()
		}
	}
	return job, nil
}

func TestProviderControllerRejectsUnverifiedPlanningBeforeIntent(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.capabilities.ReadOnlyContextEnforcement = false
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.startCalls != 0 {
		t.Fatalf("unverified planning started: starts=%d err=%v", runtime.startCalls, err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.ProviderStartIntents) != 0 {
		t.Fatalf("unverified planning saved an intent: %#v err=%v", job.ProviderStartIntents, err)
	}
	runtime.capabilities.ReadOnlyContextEnforcement = true
	if _, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal); err != nil || runtime.startCalls != 1 {
		t.Fatalf("verified retry did not start: starts=%d err=%v", runtime.startCalls, err)
	}
}

func TestProviderControllerStartsAfterBoundaryFailureFollowingIntent(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.capabilities.StartLookup = false
	runtime.capabilities.StartIdempotency = false
	calls := 0
	runtime.capabilitiesHook = func() {
		calls++
		if calls == 2 {
			runtime.capabilities.ReadOnlyContextEnforcement = false
		}
	}
	commandID := "cmd_20202020202020202020202020202020"
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.startCalls != 0 {
		t.Fatalf("unverified start: calls=%d err=%v", runtime.startCalls, err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.ProviderStartIntents) != 1 || len(job.ProviderStartObservations) != 0 {
		t.Fatalf("unattempted start claimed: intents=%d observations=%d err=%v", len(job.ProviderStartIntents), len(job.ProviderStartObservations), err)
	}
	runtime.capabilitiesHook = nil
	runtime.capabilities.ReadOnlyContextEnforcement = true
	_, _, _, _, err = controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err != nil || runtime.startCalls != 1 {
		t.Fatalf("first dispatch after boundary recovery: calls=%d err=%v", runtime.startCalls, err)
	}
}

func TestProviderControllerDoesNotReplayStartWhileFirstDispatchIsRunning(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, _, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.capabilities.StartIdempotency = false
	started, release := make(chan struct{}), make(chan struct{})
	runtime.startHook = func(context.Context, provider.StartRequest) error {
		close(started)
		<-release
		return nil
	}
	commandID := "cmd_21212121212121212121212121212121"
	runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: domain.ProviderStartOperationID(commandID),
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("f", 64),
	}
	done := make(chan error, 1)
	go func() {
		_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
		done <- err
	}()
	<-started
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	close(release)
	if !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("concurrent retry was not blocked: %v", err)
	}
	if err := <-done; err != nil || runtime.startCalls != 1 {
		t.Fatalf("first dispatch failed or was replayed: calls=%d err=%v", runtime.startCalls, err)
	}
}

func TestProviderControllerKeepsStartUnknownWhileIdempotentRetryIsRunning(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	commandID := "cmd_23232323232323232323232323232323"
	runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: domain.ProviderStartOperationID(commandID),
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("f", 64),
	}
	firstStarted, secondStarted := make(chan struct{}), make(chan struct{})
	releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	starts := 0
	runtime.startHook = func(context.Context, provider.StartRequest) error {
		mu.Lock()
		starts++
		call := starts
		mu.Unlock()
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
			return &provider.OperationError{Class: provider.FailureDefinitive, Code: provider.OperationCodeRateLimited}
		}
		close(secondStarted)
		<-releaseSecond
		return nil
	}
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
		firstDone <- err
	}()
	<-firstStarted
	go func() {
		_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
		secondDone <- err
	}()
	<-secondStarted
	close(releaseFirst)
	<-firstDone
	job, getErr := database.GetJob(context.Background(), jobID)
	close(releaseSecond)
	secondErr := <-secondDone
	if getErr != nil || len(job.ProviderStartObservations) != 1 ||
		job.ProviderStartObservations[0].Status != domain.ProviderStartUnknown || secondErr != nil {
		t.Fatalf("concurrent retry was falsely finalized: observations=%#v getErr=%v retryErr=%v",
			job.ProviderStartObservations, getErr, secondErr)
	}
}

func TestProviderControllerStartsAfterRuntimeRejectsBeforeClaim(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{startClaimErr: provider.ErrUnsupportedCapability}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.capabilities.StartLookup = false
	runtime.capabilities.StartIdempotency = false
	commandID := "cmd_22222222222222222222222222222222"
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	job, getErr := database.GetJob(context.Background(), jobID)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || getErr != nil || runtime.startCalls != 0 ||
		len(job.ProviderStartIntents) != 1 || len(job.ProviderStartObservations) != 0 {
		t.Fatalf("unattempted start was claimed: starts=%d observations=%d err=%v getErr=%v",
			runtime.startCalls, len(job.ProviderStartObservations), err, getErr)
	}
	runtime.startClaimErr = nil
	if _, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal); err != nil || runtime.startCalls != 1 {
		t.Fatalf("first dispatch after runtime recovery: starts=%d err=%v", runtime.startCalls, err)
	}
}

func TestProviderControllerRejectsTurnAfterPlanningBoundaryIsLost(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.capabilities.ReadOnlyContextEnforcement = false
	_, _, _, _, err = controller.SendPlanning(context.Background(), jobID, execution.ID, "session_1",
		"cmd_19191919191919191919191919191919", "continue", principal)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.sendCalls != 0 {
		t.Fatalf("unverified turn sent: sends=%d err=%v", runtime.sendCalls, err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.PlanningTurnIntents) != 0 {
		t.Fatalf("unverified turn saved an intent: %#v err=%v", job.PlanningTurnIntents, err)
	}
	runtime.capabilities.ReadOnlyContextEnforcement = true
	if _, _, observation, _, err := controller.SendPlanning(context.Background(), jobID, execution.ID, "session_1",
		"cmd_19191919191919191919191919191919", "continue", principal); err != nil ||
		observation.Status != domain.PlanningTurnCompleted || runtime.sendCalls != 1 {
		t.Fatalf("verified turn did not run: observation=%#v sends=%d err=%v", observation, runtime.sendCalls, err)
	}
}

func TestProviderControllerCanSendAfterBoundaryFailsBeforeClaim(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runtime.capabilitiesHook = func() {
		calls++
		if calls == 2 {
			runtime.capabilities.ReadOnlyContextEnforcement = false
		}
	}
	commandID := "cmd_19191919191919191919191919191919"
	_, _, _, _, err = controller.SendPlanning(context.Background(), jobID, execution.ID, "session_1",
		commandID, "continue", principal)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.sendCalls != 0 {
		t.Fatalf("unverified turn sent: sends=%d err=%v", runtime.sendCalls, err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.PlanningTurnIntents) != 1 || len(job.PlanningTurnObservations) != 0 {
		t.Fatalf("unattempted turn claimed: intents=%d observations=%d err=%v", len(job.PlanningTurnIntents), len(job.PlanningTurnObservations), err)
	}
	runtime.capabilitiesHook = nil
	runtime.capabilities.ReadOnlyContextEnforcement = true
	_, _, observation, _, err := controller.SendPlanning(context.Background(), jobID, execution.ID, "session_1",
		commandID, "continue", principal)
	if err != nil || observation.Status != domain.PlanningTurnCompleted || runtime.sendCalls != 1 {
		t.Fatalf("first dispatch after boundary recovery: observation=%#v sends=%d err=%v", observation, runtime.sendCalls, err)
	}
}

func TestProviderControllerDoesNotRetryStartAfterPlanningBoundaryIsLost(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		startErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, _, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	commandID := "cmd_18181818181818181818181818181818"
	_, _, _, _, _ = controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	runtime.startErr = nil
	runtime.lookupResult = provider.LookupResult{
		OperationID: domain.ProviderStartOperationID(commandID),
		Status:      provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("f", 64),
	}
	runtime.capabilities.ReadOnlyContextEnforcement = false
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.startCalls != 1 || runtime.lookupCalls != 1 {
		t.Fatalf("unverified recovery: starts=%d lookups=%d err=%v", runtime.startCalls, runtime.lookupCalls, err)
	}
}

func TestProviderPlanningTurnDispatchClaimIsExclusive(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_18181818181818181818181818181818", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before turn intent: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(
		context.Background(), jobID, job.Revision, execution.ID, "session_1",
		"cmd_19191919191919191919191919191919", strings.Repeat("f", 64), principal,
	)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	barrierStore := &providerClaimBarrierStore{Store: database, release: make(chan struct{})}
	claimController := &ProviderController{store: barrierStore}
	type claimResult struct {
		claimed bool
		err     error
	}
	results := make(chan claimResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, claimed, claimErr := claimController.claimPlanningTurnDispatch(
				context.Background(), jobID, turn.ID, principal,
			)
			results <- claimResult{claimed: claimed, err: claimErr}
		}()
	}
	first, second := <-results, <-results
	claims := 0
	conflicts := 0
	for _, result := range []claimResult{first, second} {
		if result.claimed {
			claims++
		}
		if result.err != nil {
			if !errors.Is(result.err, store.ErrRevisionConflict) {
				t.Fatalf("dispatch claim failed for an unexpected reason: %v", result.err)
			}
			conflicts++
		}
	}
	if claims != 1 || conflicts != 1 {
		t.Fatalf("dispatch claims = %d, revision conflicts = %d; want one exclusive claim", claims, conflicts)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after planning dispatch claim: %v", err)
	}
	observations := 0
	for _, observation := range job.PlanningTurnObservations {
		if observation.TurnID == turn.ID && observation.Status == domain.PlanningTurnUnknown {
			observations++
		}
	}
	if observations != 1 {
		t.Fatalf("UNKNOWN planning dispatch claims = %d, want 1", observations)
	}
}

func TestProviderControllerRejectsInvalidResultAndUsageResponses(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	now := time.Now().UTC()
	runtime.capabilities.FinalOutputRetrieval = true
	runtime.capabilities.UsageIdentity = true
	runtime.capabilities.UsageCompleteness = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_16161616161616161616161616161616", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision, execution.ID,
		"session_1", "cmd_17171717171717171717171717171717", strings.Repeat("f", 64), principal)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	content := "final result"
	validDigest := sha256String(content)
	runtime.resultResult = provider.Result{
		OperationID: "wrong_operation", SessionID: "session_1", TurnID: turn.ID,
		Content: content, ResultSHA256: validDigest, Final: true, EvidenceSHA256: strings.Repeat("4", 64),
	}
	if _, _, err := controller.RetrieveProviderResult(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); !errors.Is(err, provider.ErrProviderInvalidResult) {
		t.Fatalf("mismatched Result identity error = %v, want ErrProviderInvalidResult", err)
	}
	runtime.resultResult = provider.Result{
		OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID,
		Content: content, ResultSHA256: validDigest, Final: false, EvidenceSHA256: strings.Repeat("4", 64),
	}
	if _, _, err := controller.RetrieveProviderResult(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); err == nil {
		t.Fatal("non-final Provider Result was accepted")
	}
	runtime.resultResult = provider.Result{
		OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID,
		Content: content, ResultSHA256: strings.Repeat("0", 64), Final: true, EvidenceSHA256: strings.Repeat("4", 64),
	}
	if _, _, err := controller.RetrieveProviderResult(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); err == nil {
		t.Fatal("Provider Result with a mismatched content digest was accepted")
	}
	runtime.usageResult = provider.Usage{
		OperationID: "wrong_operation", SessionID: "session_1", TurnID: turn.ID,
		UsageID: "usage_invalid", Tokens: 3, Complete: true, EvidenceSHA256: strings.Repeat("5", 64),
	}
	if _, _, err := controller.RetrieveProviderUsage(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); !errors.Is(err, provider.ErrProviderInvalidResult) {
		t.Fatalf("mismatched Usage identity error = %v, want ErrProviderInvalidResult", err)
	}
	runtime.usageResult = provider.Usage{
		UsageID: "usage invalid", Tokens: 3, Complete: true, EvidenceSHA256: strings.Repeat("5", 64),
	}
	if _, _, err := controller.RetrieveProviderUsage(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); err == nil {
		t.Fatal("Provider Usage with an invalid UsageID was accepted")
	}
	runtime.resultErr = errors.New("Provider Result retrieval failed")
	if _, _, err := controller.RetrieveProviderResult(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); err == nil {
		t.Fatal("Provider Result retrieval error was ignored")
	}
	runtime.usageErr = errors.New("Provider Usage retrieval failed")
	if _, _, err := controller.RetrieveProviderUsage(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); err == nil {
		t.Fatal("Provider Usage retrieval error was ignored")
	}
	after, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after invalid Provider responses: %v", err)
	}
	if len(after.ProviderResultObservations) != 0 || len(after.ProviderUsageObservations) != 0 {
		t.Fatalf("invalid Provider responses created success facts: %#v %#v", after.ProviderResultObservations, after.ProviderUsageObservations)
	}
}

func TestProviderControllerPersistsResumeIntentBeforeRuntime(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	now := time.Now().UTC()
	runtime.capabilities.CrossProcessSessionReattach = true
	resumeEvidence := strings.Repeat("a", 64)
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySessionReattach, Identity: runtime.identity, TestID: "resume-test", EvidenceSHA256: resumeEvidence, EvidenceLevel: provider.EvidenceDurable, VerifiedAt: now},
	)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_24242424242424242424242424242424", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	if _, _, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision, execution.ID,
		"session_1", "cmd_25252525252525252525252525252525", strings.Repeat("f", 64), principal); err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	intentSeen := false
	runtime.resumeHook = func(ctx context.Context, request provider.ResumeRequest) error {
		current, err := database.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		for _, intent := range current.PlanningResumeIntents {
			if intent.OperationID == request.OperationID && intent.ProviderSessionID == request.SessionID {
				intentSeen = true
				if len(current.PlanningResumeObservations) != 0 {
					return errors.New("Resume observation was written before Runtime.Resume")
				}
				return nil
			}
		}
		return errors.New("Resume intent was not persisted before Runtime.Resume")
	}
	receipt, intent, observation, result, err := controller.ResumePlanning(
		context.Background(), jobID, execution.ID, "session_1",
		"cmd_26262626262626262626262626262626", principal,
	)
	if err != nil || !intentSeen || runtime.resumeCalls != 1 || !result.Attached ||
		observation.Status != domain.PlanningResumeAttached || observation.CreatedRevision <= receipt.Revision ||
		intent.CapabilityEvidenceSHA256 != resumeEvidence || intent.OperationID != result.OperationID {
		t.Fatalf("ResumePlanning: receipt=%#v intent=%#v observation=%#v result=%#v seen=%v calls=%d err=%v",
			receipt, intent, observation, result, intentSeen, runtime.resumeCalls, err)
	}
}

func TestProviderControllerReconcilesUnknownResumeWithSameOperationID(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.capabilities.CrossProcessSessionReattach = true
	resumeEvidence := strings.Repeat("b", 64)
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySessionReattach, Identity: runtime.identity, TestID: "resume-test", EvidenceSHA256: resumeEvidence, EvidenceLevel: provider.EvidenceDurable, VerifiedAt: time.Now().UTC()},
	)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_27272727272727272727272727272727", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	if _, _, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision, execution.ID,
		"session_1", "cmd_28282828282828282828282828282828", strings.Repeat("f", 64), principal); err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	commandID := "cmd_29292929292929292929292929292929"
	runtime.resumeErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown}
	receipt, intent, unknown, _, err := controller.ResumePlanning(
		context.Background(), jobID, execution.ID, "session_1", commandID, principal,
	)
	if err == nil || unknown.Status != domain.PlanningResumeUnknown || receipt.Revision == 0 || runtime.resumeCalls != 1 {
		t.Fatalf("ambiguous Resume did not persist UNKNOWN: %#v %#v calls=%d err=%v", intent, unknown, runtime.resumeCalls, err)
	}
	runtime.resumeErr = nil
	runtime.resumeResult = provider.ResumeResult{
		OperationID: intent.OperationID, SessionID: "session_1", Attached: true, EvidenceSHA256: strings.Repeat("c", 64),
	}
	receipt, resumedIntent, attached, result, err := controller.ResumePlanning(
		context.Background(), jobID, execution.ID, "session_1", commandID, principal,
	)
	if err != nil || receipt.Revision == 0 || resumedIntent.ID != intent.ID ||
		attached.Status != domain.PlanningResumeAttached || !attached.ReconcilesUnknown ||
		!result.Attached || runtime.resumeCalls != 2 {
		t.Fatalf("Resume UNKNOWN reconciliation failed: %#v %#v %#v calls=%d err=%v",
			receipt, resumedIntent, attached, runtime.resumeCalls, err)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil || len(job.PlanningResumeIntents) != 1 || len(job.PlanningResumeObservations) != 2 ||
		job.PlanningResumeObservations[0].Status != domain.PlanningResumeUnknown ||
		!job.PlanningResumeObservations[1].ReconcilesUnknown {
		t.Fatalf("Resume reconciliation facts are inconsistent: %#v %v", job.PlanningResumeObservations, err)
	}
}

func TestProviderControllerRejectsResumeWithoutE5Capability(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_30303030303030303030303030303030", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	if _, _, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision, execution.ID,
		"session_1", "cmd_31313131313131313131313131313131", strings.Repeat("f", 64), principal); err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	if _, _, _, _, err := controller.ResumePlanning(
		context.Background(), jobID, execution.ID, "session_1",
		"cmd_32323232323232323232323232323232", principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("ResumePlanning error = %v, want ErrUnsupportedCapability", err)
	}
	after, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(after.PlanningResumeIntents) != 0 || runtime.resumeCalls != 0 {
		t.Fatalf("unsupported Resume created intent or called Runtime: %#v calls=%d err=%v", after.PlanningResumeIntents, runtime.resumeCalls, err)
	}
}

func TestProviderControllerPersistsTurnIntentBeforeSend(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.capabilities.SendIdempotency = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: time.Now().UTC()},
	)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_18181818181818181818181818181818", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	intentSeen := false
	runtime.sendHook = func(ctx context.Context, request provider.TurnRequest) error {
		job, err := database.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		for _, intent := range job.PlanningTurnIntents {
			if intent.ID == request.TurnID && intent.OperationID == request.OperationID {
				intentSeen = true
				if len(job.PlanningTurnObservations) != 1 ||
					job.PlanningTurnObservations[0].TurnID != request.TurnID ||
					job.PlanningTurnObservations[0].Status != domain.PlanningTurnUnknown {
					return errors.New("exclusive UNKNOWN dispatch claim was not persisted before Runtime.Send")
				}
				return nil
			}
		}
		return errors.New("Turn intent was not persisted before Runtime.Send")
	}
	receipt, turn, observation, result, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1",
		"cmd_19191919191919191919191919191919", "inspect the accepted repository snapshot", principal,
	)
	if err != nil || !intentSeen || runtime.sendCalls != 1 ||
		turn.OperationID != result.OperationID || turn.ID != result.TurnID ||
		observation.Status != domain.PlanningTurnCompleted || observation.CreatedRevision <= receipt.Revision ||
		result.Status != domain.PlanningTurnCompleted {
		t.Fatalf("SendPlanning: receipt=%#v turn=%#v observation=%#v result=%#v intentSeen=%v calls=%d err=%v",
			receipt, turn, observation, result, intentSeen, runtime.sendCalls, err)
	}
}

func TestProviderControllerPersistsAndRestoresPlannerResponse(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime, path)
	_, execution, _, _, err := controller.StartPlanning(ctx, jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	commandID := "cmd_19191919191919191919191919191919"
	_, _, observation, result, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err != nil || result.Output != "turn output" {
		t.Fatalf("SendPlanning: result=%#v err=%v", result, err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobMessages) != 1 {
		t.Fatalf("persisted messages=%#v err=%v", job.JobMessages, err)
	}
	message := job.JobMessages[0]
	if message.Body != result.Output || message.Sender != (domain.ActorRef{Kind: domain.ActorPlanner, ID: execution.ID}) ||
		message.Recipient != (domain.ActorRef{Kind: domain.ActorOrchestrator, ID: domain.OrchestratorActorID}) ||
		message.CreatedRevision >= observation.CreatedRevision {
		t.Fatalf("Planner response was not saved before completion: %#v %#v", message, observation)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	controller, err = NewProviderController(reopened, runtime)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, retry, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err != nil || retry.Output != result.Output || runtime.sendCalls != 1 {
		t.Fatalf("restored response=%#v sends=%d err=%v", retry, runtime.sendCalls, err)
	}
	job, err = reopened.GetJob(ctx, jobID)
	if err != nil || len(job.JobMessages) != 1 || job.JobMessages[0].ID != message.ID {
		t.Fatalf("retry duplicated or lost the message: %#v err=%v", job.JobMessages, err)
	}
}

type rejectPlannerMessageStore struct{ *store.Store }

func (s rejectPlannerMessageStore) AddJobMessage(context.Context, string, uint64, string, domain.JobMessageRequest) (store.CommandReceipt, domain.JobMessage, error) {
	return store.CommandReceipt{}, domain.JobMessage{}, errors.New("message store unavailable")
}

func TestProviderControllerDoesNotCompleteOrResendWhenSavingResponseFails(t *testing.T) {
	ctx := context.Background()
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(ctx, jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	failing, err := NewProviderController(rejectPlannerMessageStore{database}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	commandID := "cmd_19191919191919191919191919191919"
	_, turn, observation, result, err := failing.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err == nil || result.Output != "turn output" || observation.Status != domain.PlanningTurnUnknown {
		t.Fatalf("save failure: observation=%#v result=%#v err=%v", observation, result, err)
	}
	if _, _, _, _, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal); !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.sendCalls != 1 {
		t.Fatalf("unknown result was resent: sends=%d err=%v", runtime.sendCalls, err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobMessages) != 0 || len(job.PlanningTurnObservations) != 1 ||
		job.PlanningTurnObservations[0].Status != domain.PlanningTurnUnknown {
		t.Fatalf("save failure must remain UNKNOWN: %#v err=%v", job, err)
	}
	now := time.Now().UTC()
	runtime.capabilities.SendLookup = true
	runtime.capabilities.UsageIdentity = true
	runtime.capabilities.UsageCompleteness = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID,
		Status: domain.PlanningTurnCompleted, ReceiptID: "receipt_1", UsageID: "usage_1",
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	_, _, observation, _, err = controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || observation.Status != domain.PlanningTurnUnknown || runtime.sendCalls != 1 {
		t.Fatalf("unrecoverable output must remain UNKNOWN: observation=%#v sends=%d err=%v", observation, runtime.sendCalls, err)
	}
	runtime.capabilities.FinalOutputRetrieval = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	runtime.lookupResult.ResultSHA256 = sha256String("different response")
	if _, _, observation, _, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal); err == nil || observation.Status != domain.PlanningTurnUnknown {
		t.Fatalf("mismatched recovered output must remain UNKNOWN: observation=%#v err=%v", observation, err)
	}
	runtime.lookupResult.ResultSHA256 = sha256String("final provider result")
	if _, _, observation, _, err := failing.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal); err == nil || observation.Status != domain.PlanningTurnUnknown {
		t.Fatalf("recovered output save failure: observation=%#v err=%v", observation, err)
	}
	runtime.capabilities.FinalOutputRetrieval = false
	_, _, observation, recovered, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err != nil || observation.Status != domain.PlanningTurnCompleted ||
		recovered.Output != "final provider result" || runtime.sendCalls != 1 || runtime.resultCalls != 1 {
		t.Fatalf("recovered response=%#v observation=%#v sends=%d results=%d err=%v", recovered, observation, runtime.sendCalls, runtime.resultCalls, err)
	}
}

type rejectTerminalTurnStore struct{ *store.Store }

func (s rejectTerminalTurnStore) AddPlanningTurnObservation(ctx context.Context, jobID string, revision uint64,
	turnID, commandID, status, evidence string, reconciles bool, principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningTurnObservation, error) {
	if status == domain.PlanningTurnCompleted {
		return store.CommandReceipt{}, domain.PlanningTurnObservation{}, errors.New("terminal observation unavailable")
	}
	return s.Store.AddPlanningTurnObservation(ctx, jobID, revision, turnID, commandID, status, evidence, reconciles, principal)
}

func TestProviderControllerPreservesResponseWhenTerminalObservationFails(t *testing.T) {
	ctx := context.Background()
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(ctx, jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	failing, err := NewProviderController(rejectTerminalTurnStore{database}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	commandID := "cmd_19191919191919191919191919191919"
	_, turn, observation, _, err := failing.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err == nil || observation.Status != domain.PlanningTurnUnknown {
		t.Fatalf("terminal failure: observation=%#v err=%v", observation, err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobMessages) != 1 || job.JobMessages[0].Body != "turn output" {
		t.Fatalf("response was lost: %#v err=%v", job.JobMessages, err)
	}
	now := time.Now().UTC()
	runtime.capabilities.SendLookup = true
	runtime.capabilities.UsageIdentity = true
	runtime.capabilities.UsageCompleteness = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID,
		Status: domain.PlanningTurnCompleted, ReceiptID: "receipt_1", UsageID: "usage_1",
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	runtime.lookupResult.Status = domain.PlanningTurnFailed
	if _, _, observation, _, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal); err == nil || observation.Status != domain.PlanningTurnUnknown {
		t.Fatalf("failure conflicting with saved response must remain UNKNOWN: observation=%#v err=%v", observation, err)
	}
	runtime.lookupResult.Status = domain.PlanningTurnCompleted
	runtime.lookupResult.ResultSHA256 = sha256String("different response")
	if _, _, observation, _, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal); err == nil || observation.Status != domain.PlanningTurnUnknown {
		t.Fatalf("mismatched saved response must remain UNKNOWN: observation=%#v err=%v", observation, err)
	}
	runtime.capabilities.SendLookup = false
	runtime.capabilities.SendIdempotency = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	runtime.sendResult = provider.TurnResult{
		OperationID: turn.OperationID, TurnID: turn.ID, SessionID: "session_1",
		Status: domain.PlanningTurnFailed, EvidenceSHA256: strings.Repeat("f", 64),
	}
	if _, _, observation, _, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal); err == nil || observation.Status != domain.PlanningTurnUnknown {
		t.Fatalf("retry conflicting with saved response must remain UNKNOWN: observation=%#v err=%v", observation, err)
	}
	runtime.capabilities.SendLookup = true
	runtime.lookupResult.ResultSHA256 = sha256String("turn output")
	_, _, observation, result, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err != nil || observation.Status != domain.PlanningTurnCompleted ||
		result.Output != "turn output" || runtime.sendCalls != 2 {
		t.Fatalf("reconciled response=%#v observation=%#v sends=%d err=%v", result, observation, runtime.sendCalls, err)
	}
}

func TestProviderControllerDoesNotPublishInvalidTurnResult(t *testing.T) {
	ctx := context.Background()
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(ctx, jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	commandID := "cmd_19191919191919191919191919191919"
	runtime.sendResult = provider.TurnResult{
		OperationID: domain.PlanningTurnOperationID(commandID), TurnID: domain.PlanningTurnIntentID(commandID),
		SessionID: "session_1", Status: "invalid",
		EvidenceSHA256: strings.Repeat("f", 64), Output: "must not be published",
	}
	if _, _, _, _, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal); err == nil {
		t.Fatal("invalid result was accepted")
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobMessages) != 0 || len(job.PlanningTurnObservations) != 1 ||
		job.PlanningTurnObservations[0].Status != domain.PlanningTurnUnknown {
		t.Fatalf("invalid result was published or completed: %#v err=%v", job, err)
	}
}

func TestProviderControllerReconcilesVerifiedBlankPlannerResponse(t *testing.T) {
	ctx := context.Background()
	runtime := &fakePlanningProviderRuntime{sendErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout}}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(ctx, jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	commandID := "cmd_19191919191919191919191919191919"
	_, turn, unknown, _, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err == nil || unknown.Status != domain.PlanningTurnUnknown {
		t.Fatalf("initial turn was not UNKNOWN: %#v %v", unknown, err)
	}
	now := time.Now().UTC()
	runtime.capabilities.SendLookup = true
	runtime.capabilities.UsageIdentity = true
	runtime.capabilities.UsageCompleteness = true
	runtime.capabilities.FinalOutputRetrieval = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID,
		Status: domain.PlanningTurnCompleted, ReceiptID: "receipt_1", UsageID: "usage_1",
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	runtime.resultResult = provider.Result{
		OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID, Content: "",
		ResultSHA256: sha256String(""), Final: true, EvidenceSHA256: strings.Repeat("f", 64),
	}
	_, _, observation, result, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err != nil || observation.Status != domain.PlanningTurnCompleted || result.Output != "" || runtime.sendCalls != 1 {
		t.Fatalf("blank response: observation=%#v result=%#v err=%v", observation, result, err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobMessages) != 0 || len(job.ProviderResultObservations) != 1 ||
		job.ProviderResultObservations[0].Content != "" {
		t.Fatalf("blank result evidence=%#v messages=%#v err=%v", job.ProviderResultObservations, job.JobMessages, err)
	}
}

func TestProviderControllerReconcilesUnknownTurnWithoutUsage(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	now := time.Now().UTC()
	runtime.capabilities.SendLookup = true
	runtime.capabilities.FinalOutputRetrieval = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_20202020202020202020202020202020", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	commandID := "cmd_21212121212121212121212121212121"
	operationID := domain.PlanningTurnOperationID(commandID)
	turnID := domain.PlanningTurnIntentID(commandID)
	runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: operationID, Status: provider.LookupStatusNotFound,
		SessionID: "session_1", TurnID: turnID, EvidenceSHA256: strings.Repeat("d", 64),
	}
	runtime.sendErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown}
	receipt, turn, unknown, _, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1", commandID,
		"inspect the accepted repository snapshot", principal,
	)
	if err == nil || unknown.Status != domain.PlanningTurnUnknown || runtime.sendCalls != 1 || receipt.Revision == 0 {
		t.Fatalf("ambiguous Send did not persist UNKNOWN: %#v %#v calls=%d err=%v", turn, unknown, runtime.sendCalls, err)
	}
	if _, _, _, _, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1", commandID,
		"inspect the accepted repository snapshot", principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.sendCalls != 1 {
		t.Fatalf("NOT_FOUND without idempotency retried Send: calls=%d err=%v", runtime.sendCalls, err)
	}
	runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: operationID, Status: domain.PlanningTurnCompleted,
		ReceiptID: "receipt_turn_1", SessionID: "session_1", TurnID: turnID,
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	receipt, reconciledTurn, completed, result, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1", commandID,
		"inspect the accepted repository snapshot", principal,
	)
	if err != nil || receipt.Revision == 0 || reconciledTurn.ID != turn.ID ||
		completed.Status != domain.PlanningTurnCompleted || !completed.ReconcilesUnknown ||
		result.Status != domain.PlanningTurnCompleted ||
		runtime.sendCalls != 1 || runtime.lookupCalls != 2 || runtime.usageCalls != 0 {
		t.Fatalf("same-Turn reconciliation failed: %#v %#v %#v calls(send=%d lookup=%d usage=%d) err=%v",
			receipt, reconciledTurn, completed, runtime.sendCalls, runtime.lookupCalls, runtime.usageCalls, err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.PlanningTurnIntents) != 1 || len(job.PlanningTurnObservations) != 2 ||
		job.PlanningTurnObservations[0].Status != domain.PlanningTurnUnknown ||
		!job.PlanningTurnObservations[1].ReconcilesUnknown ||
		len(job.ProviderUsageObservations) != 0 {
		t.Fatalf("reconciled Turn facts are inconsistent: %#v %v", job, err)
	}
}

func TestProviderControllerContinuesWithoutUsageReports(t *testing.T) {
	ctx := context.Background()
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(ctx, jobID, grantID,
		"cmd_18181818181818181818181818181818", documents, principal)
	if err != nil {
		t.Fatal(err)
	}
	for _, commandID := range []string{
		"cmd_19191919191919191919191919191919",
		"cmd_20202020202020202020202020202020",
		"cmd_21212121212121212121212121212121",
		"cmd_22222222222222222222222222222222",
	} {
		_, _, observation, result, err := controller.SendPlanning(ctx, jobID, execution.ID, "session_1", commandID, "continue", principal)
		if err != nil || observation.Status != domain.PlanningTurnCompleted || result.Output != "turn output" {
			t.Fatalf("turn: observation=%#v result=%#v err=%v", observation, result, err)
		}
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobMessages) != 4 || runtime.sendCalls != 4 || runtime.usageCalls != 0 ||
		len(job.ProviderUsageObservations) != 0 {
		t.Fatalf("conversation without usage: messages=%d sends=%d usage=%d err=%v", len(job.JobMessages), runtime.sendCalls, runtime.usageCalls, err)
	}
}

func TestProviderControllerAllowsOneShotSendButNeverRetriesUnknownWithoutProof(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		sendErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_22222222222222222222222222222222", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	commandID := "cmd_23232323232323232323232323232323"
	_, turn, observation, _, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal,
	)
	if err == nil || observation.Status != domain.PlanningTurnUnknown || runtime.sendCalls != 1 {
		t.Fatalf("initial Send was not attempted exactly once and recorded UNKNOWN: turn=%#v observation=%#v calls=%d err=%v",
			turn, observation, runtime.sendCalls, err)
	}
	runtime.capabilities = provider.Capabilities{Identity: runtime.identity}
	if _, _, _, _, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("UNKNOWN retry error = %v, want ErrUnsupportedCapability", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.PlanningTurnIntents) != 1 || len(job.PlanningTurnObservations) != 1 ||
		runtime.sendCalls != 1 || runtime.lookupCalls != 0 {
		t.Fatalf("unsafe retry or missing lifecycle facts: intents=%d observations=%d sends=%d lookups=%d err=%v",
			len(job.PlanningTurnIntents), len(job.PlanningTurnObservations), runtime.sendCalls, runtime.lookupCalls, err)
	}
}

func TestProviderControllerRetriesMissingTurnOnlyWithIdempotency(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		sendErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, _, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_24242424242424242424242424242424", documents, principal,
	)
	if err != nil {
		t.Fatal(err)
	}
	commandID := "cmd_25252525252525252525252525252525"
	_, _, unknown, _, err := controller.SendPlanning(context.Background(), jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err == nil || unknown.Status != domain.PlanningTurnUnknown || runtime.sendCalls != 1 {
		t.Fatalf("initial Send did not become UNKNOWN: observation=%#v sends=%d err=%v", unknown, runtime.sendCalls, err)
	}
	now := time.Now().UTC()
	runtime.capabilities.SendLookup = true
	runtime.capabilities.SendIdempotency = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	runtime.lookupResult = provider.LookupResult{
		OperationID: domain.PlanningTurnOperationID(commandID), TurnID: domain.PlanningTurnIntentID(commandID),
		SessionID: "session_1", Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("c", 64),
	}
	runtime.sendErr = nil
	_, _, observation, _, err := controller.SendPlanning(context.Background(), jobID, execution.ID, "session_1",
		commandID, "inspect the accepted repository snapshot", principal)
	if err != nil || observation.Status != domain.PlanningTurnCompleted || runtime.sendCalls != 2 {
		t.Fatalf("idempotent retry failed: observation=%#v sends=%d err=%v", observation, runtime.sendCalls, err)
	}
}

func TestProviderControllerPersistsResultAndUsageForAcceptedTurn(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	now := time.Now().UTC()
	runtime.capabilities.FinalOutputRetrieval = true
	runtime.capabilities.UsageIdentity = true
	runtime.capabilities.UsageCompleteness = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	_, execution, startIntent, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_12121212121212121212121212121212", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision, execution.ID,
		"session_1", "cmd_13131313131313131313131313131313", strings.Repeat("f", 64), principal)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	content := "final result for the accepted turn"
	runtime.resultResult = provider.Result{
		OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID,
		Content: content, ResultSHA256: sha256String(content), Final: true, EvidenceSHA256: strings.Repeat("4", 64),
	}
	resultReceipt, result, err := controller.RetrieveProviderResult(
		context.Background(), jobID, execution.ID, "session_1", turn.ID, principal,
	)
	if err != nil || !result.Final || result.OperationID != turn.OperationID ||
		result.CreatedRevision != resultReceipt.Revision || runtime.resultCalls != 1 {
		t.Fatalf("RetrieveProviderResult: %#v %#v calls=%d err=%v", resultReceipt, result, runtime.resultCalls, err)
	}
	runtime.usageResult = provider.Usage{
		UsageID: "usage_turn_1", Tokens: 11, Complete: false, EvidenceSHA256: strings.Repeat("5", 64),
	}
	partialReceipt, partial, err := controller.RetrieveProviderUsage(
		context.Background(), jobID, execution.ID, "session_1", turn.ID, principal,
	)
	if err != nil || partial.Complete || partial.Tokens != 11 || partial.CreatedRevision != partialReceipt.Revision {
		t.Fatalf("RetrieveProviderUsage partial: %#v %#v %v", partialReceipt, partial, err)
	}
	runtime.usageResult = provider.Usage{
		UsageID: "usage_turn_1", Tokens: 19, Complete: true, EvidenceSHA256: strings.Repeat("6", 64),
	}
	completeReceipt, complete, err := controller.RetrieveProviderUsage(
		context.Background(), jobID, execution.ID, "session_1", turn.ID, principal,
	)
	if err != nil || !complete.Complete || complete.Tokens != 19 || complete.CreatedRevision != completeReceipt.Revision {
		t.Fatalf("RetrieveProviderUsage complete: %#v %#v %v", completeReceipt, complete, err)
	}
	resultRetryReceipt, resultRetry, err := controller.RetrieveProviderResult(
		context.Background(), jobID, execution.ID, "session_1", turn.ID, principal,
	)
	if err != nil || resultRetryReceipt != resultReceipt || resultRetry != result {
		t.Fatalf("idempotent Result retrieval: %#v %#v %v", resultRetryReceipt, resultRetry, err)
	}
	usageRetryReceipt, usageRetry, err := controller.RetrieveProviderUsage(
		context.Background(), jobID, execution.ID, "session_1", turn.ID, principal,
	)
	if err != nil || usageRetryReceipt != completeReceipt || usageRetry != complete {
		t.Fatalf("idempotent Usage retrieval: %#v %#v %v", usageRetryReceipt, usageRetry, err)
	}
	restored, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Result / Usage: %v", err)
	}
	if len(restored.ProviderResultObservations) != 1 || len(restored.ProviderUsageObservations) != 2 ||
		restored.ProviderUsageObservations[1].UsageID != "usage_turn_1" || !restored.ProviderUsageObservations[1].Complete ||
		startIntent.ID != restored.ProviderStartIntents[0].ID {
		t.Fatalf("Provider Result / Usage facts were not preserved: %#v %#v", restored.ProviderResultObservations, restored.ProviderUsageObservations)
	}
}

func TestProviderControllerRejectsResultAndUsageWithoutVerifiedCapabilities(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_14141414141414141414141414141414", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision, execution.ID,
		"session_1", "cmd_15151515151515151515151515151515", strings.Repeat("f", 64), principal)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	if _, _, err := controller.RetrieveProviderResult(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("RetrieveProviderResult error = %v, want ErrUnsupportedCapability", err)
	}
	if _, _, err := controller.RetrieveProviderUsage(context.Background(), jobID, execution.ID, "session_1", turn.ID, principal); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("RetrieveProviderUsage error = %v, want ErrUnsupportedCapability", err)
	}
	if runtime.resultCalls != 0 || runtime.usageCalls != 0 {
		t.Fatalf("unsupported capability invoked Provider methods: result=%d usage=%d", runtime.resultCalls, runtime.usageCalls)
	}
}
func TestProviderControllerPersistsStartIntentBeforeCallingRuntime(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.startHook = func(ctx context.Context, request provider.StartRequest) error {
		job, err := database.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if len(job.ProviderStartIntents) != 1 || job.ProviderStartIntents[0].ID != request.OperationID {
			return errors.New("Provider Start was called before intent commit")
		}
		if len(job.ProviderStartObservations) != 1 || job.ProviderStartObservations[0].Status != domain.ProviderStartUnknown {
			return errors.New("Provider Start dispatch was not claimed before the external call")
		}
		if job.ProviderLifecycleEnforcedFromRevision != job.ProviderStartIntents[0].CreatedRevision {
			return errors.New("Provider lifecycle enforcement was not activated with the Start intent")
		}
		return nil
	}
	receipt, execution, intent, result, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_cccccccccccccccccccccccccccccccc", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	if receipt.Revision == 0 || execution.ID == "" || intent.ID != result.OperationID || result.SessionID != "session_1" {
		t.Fatalf("unexpected Start result: receipt=%#v execution=%#v intent=%#v result=%#v", receipt, execution, intent, result)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.ProviderStartObservations) != 2 || job.ProviderStartObservations[1].Status != domain.ProviderStartAccepted ||
		!job.ProviderStartObservations[1].ReconcilesUnknown {
		t.Fatalf("Provider Start acceptance was not persisted: %#v", job.ProviderStartObservations)
	}
}

func TestProviderControllerReconcilesAmbiguousStartWithoutBlindReplay(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		startErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	commandID := "cmd_cccccccccccccccccccccccccccccccc"
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err == nil {
		t.Fatal("ambiguous Start error was not returned")
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after ambiguous Start: %v", err)
	}
	if len(job.ProviderStartObservations) != 1 || job.ProviderStartObservations[0].Status != domain.ProviderStartUnknown {
		t.Fatalf("ambiguous Start was not persisted as UNKNOWN: %#v", job.ProviderStartObservations)
	}
	runtime.startErr = nil
	_, _, _, result, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err != nil {
		t.Fatalf("StartPlanning recovery: %v", err)
	}
	if runtime.startCalls != 1 || runtime.lookupCalls != 1 || result.SessionID != "session_1" {
		t.Fatalf("recovery replayed Start or failed lookup: starts=%d lookups=%d result=%#v", runtime.startCalls, runtime.lookupCalls, result)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after recovery: %v", err)
	}
	if len(job.ProviderStartObservations) != 2 ||
		job.ProviderStartObservations[1].Status != domain.ProviderStartAccepted ||
		!job.ProviderStartObservations[1].ReconcilesUnknown {
		t.Fatalf("UNKNOWN was not reconciled by same OperationID: %#v", job.ProviderStartObservations)
	}
}

func TestProviderControllerKeepsClaimedDefinitiveStartFailureUnknown(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		startErr: &provider.OperationError{Class: provider.FailureDefinitive, Code: provider.OperationCodeInvalidRequest},
	}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	commandID := "cmd_cccccccccccccccccccccccccccccccc"
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err == nil {
		t.Fatal("definitive Start failure was not returned")
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after definitive Start failure: %v", err)
	}
	if len(job.ProviderStartObservations) != 1 ||
		job.ProviderStartObservations[0].Status != domain.ProviderStartUnknown {
		t.Fatalf("claimed Start failure was finalized despite possible concurrent retry: %#v", job.ProviderStartObservations)
	}
	if runtime.startCalls != 1 || runtime.lookupCalls != 0 {
		t.Fatalf("definitive failure was retried or looked up: starts=%d lookups=%d", runtime.startCalls, runtime.lookupCalls)
	}
}

func TestProviderControllerKeepsUnknownAfterDefinitiveRetryFailure(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		startErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	commandID := "cmd_cccccccccccccccccccccccccccccccc"
	_, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err == nil {
		t.Fatal("ambiguous Start error was not returned")
	}
	runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: domain.ProviderStartOperationID(commandID),
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("f", 64),
	}
	runtime.startErr = &provider.OperationError{Class: provider.FailureDefinitive, Code: provider.OperationCodeInvalidRequest}
	_, _, _, _, err = controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err == nil {
		t.Fatal("definitive retry failure was not returned")
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after definitive retry failure: %v", err)
	}
	if len(job.ProviderStartObservations) != 1 || job.ProviderStartObservations[0].Status != domain.ProviderStartUnknown {
		t.Fatalf("definitive retry did not preserve UNKNOWN: %#v", job.ProviderStartObservations)
	}
	if runtime.startCalls != 2 || runtime.lookupCalls != 1 {
		t.Fatalf("terminal failure was replayed: starts=%d lookups=%d", runtime.startCalls, runtime.lookupCalls)
	}
}

func TestProviderControllerDoesNotStartAfterGrantDeadlineButAllowsLookupRecovery(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		startErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	commandID := "cmd_cccccccccccccccccccccccccccccccc"
	_, _, _, _, _ = controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	controller.now = func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }
	_, _, _, result, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err != nil {
		t.Fatalf("lookup recovery after deadline: %v", err)
	}
	if runtime.startCalls != 1 || runtime.lookupCalls != 1 || result.SessionID != "session_1" {
		t.Fatalf("deadline recovery issued a new Start: starts=%d lookups=%d", runtime.startCalls, runtime.lookupCalls)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.ProviderStartObservations) != 2 || !job.ProviderStartObservations[1].ReconcilesUnknown {
		t.Fatalf("deadline recovery did not append reconciliation: %#v", job.ProviderStartObservations)
	}
}

func TestProviderControllerRejectsProviderIdentityAndContextMismatchBeforeIntent(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.identity.ProviderID = "provider.other"
	if _, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, "cmd_dddddddddddddddddddddddddddddddd", documents, principal); err == nil {
		t.Fatal("Provider identity mismatch was accepted")
	}
	runtime.identity.ProviderID = "provider.test"
	documents[0].Content = "tampered snapshot"
	if _, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", documents, principal); err == nil {
		t.Fatal("tampered context was accepted")
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.ProviderStartIntents) != 0 || runtime.startCalls != 0 {
		t.Fatalf("preflight mismatch caused a Provider intent or call: intents=%d calls=%d", len(job.ProviderStartIntents), runtime.startCalls)
	}
}

func TestProviderControllerRejectsUnverifiedSDKBeforeProviderSideEffect(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	runtime.identity.SDKVersion = "other-sdk/2"
	commandID := "cmd_dddddddddddddddddddddddddddddddd"
	if _, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("unverified SDK version error = %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.ProviderStartIntents) != 0 || len(job.ProviderStartObservations) != 0 || runtime.startCalls != 0 {
		t.Fatalf("unverified SDK version caused external execution: intents=%d observations=%d starts=%d",
			len(job.ProviderStartIntents), len(job.ProviderStartObservations), runtime.startCalls)
	}
	runtime.identity.SDKVersion = "test-sdk/1"
	if _, _, _, result, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal); err != nil {
		t.Fatalf("retry after trusted SDK verification: %v", err)
	} else if result.SessionID != "session_1" {
		t.Fatalf("unexpected Start result: %#v", result)
	}
	if runtime.startCalls != 1 || runtime.lookupCalls != 0 {
		t.Fatalf("preflight retry caused unexpected calls: starts=%d lookups=%d", runtime.startCalls, runtime.lookupCalls)
	}
}

func TestProviderControllerDoesNotBlindlyRetryWhenNoSafeStartCapabilityExists(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		startErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	commandID := "cmd_cccccccccccccccccccccccccccccccc"
	_, _, _, _, _ = controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	runtime.capabilities = provider.Capabilities{Identity: runtime.identity}
	if _, _, _, _, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("retry without safe capability error = %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if runtime.startCalls != 1 || runtime.lookupCalls != 0 ||
		len(job.ProviderStartObservations) != 1 || job.ProviderStartObservations[0].Status != domain.ProviderStartUnknown {
		t.Fatalf("unsafe retry occurred: starts=%d lookups=%d observations=%#v", runtime.startCalls, runtime.lookupCalls, job.ProviderStartObservations)
	}
}

func TestProviderControllerStopsPlanningIntentOnlyAfterPersistingStopIntent(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_cccccccccccccccccccccccccccccccc", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	runtime.stopHook = func(ctx context.Context, request provider.StopRequest) error {
		job, err := database.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if len(job.ProviderStopIntents) != 1 || job.ProviderStopIntents[0].OperationID != request.OperationID ||
			job.ProviderStopIntents[0].SessionID != request.SessionID {
			return errors.New("Provider Stop was called before its intent was committed")
		}
		if len(job.ProviderStopObservations) != 0 {
			return errors.New("Stop observation exists before the Provider call")
		}
		return nil
	}
	receipt, intent, observation, err := controller.StopPlanning(
		context.Background(), jobID, execution.ID, "session_1",
		"cmd_ffffffffffffffffffffffffffffffff", domain.ProviderStopReasonPlanningComplete, principal,
	)
	if err != nil {
		t.Fatalf("StopPlanning: %v", err)
	}
	if receipt.Revision == 0 || intent.OperationID == "" || !observation.Stopped || !observation.Quiescent ||
		observation.EvidenceSHA256 != strings.Repeat("d", 64) || runtime.stopCalls != 1 {
		t.Fatalf("unexpected Stop result: receipt=%#v intent=%#v observation=%#v calls=%d",
			receipt, intent, observation, runtime.stopCalls)
	}
}

func TestProviderControllerDoesNotReplayAmbiguousStop(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{
		stopErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_cccccccccccccccccccccccccccccccc", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	stopCommandID := "cmd_ffffffffffffffffffffffffffffffff"
	_, intent, firstObservation, err := controller.StopPlanning(
		context.Background(), jobID, execution.ID, "session_1", stopCommandID,
		domain.ProviderStopReasonPlanningComplete, principal,
	)
	if err == nil || !providerStopObservationUnknown(firstObservation) || runtime.stopCalls != 1 {
		t.Fatalf("ambiguous Stop was not retained as UNKNOWN: observation=%#v calls=%d err=%v",
			firstObservation, runtime.stopCalls, err)
	}
	runtime.stopErr = nil
	_, retryIntent, retryObservation, err := controller.StopPlanning(
		context.Background(), jobID, execution.ID, "session_1", stopCommandID,
		domain.ProviderStopReasonPlanningComplete, principal,
	)
	if !errors.Is(err, provider.ErrStopUnconfirmed) || retryIntent.ID != intent.ID ||
		retryObservation.ID != firstObservation.ID || runtime.stopCalls != 1 {
		t.Fatalf("ambiguous Stop was replayed: intent=%#v observation=%#v calls=%d err=%v",
			retryIntent, retryObservation, runtime.stopCalls, err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.ProviderStopIntents) != 1 || len(job.ProviderStopObservations) != 1 {
		t.Fatalf("Stop retry changed immutable facts: intents=%d observations=%d",
			len(job.ProviderStopIntents), len(job.ProviderStopObservations))
	}
}

func TestProviderControllerTreatsExistingStopIntentWithoutObservationAsUnknown(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_cccccccccccccccccccccccccccccccc", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	stopCommandID := "cmd_ffffffffffffffffffffffffffffffff"
	if _, _, err := database.AddProviderStopIntent(
		context.Background(), jobID, job.Revision, execution.ID, "session_1", stopCommandID,
		domain.ProviderStopReasonPlanningComplete, principal,
	); err != nil {
		t.Fatalf("pre-create Stop intent: %v", err)
	}
	_, _, observation, err := controller.StopPlanning(
		context.Background(), jobID, execution.ID, "session_1", stopCommandID,
		domain.ProviderStopReasonPlanningComplete, principal,
	)
	if !errors.Is(err, provider.ErrStopUnconfirmed) || !providerStopObservationUnknown(observation) || runtime.stopCalls != 0 {
		t.Fatalf("existing intent was blindly dispatched: observation=%#v calls=%d err=%v",
			observation, runtime.stopCalls, err)
	}
}
