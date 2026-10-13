// Package forge checks provider-specific repository access without publishing.
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
)

var credentialEnvironmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// RepositoryAccessAdapter reports implementation coverage, not live availability.
func RepositoryAccessAdapter(selected config.Config) string {
	if selected.Repository.Provider == "github" && strings.EqualFold(selected.Repository.Host, "github.com") {
		return "GITHUB_COM_GH_API"
	}
	if selected.Repository.Provider == "gitlab" && selected.Repository.Host != "" {
		return "GITLAB_API_GLAB"
	}
	return "UNIMPLEMENTED"
}

// CheckRepositoryAccess confirms the selected API identity and permissions.
// It does not establish the identity used by Git SSH/HTTPS or publish changes.
func CheckRepositoryAccess(ctx context.Context, selected config.Config, write bool) error {
	if err := selected.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch RepositoryAccessAdapter(selected) {
	case "GITHUB_COM_GH_API":
		env, err := githubEnvironment(selected.Repository.CredentialRef)
		if err != nil {
			return err
		}
		query := func(endpoint string) ([]byte, error) {
			command := exec.CommandContext(ctx, "gh", "api", "--hostname", "github.com", endpoint)
			command.Env = env
			output, err := command.Output()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				return nil, fmt.Errorf("taxion: repository authentication or access query failed; provider details withheld")
			}
			return output, nil
		}
		return checkGitHubAccess(selected, write, query)
	case "GITLAB_API_GLAB":
		env, err := gitlabEnvironment(selected.Repository.CredentialRef)
		if err != nil {
			return err
		}
		query := func(endpoint string) ([]byte, error) {
			return gitlabAPI(ctx, selected.Repository.Host, env, "GET", endpoint, nil)
		}
		return checkGitLabAccess(selected, write, query)
	default:
		return fmt.Errorf("taxion: repository access adapter is unavailable for the selected provider and host")
	}
}

func githubEnvironment(ref string) ([]string, error) {
	var name, token string
	switch {
	case ref == "gh:active":
	case strings.HasPrefix(ref, "env:"):
		name = strings.TrimPrefix(ref, "env:")
		if !credentialEnvironmentName.MatchString(name) {
			return nil, fmt.Errorf("taxion: repository credential requires an uppercase environment name")
		}
		token = os.Getenv(name)
		if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
			return nil, fmt.Errorf("taxion: selected repository credential is missing or invalid; value withheld")
		}
	default:
		return nil, fmt.Errorf("taxion: repository credential requires gh:active or env:NAME")
	}
	env := []string{}
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch key {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
			"COPILOT_GITHUB_TOKEN", "GH_HOST", "GH_DEBUG", "GH_PROMPT_DISABLED", "GH_NO_UPDATE_NOTIFIER":
			continue
		}
		if name != "" && key == name {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	if name != "" {
		env = append(env, "GH_TOKEN="+token)
	}
	return env, nil
}

func checkGitHubAccess(selected config.Config, write bool, query func(string) ([]byte, error)) error {
	user, err := query("user")
	if err != nil {
		return err
	}
	var identity struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(user, &identity) != nil || identity.Login == "" {
		return fmt.Errorf("taxion: repository authentication identity is unconfirmed; provider details withheld")
	}
	if !strings.EqualFold(identity.Login, selected.Repository.Account) {
		return fmt.Errorf("taxion: effective repository account does not match the expected account")
	}
	body, err := query("repos/" + selected.Repository.Path)
	if err != nil {
		return err
	}
	var repository struct {
		FullName    string `json:"full_name"`
		Permissions struct {
			Pull *bool `json:"pull"`
			Push *bool `json:"push"`
		} `json:"permissions"`
	}
	if json.Unmarshal(body, &repository) != nil ||
		!strings.EqualFold(repository.FullName, selected.Repository.Path) {
		return fmt.Errorf("taxion: repository access target is unconfirmed; provider details withheld")
	}
	if repository.Permissions.Pull == nil || !*repository.Permissions.Pull {
		return fmt.Errorf("taxion: selected repository credential has no confirmed read permission")
	}
	if write && (repository.Permissions.Push == nil || !*repository.Permissions.Push) {
		return fmt.Errorf("taxion: selected repository credential has no confirmed write permission")
	}
	return nil
}
