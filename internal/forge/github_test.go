package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
)

func repositoryConfig(t *testing.T) config.Config {
	t.Helper()
	selected, err := config.Load([]byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: gh:active}
runtime: {provider: copilot, account: ai-user, credentialRef: copilot:logged-in, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`))
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

func TestGitHubIdentityAndPermissions(t *testing.T) {
	selected := repositoryConfig(t)
	for _, test := range []struct {
		name, user, repository string
		write, denied          bool
	}{
		{"coder", `{"login":"SCM-USER"}`, `{"full_name":"Owner/Repo","permissions":{"pull":true,"push":true}}`, true, false},
		{"reviewer", `{"login":"scm-user"}`, `{"full_name":"owner/repo","permissions":{"pull":true,"push":false}}`, false, false},
		{"coder-read-only", `{"login":"scm-user"}`, `{"full_name":"owner/repo","permissions":{"pull":true,"push":false}}`, true, true},
		{"wrong-user", `{"login":"ai-user"}`, `{}`, false, true},
		{"missing-user", `{}`, `{}`, false, true},
		{"malformed-user", `provider-secret`, `{}`, false, true},
		{"wrong-repository", `{"login":"scm-user"}`, `{"full_name":"owner/other","permissions":{"pull":true,"push":true}}`, true, true},
		{"missing-permissions", `{"login":"scm-user"}`, `{"full_name":"owner/repo"}`, false, true},
		{"null-permissions", `{"login":"scm-user"}`, `{"full_name":"owner/repo","permissions":{"pull":null,"push":true}}`, true, true},
		{"missing-write", `{"login":"scm-user"}`, `{"full_name":"owner/repo","permissions":{"pull":true}}`, true, true},
		{"no-read", `{"login":"scm-user"}`, `{"full_name":"owner/repo","permissions":{"pull":false,"push":true}}`, true, true},
		{"malformed-repository", `{"login":"scm-user"}`, `provider-secret`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var endpoints []string
			err := checkGitHubAccess(selected, test.write, func(endpoint string) ([]byte, error) {
				endpoints = append(endpoints, endpoint)
				if endpoint == "user" {
					return []byte(test.user), nil
				}
				return []byte(test.repository), nil
			})
			if (err != nil) != test.denied || err != nil && strings.Contains(err.Error(), "provider-secret") {
				t.Fatal("unexpected access result", err)
			}
			if endpoints[0] != "user" || len(endpoints) == 2 && endpoints[1] != "repos/owner/repo" {
				t.Fatal("wrong access targets", endpoints)
			}
			if strings.Contains(test.name, "user") && len(endpoints) != 1 {
				t.Fatal("unconfirmed actor reached repository query", endpoints)
			}
		})
	}
	unavailable := errors.New("query unavailable")
	for _, failure := range []string{"user", "repos/owner/repo"} {
		err := checkGitHubAccess(selected, false, func(endpoint string) ([]byte, error) {
			if endpoint == failure {
				return nil, unavailable
			}
			return []byte(`{"login":"scm-user"}`), nil
		})
		if !errors.Is(err, unavailable) {
			t.Fatal("query failure ignored", err)
		}
	}
}

func TestGitHubCredentialSelection(t *testing.T) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
		"COPILOT_GITHUB_TOKEN", "GH_HOST", "GH_DEBUG"} {
		t.Setenv(name, "ambient-secret")
	}
	t.Setenv("TAXION_TEST_REPOSITORY_TOKEN", "selected-secret")
	t.Setenv("GH_CONFIG_DIR", "/selected/saved/login")
	for _, ref := range []string{"gh:active", "env:TAXION_TEST_REPOSITORY_TOKEN", "env:GH_TOKEN"} {
		env, err := githubEnvironment(ref)
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for _, item := range env {
			key, value, _ := strings.Cut(item, "=")
			if _, exists := values[key]; exists {
				t.Fatal("duplicate credential environment", key)
			}
			values[key] = value
		}
		expected := ""
		if ref == "env:TAXION_TEST_REPOSITORY_TOKEN" {
			expected = "selected-secret"
		} else if ref == "env:GH_TOKEN" {
			expected = "ambient-secret"
		}
		if values["GH_TOKEN"] != expected || values["GH_CONFIG_DIR"] != "/selected/saved/login" ||
			values["GH_PROMPT_DISABLED"] != "1" {
			t.Fatal("incorrect explicit credential selection")
		}
		for _, key := range []string{"GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
			"COPILOT_GITHUB_TOKEN", "GH_HOST", "GH_DEBUG"} {
			if _, exists := values[key]; exists {
				t.Fatal("ambient credential or host survived", key)
			}
		}
		if ref == "env:TAXION_TEST_REPOSITORY_TOKEN" && values["TAXION_TEST_REPOSITORY_TOKEN"] != "" {
			t.Fatal("named credential passed twice")
		}
	}
	for _, ref := range []string{"scm", "env:lowercase", "env:MISSING_TAXION_TEST_TOKEN"} {
		if _, err := githubEnvironment(ref); err == nil {
			t.Fatal("unconfirmed credential accepted", ref)
		}
	}
	for _, token := range []string{"", " padded", "line\nbreak", "line\rbreak"} {
		t.Setenv("TAXION_TEST_REPOSITORY_TOKEN", token)
		if _, err := githubEnvironment("env:TAXION_TEST_REPOSITORY_TOKEN"); err == nil ||
			token != "" && strings.Contains(err.Error(), token) {
			t.Fatal("invalid credential accepted or exposed", err)
		}
	}
}

func TestGitHubCommandBoundary(t *testing.T) {
	selected := repositoryConfig(t)
	dir := t.TempDir()
	// This executable fixture exercises the real subprocess without credentials or network.
	script := `#!/bin/sh
if [ "$1" != api ] || [ "$2" != --hostname ] || [ "$3" != github.com ]; then exit 1; fi
if [ "$GH_TOKEN" != selected-secret ] || [ -n "$GITHUB_TOKEN" ] || [ -n "$GH_HOST" ]; then exit 1; fi
case "$4" in
user) printf '%s' '{"login":"scm-user"}' ;;
repos/owner/repo) printf '%s' '{"full_name":"owner/repo","permissions":{"pull":true,"push":true}}' ;;
*) echo private-provider-detail >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TAXION_TEST_REPOSITORY_TOKEN", "selected-secret")
	t.Setenv("GITHUB_TOKEN", "ambient-secret")
	t.Setenv("GH_HOST", "other.example")
	selected.Repository.CredentialRef = "env:TAXION_TEST_REPOSITORY_TOKEN"
	if err := CheckRepositoryAccess(context.Background(), selected, true); err != nil {
		t.Fatal(err)
	}
	before := selected
	selected.Repository.Path = "owner/other"
	if err := CheckRepositoryAccess(context.Background(), selected, false); err == nil ||
		strings.Contains(err.Error(), "private-provider-detail") {
		t.Fatal("provider failure ignored or disclosed", err)
	}
	selected = before
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckRepositoryAccess(ctx, selected, false); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	for _, provider := range []string{"bitbucket"} {
		selected.Repository.Provider = provider
		if err := CheckRepositoryAccess(context.Background(), selected, false); err == nil {
			t.Fatal("unsupported provider accepted")
		}
	}
	selected = before
	selected.Repository.Host = "github.example"
	if err := CheckRepositoryAccess(context.Background(), selected, false); err == nil {
		t.Fatal("unsupported host accepted")
	}
	t.Setenv("PATH", t.TempDir())
	if err := CheckRepositoryAccess(context.Background(), before, false); err == nil {
		t.Fatal("missing gh accepted")
	}
}

func TestLiveGitHubRepositoryAccess(t *testing.T) {
	if os.Getenv("TAXION_GITHUB_INTEGRATION") != "1" {
		t.Skip("set TAXION_GITHUB_INTEGRATION=1 for read-only GitHub API queries")
	}
	selected := repositoryConfig(t)
	selected.Repository.Account = os.Getenv("TAXION_TEST_GITHUB_ACCOUNT")
	selected.Repository.Path = os.Getenv("TAXION_TEST_GITHUB_REPOSITORY")
	if err := CheckRepositoryAccess(context.Background(), selected, true); err != nil {
		t.Fatal(err)
	}
}
