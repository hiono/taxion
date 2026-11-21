package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
	"golang.org/x/oauth2"
)

func TestJobCreateShowAndList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), []string{"dev", "job", "create", "--store", path, "--request", "Help me turn these notes into a working plan."}, &stdout, &stderr, VersionInfo{Version: "test"})
	if code != 0 {
		t.Fatalf("create exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "State: DRAFT") {
		t.Fatalf("create output = %q, want DRAFT state", stdout.String())
	}
	lines := strings.Split(stdout.String(), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "ID: ") {
		t.Fatalf("unexpected create output: %q", stdout.String())
	}
	jobID := strings.TrimPrefix(lines[1], "ID: ")

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"job", "show", "--store", path, jobID}, &stdout, &stderr, VersionInfo{})
	if code != 0 {
		t.Fatalf("show exit code = %d, stderr = %s", code, stderr.String())
	}
	var job domain.Job
	if err := json.Unmarshal(stdout.Bytes(), &job); err != nil {
		t.Fatalf("show output is not Job JSON: %v", err)
	}
	if job.ID != jobID || job.State != domain.JobDraft {
		t.Fatalf("show Job = %#v", job)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"job", "list", "--store", path}, &stdout, &stderr, VersionInfo{})
	if code != 0 {
		t.Fatalf("list exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), jobID) {
		t.Fatalf("list output = %q, want Job ID", stdout.String())
	}
}

func TestJobListTruncatesLongUnicodeRequestWithoutBreakingUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	request := strings.Repeat("日本語", 30)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"dev", "job", "create", "--store", path, "--request", request}, &stdout, &stderr, VersionInfo{})
	if code != 0 {
		t.Fatalf("create exit code = %d, stderr = %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{"job", "list", "--store", path}, &stdout, &stderr, VersionInfo{})
	if code != 0 {
		t.Fatalf("list exit code = %d, stderr = %s", code, stderr.String())
	}
	if !utf8.Valid(stdout.Bytes()) {
		t.Fatalf("list output is not valid UTF-8: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "...") {
		t.Fatalf("list output = %q, want truncated request marker", stdout.String())
	}
}

func TestJobCreateRequiresRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"dev", "job", "create"}, &stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--request") {
		t.Fatalf("stderr = %q, want request guidance", stderr.String())
	}
}

func TestProductionJobCreateRequiresOIDCConfigurationWithoutCreatingStore(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	t.Setenv("TAXION_OIDC_CLIENT_ID", "")
	t.Setenv("TAXION_OIDC_AUDIENCE", "")
	t.Setenv("TAXION_OIDC_REDIRECT_URI", "")
	path := filepath.Join(t.TempDir(), "must-not-exist", "jobs.sqlite")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"job", "create", "--store", path, "--request", "trusted request"}, &stdout, &stderr, VersionInfo{})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "TAXION_OIDC_ISSUER") {
		t.Fatalf("stderr = %q, want OIDC configuration guidance", stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store path exists after authentication failure: stat err = %v", err)
	}
}

func TestTrustedJobCreateRejectsEmptyAuthenticatedPrincipalBeforeOpeningStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "must-not-exist", "jobs.sqlite")
	var stdout, stderr bytes.Buffer
	code := createTrustedJobWithPrincipal(context.Background(), "request", path, "cmd_invalid", time.Now().UTC(), auth.AuthenticatedPrincipal{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("createTrustedJobWithPrincipal() code = %d, want 1", code)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store path exists after invalid principal: stat err = %v", err)
	}
}

func TestAuthLoginRequiresOIDCConfiguration(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	t.Setenv("TAXION_OIDC_CLIENT_ID", "")
	t.Setenv("TAXION_OIDC_AUDIENCE", "")
	t.Setenv("TAXION_OIDC_REDIRECT_URI", "")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"auth", "login"}, &stdout, &stderr, VersionInfo{})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "TAXION_OIDC_ISSUER") {
		t.Fatalf("stderr = %q, want issuer configuration guidance", stderr.String())
	}
}

func TestProductionJobCreateRejectsInvalidUTF8BeforeAuthentication(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	path := filepath.Join(t.TempDir(), "must-not-exist", "jobs.sqlite")
	invalidRequest := string([]byte{0xff, 0xfe})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"job", "create", "--store", path, "--request", invalidRequest}, &stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "valid UTF-8") {
		t.Fatalf("stderr = %q, want UTF-8 validation error", stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store path exists after invalid request: stat err = %v", err)
	}
}

