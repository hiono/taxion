package store

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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/identity"
	"golang.org/x/oauth2"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database, path
}

func newTestJob(t *testing.T, id, request string) domain.Job {
	t.Helper()
	job, err := domain.NewDraftJob(id, request, time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewDraftJob() error = %v", err)
	}
	return job
}

func TestCreateJobPersistsAggregateAndCommandReceipt(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_one", "Build a small useful product from my notes.")

	receipt, err := database.CreateLocalDraftJob(ctx, job, "cmd_one")
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if receipt.JobID != job.ID || receipt.Revision != 1 {
		t.Fatalf("receipt = %#v", receipt)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if !reflect.DeepEqual(got, job) {
		t.Fatalf("GetJob() = %#v, want %#v", got, job)
	}
	jobs, err := reopened.ListJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("ListJobs() = %#v, want one Job", jobs)
	}
}

func TestCreateJobIsIdempotentForSameCommandAndPayload(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_idempotent", "Keep this request exactly.")

	first, err := database.CreateLocalDraftJob(ctx, job, "cmd_idempotent")
	if err != nil {
		t.Fatalf("first CreateJob() error = %v", err)
	}
	second, err := database.CreateLocalDraftJob(ctx, job, "cmd_idempotent")
	if err != nil {
		t.Fatalf("retry CreateJob() error = %v", err)
	}
	if first != second {
		t.Fatalf("retry receipt = %#v, want %#v", second, first)
	}
	jobs, err := database.ListJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("stored Jobs = %d, want 1", len(jobs))
	}
}

func TestCreateJobRequiresInitialRevision(t *testing.T) {
	database, _ := newTestStore(t)
	job := newTestJob(t, "job_later_revision", "A newly created Job must start at revision one.")
	job.Revision = 2
	if _, err := database.CreateLocalDraftJob(context.Background(), job, "cmd_later_revision"); !errors.Is(err, domain.ErrInvalidJob) {
		t.Fatalf("CreateJob() error = %v, want domain.ErrInvalidJob", err)
	}
}

func TestGetJobAcceptsLaterAggregateRevision(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_revision_two", "A persisted aggregate can advance revisions.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_revision_two"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	job.Revision = 2
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal updated Job: %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET revision = ?, payload_json = ?, integrity_sha256 = ? WHERE job_id = ?",
		job.Revision, string(payload), digest(payload), job.ID,
	); err != nil {
		t.Fatalf("update aggregate fixture: %v", err)
	}
	got, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() rejected revision 2: %v", err)
	}
	if got.Revision != 2 {
		t.Fatalf("GetJob() revision = %d, want 2", got.Revision)
	}
}

func TestGetJobDetectsAggregateMetadataTampering(t *testing.T) {
	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{name: "revision", query: "UPDATE job_aggregates SET revision = revision + 1 WHERE job_id = ?", args: []any{"job_metadata"}},
		{name: "created_at", query: "UPDATE job_aggregates SET created_at = ? WHERE job_id = ?", args: []any{"2026-11-20T13:00:00Z", "job_metadata"}},
		{name: "updated_at", query: "UPDATE job_aggregates SET updated_at = ? WHERE job_id = ?", args: []any{"2026-11-20T13:00:00Z", "job_metadata"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, _ := newTestStore(t)
			ctx := context.Background()
			job := newTestJob(t, "job_metadata", "Detect duplicated metadata tampering.")
			if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_metadata"); err != nil {
				t.Fatalf("CreateJob() error = %v", err)
			}
			if _, err := database.db.ExecContext(ctx, test.query, test.args...); err != nil {
				t.Fatalf("tamper metadata: %v", err)
			}
			if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("GetJob() error = %v, want ErrIntegrity", err)
			}
		})
	}
}

func TestListJobsDetectsAggregateMetadataTampering(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_list_metadata", "List must validate indexed metadata.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_list_metadata"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET updated_at = ? WHERE job_id = ?",
		"2026-11-20T13:00:00Z", job.ID,
	); err != nil {
		t.Fatalf("tamper metadata: %v", err)
	}
	if _, err := database.ListJobs(ctx, 10); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ListJobs() error = %v, want ErrIntegrity", err)
	}
}

