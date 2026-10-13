package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
)

func gitlabEnvironment(ref string) ([]string, error) {
	var name, token string
	switch {
	case ref == "glab:active":
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
		return nil, fmt.Errorf("taxion: repository credential requires glab:active or env:NAME")
	}
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch key {
		case "GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN",
			"GITLAB_HOST", "GITLAB_API_HOST", "GLAB_HOST", "GLAB_DEBUG":
			continue
		}
		if name != "" && key == name {
			continue
		}
		env = append(env, item)
	}
	if name != "" {
		env = append(env, "GITLAB_TOKEN="+token)
	}
	return env, nil
}

func validGitLabHost(host string) bool {
	parsed, err := url.Parse("https://" + host)
	return err == nil && parsed.Hostname() != "" && parsed.Host == host &&
		parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" &&
		!strings.ContainsAny(host, "\\?#")
}

func checkGitLabAccess(selected config.Config, write bool, query func(string) ([]byte, error)) error {
	return checkGitLabTarget(selected.Repository.Path, selected.Repository.Account, write, query)
}

func checkGitLabTarget(path, account string, write bool, query func(string) ([]byte, error)) error {
	user, err := query("user")
	if err != nil {
		return err
	}
	var identity struct {
		Username string `json:"username"`
	}
	if json.Unmarshal(user, &identity) != nil || identity.Username == "" {
		return fmt.Errorf("taxion: repository authentication identity is unconfirmed; provider details withheld")
	}
	if !strings.EqualFold(identity.Username, account) {
		return fmt.Errorf("taxion: effective repository account does not match the expected account")
	}
	body, err := query("projects/" + url.PathEscape(path))
	if err != nil {
		return err
	}
	var project struct {
		PathWithNamespace string `json:"path_with_namespace"`
		Permissions       struct {
			ProjectAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"project_access"`
			GroupAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"group_access"`
		} `json:"permissions"`
	}
	if json.Unmarshal(body, &project) != nil || !strings.EqualFold(project.PathWithNamespace, path) {
		return fmt.Errorf("taxion: repository access target is unconfirmed; provider details withheld")
	}
	level := 0
	if project.Permissions.ProjectAccess != nil {
		level = project.Permissions.ProjectAccess.AccessLevel
	}
	if project.Permissions.GroupAccess != nil && project.Permissions.GroupAccess.AccessLevel > level {
		level = project.Permissions.GroupAccess.AccessLevel
	}
	if level < 10 {
		return fmt.Errorf("taxion: selected repository credential has no confirmed read permission")
	}
	if write && level < 20 {
		return fmt.Errorf("taxion: selected repository credential has no confirmed external-record write permission")
	}
	return nil
}

func gitlabAPI(ctx context.Context, host string, env []string, method, endpoint string, payload map[string]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"api", "--hostname", host, "--method", method, endpoint}
	if payload != nil {
		keys := make([]string, 0, len(payload))
		for key := range payload {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, "--raw-field", key+"="+payload[key])
		}
	}
	command := exec.CommandContext(ctx, "glab", args...)
	command.Env = env
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("taxion: GitLab API failed; delivery may be uncertain, provider details withheld")
	}
	return output, nil
}
