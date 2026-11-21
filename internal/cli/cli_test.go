package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
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