func TestCreateJobRejectsCommandTypeMismatch(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_command_type", "Command type is part of idempotency identity.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_type"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_commands SET command_type = ? WHERE command_id = ?",
		"DifferentCommand", "cmd_command_type",
	); err != nil {
		t.Fatalf("tamper command type: %v", err)
	}
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_type"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("replayed CreateJob() error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestCreateJobDetectsCommandReceiptMetadataTampering(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_command_receipt", "Command receipt metadata is integrity checked.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_receipt"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_commands SET created_at = ? WHERE command_id = ?",
		"2026-11-20T13:00:00Z", "cmd_command_receipt",
	); err != nil {
		t.Fatalf("tamper command receipt: %v", err)
	}
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_receipt"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("replayed CreateJob() error = %v, want ErrIntegrity", err)
	}
}

func TestCreateJobRejectsIdempotencyKeyReuseWithDifferentPayload(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	firstJob := newTestJob(t, "job_first", "First request.")
	secondJob := newTestJob(t, "job_second", "Different request.")

	if _, err := database.CreateLocalDraftJob(ctx, firstJob, "cmd_reused"); err != nil {
		t.Fatalf("first CreateJob() error = %v", err)
	}
	if _, err := database.CreateLocalDraftJob(ctx, secondJob, "cmd_reused"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("second CreateJob() error = %v, want ErrIdempotencyConflict", err)
	}
	got, err := database.GetJob(ctx, firstJob.ID)
	if err != nil || got.Request != firstJob.Request {
		t.Fatalf("original Job changed: got=%#v err=%v", got, err)
	}
	if _, err := database.GetJob(ctx, secondJob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Job lookup error = %v, want ErrNotFound", err)
	}
}

func TestGetJobDetectsPayloadTampering(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_tamper", "Original request.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_tamper"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET payload_json = ? WHERE job_id = ?",
		`{"id":"job_tamper","request":"tampered","state":"DRAFT","revision":1,"created_at":"2026-11-20T12:00:00Z","updated_at":"2026-11-20T12:00:00Z"}`,
		job.ID,
	); err != nil {
		t.Fatalf("tamper fixture: %v", err)
	}
	if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("GetJob() error = %v, want ErrIntegrity", err)
	}
}

func newTrustedTestJob(t *testing.T, id, request, commandID string) domain.Job {
	t.Helper()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	provenance := domain.IntakeProvenance{
		PrincipalID: identity.PrincipalID("https://identity.example", "subject-123"), Issuer: "https://identity.example",
		Subject: "subject-123", IngressSource: domain.IngressSourceCLI,
		AuthenticatedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-2 * time.Minute),
		RequestIdentity: commandID, SchemaVersion: domain.IntakeProvenanceSchemaVersion,
	}
	job, err := domain.NewDraftJobWithProvenance(id, request, provenance, now)
	if err != nil {
		t.Fatalf("NewDraftJobWithProvenance() error = %v", err)
	}
	return job
}

func TestCreateTrustedDraftJobPersistsProvenanceAndReceipt(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	job := newTrustedTestJob(t, "job_trusted", "  Preserve this original request.\n  ", "cmd_trusted")
	first, err := database.createJob(ctx, job, "cmd_trusted", createTrustedDraftJobCommandType)
	if err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	if first.JobID != job.ID || first.Revision != 1 {
		t.Fatalf("receipt = %#v", first)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if !reflect.DeepEqual(got, job) {
		t.Fatalf("GetJob() = %#v, want %#v", got, job)
	}
	provenance := got.IntakeProvenance
	if provenance == nil || provenance.RequestIdentity != first.CommandID ||
		provenance.IngressSource != domain.IngressSourceCLI ||
		provenance.OriginalRequestSHA256 == "" ||
		provenance.SchemaVersion != domain.IntakeProvenanceSchemaVersion {
		t.Fatalf("persisted IntakeProvenance = %#v", provenance)
	}
	retry, err := reopened.createJob(ctx, job, "cmd_trusted", createTrustedDraftJobCommandType)
	if err != nil {
		t.Fatalf("idempotent CreateTrustedDraftJob() error = %v", err)
	}
	if retry != first {
		t.Fatalf("retry receipt = %#v, want %#v", retry, first)
	}
}

func TestCreateTrustedDraftJobRequiresAuthenticatedPrincipal(t *testing.T) {
	database, _ := newTestStore(t)
	if _, err := database.CreateTrustedDraftJob(context.Background(), "job_missing_principal", "Trusted request.", "cmd_missing_principal", time.Now().UTC(), auth.AuthenticatedPrincipal{}); err == nil {
		t.Fatal("CreateTrustedDraftJob() accepted an empty AuthenticatedPrincipal")
	}
	jobs, err := database.ListJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("stored Jobs = %d, want 0", len(jobs))
	}
}

