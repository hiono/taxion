package cli

import (
	"bufio"
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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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

func TestJobMessageSavesAndRetriesOnlyForOwner(t *testing.T) {
	server := newCLIAuthServer(t)
	t.Setenv("TAXION_OIDC_ISSUER", server.URL)
	t.Setenv("TAXION_OIDC_CLIENT_ID", "taxion-cli-test")
	t.Setenv("TAXION_OIDC_AUDIENCE", "taxion-cli-test")
	t.Setenv("TAXION_OIDC_REDIRECT_URI", "http://127.0.0.1:0/callback")
	principal := authenticateCLIPrincipal(t, server)
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	ctx := context.Background()
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	const jobID = "job_cli_message"
	_, err = database.CreateTrustedDraftJob(ctx, jobID, "Plan the work.",
		"cmd_11111111111111111111111111111111", principal.AuthenticatedAt(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	const commandID = "cmd_22222222222222222222222222222222"
	args := []string{"job", "message", "--store", path, "--body", "安全性を優先してください。",
		"--command-id", commandID, jobID}
	for range 2 {
		code, output, stderr := runCLIWithFakeOIDC(t, server, args)
		if code != 0 || !strings.Contains(output, "Job message saved (not delivered)") ||
			!strings.Contains(output, "Revision: 2") {
			t.Fatalf("job message exit=%d, output=%q, stderr=%q", code, output, stderr)
		}
	}
	other := newCLIAuthServer(t)
	t.Setenv("TAXION_OIDC_ISSUER", other.URL)
	code, _, stderr := runCLIWithFakeOIDC(t, other, args)
	if code != 1 || !strings.Contains(stderr, "does not own the Job") {
		t.Fatalf("non-owner exit=%d, stderr=%q", code, stderr)
	}

	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.JobMessages) != 1 || job.JobMessages[0].Body != "安全性を優先してください。" ||
		job.JobMessages[0].Recipient.ID != domain.ControllerActorID || job.Revision != 2 {
		t.Fatalf("persisted Job messages = %#v, revision = %d", job.JobMessages, job.Revision)
	}
}

func TestJobMessageRejectsMissingBodyBeforeAuthentication(t *testing.T) {
	t.Setenv("TAXION_OIDC_ISSUER", "")
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"job", "message", "job_missing"}, &stdout, &stderr, VersionInfo{}); code != 2 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
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
	const messageCommand = "cmd_33333333333333333333333333333333"
	roleID := job.RoleExecutions[0].ID
	code, _, stderr = runCLIWithFakeOIDC(t, server, []string{"job", "message", "--store", path,
		"--body", "Please clarify the constraints.", "--to", roleID, "--command-id", messageCommand, jobID})
	if code != 0 {
		t.Fatalf("message to Planner exit=%d, stderr=%q", code, stderr)
	}
	code, _, stderr = runCLIWithFakeOIDC(t, server, []string{"job", "message", "--store", path,
		"--body", "Additional context.", "--reply-to", domain.JobMessageID(messageCommand), jobID})
	if code != 0 {
		t.Fatalf("message reply exit=%d, stderr=%q", code, stderr)
	}
	code, _, _ = runCLIWithFakeOIDC(t, server, []string{"job", "message", "--store", path,
		"--body", "Invalid recipient.", "--to", "missing_role", jobID})
	if code != 1 {
		t.Fatalf("message to missing role exit=%d, want 1", code)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.JobMessages) != 2 || job.JobMessages[0].Recipient.ID != roleID ||
		job.JobMessages[0].Recipient.Kind != domain.ActorPlanner ||
		job.JobMessages[1].ReplyToMessageID != job.JobMessages[0].ID {
		t.Fatalf("persisted messages = %#v", job.JobMessages)
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
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 {
		t.Fatalf("approval retry appended another fact: approvals=%d state=%s err=%v", len(job.JobApprovals), job.State, err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close Store after approval retry: %v", err)
	}

	amendedContract := contract
	amendedContract.Purpose = "Deliver the explicitly amended bounded feature"
	amendedContract.Budget.MaxTokens++
	amendedContract.Deadline = time.Now().UTC().Add(2 * time.Hour)
	contractJSON, err := json.Marshal(amendedContract)
	if err != nil {
		t.Fatalf("Marshal amended JobContract: %v", err)
	}
	contractPath := filepath.Join(t.TempDir(), "amended-job-contract.json")
	if err := os.WriteFile(contractPath, contractJSON, 0o600); err != nil {
		t.Fatalf("write amended JobContract: %v", err)
	}
	amendmentCommandID := "cmd_d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1"
	amendArgs := []string{
		"job", "amend", "--store", path,
		"--revision", "1",
		"--canonical-hash", revision.CanonicalHash,
		"--reason", "Human-reviewed change to the approved target",
		"--contract-file", contractPath,
		"--command-id", amendmentCommandID,
		jobID,
	}
	code, amendOutput, stderr := runCLIWithFakeOIDC(t, server, amendArgs)
	if code != 0 || !strings.Contains(amendOutput, "Origin: HUMAN_AMENDMENT") ||
		!strings.Contains(amendOutput, "State at amendment: DRAFT") || !strings.Contains(amendOutput, "Current Job State: DRAFT") {
		t.Fatalf("job amend: code=%d output=%q stderr=%q", code, amendOutput, stderr)
	}
	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen Store after amendment: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobDraft || len(job.JobContractRevisions) != 2 ||
		len(job.JobApprovals) != 1 || job.JobContractRevisions[1].Origin != domain.JobContractRevisionOriginHumanAmendment {
		t.Fatalf("Job after amendment = %#v err=%v", job, err)
	}
	amendedRevision := job.JobContractRevisions[1]
	if err := database.Close(); err != nil {
		t.Fatalf("close Store after amendment: %v", err)
	}

	code, amendRetryOutput, stderr := runCLIWithFakeOIDC(t, server, amendArgs)
	if code != 0 || !strings.Contains(amendRetryOutput, "Origin: HUMAN_AMENDMENT") {
		t.Fatalf("idempotent job amend retry: code=%d output=%q stderr=%q", code, amendRetryOutput, stderr)
	}
	approvalAmendedArgs := []string{
		"job", "approve", "--store", path,
		"--revision", "2",
		"--canonical-hash", amendedRevision.CanonicalHash,
		"--canonicalization-version", "1",
		"--reason", "Approved the amended exact scope",
		"--command-id", "cmd_d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2",
		jobID,
	}
	code, amendedApprovalOutput, stderr := runCLIWithFakeOIDC(t, server, approvalAmendedArgs)
	if code != 0 || !strings.Contains(amendedApprovalOutput, "State: ACTIVE") {
		t.Fatalf("approve amended revision: code=%d output=%q stderr=%q", code, amendedApprovalOutput, stderr)
	}
	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen Store after amended approval: %v", err)
	}
	defer database.Close()
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobContractRevisions) != 2 ||
		len(job.JobApprovals) != 2 || job.JobApprovals[0].ContractRevision != 1 ||
		job.JobApprovals[1].ContractRevision != 2 {
		t.Fatalf("Job after amendment reapproval = %#v err=%v", job, err)
	}
}

