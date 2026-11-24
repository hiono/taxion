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
	identity         provider.Identity
	capabilities     provider.Capabilities
	startResult      provider.StartResult
	startErr         error
	startCalls       int
	lookupCalls      int
	lookupResult     provider.LookupResult
	lookupErr        error
	sendResult       provider.TurnResult
	sendErr          error
	sendCalls        int
	sendHook         func(context.Context, provider.TurnRequest) error
	replayPage       provider.EventPage
	replayErr        error
	replayCalls      int
	stopResult       provider.StopResult
	stopErr          error
	stopCalls        int
	stopRequests     []provider.StopRequest
	stopFencingTokens map[string]uint64
	stopOperations   map[string]provider.StopResult
	resultResult     provider.Result
	resultErr        error
	resultCalls      int
	usageResult      provider.Usage
	usageErr         error
	usageCalls       int
	resumeResult     provider.ResumeResult
	resumeErr        error
	resumeCalls      int
	resumeHook       func(context.Context, provider.ResumeRequest) error
	stopHook         func(context.Context, provider.StopRequest) error
	startHook        func(context.Context, provider.StartRequest) error
}

func (f *fakePlanningProviderRuntime) Identity() provider.Identity { return f.identity }

func (f *fakePlanningProviderRuntime) Capabilities(context.Context) (provider.Capabilities, error) {
	return f.capabilities.Clone(), nil
}

func (f *fakePlanningProviderRuntime) Start(ctx context.Context, request provider.StartRequest) (provider.StartResult, error) {
	if f.identity.SDKVersion != "test-sdk/1" {
		return provider.StartResult{}, provider.ErrUnsupportedCapability
	}
	f.startCalls++
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
			ReceiptID: "receipt_turn_1", Status: domain.PlanningTurnCompleted, TokensUsed: 10,
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
	f.stopRequests = append(f.stopRequests, request)
	if f.capabilities.Has(provider.CapabilityStopDispatchFencing) {
		key := request.OperationID + "\x00" + request.SessionID
		if f.stopFencingTokens == nil {
			f.stopFencingTokens = make(map[string]uint64)
		}
		if request.FencingToken < f.stopFencingTokens[key] {
			return provider.StopResult{}, errors.New("stale Provider Stop dispatch fencing token")
		}
		if f.stopOperations == nil {
			f.stopOperations = make(map[string]provider.StopResult)
		}
		if request.FencingToken > f.stopFencingTokens[key] {
			f.stopFencingTokens[key] = request.FencingToken
		}
		if result, ok := f.stopOperations[key]; ok {
			return result, nil
		}
	}
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
	if f.capabilities.Has(provider.CapabilityStopDispatchFencing) && f.stopOperations != nil {
		f.stopOperations[request.OperationID+"\x00"+request.SessionID] = result
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

func newProviderControllerFixture(t *testing.T, runtime *fakePlanningProviderRuntime) (*ProviderController, *store.Store, auth.AuthenticatedPrincipal, string, string, []provider.ContextDocument) {
	t.Helper()
	ctx := context.Background()
	principal := newProviderTestPrincipal(t)
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "provider-controller.sqlite"))
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
		Budget:   domain.PlanningBudget{MaxTurns: 3, MaxTokens: 1000},
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
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStartLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		},
	}
	controller, err := NewProviderController(database, runtime)
	if err != nil {
		t.Fatalf("NewProviderController: %v", err)
	}
	documents := []provider.ContextDocument{{ID: "snapshot_1", SHA256: digest, Content: content}}
	return controller, database, principal, jobID, grant.ID, documents
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
		"session_1", "cmd_17171717171717171717171717171717", strings.Repeat("f", 64), 100, principal)
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
		"session_1", "cmd_25252525252525252525252525252525", strings.Repeat("f", 64), 100, principal); err != nil {
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
		"session_1", "cmd_28282828282828282828282828282828", strings.Repeat("f", 64), 100, principal); err != nil {
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
		"session_1", "cmd_31313131313131313131313131313131", strings.Repeat("f", 64), 100, principal); err != nil {
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
				if len(job.PlanningTurnObservations) != 0 {
					return errors.New("Turn observation was written before Runtime.Send")
				}
				return nil
			}
		}
		return errors.New("Turn intent was not persisted before Runtime.Send")
	}
	receipt, turn, observation, result, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1",
		"cmd_19191919191919191919191919191919", "inspect the accepted repository snapshot", 100, principal,
	)
	if err != nil || !intentSeen || runtime.sendCalls != 1 ||
		turn.OperationID != result.OperationID || turn.ID != result.TurnID ||
		observation.Status != domain.PlanningTurnCompleted || observation.CreatedRevision <= receipt.Revision ||
		result.Status != domain.PlanningTurnCompleted {
		t.Fatalf("SendPlanning: receipt=%#v turn=%#v observation=%#v result=%#v intentSeen=%v calls=%d err=%v",
			receipt, turn, observation, result, intentSeen, runtime.sendCalls, err)
	}
}