func TestCreateLocalDraftJobRejectsProvenance(t *testing.T) {
	database, _ := newTestStore(t)
	job := newTrustedTestJob(t, "job_wrong_path", "Trusted provenance cannot use local path.", "cmd_wrong_path")
	if _, err := database.CreateLocalDraftJob(context.Background(), job, "cmd_wrong_path"); err == nil {
		t.Fatal("CreateLocalDraftJob() accepted a trusted Job")
	}
}

func TestCreateTrustedDraftJobRejectsCrossPathCommandIDReuse(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	local := newTestJob(t, "job_local_command", "Local DRAFT command.")
	if _, err := database.CreateLocalDraftJob(ctx, local, "cmd_shared"); err != nil {
		t.Fatalf("CreateLocalDraftJob() error = %v", err)
	}
	trusted := newTrustedTestJob(t, "job_trusted_command", "Trusted DRAFT command.", "cmd_shared")
	if _, err := database.createJob(ctx, trusted, "cmd_shared", createTrustedDraftJobCommandType); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("CreateTrustedDraftJob() error = %v, want ErrIdempotencyConflict", err)
	}
	if _, err := database.GetJob(ctx, trusted.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trusted Job lookup error = %v, want ErrNotFound", err)
	}
}

func TestTrustedJobAggregateAndReceiptCommitAtomically(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTrustedTestJob(t, "job_atomic", "Aggregate and receipt are one transaction.", "cmd_atomic")
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_command_receipt
		BEFORE INSERT ON job_commands
		BEGIN SELECT RAISE(ABORT, 'simulated receipt failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if _, err := database.createJob(ctx, job, "cmd_atomic", createTrustedDraftJobCommandType); err == nil {
		t.Fatal("CreateTrustedDraftJob() succeeded despite receipt insert failure")
	}
	if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetJob() error = %v, want ErrNotFound after rollback", err)
	}
	var count int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_commands WHERE command_id = ?", "cmd_atomic").Scan(&count); err != nil {
		t.Fatalf("count command receipts: %v", err)
	}
	if count != 0 {
		t.Fatalf("command receipt count = %d, want 0 after rollback", count)
	}
}

func TestGetJobRejectsProvenanceTamperingAfterPayloadHashRecomputed(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTrustedTestJob(t, "job_provenance_tamper", "Detect provenance tampering.", "cmd_provenance_tamper")
	if _, err := database.createJob(ctx, job, "cmd_provenance_tamper", createTrustedDraftJobCommandType); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job.IntakeProvenance.OriginalRequestSHA256 = strings.Repeat("0", 64)
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal tampered Job: %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET payload_json = ?, integrity_sha256 = ? WHERE job_id = ?",
		string(payload), digest(payload), job.ID,
	); err != nil {
		t.Fatalf("update tampered aggregate: %v", err)
	}
	if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("GetJob() error = %v, want ErrIntegrity", err)
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func newAuthenticatedTestPrincipal(t *testing.T) auth.AuthenticatedPrincipal {
	t.Helper()
	trustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC signing key: %v", err)
	}
	provider := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{
		PublicKey: trustedKey.Public(), KeyID: "taxion-store-test-key", Algorithm: oidc.RS256,
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
			"iss": "https://" + r.Host, "aud": "taxion-store-test", "sub": "subject-store-test",
			"exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": expectedNonce,
		})
		if err != nil {
			http.Error(w, "encode claims", http.StatusInternalServerError)
			return
		}
		token := oidctest.SignIDToken(trustedKey, "taxion-store-test-key", oidc.RS256, string(claims))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token", "token_type": "Bearer",
			"expires_in": 300, "id_token": token,
		})
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()
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
	})

	authenticator, err := auth.NewOIDCAuthenticator(auth.OIDCConfig{
		Issuer: server.URL, ClientID: "taxion-store-test", Audience: "taxion-store-test",
		RedirectURI: "http://127.0.0.1:0/callback", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator() error = %v", err)
	}
	output := &synchronizedBuffer{}
	type result struct {
		principal auth.AuthenticatedPrincipal
		err       error
	}
	done := make(chan result, 1)
	go func() {
		principal, err := authenticator.Authenticate(context.Background(), output)
		done <- result{principal: principal, err: err}
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
			t.Fatalf("Authenticate() error = %v", completed.err)
		}
		return completed.principal
	case <-time.After(5 * time.Second):
		t.Fatal("OIDC authentication did not complete")
		return auth.AuthenticatedPrincipal{}
	}
}

