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
	identity     provider.Identity
	capabilities provider.Capabilities
	startResult  provider.StartResult
	startErr     error
	startCalls   int
	lookupCalls  int
	lookupResult provider.LookupResult
	lookupErr    error
	replayPage   provider.EventPage
	replayErr    error
	replayCalls  int
	stopResult   provider.StopResult
	stopErr      error
	stopCalls    int
	stopHook     func(context.Context, provider.StopRequest) error
	startHook    func(context.Context, provider.StartRequest) error
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
