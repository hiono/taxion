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

func gitLabConfig(t *testing.T) config.Config {
	t.Helper()
	selected := repositoryConfig(t)
	selected.Repository.Provider = "gitlab"
	selected.Repository.Host = "gitlab.example"
	selected.Repository.Path = "group/subgroup/repo"
	selected.Repository.Account = "scm-user"
	selected.Repository.CredentialRef = "glab:active"
	return selected
}

func TestGitLabIdentityAndPermissions(t *testing.T) {
	selected := gitLabConfig(t)
	if got := RepositoryAccessAdapter(selected); got != "GITLAB_API_GLAB" {
		t.Fatal("GitLab adapter coverage was not reported", got)
	}
	for _, test := range []struct {
		name, user, project string
		write, denied       bool
	}{
		{"project-writer", `{"username":"SCM-USER"}`, `{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":20}}}`, true, false},
		{"group-writer", `{"username":"scm-user"}`, `{"path_with_namespace":"group/subgroup/repo","permissions":{"group_access":{"access_level":30}}}`, true, false},
		{"read-only", `{"username":"scm-user"}`, `{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":10}}}`, false, false},
		{"insufficient-write", `{"username":"scm-user"}`, `{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":10}}}`, true, true},
		{"wrong-user", `{"username":"other"}`, `{}`, false, true},
		{"missing-user", `{}`, `{}`, false, true},
		{"malformed-user", `provider-secret`, `{}`, false, true},
		{"wrong-project", `{"username":"scm-user"}`, `{"path_with_namespace":"group/other/repo","permissions":{"project_access":{"access_level":50}}}`, true, true},
		{"missing-permissions", `{"username":"scm-user"}`, `{"path_with_namespace":"group/subgroup/repo"}`, false, true},
		{"malformed-project", `{"username":"scm-user"}`, `provider-secret`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var endpoints []string
			err := checkGitLabAccess(selected, test.write, func(endpoint string) ([]byte, error) {
				endpoints = append(endpoints, endpoint)
				if endpoint == "user" {
					return []byte(test.user), nil
				}
				return []byte(test.project), nil
			})
			if (err != nil) != test.denied || err != nil && strings.Contains(err.Error(), "provider-secret") {
				t.Fatal("unexpected access result", err)
			}
			if endpoints[0] != "user" || len(endpoints) == 2 && endpoints[1] != "projects/group%2Fsubgroup%2Frepo" {
				t.Fatal("wrong access targets", endpoints)
			}
			if strings.Contains(test.name, "user") && len(endpoints) != 1 {
				t.Fatal("unconfirmed actor reached project query", endpoints)
			}
		})
	}
	unavailable := errors.New("query unavailable")
	for _, failure := range []string{"user", "projects/group%2Fsubgroup%2Frepo"} {
		err := checkGitLabAccess(selected, false, func(endpoint string) ([]byte, error) {
			if endpoint == failure {
				return nil, unavailable
			}
			return []byte(`{"username":"scm-user"}`), nil
		})
		if !errors.Is(err, unavailable) {
			t.Fatal("query failure ignored", err)
		}
	}
}

