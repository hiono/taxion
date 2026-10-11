package copilot

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
)

var credentialEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Explicit references override SDK ambient-token precedence without persisting
// token values in configuration or session hashes.
func configureCredential(options *sdk.ClientOptions, ref string) error {
	if ref != "copilot:logged-in" && !strings.HasPrefix(ref, "env:") {
		return nil
	}
	var token string
	loggedIn := ref == "copilot:logged-in"
	if !loggedIn {
		name := strings.TrimPrefix(ref, "env:")
		if !credentialEnvName.MatchString(name) {
			return fmt.Errorf("taxion: credential reference requires env: followed by an uppercase environment name")
		}
		var ok bool
		token, ok = os.LookupEnv(name)
		if !ok || token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
			return fmt.Errorf("taxion: selected runtime credential is missing or invalid; value withheld")
		}
	}
	env := []string{}
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key == "COPILOT_GITHUB_TOKEN" || key == "GH_TOKEN" || key == "GITHUB_TOKEN" {
			continue
		}
		// Do not also pass the named secret as an unrelated environment variable.
		if !loggedIn && key == strings.TrimPrefix(ref, "env:") {
			continue
		}
		env = append(env, item)
	}
	options.Env = env
	options.GitHubToken = token
	options.UseLoggedInUser = &loggedIn
	return nil
}