func TestProviderControllerReconcilesUnknownTurnWithLookupAndCompleteUsage(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	now := time.Now().UTC()
	runtime.capabilities.SendLookup = true
	runtime.capabilities.UsageIdentity = true
	runtime.capabilities.UsageCompleteness = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "controller-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
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
		"inspect the accepted repository snapshot", 100, principal,
	)
	if err == nil || unknown.Status != domain.PlanningTurnUnknown || runtime.sendCalls != 1 || receipt.Revision == 0 {
		t.Fatalf("ambiguous Send did not persist UNKNOWN: %#v %#v calls=%d err=%v", turn, unknown, runtime.sendCalls, err)
	}
	runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: operationID, Status: domain.PlanningTurnCompleted,
		ReceiptID: "receipt_turn_1", SessionID: "session_1", TurnID: turnID,
		UsageID: "usage_turn_reconcile", EvidenceSHA256: strings.Repeat("e", 64),
	}
	runtime.usageResult = provider.Usage{
		UsageID: "usage_turn_reconcile", Tokens: 18, Complete: true, EvidenceSHA256: strings.Repeat("f", 64),
	}
	receipt, reconciledTurn, completed, result, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1", commandID,
		"inspect the accepted repository snapshot", 100, principal,
	)
	if err != nil || receipt.Revision == 0 || reconciledTurn.ID != turn.ID ||
		completed.Status != domain.PlanningTurnCompleted || !completed.ReconcilesUnknown ||
		result.Status != domain.PlanningTurnCompleted || result.TokensUsed != 18 ||
		runtime.sendCalls != 1 || runtime.lookupCalls != 2 || runtime.usageCalls != 1 {
		t.Fatalf("same-Turn reconciliation failed: %#v %#v %#v calls(send=%d lookup=%d usage=%d) err=%v",
			receipt, reconciledTurn, completed, runtime.sendCalls, runtime.lookupCalls, runtime.usageCalls, err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.PlanningTurnIntents) != 1 || len(job.PlanningTurnObservations) != 2 ||
		job.PlanningTurnObservations[0].Status != domain.PlanningTurnUnknown ||
		!job.PlanningTurnObservations[1].ReconcilesUnknown ||
		len(job.ProviderUsageObservations) != 1 || !job.ProviderUsageObservations[0].Complete {
		t.Fatalf("reconciled Turn facts are inconsistent: %#v %v", job, err)
	}
}

