package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
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
	var endpoints *config.Endpoints
	if selected.HasEndpoints() {
		declared := selected.Endpoints
		endpoints = &declared
	}
	return checkGitLabTargetEndpoints(endpoints, selected.Repository.Path, selected.Repository.Account, write, query)
}

// checkGitLabTarget verifies the API target for a repository selected without
// an endpoints group.
func checkGitLabTarget(path, account string, write bool, query func(string) ([]byte, error)) error {
	return checkGitLabTargetEndpoints(nil, path, account, write, query)
}

// checkGitLabTargetEndpoints verifies the API target. An explicit endpoints
// group adds the same-response identity cross-check; without one the legacy
// checks stay.
func checkGitLabTargetEndpoints(endpoints *config.Endpoints, path, account string, write bool, query func(string) ([]byte, error)) error {
	user, err := query("user")
	if err != nil {
		return gitlabQueryError(endpoints, fmt.Errorf("identity-query: %w", err))
	}
	var identity struct {
		Username string `json:"username"`
	}
	if json.Unmarshal(user, &identity) != nil || identity.Username == "" {
		return s5Error(endpoints, fmt.Errorf("taxion: repository authentication identity is unconfirmed; provider details withheld"))
	}
	if !strings.EqualFold(identity.Username, account) {
		return s5Error(endpoints, fmt.Errorf("taxion: effective repository account does not match the expected account"))
	}
	body, err := query("projects/" + url.PathEscape(path))
	if err != nil {
		return gitlabQueryError(endpoints, fmt.Errorf("project-query: %w", err))
	}
	var project gitlabProject
	if json.Unmarshal(body, &project) != nil || !strings.EqualFold(project.PathWithNamespace, path) {
		return s5Error(endpoints, fmt.Errorf("taxion: repository access target is unconfirmed; provider details withheld"))
	}
	if endpoints != nil {
		if err := verifyProjectEndpoints(*endpoints, path, project); err != nil {
			return err
		}
	}
	level := 0
	if project.Permissions.ProjectAccess != nil {
		level = project.Permissions.ProjectAccess.AccessLevel
	}
	if project.Permissions.GroupAccess != nil && project.Permissions.GroupAccess.AccessLevel > level {
		level = project.Permissions.GroupAccess.AccessLevel
	}
	if level < 10 {
		return s5Error(endpoints, fmt.Errorf("taxion: selected repository credential has no confirmed read permission"))
	}
	if write && level < 20 {
		return s5Error(endpoints, fmt.Errorf("taxion: selected repository credential has no confirmed external-record write permission"))
	}
	return nil
}

// gitlabProject is one project record. Every endpoint identity is checked
// against fields of the same API response, never against a name or an object
// hash alone.
type gitlabProject struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	SSHURLToRepo      string `json:"ssh_url_to_repo"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	WebURL            string `json:"web_url"`
	Permissions       struct {
		ProjectAccess *struct {
			AccessLevel int `json:"access_level"`
		} `json:"project_access"`
		GroupAccess *struct {
			AccessLevel int `json:"access_level"`
		} `json:"group_access"`
	} `json:"permissions"`
}

func gitlabQueryError(endpoints *config.Endpoints, err error) error {
	return s5Error(endpoints, err)
}

// s5Error labels API-stage failures with REP-006 when the repository selected an
// endpoints group, so S5 stays distinguishable from the remote binding stages.
func s5Error(endpoints *config.Endpoints, err error) error {
	if endpoints != nil {
		return fmt.Errorf("REP-006 GitLab API identity cannot be verified: %w", err)
	}
	return err
}

// verifyProjectEndpoints checks the numeric project ID and the SSH clone, HTTPS
// clone, and web addresses from one API response against the approved
// endpoints, so a matching project name in another namespace cannot pass.
func verifyProjectEndpoints(endpoints config.Endpoints, repository string, project gitlabProject) error {
	host, port, segments, err := endpoints.CloneBinding()
	if err != nil {
		return fmt.Errorf("REP-006 GitLab API identity cannot be verified: REP-002 endpoints.httpsCloneBase %w", err)
	}
	authority := host
	if port != "" {
		authority = host + ":" + port
	}
	prefix := ""
	if len(segments) > 0 {
		prefix = strings.Join(segments, "/") + "/"
	}
	expected := map[string]string{
		"id":               strings.TrimSpace(strconv.Itoa(project.ID)),
		"http_url_to_repo": "https://" + authority + "/" + prefix + repository + ".git",
		"ssh_url_to_repo":  "ssh://git@" + endpoints.SSHHost + ":" + endpoints.SSHPort + "/" + repository + ".git",
		"web_url":          strings.TrimSuffix(endpoints.WebBase, "/") + "/" + repository,
	}
	if endpoints.ProjectID != expected["id"] {
		return fmt.Errorf("REP-006 GitLab API project ID %s differs from the approved project ID %s", expected["id"], endpoints.ProjectID)
	}
	for _, key := range []string{"http_url_to_repo", "ssh_url_to_repo", "web_url"} {
		actual := project.HTTPURLToRepo
		switch key {
		case "ssh_url_to_repo":
			actual = project.SSHURLToRepo
		case "web_url":
			actual = project.WebURL
		}
		if !strings.EqualFold(actual, expected[key]) {
			return fmt.Errorf("REP-006 GitLab API %s %q differs from the approved address %q for repository %q", key, actual, expected[key], repository)
		}
	}
	return nil
}

func gitlabAPI(ctx context.Context, host string, env []string, method, endpoint string, payload map[string]string) ([]byte, error) {
	var requested any
	if payload != nil {
		requested = payload
	}
	if err := checkWritePermit(ctx, host, method, endpoint, requested); err != nil {
		return nil, err
	}
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
		if method == "GET" {
			reason := "command unavailable"
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				reason = fmt.Sprintf("command exit status %d", exit.ExitCode())
			}
			return nil, fmt.Errorf("taxion: GitLab read-only API query failed (%s, %s); no mutation requested; provider details withheld", method, reason)
		}
		return nil, fmt.Errorf("taxion: GitLab API failed; delivery may be uncertain, provider details withheld")
	}
	return output, nil
}