func TestAddPlanningAuthorizationPersistsAndIsIdempotent(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	receivedAt := principal.AuthenticatedAt()
	if _, err := database.CreateTrustedDraftJob(ctx, "job_planning_store", "Plan this request.", "cmd_planning_create", receivedAt, principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, "job_planning_store")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	issuedAt := job.UpdatedAt.Add(time.Minute)
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary:    domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_store",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:              domain.PlanningBudget{MaxTurns: 8, MaxTokens: 100000},
		Deadline:            issuedAt.Add(15 * time.Minute),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), "cmd_11111111111111111111111111111111", request, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() error = %v", err)
	}
	first, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization() error = %v", err)
	}
	if first.Revision != 2 || first.PayloadSHA256 == "" {
		t.Fatalf("receipt = %#v", first)
	}
	second, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil {
		t.Fatalf("idempotent AddPlanningAuthorization() error = %v", err)
	}
	if second != first {
		t.Fatalf("retry receipt = %#v, want %#v", second, first)
	}
	futureTimestampGrant := grant
	futureTimestampGrant.IssuedAt = time.Now().Add(24 * time.Hour)
	retried, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", futureTimestampGrant, principal)
	if err != nil || retried != first {
		t.Fatalf("retry with caller-supplied timestamp = %#v, %v; want %#v", retried, err, first)
	}
	changedGrant := grant
	changedGrant.Budget.MaxTurns++
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", changedGrant, principal); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reused command ID error = %v, want ErrIdempotencyConflict", err)
	}
	retriedWithNewRevision, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision+1, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil || retriedWithNewRevision != first {
		t.Fatalf("idempotent retry with current expected revision = %#v, %v; want %#v", retriedWithNewRevision, err, first)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision+1, "cmd_44444444444444444444444444444444", grant, principal); err == nil {
		t.Fatal("AddPlanningAuthorization() accepted a command ID that differs from the grant")
	}
	staleRequest := request
	staleRequest.Deadline = issuedAt.Add(20 * time.Minute)
	staleGrant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(),
		"cmd_33333333333333333333333333333333", staleRequest, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() for stale revision error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_33333333333333333333333333333333", staleGrant, principal); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("stale expected revision error = %v, want ErrIdempotencyConflict", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Revision != 2 || got.State != domain.JobDraft || len(got.PlanningAuthorizations) != 1 || got.PlanningAuthorizations[0].ID != grant.ID {
		t.Fatalf("persisted PlanningAuthorization = %#v", got)
	}
	if !got.PlanningAuthorizations[0].IssuedAt.Equal(first.CreatedAt) {
		t.Fatalf("persisted issue time = %s, receipt time = %s", got.PlanningAuthorizations[0].IssuedAt, first.CreatedAt)
	}
	replayed, err := reopened.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil || replayed != first {
		t.Fatalf("reopened idempotent retry = %#v, %v; want %#v", replayed, err, first)
	}
	got.PlanningAuthorizations = nil
	tamperedPayload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal altered aggregate: %v", err)
	}
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE job_aggregates SET payload_json = ?, integrity_sha256 = ? WHERE job_id = ?",
		string(tamperedPayload), digest(tamperedPayload), job.ID,
	); err != nil {
		t.Fatalf("remove persisted grant fixture: %v", err)
	}
	if _, err := reopened.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("retry after removing persisted grant error = %v, want ErrIntegrity", err)
	}
}