func TestJobContractCLIRejectsIncompleteInputsBeforeAuthentication(t *testing.T) {
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

	amendPath := filepath.Join(t.TempDir(), "must-not-exist", "amend.sqlite")
	code = Run(context.Background(), []string{"job", "amend", "--store", amendPath, "job_missing"},
		&stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("amend exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(amendPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Store path exists after invalid amendment request: %v", err)
	}

	invalidContractPath := filepath.Join(t.TempDir(), "invalid-contract.json")
	if err := os.WriteFile(invalidContractPath, []byte("not JSON"), 0o600); err != nil {
		t.Fatalf("write invalid contract: %v", err)
	}
	invalidStorePath := filepath.Join(t.TempDir(), "must-not-open", "invalid.sqlite")
	var amendStdout, amendStderr bytes.Buffer
	code = Run(context.Background(), []string{
		"job", "amend", "--store", invalidStorePath, "--revision", "1",
		"--canonical-hash", strings.Repeat("0", 64), "--reason", "test invalid JSON",
		"--contract-file", invalidContractPath, "job_missing",
	}, &amendStdout, &amendStderr, VersionInfo{})
	if code != 1 || !strings.Contains(amendStderr.String(), "JSON") {
		t.Fatalf("invalid amendment JSON exit code = %d, stderr=%q", code, amendStderr.String())
	}
	if _, err := os.Stat(invalidStorePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Store path exists after invalid amendment JSON: %v", err)
	}

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "invalid UTF-8", data: []byte{0xff}},
		{name: "oversized", data: bytes.Repeat([]byte(" "), (1<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contractPath := filepath.Join(t.TempDir(), "contract.json")
			if err := os.WriteFile(contractPath, tc.data, 0o600); err != nil {
				t.Fatalf("write contract file: %v", err)
			}
			storePath := filepath.Join(t.TempDir(), "must-not-open", "jobs.sqlite")
			var out, errOut bytes.Buffer
			code := Run(context.Background(), []string{
				"job", "amend", "--store", storePath, "--revision", "1",
				"--canonical-hash", strings.Repeat("0", 64), "--reason", "test invalid file",
				"--contract-file", contractPath, "job_missing",
			}, &out, &errOut, VersionInfo{})
			if code != 2 {
				t.Fatalf("amend exit code = %d, want 2; stderr=%q", code, errOut.String())
			}
			if _, err := os.Stat(storePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Store path exists after invalid contract file: %v", err)
			}
		})
	}
}