func TestAuthorizePlanningRequiresScopeBeforeAuthentication(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	t.Setenv("TAXION_OIDC_CLIENT_ID", "")
	path := filepath.Join(t.TempDir(), "must-not-exist", "jobs.sqlite")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"job", "authorize-planning", "--store", path, "job_missing"},
		&stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Provider") {
		t.Fatalf("stderr = %q, want required scope guidance", stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store path exists after invalid scope: stat err = %v", err)
	}
}

func TestAuthorizePlanningRequiresOIDCBeforeOpeningStore(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	t.Setenv("TAXION_OIDC_CLIENT_ID", "")
	t.Setenv("TAXION_OIDC_AUDIENCE", "")
	t.Setenv("TAXION_OIDC_REDIRECT_URI", "")
	path := filepath.Join(t.TempDir(), "must-not-exist", "jobs.sqlite")
	args := []string{
		"job", "authorize-planning",
		"--provider", "provider.test",
		"--account", "account.test",
		"--configuration-sha256", strings.Repeat("a", 64),
		"--repository", "https://github.com/example/project",
		"--commit", strings.Repeat("b", 40),
		"--read-only-mode", "snapshot",
		"--read-only-reference", "snapshot_1",
		"--read-only-sha256", strings.Repeat("c", 64),
		"--max-turns", "8",
		"--max-tokens", "100000",
		"--deadline", time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		"--store", path,
		"job_missing",
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr, VersionInfo{})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "TAXION_OIDC_ISSUER") {
		t.Fatalf("stderr = %q, want OIDC configuration guidance", stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store path exists after authentication failure: stat err = %v", err)
	}
}

func TestAuthorizePlanningWithPrincipalRejectsEmptyPrincipalBeforeOpeningStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "must-not-exist", "jobs.sqlite")
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_1",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 8, MaxTokens: 100000},
		Deadline: time.Now().UTC().Add(time.Hour),
	}
	var stdout, stderr bytes.Buffer
	code := authorizePlanningWithPrincipal(context.Background(), "job_missing", request,
		"cmd_11111111111111111111111111111111", path, auth.AuthenticatedPrincipal{},
		&stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store path exists after invalid principal: stat err = %v", err)
	}
}

type cliSynchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *cliSynchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *cliSynchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func newCLIAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	trustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC signing key: %v", err)
	}
	provider := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{
		PublicKey: trustedKey.Public(), KeyID: "taxion-cli-test-key", Algorithm: oidc.RS256,
	}}}
	var mu sync.Mutex
	var challenge, nonce string
	mux := http.NewServeMux()
	mux.Handle("/.well-known/openid-configuration", provider)
	mux.Handle("/keys", provider)
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
			"iss": "https://" + r.Host, "aud": "taxion-cli-test", "sub": "subject-cli-test",
			"exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": expectedNonce,
		})
		if err != nil {
			http.Error(w, "encode claims", http.StatusInternalServerError)
			return
		}
		token := oidctest.SignIDToken(trustedKey, "taxion-cli-test-key", oidc.RS256, string(claims))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token", "token_type": "Bearer",
			"expires_in": 300, "id_token": token,
		})
	})
	server := httptest.NewTLSServer(mux)
	provider.SetIssuer(server.URL)
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
		server.Close()
	})
	return server
}

func completeCLIAuth(t *testing.T, server *httptest.Server, output *cliSynchronizedBuffer) {
	t.Helper()
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
		t.Fatalf("OIDC authorization URL was not written; output=%q", output.String())
	}
	response, err := server.Client().Get(authorizationURL)
	if err != nil {
		t.Fatalf("complete fake OIDC authorization: %v", err)
	}
	_ = response.Body.Close()
}

func runCLIWithFakeOIDC(t *testing.T, server *httptest.Server, args []string) (int, string, string) {
	t.Helper()
	stdout := &cliSynchronizedBuffer{}
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(context.Background(), args, stdout, &stderr, VersionInfo{Version: "test"})
	}()
	completeCLIAuth(t, server, stdout)
	select {
	case code := <-done:
		return code, stdout.String(), stderr.String()
	case <-time.After(10 * time.Second):
		t.Fatal("CLI command did not finish after OIDC callback")
		return 1, stdout.String(), stderr.String()
	}
}