func TestProviderControllerRejectsSendWithoutSafeCapability(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_22222222222222222222222222222222", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	if _, _, _, _, err := controller.SendPlanning(
		context.Background(), jobID, execution.ID, "session_1",
		"cmd_23232323232323232323232323232323", "inspect the accepted repository snapshot", 100, principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("SendPlanning error = %v, want ErrUnsupportedCapability", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil || len(job.PlanningTurnIntents) != 0 || runtime.sendCalls != 0 {
		t.Fatalf("unsupported Send created intent or called Runtime: %#v calls=%d err=%v", job.PlanningTurnIntents, runtime.sendCalls, err)
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
		"session_1", "cmd_13131313131313131313131313131313", strings.Repeat("f", 64), 100, principal)
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
		"session_1", "cmd_15151515151515151515151515151515", strings.Repeat("f", 64), 100, principal)
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
		if len(job.ProviderStartObservations) != 0 {
			return errors.New("Provider Start observation exists before the external call")
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
	if len(job.ProviderStartObservations) != 1 || job.ProviderStartObservations[0].Status != domain.ProviderStartAccepted {
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

func TestProviderControllerPersistsDefinitiveStartFailureWithoutLookupOrReplay(t *testing.T) {
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
		job.ProviderStartObservations[0].Status != domain.ProviderStartFailed ||
		job.ProviderStartObservations[0].EvidenceSHA256 == "" {
		t.Fatalf("definitive Start failure was not persisted as FAILED: %#v", job.ProviderStartObservations)
	}
	_, _, _, _, err = controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err == nil || !strings.Contains(err.Error(), "definitively rejected") {
		t.Fatalf("retry after definitive Start failure = %v", err)
	}
	if runtime.startCalls != 1 || runtime.lookupCalls != 0 {
		t.Fatalf("definitive failure was retried or looked up: starts=%d lookups=%d", runtime.startCalls, runtime.lookupCalls)
	}
}

func TestProviderControllerReconcilesUnknownToFailedAfterDefinitiveRetry(t *testing.T) {
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
	if len(job.ProviderStartObservations) != 2 ||
		job.ProviderStartObservations[0].Status != domain.ProviderStartUnknown ||
		job.ProviderStartObservations[1].Status != domain.ProviderStartFailed ||
		!job.ProviderStartObservations[1].ReconcilesUnknown {
		t.Fatalf("UNKNOWN was not terminally reconciled as FAILED: %#v", job.ProviderStartObservations)
	}
	_, _, _, _, err = controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal)
	if err == nil || !strings.Contains(err.Error(), "definitively rejected") {
		t.Fatalf("retry after terminal FAILED observation = %v", err)
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
	if len(job.ProviderStartIntents) != 1 || len(job.ProviderStartObservations) != 0 || runtime.startCalls != 0 {
		t.Fatalf("unverified SDK version caused external execution: intents=%d observations=%d starts=%d",
			len(job.ProviderStartIntents), len(job.ProviderStartObservations), runtime.startCalls)
	}
	runtime.identity.SDKVersion = "test-sdk/1"
	runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: domain.ProviderStartOperationID(commandID),
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("f", 64),
	}
	if _, _, _, result, err := controller.StartPlanning(context.Background(), jobID, grantID, commandID, documents, principal); err != nil {
		t.Fatalf("retry after trusted SDK verification: %v", err)
	} else if result.SessionID != "session_1" {
		t.Fatalf("unexpected Start result: %#v", result)
	}
	if runtime.startCalls != 1 || runtime.lookupCalls != 1 {
		t.Fatalf("SDK retry did not use lookup-before-start: starts=%d lookups=%d", runtime.startCalls, runtime.lookupCalls)
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

func TestProviderControllerDispatchesTaskCoderStartWithDurableIntentAndReconcilesUnknown(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	executionController, err := NewExecutionControllerWithWorktree(database, assessor, &fakeTaskWorktreeVerifier{})
	if err != nil {
		t.Fatalf("NewExecutionController: %v", err)
	}
	executionController.now = func() time.Time { return now }
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_b1111111111111111111111111111111",
		Role: domain.RoleCoder, Effect: "repository.write",
		Resource: revision.Contract.TargetRepositories[0], ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       2, ReservedTokens: 500, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(ctx, permissionRequest)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	_, execution, intent, err := executionController.StartTaskExecution(
		ctx, permission.ID, "cmd_b2222222222222222222222222222222", permissionRequest,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	runtime := &fakePlanningProviderRuntime{
		identity: provider.Identity{
			ProviderID: permission.ProviderID, AccountID: permission.AccountID,
			SDKVersion: "test-sdk/1", ConfigurationSHA256: permission.ConfigurationSHA256,
		},
		lookupResult: provider.LookupResult{
			Found: false, OperationID: intent.ID, Status: provider.LookupStatusNotFound,
			EvidenceSHA256: strings.Repeat("f", 64),
		},
	}
	runtime.capabilities = provider.Capabilities{
		Identity: runtime.identity, StartLookup: true, StartIdempotency: true, TaskWorktree: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilityStartLookup, Identity: runtime.identity, TestID: "task-start-test",
				EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity, TestID: "task-start-test",
				EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityTaskWorktree, Identity: runtime.identity, TestID: "task-worktree-test",
				EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		},
	}
	controller, err := NewProviderControllerWithExecutionAndWorktree(database, runtime, executionController, &fakeTaskWorktreeVerifier{})
	if err != nil {
		t.Fatalf("NewProviderControllerWithExecution: %v", err)
	}
	runtime.startHook = func(callCtx context.Context, request provider.StartRequest) error {
		persisted, getErr := database.GetJob(callCtx, jobID)
		if getErr != nil {
			return getErr
		}
		if len(persisted.ProviderStartIntents) == 0 ||
			persisted.ProviderStartIntents[len(persisted.ProviderStartIntents)-1].ID != request.OperationID {
			return errors.New("Task Provider Start was called before intent persistence")
		}
		if len(persisted.WorktreeAssignments) != 1 {
			return errors.New("Task Provider Start was called without a persisted Worktree assignment")
		}
		assignment := persisted.WorktreeAssignments[0]
		if request.RepositoryID != assignment.RepositoryID || request.WorktreePath != assignment.WorktreePath ||
			request.BaselineCommit != assignment.BaselineCommit {
			return errors.New("Task Provider Start request is not bound to its persisted Worktree")
		}
		if request.RoleExecutionID != execution.ID || request.RequestSHA256 != permission.RequestSHA256 ||
			request.PromptSHA256 != sha256String(request.Prompt) || !strings.Contains(request.Prompt, task.Title) {
			return errors.New("Task Provider Start request is not derived from the persisted Task and permission")
		}
		return nil
	}
	observation, result, err := controller.DispatchTaskExecution(ctx, jobID, execution.ID, permission.ID, permissionRequest)
	if err != nil {
		t.Fatalf("DispatchTaskExecution: %v", err)
	}
	if observation.Status != domain.ProviderStartAccepted || result.OperationID != intent.ID ||
		result.SessionID == "" || runtime.lookupCalls != 1 || runtime.startCalls != 1 {
		t.Fatalf("Task Provider Start was not accepted exactly once: observation=%#v result=%#v lookups=%d starts=%d",
			observation, result, runtime.lookupCalls, runtime.startCalls)
	}
	retryObservation, retryResult, err := controller.DispatchTaskExecution(ctx, jobID, execution.ID, permission.ID, permissionRequest)
	if err != nil || retryObservation.ID != observation.ID || retryResult != result ||
		runtime.lookupCalls != 1 || runtime.startCalls != 1 {
		t.Fatalf("exact Task Start replay changed terminal evidence or redispatched: %#v %#v %v", retryObservation, retryResult, err)
	}
}

func TestProviderControllerReconcilesTaskCoderStartUnknownBySameOperationID(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	executionController, err := NewExecutionControllerWithWorktree(database, assessor, &fakeTaskWorktreeVerifier{})
	if err != nil {
		t.Fatalf("NewExecutionController: %v", err)
	}
	executionController.now = func() time.Time { return now }
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_b3333333333333333333333333333333",
		Role: domain.RoleCoder, Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       2, ReservedTokens: 500, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(ctx, request)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	_, execution, intent, err := executionController.StartTaskExecution(
		ctx, permission.ID, "cmd_b4444444444444444444444444444444", request,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	runtime := &fakePlanningProviderRuntime{
		identity: provider.Identity{
			ProviderID: permission.ProviderID, AccountID: permission.AccountID,
			SDKVersion: "test-sdk/1", ConfigurationSHA256: permission.ConfigurationSHA256,
		},
		lookupResult: provider.LookupResult{
			Found: false, OperationID: intent.ID, Status: provider.LookupStatusNotFound,
			EvidenceSHA256: strings.Repeat("c", 64),
		},
		startErr: &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout},
	}
	runtime.capabilities = provider.Capabilities{
		Identity: runtime.identity, StartLookup: true, TaskWorktree: true,
		Evidence: []provider.CapabilityEvidence{{
			Capability: provider.CapabilityStartLookup, Identity: runtime.identity,
			TestID: "task-start-test", EvidenceSHA256: strings.Repeat("d", 64),
			EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
		}, {
			Capability: provider.CapabilityTaskWorktree, Identity: runtime.identity,
			TestID: "task-worktree-test", EvidenceSHA256: strings.Repeat("b", 64),
			EvidenceLevel: provider.EvidenceLive, VerifiedAt: now,
		}},
	}
	controller, err := NewProviderControllerWithExecutionAndWorktree(database, runtime, executionController, &fakeTaskWorktreeVerifier{})
	if err != nil {
		t.Fatalf("NewProviderControllerWithExecution: %v", err)
	}
	unknown, _, err := controller.DispatchTaskExecution(ctx, jobID, execution.ID, permission.ID, request)
	if err == nil || unknown.Status != domain.ProviderStartUnknown || runtime.startCalls != 1 {
		t.Fatalf("ambiguous Task Start was not retained as UNKNOWN: %#v starts=%d err=%v", unknown, runtime.startCalls, err)
	}
	runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: intent.ID, Status: "RUNNING",
		ReceiptID: "receipt_1", SessionID: "session_1", EvidenceSHA256: strings.Repeat("e", 64),
	}
	runtime.startErr = nil
	// An expired permission cannot authorize a new Start, but it must not prevent
	// reconciliation of the already-issued OperationID.
	executionController.now = func() time.Time { return request.ExpiresAt.Add(time.Second) }
	accepted, result, err := controller.DispatchTaskExecution(ctx, jobID, execution.ID, permission.ID, request)
	if err != nil || accepted.Status != domain.ProviderStartAccepted || !accepted.ReconcilesUnknown ||
		result.OperationID != intent.ID || runtime.startCalls != 1 || runtime.lookupCalls != 2 {
		t.Fatalf("UNKNOWN Task Start was not reconciled by same OperationID Lookup: %#v %#v starts=%d lookups=%d err=%v",
			accepted, result, runtime.startCalls, runtime.lookupCalls, err)
	}
}

func TestProviderControllerRejectsReviewerBeforeMutableWorktreePreparation(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Round(0)
	worktrees := &fakeTaskWorktreeVerifier{}
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: false, ValidUntil: now.Add(45 * time.Minute),
	}}
	executionController, err := NewExecutionControllerWithWorktree(database, assessor, worktrees)
	if err != nil {
		t.Fatalf("NewExecutionControllerWithWorktree: %v", err)
	}
	executionController.now = func() time.Time { return now }
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_b5555555555555555555555555555555",
		Role: domain.RoleReviewer, Effect: "repository.read", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 200, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(ctx, request)
	if err != nil {
		t.Fatalf("Authorize Reviewer: %v", err)
	}
	before, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Reviewer start: %v", err)
	}
	if _, _, _, err := executionController.StartTaskExecution(
		ctx, permission.ID, "cmd_b6666666666666666666666666666666", request,
	); err == nil || !strings.Contains(err.Error(), "immutable JobOutput snapshot") {
		t.Fatalf("Reviewer Start without immutable snapshot boundary = %v", err)
	}
	after, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Reviewer start: %v", err)
	}
	if worktrees.prepareCalls != 0 || worktrees.verifyCalls != 0 ||
		after.Revision != before.Revision || len(after.RoleExecutions) != len(before.RoleExecutions) ||
		len(after.ProviderStartIntents) != len(before.ProviderStartIntents) ||
		len(after.WorktreeAssignments) != len(before.WorktreeAssignments) {
		t.Fatalf("Reviewer start crossed mutable Worktree boundary: prepare=%d verify=%d before=%#v after=%#v",
			worktrees.prepareCalls, worktrees.verifyCalls, before, after)
	}
}