func TestDevProviderRunRequiresPrompt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"dev", "provider", "run"}, &stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--prompt") {
		t.Fatalf("stderr = %q, want prompt guidance", stderr.String())
	}
}

func fakeOpenCode(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a shell script")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "opencode")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$1\" = acp ] || exit 1\nTAXION_TEST_CLI_HELPER=1 exec %q -test.run=^TestDevProviderACPHelper$\n", executable)
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return helper
}

func TestDevProviderRunOpenCode(t *testing.T) {
	helper := fakeOpenCode(t)
	t.Setenv("TAXION_TEST_HOST_SECRET", "must-not-be-inherited")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"dev", "provider", "run", "--provider", "opencode",
		"--command", helper, "--working-directory", t.TempDir(), "--prompt", "hello"},
		&stdout, &stderr, VersionInfo{})
	if code != 0 || !strings.Contains(stdout.String(), "Provider: opencode") ||
		!strings.Contains(stdout.String(), "hello from ACP") {
		t.Fatalf("exit=%d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDevProviderConversationPersistsAndReusesLocalJobHistory(t *testing.T) {
	helper := fakeOpenCode(t)
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"dev", "job", "create", "--store", path,
		"--request", "The original request."}, &stdout, &stderr, VersionInfo{}); code != 0 {
		t.Fatalf("create exit=%d, stderr=%q", code, stderr.String())
	}
	jobID := strings.TrimPrefix(strings.Split(stdout.String(), "\n")[1], "ID: ")
	for _, prompt := range []string{"Remember the token HARBOR42.", "what token?"} {
		stdout.Reset()
		stderr.Reset()
		code := Run(context.Background(), []string{"dev", "provider", "run", "--provider", "opencode",
			"--command", helper, "--working-directory", t.TempDir(), "--store", path,
			"--job", jobID, "--prompt", prompt}, &stdout, &stderr, VersionInfo{})
		if code != 0 || !strings.Contains(stdout.String(), "Local Job conversation saved") {
			t.Fatalf("conversation exit=%d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
		}
	}
	if !strings.Contains(stdout.String(), "HARBOR42") {
		t.Fatalf("follow-up response=%q", stdout.String())
	}
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.JobMessages) != 4 ||
		job.JobMessages[0].Sender.Kind != domain.ActorController ||
		job.JobMessages[1].Sender.Kind != domain.ActorOrchestrator ||
		job.JobMessages[1].ReplyToMessageID != job.JobMessages[0].ID ||
		job.JobMessages[3].ReplyToMessageID != job.JobMessages[2].ID ||
		job.JobMessages[3].Body != "HARBOR42" {
		t.Fatalf("stored conversation=%#v", job.JobMessages)
	}
}