func TestAuthorizePlanningIssuesBoundedGrantAndSupportsIdempotentRetry(t *testing.T) {
	server := newCLIAuthServer(t)
	t.Setenv("TAXION_OIDC_ISSUER", server.URL)
	t.Setenv("TAXION_OIDC_CLIENT_ID", "taxion-cli-test")
	t.Setenv("TAXION_OIDC_AUDIENCE", "taxion-cli-test")
	t.Setenv("TAXION_OIDC_REDIRECT_URI", "http://127.0.0.1:0/callback")
	path := filepath.Join(t.TempDir(), "jobs.sqlite")

	code, output, stderr := runCLIWithFakeOIDC(t, server, []string{
		"job", "create", "--store", path, "--request", "Create a bounded planning grant for this request.",
	})
	if code != 0 {
		t.Fatalf("job create exit code = %d, stderr=%q", code, stderr)
	}
	var jobID string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "ID: ") {
			jobID = strings.TrimPrefix(line, "ID: ")
			break
		}
	}
	if jobID == "" {
		t.Fatalf("trusted Job ID missing from output: %q", output)
	}

	deadline := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	args := []string{
		"job", "authorize-planning",
		"--store", path,
		"--provider", "provider.test",
		"--account", "account.primary",
		"--configuration-sha256", strings.Repeat("a", 64),
		"--repository", "https://github.com/example/project",
		"--commit", strings.Repeat("b", 40),
		"--read-only-mode", "snapshot",
		"--read-only-reference", "snapshot_123",
		"--read-only-sha256", strings.Repeat("c", 64),
		"--max-turns", "8",
		"--max-tokens", "100000",
		"--deadline", deadline,
		jobID,
	}
	code, output, stderr = runCLIWithFakeOIDC(t, server, args)
	if code != 0 {
		t.Fatalf("authorize-planning exit code = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(output, "PlanningAuthorization issued") || !strings.Contains(output, "State: DRAFT") {
		t.Fatalf("authorize-planning output = %q", output)
	}
	var commandID string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Command ID: ") {
			commandID = strings.TrimPrefix(line, "Command ID: ")
			break
		}
	}
	if commandID == "" {
		t.Fatalf("Command ID missing from output: %q", output)
	}
	var authorizationID string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "ID: plan_auth_") {
			authorizationID = strings.TrimPrefix(line, "ID: ")
			break
		}
	}
	if authorizationID == "" {
		t.Fatalf("PlanningAuthorization ID missing from output: %q", output)
	}

	retryArgs := append(append([]string(nil), args[:len(args)-1]...), "--command-id", commandID, jobID)
	code, retryOutput, stderr := runCLIWithFakeOIDC(t, server, retryArgs)
	if code != 0 {
		t.Fatalf("idempotent authorize-planning retry exit code = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(retryOutput, "Revision: 2") {
		t.Fatalf("retry output = %q, want original revision 2", retryOutput)
	}

	startArgs := []string{"job", "start-planning", "--store", path, "--authorization-id", authorizationID, jobID}
	code, startOutput, stderr := runCLIWithFakeOIDC(t, server, startArgs)
	if code != 0 {
		t.Fatalf("start-planning exit code = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(startOutput, "Taskless PLANNER Start intent persisted") ||
		!strings.Contains(startOutput, "State: DRAFT") || !strings.Contains(startOutput, "Operation ID: provider_op_") {
		t.Fatalf("start-planning output = %q", startOutput)
	}
	var startCommandID string
	for _, line := range strings.Split(startOutput, "\n") {
		if strings.HasPrefix(line, "Command ID: ") {
			startCommandID = strings.TrimPrefix(line, "Command ID: ")
			break
		}
	}
	if startCommandID == "" {
		t.Fatalf("start Command ID missing from output: %q", startOutput)
	}
	startRetryArgs := []string{"job", "start-planning", "--store", path,
		"--authorization-id", authorizationID, "--command-id", startCommandID, jobID}
	code, startRetryOutput, stderr := runCLIWithFakeOIDC(t, server, startRetryArgs)
	if code != 0 {
		t.Fatalf("idempotent start-planning retry exit code = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(startRetryOutput, "Revision: 3") {
		t.Fatalf("start retry output = %q, want original revision 3", startRetryOutput)
	}

	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open Job store: %v", err)
	}
	defer database.Close()
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.State != domain.JobDraft || job.Revision != 3 || len(job.PlanningAuthorizations) != 1 ||
		len(job.RoleExecutions) != 1 || len(job.ProviderStartIntents) != 1 {
		t.Fatalf("persisted Job = %#v", job)
	}
	grant := job.PlanningAuthorizations[0]
	if grant.CommandID != commandID || grant.Budget.MaxTurns != 8 || grant.Budget.MaxTokens != 100000 ||
		grant.ReadOnlyBoundary.Mode != domain.PlanningReadOnlyBoundarySnapshot ||
		grant.ReadOnlyBoundary.Reference != "snapshot_123" || grant.ReadOnlyBoundary.SHA256 != strings.Repeat("c", 64) {
		t.Fatalf("persisted PlanningAuthorization = %#v", grant)
	}
}

func TestAuthorizePlanningRejectsInvalidArgumentsBeforeAuthentication(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"job", "authorize-planning",
		"--provider", "provider.test",
		"--account", "account.primary",
		"--configuration-sha256", strings.Repeat("a", 64),
		"--repository", "https://github.com/example/project",
		"--commit", strings.Repeat("b", 40),
		"--read-only-mode", "snapshot",
		"--read-only-reference", "snapshot_123",
		"--read-only-sha256", strings.Repeat("c", 64),
		"--max-turns", "0",
		"--max-tokens", "100000",
		"--deadline", time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"job_missing",
	}, &stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("authorize-planning exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "TAXION_OIDC_ISSUER") {
		t.Fatalf("invalid arguments reached OIDC authentication: %q", stderr.String())
	}
}

func authenticateCLIPrincipal(t *testing.T, server *httptest.Server) auth.AuthenticatedPrincipal {
	t.Helper()
	authenticator, err := auth.NewOIDCAuthenticator(auth.OIDCConfig{
		Issuer: server.URL, ClientID: "taxion-cli-test", Audience: "taxion-cli-test",
		RedirectURI: "http://127.0.0.1:0/callback", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator: %v", err)
	}
	output := &cliSynchronizedBuffer{}
	type result struct {
		principal auth.AuthenticatedPrincipal
		err       error
	}
	done := make(chan result, 1)
	go func() {
		principal, err := authenticator.Authenticate(context.Background(), output)
		done <- result{principal: principal, err: err}
	}()
	completeCLIAuth(t, server, output)
	select {
	case completed := <-done:
		if completed.err != nil {
			t.Fatalf("Authenticate: %v", completed.err)
		}
		return completed.principal
	case <-time.After(10 * time.Second):
		t.Fatal("OIDC authentication did not complete")
		return auth.AuthenticatedPrincipal{}
	}
}

func TestJobApproveCLIRequiresOIDCAndApprovesExactRevision(t *testing.T) {
	server := newCLIAuthServer(t)
	t.Setenv("TAXION_OIDC_ISSUER", server.URL)
	t.Setenv("TAXION_OIDC_CLIENT_ID", "taxion-cli-test")
	t.Setenv("TAXION_OIDC_AUDIENCE", "taxion-cli-test")
	t.Setenv("TAXION_OIDC_REDIRECT_URI", "http://127.0.0.1:0/callback")
	principal := authenticateCLIPrincipal(t, server)

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	jobID := "job_cli_approval"
	if _, err := database.CreateTrustedDraftJob(
		ctx, jobID, "Deliver the approved bounded feature.",
		"cmd_78787878787878787878787878787878", principal.AuthenticatedAt(), principal,
	); err != nil {
		t.Fatalf("CreateTrustedDraftJob: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	now := time.Now().UTC()
	grantCommandID := "cmd_89898989898989898989898989898989"
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommandID,
		domain.PlanningAuthorizationRequest{
			ProviderID: "provider.test", AccountID: "account.primary",
			ConfigurationSHA256: strings.Repeat("a", 64),
			RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
			ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
				Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1",
				SHA256: strings.Repeat("c", 64),
			},
			Budget:   domain.PlanningBudget{MaxTurns: 5, MaxTokens: 5000},
			Deadline: now.Add(time.Hour),
		}, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grantCommandID, grant, principal); err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after grant: %v", err)
	}
	startCommandID := "cmd_90909090909090909090909090909090"
	_, execution, intent, err := database.StartTasklessPlanner(ctx, jobID, job.Revision, grant.ID, startCommandID, principal)
	if err != nil {
		t.Fatalf("StartTasklessPlanner: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Start intent: %v", err)
	}
	if _, _, err := database.AddProviderStartObservation(
		ctx, jobID, job.Revision, intent.ID,
		domain.ProviderStartObservationCommandID(intent.ID, domain.ProviderStartAccepted, "session_1", "receipt_1", strings.Repeat("d", 64), false),
		domain.ProviderStartAccepted, "session_1", "receipt_1", strings.Repeat("d", 64), false, principal,
	); err != nil {
		t.Fatalf("AddProviderStartObservation: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Turn: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(
		ctx, jobID, job.Revision, execution.ID, "session_1",
		"cmd_d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", strings.Repeat("a", 64), 100, principal,
	)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Turn observation: %v", err)
	}
	if _, _, err := database.AddPlanningTurnObservation(
		ctx, jobID, job.Revision, turn.ID,
		"cmd_e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5",
		domain.PlanningTurnCompleted, 10, strings.Repeat("b", 64), false, principal,
	); err != nil {
		t.Fatalf("AddPlanningTurnObservation: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Result: %v", err)
	}
	resultContent := "Final Planner result"
	resultSum := sha256.Sum256([]byte(resultContent))
	resultDigest := hex.EncodeToString(resultSum[:])
	_, sourceResult, err := database.AddProviderResultObservation(
		ctx, jobID, job.Revision, execution.ID, turn.OperationID, turn.ID, "session_1",
		resultContent, resultDigest, true, strings.Repeat("c", 64), principal,
	)
	if err != nil {
		t.Fatalf("AddProviderResultObservation: %v", err)
	}
	contract := domain.JobContract{
		Purpose:                 "Deliver the approved bounded feature",
		Constraints:             []string{"Preserve the authorization boundary"},
		TargetRepositories:      []string{"https://github.com/example/project"},
		Workspaces:              []string{"workspace/main"},
		AcceptanceCriteria:      []string{"Linux tests pass"},
		RequiredEvidence:        []string{"Linux CI"},
		Budget:                  domain.JobContractBudget{MaxTurns: 5, MaxTokens: 5000},
		ConcurrencyCeiling:      1,
		Deadline:                now.Add(45 * time.Minute),
		ProhibitedEffects:       []string{"publish"},
		ApprovalRequiredEffects: []string{"external.delete"},
		ConfigurationSHA256:     strings.Repeat("a", 64),
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before proposal: %v", err)
	}
	_, revision, err := database.AddJobContractRevision(
		ctx, jobID, job.Revision, "cmd_b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", execution.ID,
		sourceResult.ID, sourceResult.ContentSHA256, contract,
	)
	if err != nil {
		t.Fatalf("AddJobContractRevision: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close setup Store: %v", err)
	}

	approvalCommandID := "cmd_c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
	args := []string{
		"job", "approve", "--store", path,
		"--revision", "1",
		"--canonical-hash", revision.CanonicalHash,
		"--canonicalization-version", "1",
		"--reason", "Reviewed and approved the exact bounded scope",
		"--command-id", approvalCommandID,
		jobID,
	}
	code, output, stderr := runCLIWithFakeOIDC(t, server, args)
	if code != 0 {
		t.Fatalf("job approve exit code = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(output, "JobApproval recorded") ||
		!strings.Contains(output, "Canonical Hash: "+revision.CanonicalHash) ||
		!strings.Contains(output, "State: ACTIVE") {
		t.Fatalf("job approve output = %q", output)
	}

	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen Store: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 ||
		job.JobApprovals[0].CanonicalHash != revision.CanonicalHash ||
		job.JobApprovals[0].ApproverPrincipalID != principal.PrincipalID() {
		t.Fatalf("Job after approval = %#v err=%v", job, err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close Store: %v", err)
	}

	code, retryOutput, stderr := runCLIWithFakeOIDC(t, server, args)
	if code != 0 || !strings.Contains(retryOutput, "JobApproval recorded") {
		t.Fatalf("idempotent job approve retry: code=%d output=%q stderr=%q", code, retryOutput, stderr)
	}
	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen Store after retry: %v", err)
	}
	defer database.Close()
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 {
		t.Fatalf("approval retry appended another fact: approvals=%d state=%s err=%v", len(job.JobApprovals), job.State, err)
	}
}

func TestJobApproveCLIRejectsMissingExactRevisionFieldsBeforeAuthentication(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	path := filepath.Join(t.TempDir(), "must-not-exist", "jobs.sqlite")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"job", "approve", "--store", path, "job_missing"},
		&stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Store path exists after invalid approval request: %v", err)
	}
}