func TestGitLabCredentialSelection(t *testing.T) {
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN",
		"GITLAB_HOST", "GITLAB_API_HOST", "GLAB_HOST", "GLAB_DEBUG"} {
		t.Setenv(name, "ambient-secret")
	}
	t.Setenv("TAXION_TEST_GITLAB_TOKEN", "selected-secret")
	t.Setenv("GLAB_CONFIG_DIR", "/selected/saved/login")
	for _, ref := range []string{"glab:active", "env:TAXION_TEST_GITLAB_TOKEN", "env:GITLAB_TOKEN"} {
		env, err := gitlabEnvironment(ref)
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
		if ref == "env:TAXION_TEST_GITLAB_TOKEN" {
			expected = "selected-secret"
		} else if ref == "env:GITLAB_TOKEN" {
			expected = "ambient-secret"
		}
		if values["GITLAB_TOKEN"] != expected || values["GLAB_CONFIG_DIR"] != "/selected/saved/login" {
			t.Fatal("incorrect explicit credential selection")
		}
		for _, key := range []string{"GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "GITLAB_HOST",
			"GITLAB_API_HOST", "GLAB_HOST", "GLAB_DEBUG"} {
			if _, exists := values[key]; exists {
				t.Fatal("ambient credential or host survived", key)
			}
		}
		if ref == "env:TAXION_TEST_GITLAB_TOKEN" && values["TAXION_TEST_GITLAB_TOKEN"] != "" {
			t.Fatal("named credential passed twice")
		}
	}
	for _, ref := range []string{"scm", "env:lowercase", "env:MISSING_TAXION_GITLAB_TOKEN"} {
		if _, err := gitlabEnvironment(ref); err == nil {
			t.Fatal("unconfirmed credential accepted", ref)
		}
	}
	for _, token := range []string{"", " padded", "line\nbreak", "line\rbreak"} {
		t.Setenv("TAXION_TEST_GITLAB_TOKEN", token)
		if _, err := gitlabEnvironment("env:TAXION_TEST_GITLAB_TOKEN"); err == nil ||
			token != "" && strings.Contains(err.Error(), token) {
			t.Fatal("invalid credential accepted or exposed", err)
		}
	}
}

func TestGitLabCommandBoundary(t *testing.T) {
	selected := gitLabConfig(t)
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" != api ] || [ "$2" != --hostname ] || [ "$3" != gitlab.example ]; then exit 1; fi
if [ "$GITLAB_TOKEN" != selected-secret ] || [ -n "$GITLAB_ACCESS_TOKEN" ] || [ -n "$GLAB_HOST" ]; then exit 1; fi
case "$6" in
user) printf '%s' '{"username":"scm-user"}' ;;
projects/group%2Fsubgroup%2Frepo) printf '%s' '{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":50}}}' ;;
*) echo private-provider-detail >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TAXION_TEST_GITLAB_TOKEN", "selected-secret")
	t.Setenv("GITLAB_ACCESS_TOKEN", "ambient-secret")
	t.Setenv("GLAB_HOST", "other.example")
	selected.Repository.CredentialRef = "env:TAXION_TEST_GITLAB_TOKEN"
	if err := CheckRepositoryAccess(context.Background(), selected, true); err != nil {
		t.Fatal(err)
	}
	selected.Repository.Path = "group/other/repo"
	if err := CheckRepositoryAccess(context.Background(), selected, false); err == nil ||
		strings.Contains(err.Error(), "private-provider-detail") {
		t.Fatal("provider failure ignored or disclosed", err)
	}
	selected = gitLabConfig(t)
	selected.Repository.CredentialRef = "env:TAXION_TEST_GITLAB_TOKEN"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckRepositoryAccess(ctx, selected, false); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestLiveGitLabRepositoryAccess(t *testing.T) {
	if os.Getenv("TAXION_GITLAB_INTEGRATION") != "1" {
		t.Skip("set TAXION_GITLAB_INTEGRATION=1 for read-only GitLab API queries")
	}
	selected := gitLabConfig(t)
	selected.Repository.Host = os.Getenv("TAXION_GITLAB_TEST_HOST")
	selected.Repository.Account = os.Getenv("TAXION_GITLAB_TEST_ACCOUNT")
	selected.Repository.Path = os.Getenv("TAXION_GITLAB_TEST_PROJECT")
	selected.Repository.CredentialRef = os.Getenv("TAXION_GITLAB_TEST_CREDENTIAL_REF")
	if selected.Repository.CredentialRef == "" {
		selected.Repository.CredentialRef = "glab:active"
	}
	if err := CheckRepositoryAccess(context.Background(), selected, true); err != nil {
		t.Fatal(err)
	}
}