func TestAddPlanningAuthorizationRejectsWrongPrincipalAndRollsBack(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	otherPrincipal := newAuthenticatedTestPrincipal(t)
	receivedAt := principal.AuthenticatedAt()
	if _, err := database.CreateTrustedDraftJob(ctx, "job_planning_atomic", "Plan this request.", "cmd_planning_atomic_create", receivedAt, principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, "job_planning_atomic")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	issuedAt := job.UpdatedAt.Add(time.Minute)
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("b", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary:    domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundaryDocuments,
			Reference: "documents_atomic",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:              domain.PlanningBudget{MaxTurns: 4, MaxTokens: 50000},
		Deadline:            issuedAt.Add(10 * time.Minute),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), "cmd_22222222222222222222222222222222", request, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_55555555555555555555555555555555", grant, otherPrincipal); err == nil {
		t.Fatal("AddPlanningAuthorization() accepted a different HUMAN Principal")
	}
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_planning_receipt
		BEFORE INSERT ON job_commands
		WHEN NEW.command_type = 'AddPlanningAuthorization'
		BEGIN SELECT RAISE(ABORT, 'simulated receipt failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_22222222222222222222222222222222", grant, principal); err == nil {
		t.Fatal("AddPlanningAuthorization() succeeded despite receipt insert failure")
	}
	got, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() after rollback error = %v", err)
	}
	if got.Revision != 1 || len(got.PlanningAuthorizations) != 0 {
		t.Fatalf("Job changed despite transaction rollback: %#v", got)
	}
}

func TestAddPlanningAuthorizationAppendsWithoutChangingExistingFacts(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	if _, err := database.CreateTrustedDraftJob(ctx, "job_planning_append", "Plan this request.", "cmd_planning_append_create", principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, "job_planning_append")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	firstIssuedAt := time.Now().UTC()
	firstRequest := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary:    domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_first",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:              domain.PlanningBudget{MaxTurns: 4, MaxTokens: 50000},
		Deadline:            firstIssuedAt.Add(time.Hour),
	}
	firstGrant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(),
		"cmd_66666666666666666666666666666666", firstRequest, firstIssuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() first error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision,
		firstGrant.CommandID, firstGrant, principal); err != nil {
		t.Fatalf("AddPlanningAuthorization() first error = %v", err)
	}
	afterFirst, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() after first grant error = %v", err)
	}
	originalFirst := afterFirst.PlanningAuthorizations[0]

	secondIssuedAt := time.Now().UTC()
	secondRequest := firstRequest
	secondRequest.ConfigurationSHA256 = strings.Repeat("d", 64)
	secondRequest.ReadOnlyBoundary = domain.PlanningReadOnlyBoundary{
		Mode: domain.PlanningReadOnlyBoundaryDocuments,
		Reference: "documents_second",
		SHA256: strings.Repeat("e", 64),
	}
	secondRequest.Budget = domain.PlanningBudget{MaxTurns: 6, MaxTokens: 75000}
	secondRequest.Deadline = secondIssuedAt.Add(2 * time.Hour)
	secondGrant, err := domain.NewPlanningAuthorization(afterFirst, principal.PrincipalID(),
		"cmd_77777777777777777777777777777777", secondRequest, secondIssuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() second error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, afterFirst.ID, afterFirst.Revision,
		secondGrant.CommandID, secondGrant, principal); err != nil {
		t.Fatalf("AddPlanningAuthorization() second error = %v", err)
	}
	finalJob, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() final error = %v", err)
	}
	if finalJob.State != domain.JobDraft || finalJob.Revision != 3 || len(finalJob.PlanningAuthorizations) != 2 {
		t.Fatalf("final Job = %#v", finalJob)
	}
	if !reflect.DeepEqual(finalJob.PlanningAuthorizations[0], originalFirst) {
		t.Fatalf("first PlanningAuthorization changed: got %#v, want %#v", finalJob.PlanningAuthorizations[0], originalFirst)
	}
}