type failOnSecondTaskWorktreeVerification struct {
	verifyCalls int
}

func (v *failOnSecondTaskWorktreeVerification) Verify(context.Context, string, domain.Task, domain.WorktreeSnapshot) error {
	v.verifyCalls++
	if v.verifyCalls == 2 {
		return errors.New("Worktree changed after Provider lookup")
	}
	return nil
}

func TestProviderControllerRevalidatesTaskWorktreeAfterLookupBeforeStart(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	executionController, err := NewExecutionControllerWithWorktree(database, assessor, &fakeTaskWorktreeVerifier{})
	if err != nil {
		t.Fatalf("NewExecutionController: %v", err)
	}
	executionController.now = func() time.Time { return now }
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_c1111111111111111111111111111111",
		Role: domain.RoleCoder, Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       2, ReservedTokens: 500, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(ctx, permissionRequest)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	_, execution, intent, err := executionController.StartTaskExecution(
		ctx, permission.ID, "cmd_c2222222222222222222222222222222", permissionRequest,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	runtime := &fakePlanningProviderRuntime{
		identity: provider.Identity{
			ProviderID: permission.ProviderID, AccountID: permission.AccountID,
			SDKVersion: "test-sdk/1", ConfigurationSHA256: permission.ConfigurationSHA256,
		},
		lookupResult: provider.LookupResult{
			Found: false, OperationID: intent.ID, Status: provider.LookupStatusNotFound,
			EvidenceSHA256: strings.Repeat("f", 64),
		},
	}
	runtime.capabilities = provider.Capabilities{
		Identity: runtime.identity, StartLookup: true, StartIdempotency: true, TaskWorktree: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilityStartLookup, Identity: runtime.identity, TestID: "task-start-test",
				EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity, TestID: "task-start-test",
				EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityTaskWorktree, Identity: runtime.identity, TestID: "task-worktree-test",
				EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		},
	}
	verifier := &failOnSecondTaskWorktreeVerification{}
	controller, err := NewProviderControllerWithExecutionAndWorktree(database, runtime, executionController, verifier)
	if err != nil {
		t.Fatalf("NewProviderControllerWithExecutionAndWorktree: %v", err)
	}
	_, _, err = controller.DispatchTaskExecution(ctx, jobID, execution.ID, permission.ID, permissionRequest)
	if err == nil || !strings.Contains(err.Error(), "immediately before Provider Start") {
		t.Fatalf("changed Worktree was not rejected before Provider Start: %v", err)
	}
	if verifier.verifyCalls != 2 || runtime.lookupCalls != 1 || runtime.startCalls != 0 {
		t.Fatalf("Worktree revalidation / Provider calls = verifies:%d lookups:%d starts:%d",
			verifier.verifyCalls, runtime.lookupCalls, runtime.startCalls)
	}
	persisted, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if _, found := latestStartObservation(persisted, intent.ID); found {
		t.Fatal("failed pre-dispatch Worktree revalidation appended a Provider Start observation")
	}
}
