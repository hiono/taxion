package forge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hiono/taxion/internal/publictext"
	"github.com/hiono/taxion/internal/store"
)

type publicWriteKey struct{}

func writeFingerprint(host, method, endpoint string, payload any) ([32]byte, error) {
	body, err := json.Marshal(struct {
		Host, Method, Endpoint string
		Payload                any
	}{host, method, endpoint, payload})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(body), nil
}

func checkWritePermit(ctx context.Context, host, method, endpoint string, payload any) error {
	if method == "GET" && payload == nil {
		return nil
	}
	expected, err := writeFingerprint(host, method, endpoint, payload)
	approved, ok := ctx.Value(publicWriteKey{}).([32]byte)
	if err != nil || !ok || expected != approved {
		return fmt.Errorf("taxion: forge mutation requires the exact shared public-text gate result")
	}
	return nil
}

func publicWriteContext(ctx context.Context, update store.ExternalUpdate, host, method, endpoint string, payload any, env []string) (context.Context, error) {
	if update.Host != host || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(update.Hash) ||
		!regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+$`).MatchString(update.Repository) ||
		update.Provider == "github" && strings.Count(update.Repository, "/") != 1 ||
		update.Kind != "ISSUE" && update.Kind != "PR" ||
		update.Action == "CREATE" && (update.Kind != "ISSUE" || update.Number != 0 || update.Title == "" || update.Body == "") ||
		update.Action == "COMMENT" && (update.Number <= 0 || update.Title != "" || update.Body == "") ||
		update.Action == "CLOSE" && (update.Kind != "ISSUE" || update.Number <= 0 || update.Title != "" || update.Body != "") ||
		update.Action != "CREATE" && update.Action != "COMMENT" && update.Action != "CLOSE" {
		return nil, fmt.Errorf("taxion: forge mutation has an unsupported target, action or payload")
	}
	expectedMethod, expectedEndpoint := "POST", "repos/"+update.Repository+"/issues"
	expectedPayload := map[string]string{"body": update.Body + "\n\n" + PublicationMarker(update)}
	switch update.Provider {
	case "github":
		if host != "github.com" {
			return nil, fmt.Errorf("taxion: GitHub mutation requires github.com")
		}
		switch update.Action {
		case "CREATE":
			expectedPayload["title"] = update.Title
		case "COMMENT":
			expectedEndpoint += "/" + strconv.Itoa(update.Number) + "/comments"
		case "CLOSE":
			expectedMethod, expectedEndpoint = "PATCH", expectedEndpoint+"/"+strconv.Itoa(update.Number)
			expectedPayload = map[string]string{"state": "closed", "state_reason": "completed"}
		}
	case "gitlab":
		if !validGitLabHost(host) {
			return nil, fmt.Errorf("taxion: GitLab mutation requires an exact host")
		}
		expectedEndpoint = gitlabProjectEndpoint(update.Repository) + "/issues"
		switch update.Action {
		case "CREATE":
			expectedPayload = map[string]string{"title": update.Title, "description": update.Body + "\n\n" + PublicationMarker(update)}
		case "COMMENT":
			expectedEndpoint = gitlabObjectEndpoint(update) + "/notes"
		case "CLOSE":
			expectedMethod, expectedEndpoint = "PUT", gitlabObjectEndpoint(update)
			expectedPayload = map[string]string{"state_event": "close"}
		}
	default:
		return nil, fmt.Errorf("taxion: unsupported forge provider")
	}
	actual, err := writeFingerprint(host, method, endpoint, payload)
	if err != nil {
		return nil, err
	}
	expected, err := writeFingerprint(host, expectedMethod, expectedEndpoint, expectedPayload)
	if err != nil || actual != expected {
		return nil, fmt.Errorf("taxion: forge request differs from the text and target checked by policy")
	}
	resolve := func(ctx context.Context, target string) (publictext.Reference, error) {
		if update.Provider == "github" {
			return ResolveGitHubReference(ctx, target, env)
		}
		return resolveGitLabReference(ctx, update.Host, update.Repository, target, env)
	}
	if err := publictext.CheckMutation(ctx, update.PublicText(), resolve); err != nil {
		return nil, fmt.Errorf("taxion: public-text gate: %w: %w", publictext.ErrMutationRejected, err)
	}
	return context.WithValue(ctx, publicWriteKey{}, actual), nil
}