func TestDevProviderConversationRejectsTrustedJob(t *testing.T) {
	helper := fakeOpenCode(t)
	server := newCLIAuthServer(t)
	principal := authenticateCLIPrincipal(t, server)
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	const jobID = "job_trusted_dev_rejection"
	_, err = database.CreateTrustedDraftJob(context.Background(), jobID, "trusted",
		"cmd_11111111111111111111111111111111", principal.AuthenticatedAt(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"dev", "provider", "run", "--provider", "opencode",
		"--command", helper, "--working-directory", t.TempDir(), "--store", path, "--job", jobID,
		"--prompt", "test"}, &stdout, &stderr, VersionInfo{})
	if code != 1 || !strings.Contains(stderr.String(), "untrusted local DRAFT") {
		t.Fatalf("trusted job exit=%d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDevProviderConversationRetainsInputOnProviderFailure(t *testing.T) {
	helper := fakeOpenCode(t)
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"dev", "job", "create", "--store", path,
		"--request", "The original request."}, &stdout, &stderr, VersionInfo{}); code != 0 {
		t.Fatalf("create exit=%d, stderr=%q", code, stderr.String())
	}
	jobID := strings.TrimPrefix(strings.Split(stdout.String(), "\n")[1], "ID: ")
	stdout.Reset()
	stderr.Reset()
	code := Run(context.Background(), []string{"dev", "provider", "run", "--provider", "opencode",
		"--command", helper, "--working-directory", t.TempDir(), "--store", path,
		"--job", jobID, "--prompt", "simulate provider failure"}, &stdout, &stderr, VersionInfo{})
	if code != 1 {
		t.Fatalf("provider failure exit=%d, stdout=%q", code, stdout.String())
	}
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.JobMessages) != 1 || job.JobMessages[0].Body != "simulate provider failure" {
		t.Fatalf("stored conversation=%#v", job.JobMessages)
	}
}

func TestDevProviderACPHelper(t *testing.T) {
	if os.Getenv("TAXION_TEST_CLI_HELPER") != "1" {
		return
	}
	config, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "opencode", "opencode.json"))
	if err != nil || string(config) != `{"model":"opencode/big-pickle"}` ||
		os.Getenv("TAXION_TEST_HOST_SECRET") != "" {
		os.Exit(1)
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(1)
		}
		switch request.Method {
		case "initialize":
			_ = writer.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID,
				"result": map[string]any{"protocolVersion": 1}})
		case "session/new":
			_ = writer.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID,
				"result": map[string]any{"sessionId": "session_test"}})
		case "session/prompt":
			response := "hello from ACP"
			if len(request.Params.Prompt) > 0 {
				prompt := request.Params.Prompt[0].Text
				if strings.Contains(prompt, "simulate provider failure") {
					os.Exit(1)
				} else if strings.Contains(prompt, "what token?") {
					if !strings.Contains(prompt, "The original request.") ||
						!strings.Contains(prompt, "Remember the token HARBOR42.") ||
						!strings.Contains(prompt, "remembered") {
						os.Exit(1)
					}
					response = "HARBOR42"
				} else if strings.Contains(prompt, "Remember the token HARBOR42.") {
					response = "remembered"
				}
			}
			_ = writer.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{"sessionId": "session_test", "update": map[string]any{
					"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": response},
				}}})
			_ = writer.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID,
				"result": map[string]any{"stopReason": "end_turn"}})
		}
	}
	os.Exit(0)
}

func TestDevProviderRunRejectsUnknownProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"dev", "provider", "run", "--provider", "unknown", "--prompt", "hello"},
		&stdout, &stderr, VersionInfo{})
	if code != 2 {
		t.Fatalf("exit=%d, stderr=%q", code, stderr.String())
	}
}

func TestCopilotProfileEnvironmentUsesAllowlist(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "private-profile")
	t.Setenv("PATH", "/trusted/bin")
	t.Setenv("TMPDIR", "/tmp/taxion")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("TERM", "dumb")
	t.Setenv("HOME", "/host/home")
	t.Setenv("XDG_CONFIG_HOME", "/host/config")
	t.Setenv("GH_TOKEN", "gh-secret")
	t.Setenv("GITHUB_TOKEN", "github-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "cloud-secret")
	t.Setenv("UNRELATED_SETTING", "must-not-leak")

	got := make(map[string]string)
	for _, entry := range copilotProfileEnvironment(profile) {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("environment entry %q has no separator", entry)
		}
		if _, exists := got[name]; exists {
			t.Fatalf("environment contains duplicate %q", name)
		}
		got[name] = value
	}

	for name, want := range map[string]string{
		"PATH":            "/trusted/bin",
		"TMPDIR":          "/tmp/taxion",
		"LANG":            "C.UTF-8",
		"LC_ALL":          "C.UTF-8",
		"TERM":            "dumb",
		"HOME":            profile,
		"XDG_CONFIG_HOME": filepath.Join(profile, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(profile, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(profile, ".local", "share"),
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
	for _, name := range []string{
		"GH_TOKEN", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "UNRELATED_SETTING",
	} {
		if _, exists := got[name]; exists {
			t.Errorf("environment unexpectedly contains %s", name)
		}
	}
}
